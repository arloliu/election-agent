package redlock

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"election-agent/internal/config"

	"github.com/redis/go-redis/v9"
)

// goredisConn implements `Conn` interface
type goredisConn struct {
	delegate redis.UniversalClient
}

var _ Conn = (*goredisConn)(nil)

func CreateGoredisConnections(ctx context.Context, cfg *config.Config) (ConnShards, error) {
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
	shardSize := len(redisOpts[0])
	connShards := make(ConnShards, shardSize)
	for _, optss := range redisOpts {
		for idx, opts := range optss {
			opts.DialTimeout = 2 * time.Second
			opts.ReadTimeout = 2 * time.Second
			opts.WriteTimeout = 2 * time.Second
			opts.ContextTimeoutEnabled = true
			opts.DisableIndentity = true
			client := redis.NewUniversalClient(opts)
			connShards[idx] = append(connShards[idx], &goredisConn{delegate: client})
		}
	}

	return connShards, nil
}

func (c *goredisConn) Get(ctx context.Context, name string) (string, error) {
	value, err := c.delegate.Get(ctx, name).Result()
	return value, noGoredisErrNil(err)
}

func (c *goredisConn) Set(ctx context.Context, name string, value string) (bool, error) {
	reply, err := c.delegate.Set(ctx, name, value, 0).Result()
	return reply == "OK", err
}

func (c *goredisConn) Eval(ctx context.Context, script *Script, keys []string, args []string) (any, error) {
	v, err := c.delegate.EvalSha(ctx, script.Hash, keys, args).Result()
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT ") {
		v, err = c.delegate.Eval(ctx, script.Src, keys, args).Result()
	}
	return v, noGoredisErrNil(err)
}

func (c *goredisConn) Close(_ context.Context) error {
	return c.delegate.Close()
}

func (c *goredisConn) Ping(ctx context.Context) (bool, error) {
	value, err := c.delegate.Ping(ctx).Result()
	return value == "PONG", err
}

func (c *goredisConn) MGet(ctx context.Context, keys ...string) ([]string, error) {
	vals, err := c.delegate.MGet(ctx, keys...).Result()
	err = noGoredisErrNil(err)

	strs := make([]string, len(vals))
	for i, v := range vals {
		var ok bool
		strs[i], ok = v.(string)
		if !ok {
			strs[i] = ""
		}
	}

	return strs, noGoredisErrNil(err)
}

func (c *goredisConn) MSet(ctx context.Context, pairs ...any) (bool, error) {
	reply, err := c.delegate.MSet(ctx, pairs...).Result()
	return reply == "OK", err
}

func (c *goredisConn) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	return c.delegate.Scan(ctx, cursor, match, count).Result()
}

func (c *goredisConn) NotAcceptLock() bool {
	return false
}

func noGoredisErrNil(err error) error {
	if !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

func parseRedisURLs(cfg *config.Config) ([][]*redis.UniversalOptions, error) {
	redisURLs := cfg.Redis.URLs
	mode := cfg.Redis.Mode
	redisOpts := make([][]*redis.UniversalOptions, 0)

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
			redisOpts = append(redisOpts, []*redis.UniversalOptions{uopts})
		}
	} else if mode == "cluster" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is cluster, the number of redis cluster urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is cluster, the number of redis cluster urls must be an odd number")
		}

		for _, redisURL := range redisURLs {
			copts, err := redis.ParseClusterURL(redisURL)
			if err != nil {
				return nil, err
			}

			uopts := coptsToUopts(copts)
			redisOpts = append(redisOpts, []*redis.UniversalOptions{uopts})
		}
	} else if mode == "sharding" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is sharding, the number of redis cluster urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is sharding, the number of redis cluster urls must be an odd number")
		}

		uoptssCount := 0
		for _, redisURL := range redisURLs {
			uoptss, err := parseGoredisShardingURL(redisURL)
			if err != nil {
				return nil, err
			}
			if uoptssCount == 0 {
				uoptssCount = len(uoptss)
			} else if uoptssCount != len(uoptss) {
				return nil, errors.New("The number of redis shard nodes must be the same")
			}
			if len(uoptss) < 2 {
				return nil, errors.New("When the redis mode is sharding, the number of each redis group must be at least 2")
			}

			redisOpts = append(redisOpts, uoptss)
		}
	}

	return redisOpts, nil
}

func parseGoredisShardingURL(redisURL string) ([]*redis.UniversalOptions, error) {
	copts, err := redis.ParseClusterURL(redisURL)
	if err != nil {
		return nil, err
	}
	uoptss := make([]*redis.UniversalOptions, 0, len(copts.Addrs))
	for _, addr := range copts.Addrs {
		uopts := coptsToUopts(copts)
		uopts.Addrs = []string{addr}
		uoptss = append(uoptss, uopts)
	}

	return uoptss, nil
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
