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
)

const createClientThreshold = 5 * time.Second

// rueidisConn implements `Conn` interface
type rueidisConn struct {
	client      rueidis.Client
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
		client, err := rueidis.NewClient(*opts)
		if err != nil {
			logging.Warnw("Failed to create rueidis client", "InitAddress", opts.InitAddress, "error", err)
		}

		conns[i] = &rueidisConn{client: client, opts: opts, lastErr: err}
	}

	return ConnShards{conns}, nil
}

func (c *rueidisConn) createClient() rueidis.Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	client, err := rueidis.NewClient(*c.opts)
	if err != nil {
		logging.Warnw("Failed to establish rueidis client", "addr", c.opts.InitAddress[0], "error", err)
		c.lastErr = err
		c.lastErrTime = time.Now()
		return nil
	}

	logging.Debugw("Establish rueidis client", "addr", c.opts.InitAddress[0])
	c.client = client
	c.lastErr = nil
	c.lastErrTime = time.Time{}
	return c.client
}

func (c *rueidisConn) getClient() rueidis.Client {
	c.mu.RLock()
	if c.client != nil {
		if c.lastErr == nil {
			c.mu.RUnlock()
			return c.client
		}
		if time.Since(c.lastErrTime) < createClientThreshold {
			c.mu.RUnlock()
			return nil
		}
	}
	c.mu.RUnlock()

	return c.createClient()
}

func (c *rueidisConn) checkNetOpError(err error) {
	if !isNetOpError(err) {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastErrTime.IsZero() {
		c.lastErr = err
		c.lastErrTime = time.Now()
	}
}

func (c *rueidisConn) Get(ctx context.Context, name string) (string, error) {
	client := c.getClient()
	if client == nil {
		return "", c.lastErr
	}

	value, err := client.Do(ctx, client.B().Get().Key(name).Build()).ToString()
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

func (c *rueidisConn) Eval(ctx context.Context, script *Script, keys []string, args []string) (any, error) {
	client := c.getClient()
	if client == nil {
		return 0, c.lastErr
	}
	v, err := client.Do(ctx, client.B().Evalsha().Sha1(script.Hash).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()).ToAny()
	if err != nil && strings.Contains(err.Error(), "NOSCRIPT ") {
		v, err = client.Do(ctx, client.B().Eval().Script(script.Src).Numkeys(int64(len(keys))).Key(keys...).Arg(args...).Build()).ToAny()
	}
	c.checkNetOpError(err)

	return v, noErrNil(err)
}

func (c *rueidisConn) Close(ctx context.Context) error {
	client := c.getClient()
	if client == nil {
		return nil
	}
	return client.Do(ctx, client.B().Shutdown().Build()).Error()
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
	client := c.getClient()
	if client == nil {
		return []string{}, c.lastErr
	}

	vals, err := client.Do(ctx, client.B().Mget().Key(keys...).Build()).ToArray()
	c.checkNetOpError(err)

	strs := make([]string, len(vals))
	for i, v := range vals {
		strs[i], _ = v.ToString()
	}

	return strs, noErrNil(err)
}

func (c *rueidisConn) MSet(ctx context.Context, pairs ...any) (bool, error) {
	client := c.getClient()
	if client == nil {
		return false, c.lastErr
	}

	partial := client.B().Mset().KeyValue()
	for i := 0; i < len(pairs); i += 2 {
		partial = partial.KeyValue(pairs[i].(string), pairs[i+1].(string))
	}
	cmd := partial.Build()
	reply, err := client.Do(ctx, cmd).ToString()
	c.checkNetOpError(err)

	return reply == "OK", err
}

func (c *rueidisConn) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	client := c.getClient()
	if client == nil {
		return []string{}, 0, c.lastErr
	}

	resp, err := client.Do(ctx, client.B().Scan().Cursor(cursor).Match(match).Count(count).Build()).AsScanEntry()
	c.checkNetOpError(err)

	return resp.Elements, resp.Cursor, err
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
