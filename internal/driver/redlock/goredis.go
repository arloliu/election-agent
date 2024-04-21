//go:build goredis || !rueidis

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
	ctx      context.Context
	delegate redis.UniversalClient
}

var _ Conn = (*goredisConn)(nil)

func CreateConnections(ctx context.Context, cfg *config.Config) (ConnShards, error) {
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
			connShards[idx] = append(connShards[idx], &goredisConn{ctx: ctx, delegate: client})
		}
	}

	return connShards, nil
}

func (c *goredisConn) WithContext(ctx context.Context) Conn {
	c.ctx = ctx
	return c
}

func (c *goredisConn) Get(name string) (string, error) {
	value, err := c.delegate.Get(c.ctx, name).Result()
	return value, noErrNil(err)
}

func (c *goredisConn) Set(name string, value string) (bool, error) {
	reply, err := c.delegate.Set(c.ctx, name, value, 0).Result()
	return reply == "OK", err
}

func (c *goredisConn) SetNX(name string, value string, expiry time.Duration) (bool, error) {
	return c.delegate.SetNX(c.ctx, name, value, expiry).Result()
}

func (c *goredisConn) PTTL(name string) (time.Duration, error) {
	return c.delegate.PTTL(c.ctx, name).Result()
}

func (c *goredisConn) Eval(script *Script, keysAndArgs ...interface{}) (interface{}, error) {
	keys := make([]string, script.KeyCount)
	args := keysAndArgs

	if script.KeyCount > 0 {
		for i := 0; i < script.KeyCount; i++ {
			keys[i], _ = keysAndArgs[i].(string)
		}
		args = keysAndArgs[script.KeyCount:]
	}

	v, err := c.delegate.EvalSha(c.ctx, script.Hash, keys, args...).Result()
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT ") {
		v, err = c.delegate.Eval(c.ctx, script.Src, keys, args...).Result()
	}
	return v, noErrNil(err)
}

func (c *goredisConn) Close(_ context.Context) error {
	return c.delegate.Close()
}

func (c *goredisConn) Ping() (bool, error) {
	value, err := c.delegate.Ping(c.ctx).Result()
	return value == "PONG", err
}

func (c *goredisConn) MGet(keys ...string) ([]string, error) {
	vals, err := c.delegate.MGet(c.ctx, keys...).Result()
	err = noErrNil(err)

	strs := make([]string, len(vals))
	for i, v := range vals {
		var ok bool
		strs[i], ok = v.(string)
		if !ok {
			strs[i] = ""
		}
	}

	return strs, noErrNil(err)
}

func (c *goredisConn) MSet(pairs ...any) (bool, error) {
	reply, err := c.delegate.MSet(c.ctx, pairs...).Result()
	return reply == "OK", err
}

func noErrNil(err error) error {
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
			uoptss, err := parseShardingURL(redisURL)
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

func parseShardingURL(redisURL string) ([]*redis.UniversalOptions, error) {
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
