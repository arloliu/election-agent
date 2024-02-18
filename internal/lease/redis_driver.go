package lease

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"election-agent/internal/config"

	"github.com/dolthub/maphash"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis"
	"github.com/hashicorp/go-multierror"

	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	goredis "github.com/go-redsync/redsync/v4/redis/goredis/v9"
	goredislib "github.com/redis/go-redis/v9"
)

// RedisLease implements Lease interface
type RedisLease struct {
	id     uint64
	mu     *redsync.Mutex
	driver *RedisLeaseDriver
}

func (rl *RedisLease) ID() uint64 {
	return rl.id
}

func (rl *RedisLease) Grant(ctx context.Context) error {
	return rl.mu.TryLockContext(ctx)
}

func (rl *RedisLease) Revoke(ctx context.Context) error {
	_, err := rl.mu.UnlockContext(ctx)
	if err != nil {
		if errors.Is(err, redsync.ErrLockAlreadyExpired) {
			return nil
		}

		val, _ := rl.driver.GetHolder(ctx, rl.mu.Name())
		return fmt.Errorf("Failed to revoke lease %s, expected value:%s, actual value:%s, error: %w\n", rl.mu.Name(), rl.mu.Value(), val, err)
	}
	return nil
}

func (rl *RedisLease) Extend(ctx context.Context) error {
	ok, err := rl.mu.ExtendContext(ctx)
	if err != nil {
		return fmt.Errorf("Failed to extend lease %s, got error: %w", rl.mu.Name(), err)
	}
	if !ok {
		return fmt.Errorf("Failed to extend lease %s", rl.mu.Name())
	}

	return nil
}

// RedisLeaseDriver implements LeaseDriver interface
type RedisLeaseDriver struct {
	ctx     context.Context
	cfg     *config.Config
	clients []goredislib.UniversalClient
	pools   []redsyncredis.Pool
	quorum  int
	rs      *redsync.Redsync
	hasher  maphash.Hasher[string]
}

func NewRedisLeaseDriver(ctx context.Context, cfg *config.Config) (*RedisLeaseDriver, error) {
	redisOpts, err := parseRedisURLs(cfg)
	if err != nil {
		return nil, err
	}

	clients := make([]goredislib.UniversalClient, 0, len(redisOpts))
	for _, opts := range redisOpts {
		clients = append(clients, goredislib.NewUniversalClient(opts))
	}

	pools := make([]redsyncredis.Pool, 0, len(clients))
	for _, c := range clients {
		pools = append(pools, goredis.NewPool(c))
	}

	inst := &RedisLeaseDriver{
		ctx:     ctx,
		cfg:     cfg,
		clients: clients,
		pools:   pools,
		quorum:  len(pools)/2 + 1,
		rs:      redsync.New(pools...),
		hasher:  maphash.NewHasher[string](),
	}

	return inst, nil
}

func (rd *RedisLeaseDriver) LeaseID(name string, holder string, ttl time.Duration) uint64 {
	return rd.hasher.Hash(name + holder + strconv.FormatInt(int64(ttl), 16))
}

func (rd *RedisLeaseDriver) GetHolder(ctx context.Context, name string) (string, error) {
	key := rd.redisKey(name)
	return rd.redisGetAsync(ctx, key)
}

func (rd *RedisLeaseDriver) NewLease(name string, holder string, ttl time.Duration) Lease {
	lease := &RedisLease{
		// id: rd.LeaseID(name, holder, ttl),
		mu: rd.rs.NewMutex(
			rd.redisKey(name),
			redsync.WithExpiry(ttl),
			redsync.WithTries(32),
			redsync.WithSetNXOnExtend(),
			redsync.WithTimeoutFactor(0.2),
			redsync.WithValue(holder),
			redsync.WithShufflePools(false),
			redsync.WithFailFast(true),
			redsync.WithGenValueFunc(func() (string, error) { return holder, nil }),
		),
		driver: rd,
	}

	return lease
}

func (rd *RedisLeaseDriver) Shutdown(ctx context.Context) error {
	for _, client := range rd.clients {
		client.Close()
	}
	return nil
}

func (rd *RedisLeaseDriver) redisKey(val string) string {
	return rd.cfg.Redis.Prefix + "/" + val
}

func (rd *RedisLeaseDriver) redisGetAsync(ctx context.Context, name string) (string, error) {
	type result struct {
		node  int
		reply string
		err   error
	}

	ch := make(chan result, len(rd.pools))
	for node, pool := range rd.pools {
		go func(node int, pool redis.Pool) {
			r := result{node: node}
			r.reply, r.err = rd.redisGet(ctx, name, pool)
			ch <- r
		}(node, pool)
	}

	n := 0
	replies := make([]string, 0, len(rd.pools))
	var taken []int
	var err error

	for range rd.pools {
		r := <-ch
		if len(r.reply) > 0 && r.err == nil {
			replies = append(replies, r.reply)
			n++
		} else if r.err != nil {
			err = multierror.Append(err, r.err)
		} else {
			taken = append(taken, r.node)
			err = multierror.Append(err, r.err)
		}

		if len(taken) >= rd.quorum {
			return "", err
		}
	}

	val, n := getMostFreqVal[string](replies)
	if n < rd.quorum {
		return val, fmt.Errorf("The number of the same %s:%s is not >= %d quorum", name, val, rd.quorum)
	}

	return val, nil
}

func (rd *RedisLeaseDriver) redisGet(ctx context.Context, name string, pool redsyncredis.Pool) (string, error) {
	if name == "" {
		return "", errors.New("the redis key is empty")
	}
	conn, err := pool.Get(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	val, err := conn.Get(name)

	return val, err
}

func getMostFreqVal[K comparable](vals []K) (K, int) {
	freqMap := make(map[K]int)
	for _, val := range vals {
		freqMap[val] += 1
	}

	// Find the value with the highest frequency
	var mostFreqVal K
	highestFreq := 0
	for val, frequency := range freqMap {
		if frequency > highestFreq {
			highestFreq = frequency
			mostFreqVal = val
		}
	}

	return mostFreqVal, highestFreq
}

func parseRedisURLs(cfg *config.Config) ([]*goredislib.UniversalOptions, error) {
	redisURLs := cfg.Redis.URLs
	mode := cfg.Redis.Mode
	redisOpts := make([]*goredislib.UniversalOptions, 0)

	if mode == "single" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is single, the number of redis urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is single, the number of redis urls must be an odd number")
		}

		for _, redisURL := range redisURLs {
			opts, err := goredislib.ParseURL(redisURL)
			if err != nil {
				return nil, err
			}

			uopts := optsToUopts(opts)
			redisOpts = append(redisOpts, uopts)
		}
	} else if mode == "cluster" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is cluster, the number of redis cluster urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is cluster, the number of redis cluster urls must be an odd number")
		}

		for _, urls := range redisURLs {
			copts, err := goredislib.ParseClusterURL(urls)
			if err != nil {
				return nil, err
			}

			uopts := coptsToUopts(copts)
			redisOpts = append(redisOpts, uopts)
		}
	} else if mode == "failover" {
		return nil, errors.New("The failover mode is not suppported yet")
	}

	return redisOpts, nil
}

func optsToUopts(o *goredislib.Options) *goredislib.UniversalOptions {
	return &goredislib.UniversalOptions{
		Addrs:      []string{o.Addr},
		ClientName: o.ClientName,
		Dialer:     o.Dialer,
		OnConnect:  o.OnConnect,

		DB:       o.DB,
		Protocol: o.Protocol,
		Username: o.Username,
		Password: o.Password,

		MaxRetries:      o.MaxRetries,
		MinRetryBackoff: o.MinRetryBackoff,
		MaxRetryBackoff: o.MaxRetryBackoff,

		DialTimeout:           o.DialTimeout,
		ReadTimeout:           o.ReadTimeout,
		WriteTimeout:          o.WriteTimeout,
		ContextTimeoutEnabled: o.ContextTimeoutEnabled,

		PoolFIFO:        o.PoolFIFO,
		PoolSize:        o.PoolSize,
		PoolTimeout:     o.PoolTimeout,
		MinIdleConns:    o.MinIdleConns,
		MaxIdleConns:    o.MaxIdleConns,
		MaxActiveConns:  o.MaxActiveConns,
		ConnMaxIdleTime: o.ConnMaxIdleTime,
		ConnMaxLifetime: o.ConnMaxLifetime,

		TLSConfig: o.TLSConfig,

		DisableIndentity: o.DisableIndentity,
		IdentitySuffix:   o.IdentitySuffix,
	}
}

func coptsToUopts(o *goredislib.ClusterOptions) *goredislib.UniversalOptions {
	return &goredislib.UniversalOptions{
		Addrs:      o.Addrs,
		ClientName: o.ClientName,
		Dialer:     o.Dialer,
		OnConnect:  o.OnConnect,

		Protocol: o.Protocol,
		Username: o.Username,
		Password: o.Password,

		MaxRedirects:   o.MaxRedirects,
		ReadOnly:       o.ReadOnly,
		RouteByLatency: o.RouteByLatency,
		RouteRandomly:  o.RouteRandomly,

		MaxRetries:      o.MaxRetries,
		MinRetryBackoff: o.MinRetryBackoff,
		MaxRetryBackoff: o.MaxRetryBackoff,

		DialTimeout:           o.DialTimeout,
		ReadTimeout:           o.ReadTimeout,
		WriteTimeout:          o.WriteTimeout,
		ContextTimeoutEnabled: o.ContextTimeoutEnabled,

		PoolFIFO: o.PoolFIFO,

		PoolSize:        o.PoolSize,
		PoolTimeout:     o.PoolTimeout,
		MinIdleConns:    o.MinIdleConns,
		MaxIdleConns:    o.MaxIdleConns,
		MaxActiveConns:  o.MaxActiveConns,
		ConnMaxIdleTime: o.ConnMaxIdleTime,
		ConnMaxLifetime: o.ConnMaxLifetime,

		TLSConfig: o.TLSConfig,

		DisableIndentity: o.DisableIndentity,
		IdentitySuffix:   o.IdentitySuffix,
	}
}
