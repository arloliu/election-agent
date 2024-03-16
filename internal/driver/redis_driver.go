package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
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

	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	redis "github.com/redis/go-redis/v9"
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

	if rl.driver.isRedisUnhealthy(err) {
		return &lease.UnavailableError{Err: err}
	}

	return err
}

func (rl *RedisLease) Revoke(ctx context.Context) error {
	_, err := rl.mu.UnlockContext(ctx)
	if err != nil {
		if rl.driver.isRedisUnhealthy(err) {
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
		if rl.driver.isRedisUnhealthy(err) {
			return &lease.UnavailableError{Err: err}
		}
		errTaken := &redsync.ErrTaken{}
		if errors.As(err, &errTaken) {
			e, _ := err.(*redsync.ErrTaken) //nolint:errorlint
			return &lease.TakenError{Nodes: e.Nodes}
		}
		return &lease.ExtendFailError{Lease: rl.mu.Name(), Err: err}
	}
	if !ok {
		return &lease.NonexistError{Lease: rl.mu.Name()}
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

	if rm.driver.isRedisUnhealthy(err) {
		return &lease.UnavailableError{Err: err}
	}

	return err
}

func (rm *RedisMutex) Unlock(ctx context.Context) (bool, error) {
	ok, err := rm.mu.UnlockContext(ctx)
	if err != nil && rm.driver.isRedisUnhealthy(err) {
		return ok, &lease.UnavailableError{Err: err}
	}
	return ok, err
}

// RedisKVDriver implements KVDriver interface
type RedisKVDriver struct {
	ctx         context.Context
	cfg         *config.Config
	clients     []redis.UniversalClient
	originPools []RedisPool
	pools       []RedisPool
	quorum      int
	rs          *redsync.Redsync
	mu          sync.Mutex
	hasher      maphash.Hasher[string]
	hostname    string
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

	clients := make([]redis.UniversalClient, 0, len(redisOpts))
	for _, opts := range redisOpts {
		opts.DialTimeout = 2 * time.Second
		opts.ReadTimeout = 1 * time.Second
		opts.WriteTimeout = 1 * time.Second
		clients = append(clients, redis.NewUniversalClient(opts))
	}
	pools := make([]RedisPool, 0, len(clients))
	redsyncPools := make([]redsyncredis.Pool, 0, len(clients))
	for _, c := range clients {
		pool := NewRedisPool(c)
		pools = append(pools, pool)
		redsyncPools = append(redsyncPools, redsyncredis.Pool(pool))
	}

	idx := cfg.Redis.Primary
	if idx < 0 || idx >= len(pools) {
		errMsg := fmt.Sprintf("The primary index %d is out of range", idx)
		logging.Error(errMsg)
		return nil, errors.New(errMsg)
	}

	inst := &RedisKVDriver{
		ctx:         ctx,
		cfg:         cfg,
		clients:     clients,
		originPools: pools,
		pools:       pools,
		quorum:      len(pools)/2 + 1,
		rs:          redsync.New(redsyncPools...),
		hasher:      maphash.NewHasher[string](),
		hostname:    os.Getenv("HOSTNAME"),
	}
	state, stateErr := inst.GetAgentState()
	if stateErr != nil {
		return inst, stateErr
	}
	if state != agent.UnavailableState {
		err = inst.SetAgentMode(agent.NormalMode)
	} else {
		inst.rs = redsync.New(redsyncPools...)
		logging.Warn("Initial key-value driver in unavailable state")
	}

	return inst, err
}

func (rd *RedisKVDriver) LeaseID(name string, holder string, ttl time.Duration) uint64 {
	return rd.hasher.Hash(name + holder + strconv.FormatInt(int64(ttl), 16))
}

func (rd *RedisKVDriver) GetHolder(ctx context.Context, name string) (string, error) {
	key := rd.leaseKey(name)
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
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
	state, err := rd.Get(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), true)
	if err != nil {
		return agent.UnavailableState, nil
	}

	if state == agent.UnavailableState {
		// change state to "empty" state when the previous "unavailable" value receieved
		_, _ = rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), agent.EmptyState, true)
		return agent.EmptyState, nil
	}

	if state == "" {
		return agent.EmptyState, nil
	}

	if !slices.Contains(agent.ValidStates, state) {
		return state, fmt.Errorf("The retrieved agent state '%s' is invalid", state)
	}

	logging.Debugw("driver: GetAgentState", "state", state, "key", rd.cfg.AgentInfoKey(agent.StateKey), "hostname", rd.hostname)
	return state, nil
}

func (rd *RedisKVDriver) SetAgentState(state string) error {
	if state == agent.UnavailableState {
		return nil
	}

	logging.Debugw("driver: SetAgentState", "state", state, "key", rd.cfg.AgentInfoKey(agent.StateKey), "hostname", rd.hostname)
	_, err := rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), state, true)
	return err
}

func (rd *RedisKVDriver) GetAgentMode() (string, error) {
	mode, err := rd.Get(rd.ctx, rd.cfg.AgentInfoKey(agent.ModeKey), true)
	if err != nil || mode == "" {
		return agent.UnknownMode, err
	}

	return mode, err
}

func (rd *RedisKVDriver) SetAgentMode(mode string) error {
	logging.Debugw("driver: SetAgentMode", "mode", mode, "pools", len(rd.pools), "quorum", rd.quorum, "hostname", rd.hostname)
	_, err := rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.ModeKey), mode, true)
	return err
}

func (rd *RedisKVDriver) GetAgentStatus() (*agent.Status, error) {
	stateKey := rd.cfg.AgentInfoKey(agent.StateKey)
	modeKey := rd.cfg.AgentInfoKey(agent.ModeKey)
	zoneEnableKey := rd.cfg.AgentInfoKey(agent.ZoneEnableKey)

	status := &agent.Status{State: agent.UnavailableState, Mode: agent.UnknownMode, ZoomEnable: false}
	replies, err := rd.MGet(rd.ctx, stateKey, modeKey, zoneEnableKey)
	if err != nil || len(replies) != 3 {
		status.ZoomEnable = rd.cfg.Zone.Enable
		logging.Debugw("driver: GetAgentStatus got error", "state", status.State, "mode", status.Mode, "zoomEnable", status.ZoomEnable, "error", err)
		return status, nil
	}

	// set default zoom enable settting when the zoom enable value is empty
	var zoomEnable bool
	if replies[2] == "" {
		zoomEnable = rd.cfg.Zone.Enable
	} else {
		zoomEnable = isBoolStrTrue(replies[2])
	}

	status.State = replies[0]
	status.Mode = replies[1]
	status.ZoomEnable = zoomEnable

	if status.State == agent.UnavailableState || status.State == "" {
		// change state to "empty" state when the previous "unavailable" value receieved
		status.State = agent.EmptyState
	}

	if status.Mode == "" {
		status.Mode = agent.UnknownMode
	}

	if !slices.Contains(agent.ValidStates, status.State) && status.State != agent.EmptyState {
		return status, fmt.Errorf("The retrieved agent state '%s' is invalid", status.State)
	}

	if !slices.Contains(agent.ValidModes, status.Mode) {
		return status, fmt.Errorf("The retrieved agent mode '%s' is invalid", status.Mode)
	}

	logging.Debugw("driver: GetAgentStatus", "state", status.State, "mode", status.Mode, "zoomEnable", status.ZoomEnable)

	return status, nil
}

func (rd *RedisKVDriver) SetAgentStatus(status *agent.Status) error {
	if status.State == agent.UnavailableState {
		return nil
	}

	stateKey := rd.cfg.AgentInfoKey(agent.StateKey)
	modeKey := rd.cfg.AgentInfoKey(agent.ModeKey)
	zoneEnableKey := rd.cfg.AgentInfoKey(agent.ZoneEnableKey)

	_, err := rd.MSet(rd.ctx, stateKey, status.State, modeKey, status.Mode, zoneEnableKey, status.ZoomEnable)
	return err
}

func (rd *RedisKVDriver) SetOpearationMode(mode string) {
	if mode == agent.OrphanMode {
		if len(rd.pools) == 1 {
			return
		}

		rd.pools = []RedisPool{rd.originPools[rd.cfg.Redis.Primary]}
		rd.quorum = 1
	} else {
		if len(rd.pools) > 1 {
			return
		}
		rd.pools = rd.originPools
		rd.quorum = len(rd.pools)/2 + 1
	}

	redsyncPools := make([]redsyncredis.Pool, 0, len(rd.pools))
	for _, p := range rd.pools {
		redsyncPools = append(redsyncPools, redsyncredis.Pool(p))
	}
	rd.rs = redsync.New(redsyncPools...)
}

func (rd *RedisKVDriver) Ping(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()

	return rd.actStatusOpAsync(true, func(pool RedisPool) (bool, error) {
		conn, err := getConnFromPool(ctx, pool)
		if err != nil {
			return false, err
		}
		return conn.Ping()
	})
}

func (rd *RedisKVDriver) Get(ctx context.Context, key string, strict bool) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.redisGetAsync(ctx, key, strict)
}

func (rd *RedisKVDriver) GetSetBool(ctx context.Context, key string, defVal bool, strict bool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	val, err := rd.redisGetAsync(ctx, key, strict)
	if val == "" {
		_, err := rd.SetBool(ctx, key, defVal, strict)
		if err != nil {
			return defVal, err
		}
	}
	return isBoolStrTrue(val), err
}

func (rd *RedisKVDriver) MGet(ctx context.Context, keys ...string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.redisMGetAsync(ctx, keys...)
}

func (rd *RedisKVDriver) Set(ctx context.Context, key string, value string, strict bool) (int, error) {
	if key == "" {
		return 0, errors.New("Set: the key is empty")
	}

	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()

	return rd.actStatusOpAsync(strict, func(pool RedisPool) (bool, error) {
		conn, err := getConnFromPool(ctx, pool)
		if err != nil {
			return false, err
		}
		return conn.Set(key, value)
	})
}

func (rd *RedisKVDriver) SetBool(ctx context.Context, key string, value bool, strict bool) (int, error) {
	if key == "" {
		return 0, errors.New("SetBool: the key for is empty")
	}

	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()

	return rd.actStatusOpAsync(strict, func(pool RedisPool) (bool, error) {
		conn, err := getConnFromPool(ctx, pool)
		if err != nil {
			return false, err
		}
		return conn.Set(key, boolStr(value))
	})
}

func (rd *RedisKVDriver) MSet(ctx context.Context, pairs ...any) (int, error) {
	if len(pairs) == 0 {
		return 0, errors.New("MSet: the pairs is empty")
	}
	if len(pairs)%2 != 0 {
		return 0, errors.New("MSet: the length of pairs is not an odd number")
	}

	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()

	return rd.actStatusOpAsync(true, func(pool RedisPool) (bool, error) {
		conn, err := getConnFromPool(ctx, pool)
		if err != nil {
			return false, err
		}
		return conn.MSet(pairs...)
	})
}

func (rd *RedisKVDriver) leaseKey(val string) string {
	return rd.cfg.KeyPrefix + "/lease/" + val
}

func (rd *RedisKVDriver) lockKey(val string) string {
	return rd.cfg.KeyPrefix + "/lock/" + val
}

func parseRedisURLs(cfg *config.Config) ([]*redis.UniversalOptions, error) {
	redisURLs := cfg.Redis.URLs
	mode := cfg.Redis.Mode
	redisOpts := make([]*redis.UniversalOptions, 0)

	if mode == "single" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is single, the number of redis urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is single, the number of redis urls must be an odd number")
		}

		for _, redisURL := range redisURLs {
			opts, err := redis.ParseURL(redisURL)
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
			copts, err := redis.ParseClusterURL(urls)
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

func optsToUopts(o *redis.Options) *redis.UniversalOptions {
	return &redis.UniversalOptions{
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

func coptsToUopts(o *redis.ClusterOptions) *redis.UniversalOptions {
	return &redis.UniversalOptions{
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
