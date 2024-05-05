//go:build rueidis

package redlock

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"election-agent/internal/config"
	"election-agent/internal/logging"

	"github.com/redis/rueidis"
	"github.com/redis/rueidis/rueidiscompat"
)

// rueidisConn implements `Conn` interface
type rueidisConn struct {
	client      rueidis.Client
	delegate    rueidiscompat.Cmdable
	opts        *rueidis.ClientOption
	lastErr     error
	lastErrTime time.Time
	mu          sync.RWMutex
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

	conns := make([]Conn, len(redisOpts))
	for i, opts := range redisOpts {
		opts.Dialer.Timeout = 1 * time.Second
		opts.ShuffleInit = true
		client, err := rueidis.NewClient(*opts)
		if err != nil {
			logging.Warnw("Failed to create rueidis client", "InitAddress", opts.InitAddress, "error", err)
		}
		if client == nil {
			conns[i] = &rueidisConn{client: nil, delegate: nil, opts: opts, lastErr: err}
		} else {
			conns[i] = &rueidisConn{client: client, delegate: rueidiscompat.NewAdapter(client), opts: opts, lastErr: err}
		}
	}

	return ConnShards{conns}, nil
}

func (c *rueidisConn) getInstance() (rueidis.Client, rueidiscompat.Cmdable) {
	c.mu.RLock()
	if c.delegate != nil {
		if isNetOpError(c.lastErr) && time.Since(c.lastErrTime) < time.Second {
			c.mu.RUnlock()
			return nil, nil
		}
		c.mu.RUnlock()
		return c.client, c.delegate
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	client, err := rueidis.NewClient(*c.opts)
	if err != nil {
		c.lastErr = err
		return nil, nil
	}

	c.client = client
	c.delegate = rueidiscompat.NewAdapter(client)
	c.lastErr = nil
	return c.client, c.delegate
}

func (c *rueidisConn) getDelegate() rueidiscompat.Cmdable {
	_, delegate := c.getInstance()
	return delegate
}

func (c *rueidisConn) getClient() rueidis.Client {
	client, _ := c.getInstance()
	return client
}

func (c *rueidisConn) checkNetOpError(err error) {
	if !isNetOpError(err) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastErr = err
	c.lastErrTime = time.Now()
}

func (c *rueidisConn) Get(ctx context.Context, name string) (string, error) {
	delegate := c.getDelegate()
	if delegate == nil {
		return "", c.lastErr
	}

	value, err := c.client.Do(ctx, c.client.B().Get().Key(name).Build()).ToString()
	c.checkNetOpError(err)

	return value, noErrNil(err)
}

func (c *rueidisConn) Set(ctx context.Context, name string, value string) (bool, error) {
	client := c.getClient()
	if client == nil {
		return false, c.lastErr
	}

	reply, err := client.Do(ctx, client.B().Set().Key(name).Value(value).Build()).ToString()
	c.checkNetOpError(err)

	return reply == "OK", err
}

func (c *rueidisConn) SetNX(ctx context.Context, name string, value string, expiry time.Duration) (bool, error) {
	client := c.getClient()
	if client == nil {
		return false, c.lastErr
	}

	reply, err := client.Do(ctx, client.B().Set().Key(name).Value(value).Nx().Px(expiry).Build()).ToBool()
	c.checkNetOpError(err)

	return reply, err
}

func (c *rueidisConn) PTTL(ctx context.Context, name string) (time.Duration, error) {
	client := c.getClient()
	if client == nil {
		return 0, c.lastErr
	}

	// reply, err := delegate.PTTL(ctx, name).Result()
	cmd := client.B().Pttl().Key(name).Cache()
	reply, err := client.DoCache(ctx, cmd, time.Minute).AsInt64()
	c.checkNetOpError(err)

	return time.Duration(reply) * time.Millisecond, err
}

func (c *rueidisConn) Eval(ctx context.Context, script *Script, keysAndArgs ...interface{}) (interface{}, error) {
	delegate := c.getDelegate()
	if delegate == nil {
		return nil, c.lastErr
	}

	keys := make([]string, script.KeyCount)
	args := keysAndArgs

	if script.KeyCount > 0 {
		for i := 0; i < script.KeyCount; i++ {
			keys[i], _ = keysAndArgs[i].(string)
		}
		args = keysAndArgs[script.KeyCount:]
	}

	v, err := delegate.EvalSha(ctx, script.Hash, keys, args...).Result()
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT ") {
		v, err = delegate.Eval(ctx, script.Src, keys, args...).Result()
	}
	c.checkNetOpError(err)

	return v, noErrNil(err)
}

func (c *rueidisConn) Close(ctx context.Context) error {
	delegate := c.getDelegate()
	if delegate == nil {
		return c.lastErr
	}

	_, err := delegate.Shutdown(ctx).Result()
	return err
}

func (c *rueidisConn) Ping(ctx context.Context) (bool, error) {
	client := c.getClient()
	if client == nil {
		return false, c.lastErr
	}

	value, err := client.Do(ctx, client.B().Ping().Build()).ToString()
	c.checkNetOpError(err)

	return value == "PONG", err
}

func (c *rueidisConn) MGet(ctx context.Context, keys ...string) ([]string, error) {
	delegate := c.getDelegate()
	if delegate == nil {
		return []string{}, c.lastErr
	}

	vals, err := delegate.MGet(ctx, keys...).Result()
	c.checkNetOpError(err)

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
	delegate := c.getDelegate()
	if delegate == nil {
		return false, c.lastErr
	}

	reply, err := delegate.MSet(ctx, pairs...).Result()
	c.checkNetOpError(err)

	return reply == "OK", err
}

func (c *rueidisConn) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	delegate := c.getDelegate()
	if delegate == nil {
		return []string{}, 0, c.lastErr
	}

	results, cursor, err := delegate.Scan(ctx, cursor, match, count).Result()
	c.checkNetOpError(err)

	return results, cursor, err
}

func noErrNil(err error) error {
	if rueidis.IsRedisNil(err) {
		return nil
	}
	return err
}

func parseRedisURLs(cfg *config.Config) ([]*rueidis.ClientOption, error) {
	redisURLs := cfg.Redis.URLs
	mode := cfg.Redis.Mode
	redisOpts := make([]*rueidis.ClientOption, 0)

	if mode == "single" || mode == "cluster" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is single or cluster, the number of redis urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is single or cluster, the number of redis urls must be an odd number")
		}

		for _, redisURL := range redisURLs {
			opt, err := rueidis.ParseURL(redisURL)
			if err != nil {
				return nil, err
			}
			redisOpts = append(redisOpts, &opt)
		}
	} else if mode == "sharding" {
		if len(redisURLs) < 3 {
			return nil, errors.New("When the redis mode is sharding, the number of redis urls must be at least 3")
		}
		if (len(redisURLs) % 3) != 0 {
			return nil, errors.New("When the redis mode is sharding, the number of redis urls must be an odd number")
		}
		for _, redisURL := range redisURLs {
			opts, err := parseShardingURL(redisURL)
			if err != nil {
				return nil, err
			}
			redisOpts = append(redisOpts, opts...)
		}
	}

	return redisOpts, nil
}

func parseShardingURL(redisURL string) ([]*rueidis.ClientOption, error) {
	opt, err := rueidis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	opts := make([]*rueidis.ClientOption, 0, len(opt.InitAddress))
	for _, addr := range opt.InitAddress {
		nopt := opt
		nopt.InitAddress = []string{addr}
		opts = append(opts, &nopt)
	}

	return opts, nil
}

func isNetOpError(err error) bool {
	if err == nil {
		return false
	}
	opErr := &net.OpError{}
	if errors.As(err, &opErr) {
		return true
	}
	return false
}
