package driver

import (
	"context"
	"strings"
	"time"

	redsyncredis "github.com/go-redsync/redsync/v4/redis"
	"github.com/redis/go-redis/v9"
)

// redisPool implements `redsyncredis.Pool` interface
type redisPool struct {
	delegate redis.UniversalClient
}

func (p *redisPool) Get(ctx context.Context) (redsyncredis.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return RedisConn(&redisConn{p.delegate, ctx}), nil
}

func NewRedisPool(delegate redis.UniversalClient) RedisPool {
	return &redisPool{delegate}
}

// RedisPool is the wrapper interface of redsyncredis.Pool`
type RedisPool interface {
	redsyncredis.Pool
}

// RedisConn extends `redsyncredis.Conn` interface. it's for mocking purpose
type RedisConn interface {
	redsyncredis.Conn
	Ping() (bool, error)
	MGet(keys ...string) ([]string, error)
	MSet(pairs ...any) (bool, error)
}

// redisConn implements `redsyncredis.Conn` and `RedisConn` interfaces
type redisConn struct {
	delegate redis.UniversalClient
	ctx      context.Context
}

func (c *redisConn) Get(name string) (string, error) {
	value, err := c.delegate.Get(c.ctx, name).Result()
	return value, noErrNil(err)
}

func (c *redisConn) Set(name string, value string) (bool, error) {
	reply, err := c.delegate.Set(c.ctx, name, value, 0).Result()
	return reply == "OK", err
}

func (c *redisConn) SetNX(name string, value string, expiry time.Duration) (bool, error) {
	return c.delegate.SetNX(c.ctx, name, value, expiry).Result()
}

func (c *redisConn) PTTL(name string) (time.Duration, error) {
	return c.delegate.PTTL(c.ctx, name).Result()
}

func (c *redisConn) Eval(script *redsyncredis.Script, keysAndArgs ...interface{}) (interface{}, error) {
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

func (c *redisConn) Close() error {
	return nil
}

func (c *redisConn) Ping() (bool, error) {
	value, err := c.delegate.Ping(c.ctx).Result()
	return value == "PONG", err
}

func (c *redisConn) MGet(keys ...string) ([]string, error) {
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

func (c *redisConn) MSet(pairs ...any) (bool, error) {
	reply, err := c.delegate.MSet(c.ctx, pairs...).Result()
	return reply == "OK", err
}
