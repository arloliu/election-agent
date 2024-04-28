package driver

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"election-agent/internal/config"
	"election-agent/internal/driver/redlock"
	"election-agent/internal/logging"

	"github.com/hashicorp/go-multierror"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	config.Default = &config.Config{
		Env:      "test",
		LogLevel: "debug",
	}

	logger := logging.Init()
	if logger == nil {
		panic(errors.New("Failed to initial logger"))
	}

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestRedisKVDriver_IsUnhealthy(t *testing.T) {
	require := require.New(t)

	connShards := redlock.ConnShards{redlock.ConnGroup{nil, nil, nil}}
	rd := &RedisKVDriver{
		connShards: connShards,
		rlock:      redlock.New(connShards...),
	}
	err := errors.New("error")
	require.False(rd.IsUnhealthy((err)))

	err = nil
	for i := 0; i < 2; i++ {
		err = multierror.Append(err, errors.New("error"))
	}
	require.False(rd.IsUnhealthy((err)))

	err = nil
	err = multierror.Append(err, &net.OpError{})
	require.False(rd.IsUnhealthy((err)))

	err = multierror.Append(err, &net.OpError{})
	require.True(rd.IsUnhealthy((err)))
}

func TestRedisKVDriver_mocks(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	conn := NewMockRedlockConn()

	ok, err := conn.Set(ctx, "key1", "val1")
	require.NoError(err)
	require.True(ok)

	v, err := conn.Get(ctx, "key1")
	require.NoError(err)
	require.Equal("val1", v)

	ok, err = conn.SetNX(ctx, "key1", "val2", time.Second)
	require.NoError(err)
	require.False(ok)

	ok, err = conn.SetNX(ctx, "key2", "val2", time.Second)
	require.NoError(err)
	require.True(ok)

	ttl, err := conn.PTTL(ctx, "key3")
	require.NoError(err)
	require.Equal(time.Duration(-2), ttl)

	ttl, err = conn.PTTL(ctx, "key1")
	require.NoError(err)
	require.Equal(time.Duration(-1), ttl)

	ttl, err = conn.PTTL(ctx, "key2")
	require.NoError(err)
	require.LessOrEqual(ttl, time.Second)
}

func TestRedisKVDriver_Get(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	driver := NewMockRedisKVDriver(&config.Config{})

	connSet(driver, require, "key1", "val1")
	val, err := driver.Get(ctx, "key1")
	require.NoError(err)
	require.Equal("val1", val)

	val, err = driver.Get(ctx, "key2")
	require.NoError(err)
	require.Equal("", val)
}

func TestRedisKVDriver_MGet(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	driver := NewMockRedisKVDriver(&config.Config{})

	masterConnSet(driver, require, "key1", "val1")
	masterConnSet(driver, require, "key2", "val2")
	vals, err := driver.MGet(ctx, "key1", "key2")
	require.NoError(err)
	require.Equal("val1", vals[0])
	require.Equal("val2", vals[1])
}

func TestRedisKVDriver_MSet(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	driver := NewMockRedisKVDriver(&config.Config{})

	n, err := driver.MSet(ctx, "key1", "val1", "key2", "val2")
	require.NoError(err)
	require.Equal(len(driver.connShards[0]), n)

	vals, err := driver.MGet(ctx, "key1", "key2")
	require.NoError(err)
	require.Equal("val1", vals[0])
	require.Equal("val2", vals[1])
}

func connSet(driver *RedisKVDriver, require *require.Assertions, key string, val string) {
	for _, conn := range driver.connShards.Conns(key) {
		ok, err := conn.Set(driver.ctx, key, val)
		require.NoError(err)
		require.True(ok)
	}
}

func masterConnSet(driver *RedisKVDriver, require *require.Assertions, key string, val string) {
	for _, conn := range driver.connShards[0] {
		ok, err := conn.Set(driver.ctx, key, val)
		require.NoError(err)
		require.True(ok)
	}
}

func TestRedisKVDriver_server_nonexist(t *testing.T) {
	require := require.New(t)

	ctx := context.TODO()
	cfg := config.Config{
		Redis: config.RedisConfig{
			Mode: "single",
			URLs: []string{"redis://noneexist:7000", "redis://noneexist:7001", "redis://noneexist:7002"},
		},
	}

	driver, err := NewRedisKVDriver(ctx, &cfg)
	require.NoError(err)
	require.NotNil(driver)

	for _, connShards := range driver.connShards {
		require.LessOrEqual(1, len(connShards))
		conn := connShards[0]
		result, err := conn.Ping(ctx)
		require.Error(err)
		require.False(result)

		ok, err := conn.SetNX(ctx, "test", "value", 10*time.Second)
		require.False(ok)
		require.Error(err)
	}

	mutex := driver.NewMutex("test", "", "replica1", 3*time.Second)
	require.NotNil(mutex)

	err = mutex.TryLockContext(ctx)
	require.ErrorContains(err, "noneexist")
}
