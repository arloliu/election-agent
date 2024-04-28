//go:build rueidis

package redlock

import (
	"context"
	"election-agent/internal/config"
	"election-agent/internal/logging"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/redis/rueidis"
	"github.com/redis/rueidis/rueidiscompat"
)

// rueidisConn implements `Conn` interface
type rueidisConn struct {
	delegate rueidiscompat.Cmdable
}

var _ Conn = (*rueidisConn)(nil)

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

	clients := make([]rueidis.Client, 0, len(redisOpts))
	for _, opts := range redisOpts {
		opts.ShuffleInit = true
		client, err := rueidis.NewClient(*opts)
		if err != nil {
			return nil, err
		}
		logging.Infow("rueidis client", "InitAddress", opts.InitAddress)
		clients = append(clients, client)
	}

	conns := make([]Conn, len(clients))
	for i, client := range clients {
		conns[i] = &rueidisConn{delegate: rueidiscompat.NewAdapter(client)}
	}

	return ConnShards{conns}, nil
}

func (c *rueidisConn) Get(ctx context.Context, name string) (string, error) {
	value, err := c.delegate.Get(ctx, name).Result()
	return value, noErrNil(err)
}

func (c *rueidisConn) Set(ctx context.Context, name string, value string) (bool, error) {
	reply, err := c.delegate.Set(ctx, name, value, 0).Result()
	return reply == "OK", err
}

func (c *rueidisConn) SetNX(ctx context.Context, name string, value string, expiry time.Duration) (bool, error) {
	return c.delegate.SetNX(ctx, name, value, expiry).Result()
}

func (c *rueidisConn) PTTL(ctx context.Context, name string) (time.Duration, error) {
	return c.delegate.PTTL(ctx, name).Result()
}

func (c *rueidisConn) Eval(ctx context.Context, script *Script, keysAndArgs ...interface{}) (interface{}, error) {
	keys := make([]string, script.KeyCount)
	args := keysAndArgs

	if script.KeyCount > 0 {
		for i := 0; i < script.KeyCount; i++ {
			keys[i], _ = keysAndArgs[i].(string)
		}
		args = keysAndArgs[script.KeyCount:]
	}

	v, err := c.delegate.EvalSha(ctx, script.Hash, keys, args...).Result()
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT ") {
		v, err = c.delegate.Eval(ctx, script.Src, keys, args...).Result()
	}
	return v, noErrNil(err)
}

func (c *rueidisConn) Close(ctx context.Context) error {
	_, err := c.delegate.Shutdown(ctx).Result()
	return err
}

func (c *rueidisConn) Ping(ctx context.Context) (bool, error) {
	value, err := c.delegate.Ping(ctx).Result()
	return value == "PONG", err
}

func (c *rueidisConn) MGet(ctx context.Context, keys ...string) ([]string, error) {
	vals, err := c.delegate.MGet(ctx, keys...).Result()
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

func (c *rueidisConn) MSet(ctx context.Context, pairs ...any) (bool, error) {
	reply, err := c.delegate.MSet(ctx, pairs...).Result()
	return reply == "OK", err
}

func (c *rueidisConn) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	return c.delegate.Scan(ctx, cursor, match, count).Result()
}

func noErrNil(err error) error {
	if !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

func parseRedisURLs(cfg *config.Config) ([]*rueidis.ClientOption, error) {
	redisURLs := cfg.Redis.URLs
	mode := cfg.Redis.Mode
	redisOpts := make([]*rueidis.ClientOption, 0)

	if mode == "single" || mode == "cluster" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is single, the number of redis urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is single, the number of redis urls must be an odd number")
		}

		for _, redisURL := range redisURLs {
			opt, err := rueidis.ParseURL(redisURL)
			if err != nil {
				return nil, err
			}
			redisOpts = append(redisOpts, &opt)
		}
	} else if mode == "failover" {
		return nil, errors.New("The failover mode is not suppported yet")
	}

	return redisOpts, nil
}
