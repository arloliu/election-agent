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

func CreateConnections(ctx context.Context, cfg *config.Config) ([]Conn, error) {
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

	conns := make([]Conn, len(clients))
	for i, c := range clients {
		conns[i] = &goredisConn{ctx: ctx, delegate: c}
	}

	return conns, nil
}

func (c *goredisConn) NewWithContext(ctx context.Context) Conn {
	return &goredisConn{delegate: c.delegate, ctx: ctx}
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

func (c *goredisConn) Close(ctx context.Context) error {
	_, err := c.delegate.Shutdown(ctx).Result()
	return err
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
