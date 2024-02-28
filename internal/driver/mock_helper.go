package driver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/dolthub/maphash"
	"github.com/stretchr/testify/mock"

	"election-agent/internal/config"
	mockredis "election-agent/mocks/github.com/go-redsync/redsync/v4/redis"

	"github.com/go-redsync/redsync/v4"
	redsyncredis "github.com/go-redsync/redsync/v4/redis"
)

func NewMockRedisKVDriver(cfg *config.Config) *RedisKVDriver {
	pools := []redsyncredis.Pool{NewMockRedisPool(), NewMockRedisPool(), NewMockRedisPool()}
	driver := &RedisKVDriver{
		ctx:         context.TODO(),
		cfg:         cfg,
		originPools: pools,
		pools:       pools,
		quorum:      len(pools)/2 + 1,
		rs:          redsync.New(pools...),
		hasher:      maphash.NewHasher[string](),
	}

	return driver
}

func NewMockRedisPool() *mockredis.MockPool {
	type cacheItem struct {
		val    string
		active time.Time
		ttl    time.Duration
	}

	mockConn := &mockredis.MockConn{}
	mockPool := &mockredis.MockPool{}

	mockPool.On("Get", mock.Anything).Return(mockConn, nil)

	cache := sync.Map{}

	mockConn.On("Close").Return(nil)

	mockConn.On("Get", mock.AnythingOfType("string")).
		Return(func(name string) (string, error) {
			v, ok := cache.Load(name)
			if !ok {
				return "", nil
			}
			item, ok := v.(*cacheItem)
			if !ok || (item.ttl > 0 && time.Since(item.active) > item.ttl) {
				cache.Delete(name)
				return "", nil
			}

			return item.val, nil
		})

	mockConn.On("Set", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(func(name string, value string) (bool, error) {
			cache.Store(name, &cacheItem{val: value, ttl: 0})
			return true, nil
		})

	mockConn.On("SetNX", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("time.Duration")).
		Return(func(name string, value string, expiry time.Duration) (bool, error) {
			_, ok := cache.Load(name)
			if ok {
				return false, nil
			}

			cache.Store(name, &cacheItem{val: value, ttl: expiry})
			return true, nil
		})

	mockConn.On("PTTL", mock.AnythingOfType("string")).
		Return(func(name string) (time.Duration, error) {
			v, ok := cache.Load(name)
			if !ok {
				return -2, nil
			}
			item, _ := v.(*cacheItem)
			if item.ttl == 0 {
				return -1, nil
			} else if item.ttl > 0 && time.Since(item.active) > item.ttl {
				cache.Delete(name)
			}
			return time.Until(item.active.Add(item.ttl)), nil
		})

	mockConn.On("Eval", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(script *redsyncredis.Script, keysAndArgs ...any) (any, error) {
			keys := make([]string, script.KeyCount)
			args := keysAndArgs
			if script.KeyCount > 0 {
				for i := 0; i < script.KeyCount; i++ {
					keys[i], _ = keysAndArgs[i].(string)
				}
				args = keysAndArgs[script.KeyCount:]
			}

			if strings.Contains(script.Src, "DEL") {
				v, ok := cache.Load(keys[0])
				if !ok {
					return 0, nil
				}
				item, _ := v.(*cacheItem)
				if item.val != args[0].(string) {
					return 0, nil
				}
				cache.Delete(keys[0])
				return 1, nil
			} else if strings.Contains(script.Src, "PEXPIRE") {
				v, ok := cache.Load(keys[0])
				if !ok {
					return 0, nil
				}
				item, _ := v.(*cacheItem)
				if item.val != args[0].(string) {
					return 0, nil
				}
				item.ttl = time.Duration(int64(args[1].(int)) * int64(time.Millisecond))
				item.active = time.Now()
				return 1, nil
			}

			return nil, errors.New("no script")
		})
	return mockPool
}
