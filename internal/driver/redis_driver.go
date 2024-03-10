package driver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/lease"
	"election-agent/internal/logging"

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
	driver *RedisKVDriver
}

var _ lease.Lease = (*RedisLease)(nil)

func (rl *RedisLease) ID() uint64 {
	return rl.id
}

func (rl *RedisLease) Grant(ctx context.Context) error {
	err := rl.mu.TryLockContext(ctx)
	if err == nil {
		return nil
	}

	if rl.driver.isAllRedisDown(err) {
		return &lease.UnavailableError{Err: err}
	}

	return err
}

func (rl *RedisLease) Revoke(ctx context.Context) error {
	_, err := rl.mu.UnlockContext(ctx)
	if err != nil {
		if rl.driver.isAllRedisDown(err) {
			return &lease.UnavailableError{Err: err}
		}

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
		if rl.driver.isAllRedisDown(err) {
			return &lease.UnavailableError{Err: err}
		}

		return fmt.Errorf("Failed to extend lease %s, got error: %w", rl.mu.Name(), err)
	}
	if !ok {
		return fmt.Errorf("Failed to extend lease %s", rl.mu.Name())
	}

	return nil
}

// RedisMutex implements Mutex interface
type RedisMutex struct {
	mu     *redsync.Mutex
	driver *RedisKVDriver
}

var _ lease.Mutex = (*RedisMutex)(nil)

func (rm *RedisMutex) Lock(ctx context.Context) error {
	err := rm.mu.LockContext(ctx)
	if err == nil {
		return nil
	}

	if rm.driver.isAllRedisDown(err) {
		return &lease.UnavailableError{Err: err}
	}

	return err
}

func (rm *RedisMutex) Unlock(ctx context.Context) (bool, error) {
	ok, err := rm.mu.UnlockContext(ctx)
	if err != nil && rm.driver.isAllRedisDown(err) {
		return ok, &lease.UnavailableError{Err: err}
	}
	return ok, err
}

// RedisKVDriver implements KVDriver interface
type RedisKVDriver struct {
	ctx         context.Context
	cfg         *config.Config
	clients     []goredislib.UniversalClient
	originPools []redsyncredis.Pool
	pools       []redsyncredis.Pool
	quorum      int
	rs          *redsync.Redsync
	mu          sync.Mutex
	hasher      maphash.Hasher[string]
}

var _ lease.KVDriver = (*RedisKVDriver)(nil)

func NewRedisKVDriver(ctx context.Context, cfg *config.Config) (*RedisKVDriver, error) {
	redisOpts, err := parseRedisURLs(cfg)
	if err != nil {
		return nil, err
	}

	// check redis configuration
	if len(redisOpts) < 1 {
		return nil, errors.New("Needs at least one redis URL")
	}
	if cfg.Redis.Primary < 0 || cfg.Redis.Primary >= len(redisOpts) {
		return nil, fmt.Errorf("The value of redis.primary=%d is out of range", cfg.Redis.Primary)
	}

	clients := make([]goredislib.UniversalClient, 0, len(redisOpts))
	for _, opts := range redisOpts {
		opts.DialTimeout = 2 * time.Second
		opts.ReadTimeout = 1 * time.Second
		opts.WriteTimeout = 1 * time.Second
		clients = append(clients, goredislib.NewUniversalClient(opts))
	}
	pools := make([]redsyncredis.Pool, 0, len(clients))
	for _, c := range clients {
		pools = append(pools, goredis.NewPool(c))
	}

	inst := &RedisKVDriver{
		ctx:         ctx,
		cfg:         cfg,
		clients:     clients,
		originPools: pools,
		pools:       pools,
		quorum:      len(pools)/2 + 1,
		rs:          redsync.New(pools...),
		hasher:      maphash.NewHasher[string](),
	}

	state, stateErr := inst.GetAgentState()
	if stateErr != nil {
		return inst, stateErr
	}
	if state != agent.UnavailableState {
		err = inst.SetAgentMode(agent.NormalMode)
	} else {
		inst.rs = redsync.New(inst.pools...)
		logging.Warn("Initial key-value driver in unavailable state")
	}

	return inst, err
}

func (rd *RedisKVDriver) LeaseID(name string, holder string, ttl time.Duration) uint64 {
	return rd.hasher.Hash(name + holder + strconv.FormatInt(int64(ttl), 16))
}

func (rd *RedisKVDriver) GetHolder(ctx context.Context, name string) (string, error) {
	key := rd.leaseKey(name)
	return rd.redisGetAsync(ctx, key, true)
}

func (rd *RedisKVDriver) NewLease(name string, holder string, ttl time.Duration) lease.Lease {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	lease := &RedisLease{
		id: rd.LeaseID(name, holder, ttl),
		mu: rd.rs.NewMutex(
			rd.leaseKey(name),
			redsync.WithExpiry(ttl),
			redsync.WithTries(1),
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

func (rd *RedisKVDriver) NewMutex(name string, ttl time.Duration) lease.Mutex {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	return &RedisMutex{
		driver: rd,
		mu: rd.rs.NewMutex(
			rd.lockKey(name),
			redsync.WithExpiry(ttl),
			redsync.WithTries(3),
			redsync.WithSetNXOnExtend(),
			redsync.WithTimeoutFactor(0.1),
			redsync.WithShufflePools(false),
			redsync.WithFailFast(true),
		),
	}
}

func (rd *RedisKVDriver) Shutdown(ctx context.Context) error {
	for _, client := range rd.clients {
		client.Close()
	}
	return nil
}

func (rd *RedisKVDriver) GetAgentState() (string, error) {
	state, err := rd.Get(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), false)
	if err != nil {
		return agent.UnavailableState, nil
	}

	if state == agent.UnavailableState {
		// clear state info when the previous "unavailable" value receieved
		_, _ = rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), "", false)
		return agent.EmptyState, nil
	}

	if state == "" {
		return agent.EmptyState, nil
	}

	if !slices.Contains(agent.ValidStates, state) {
		return state, fmt.Errorf("The retrieved agent state '%s' is invalid", state)
	}

	return state, nil
}

func (rd *RedisKVDriver) SetAgentState(state string) error {
	_, err := rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), state, false)
	if err != nil && state == agent.UnavailableState {
		return nil
	}

	return err
}

func (rd *RedisKVDriver) GetAgentMode() (string, error) {
	mode, err := rd.Get(rd.ctx, rd.cfg.AgentInfoKey(agent.ModeKey), false)
	if err != nil || mode == "" {
		return agent.UnknownMode, err
	}

	return mode, err
}

func (rd *RedisKVDriver) SetAgentMode(mode string) error {
	rd.mu.Lock()
	defer rd.mu.Unlock()

	var pools []redsyncredis.Pool
	if mode == agent.OrphanMode {
		idx := rd.cfg.Redis.Primary
		if idx < 0 || idx >= len(rd.originPools) {
			errMsg := fmt.Sprintf("The primary index %d is out of range", idx)
			logging.Error(errMsg)
			return errors.New(errMsg)
		}

		pools = []redsyncredis.Pool{rd.originPools[idx]}
	} else {
		pools = rd.originPools
	}

	if len(pools) != len(rd.pools) {
		rd.pools = pools
		rd.quorum = len(pools)/2 + 1
		rd.rs = redsync.New(pools...)
	}

	logging.Debugw("driver: SetAgentMode", "mode", mode, "pools", len(rd.pools), "quorum", rd.quorum)

	_, err := rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.ModeKey), mode, false)
	return err
}

func (rd *RedisKVDriver) Get(ctx context.Context, key string, strict bool) (string, error) {
	return rd.redisGetAsync(ctx, key, strict)
}

func (rd *RedisKVDriver) GetSetBool(ctx context.Context, key string, defVal bool, strict bool) (bool, error) {
	val, err := rd.redisGetAsync(ctx, key, strict)
	if val == "" {
		_, err := rd.SetBool(ctx, key, defVal, strict)
		if err != nil {
			return defVal, err
		}
	}
	return isBoolStrTrue(val), err
}

func (rd *RedisKVDriver) Set(ctx context.Context, key string, value string, strict bool) (int, error) {
	return rd.redisSetAsync(ctx, key, value, strict)
}

func (rd *RedisKVDriver) SetBool(ctx context.Context, key string, value bool, strict bool) (int, error) {
	return rd.redisSetAsync(ctx, key, boolStr(value), strict)
}

func (rd *RedisKVDriver) leaseKey(val string) string {
	return rd.cfg.KeyPrefix + "/lease/" + val
}

func (rd *RedisKVDriver) lockKey(val string) string {
	return rd.cfg.KeyPrefix + "/lock/" + val
}

func (rd *RedisKVDriver) redisGetAsync(ctx context.Context, key string, strict bool) (string, error) {
	type result struct {
		node  int
		reply string
		err   error
	}

	ch := make(chan result, len(rd.pools))
	for node, pool := range rd.pools {
		go func(node int, pool redis.Pool) {
			r := result{node: node}
			r.reply, r.err = rd.redisGet(ctx, key, pool)
			ch <- r
		}(node, pool)
	}

	n := 0
	replies := make([]string, 0, len(rd.pools))
	var err error

	for range rd.pools {
		r := <-ch
		if len(r.reply) > 0 && r.err == nil {
			replies = append(replies, r.reply)
			n++
		} else if r.err != nil {
			err = multierror.Append(err, r.err)
		}
	}

	val, n := getMostFreqVal[string](replies)
	if strict {
		if n < rd.quorum {
			return val, fmt.Errorf("Get fails, the number of the same %s:%s is not >= %d quorum, error: %w", key, val, rd.quorum, err)
		}
	} else if n == 0 && rd.isAllRedisDown(err) {
		return val, fmt.Errorf("Get %s fails, all redis nodes are down", key)
	}

	return val, nil
}

func (rd *RedisKVDriver) redisGet(ctx context.Context, key string, pool redsyncredis.Pool) (string, error) {
	if key == "" {
		return "", errors.New("The redis key is empty")
	}
	conn, err := pool.Get(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	val, err := conn.Get(key)

	return val, err
}

func (rd *RedisKVDriver) redisSetAsync(ctx context.Context, key string, value string, strict bool) (int, error) {
	type result struct {
		node     int
		statusOK bool
		err      error
	}

	ch := make(chan result, len(rd.pools))
	for node, pool := range rd.pools {
		go func(node int, pool redis.Pool) {
			r := result{node: node}
			r.statusOK, r.err = rd.redisSet(ctx, key, value, pool)
			ch <- r
		}(node, pool)
	}

	n := 0
	var taken []int
	var err error

	for range rd.pools {
		r := <-ch
		if r.statusOK {
			n++
		} else if r.err != nil {
			err = multierror.Append(err, r.err)
		} else {
			taken = append(taken, r.node)
			err = multierror.Append(err, &redsync.ErrNodeTaken{Node: r.node})
		}
	}

	if strict {
		if len(taken) >= rd.quorum {
			return n, &redsync.ErrTaken{Nodes: taken}
		}

		if n < rd.quorum {
			return n, fmt.Errorf("Set fails, the number of the same %s:%s is not >= %d quorum, error: %w", key, value, rd.quorum, err)
		}
	} else if n == 0 {
		return n, fmt.Errorf("Set %s:%s fails, all redis nodes are down", key, value)
	}

	return n, nil
}

func (rd *RedisKVDriver) redisSet(ctx context.Context, key string, value string, pool redsyncredis.Pool) (bool, error) {
	if key == "" {
		return false, errors.New("The redis key is empty")
	}
	conn, err := pool.Get(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	return conn.Set(key, value)
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

func (rd *RedisKVDriver) isAllRedisDown(err error) bool {
	// TODO: write a function to use the unavailable state cache to improve performance
	if err == nil {
		return false
	}

	merr := getMultiError(err)
	if merr == nil {
		return false
	}

	redisCount := len(rd.pools)
	if merr.Len() < redisCount {
		return false
	}

	n := 0
	for _, err := range merr.Errors {
		if isNetOpError(err) {
			n++
		}
	}

	return n == redisCount
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
