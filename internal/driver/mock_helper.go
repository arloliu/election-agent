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
	"election-agent/internal/driver/redlock"
)

func NewMockRedisKVDriver(cfg *config.Config) *RedisKVDriver {
	connGroup1 := redlock.ConnGroup{NewMockRedlockConn(), NewMockRedlockConn(), NewMockRedlockConn()}
	connGroup2 := redlock.ConnGroup{NewMockRedlockConn(), NewMockRedlockConn(), NewMockRedlockConn()}
	connShards := redlock.ConnShards{connGroup1, connGroup2}
	driver := &RedisKVDriver{
		ctx:        context.TODO(),
		cfg:        cfg,
		connShards: connShards,
		rlock:      redlock.New(connShards...),
		hasher:     maphash.NewHasher[string](),
	}

	return driver
}

type mockMutexConn struct {
	redlock.MockConn
	mu    sync.Mutex
	cache sync.Map
}

func NewMockRedlockConn() *mockMutexConn { //nolint:cyclop
	type cacheItem struct {
		val    string
		active time.Time
		ttl    time.Duration
	}

	mockConn := &mockMutexConn{}

	mockConn.On("WithContext", mock.Anything).Return(mockConn)

	mockConn.On("Close").Return(nil)

	mockConn.On("Get", mock.AnythingOfType("string")).
		Return(func(name string) (string, error) {
			mockConn.mu.Lock()
			defer mockConn.mu.Unlock()

			v, ok := mockConn.cache.Load(name)
			if !ok {
				return "", nil
			}
			item, ok := v.(*cacheItem)

			if !ok || (item.ttl > 0 && time.Since(item.active) > item.ttl) {
				mockConn.cache.Delete(name)
				return "", nil
			}

			return item.val, nil
		})

	mockConn.On("Set", mock.AnythingOfType("string"), mock.AnythingOfType("string")).
		Return(func(name string, value string) (bool, error) {
			mockConn.mu.Lock()
			defer mockConn.mu.Unlock()

			mockConn.cache.Store(name, &cacheItem{val: value, ttl: 0})
			return true, nil
		})

	mockConn.On("SetNX", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("time.Duration")).
		Return(func(name string, value string, expiry time.Duration) (bool, error) {
			mockConn.mu.Lock()
			defer mockConn.mu.Unlock()

			_, ok := mockConn.cache.Load(name)
			if ok {
				return false, nil
			}

			mockConn.cache.Store(name, &cacheItem{val: value, ttl: expiry})
			return true, nil
		})

	mockConn.On("PTTL", mock.AnythingOfType("string")).
		Return(func(name string) (time.Duration, error) {
			mockConn.mu.Lock()
			defer mockConn.mu.Unlock()

			v, ok := mockConn.cache.Load(name)
			if !ok {
				return -2, nil
			}
			item, _ := v.(*cacheItem)
			if item.ttl == 0 {
				return -1, nil
			} else if item.ttl > 0 && time.Since(item.active) > item.ttl {
				mockConn.cache.Delete(name)
			}
			return time.Until(item.active.Add(item.ttl)), nil
		})

	mockConn.On("Eval", mock.Anything, mock.Anything, mock.Anything).
		Return(func(script *redlock.Script, keysAndArgs ...any) (any, error) {
			mockConn.mu.Lock()
			defer mockConn.mu.Unlock()

			keys := make([]string, script.KeyCount)
			args := keysAndArgs
			if script.KeyCount > 0 {
				for i := 0; i < script.KeyCount; i++ {
					keys[i], _ = keysAndArgs[i].(string)
				}
				args = keysAndArgs[script.KeyCount:]
			}

			if strings.Contains(script.Src, "-- delete") {
				v, ok := mockConn.cache.Load(keys[0])
				if !ok {
					return int64(0), nil
				}

				item, _ := v.(*cacheItem)
				if item.val != args[0].(string) {
					return int64(0), nil
				}

				mockConn.cache.Delete(keys[0])
				return int64(1), nil
			} else if strings.Contains(script.Src, "-- acquire") || strings.Contains(script.Src, "-- touch") { // acquire or touch
				if v, ok := mockConn.cache.Load(keys[0]); ok {
					item, _ := v.(*cacheItem)
					if item.val != args[0].(string) {
						return int64(0), nil
					}

					item.ttl = time.Duration(int64(args[1].(int)) * int64(time.Millisecond))
					item.active = time.Now()
					return int64(1), nil
				} else {
					item := &cacheItem{
						val:    args[0].(string),
						ttl:    time.Duration(int64(args[1].(int)) * int64(time.Millisecond)),
						active: time.Now(),
					}
					mockConn.cache.Store(keys[0], item)
					return int64(1), nil
				}
			} else if strings.Contains(script.Src, "-- handover") {
				item := &cacheItem{
					val:    args[0].(string),
					ttl:    time.Duration(int64(args[1].(int)) * int64(time.Millisecond)),
					active: time.Now(),
				}
				mockConn.cache.Store(keys[0], item)
				return "OK", nil
			}

			return nil, errors.New("no script")
		})

	mockConn.On("MGet", mock.Anything).
		Return(func(keys ...string) ([]string, error) {
			mockConn.mu.Lock()
			defer mockConn.mu.Unlock()

			vals := make([]string, len(keys))
			for i, k := range keys {
				v, ok := mockConn.cache.Load(k)
				if !ok {
					vals[i] = ""
					continue
				}

				item, ok := v.(*cacheItem)
				if !ok || (item.ttl > 0 && time.Since(item.active) > item.ttl) {
					mockConn.cache.Delete(k)
					vals[i] = ""
					continue
				}

				vals[i] = item.val
			}
			return vals, nil
		})

	mockConn.On("MSet", mock.Anything).
		Return(func(pairs ...any) (bool, error) {
			mockConn.mu.Lock()
			defer mockConn.mu.Unlock()

			for i := 0; i < len(pairs); i += 2 {
				key, _ := pairs[i].(string)
				var val string
				switch v := pairs[i+1].(type) {
				case string:
					val = v
				case bool:
					val = redlock.BoolStr(v)
				}
				mockConn.cache.Store(key, &cacheItem{val: val, ttl: 0})
			}
			return true, nil
		})

	return mockConn
}
