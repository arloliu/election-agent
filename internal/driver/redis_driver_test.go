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

func TestRedisKVDriver_isRedisUnhealthy(t *testing.T) {
	require := require.New(t)

	conns := []redlock.Conn{nil, nil, nil}
	rd := &RedisKVDriver{
		conns: conns,
		rlock: redlock.New(conns...),
	}
	err := errors.New("error")
	require.False(rd.isRedisUnhealthy((err)))

	err = nil
	for i := 0; i < 2; i++ {
		err = multierror.Append(err, errors.New("error"))
	}
	require.False(rd.isRedisUnhealthy((err)))

	err = nil
	err = multierror.Append(err, &net.OpError{})
	require.False(rd.isRedisUnhealthy((err)))

	err = multierror.Append(err, &net.OpError{})
	require.True(rd.isRedisUnhealthy((err)))
}

func TestRedisKVDriver_mocks(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	conn := NewMockRedlockConn().NewWithContext(ctx)

	ok, err := conn.Set("key1", "val1")
	require.NoError(err)
	require.True(ok)

	v, err := conn.Get("key1")
	require.NoError(err)
	require.Equal("val1", v)

	ok, err = conn.SetNX("key1", "val2", time.Second)
	require.NoError(err)
	require.False(ok)

	ok, err = conn.SetNX("key2", "val2", time.Second)
	require.NoError(err)
	require.True(ok)

	ttl, err := conn.PTTL("key3")
	require.NoError(err)
	require.Equal(time.Duration(-2), ttl)

	ttl, err = conn.PTTL("key1")
	require.NoError(err)
	require.Equal(time.Duration(-1), ttl)

	ttl, err = conn.PTTL("key2")
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

	connSet(driver, require, "key1", "val1")
	connSet(driver, require, "key2", "val2")
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
	// should be equals to quorum because we implement fast return feature
	require.Equal(len(driver.conns)/2+1, n)

	vals, err := driver.MGet(ctx, "key1", "key2")
	require.NoError(err)
	require.Equal("val1", vals[0])
	require.Equal("val2", vals[1])
}

func connSet(driver *RedisKVDriver, require *require.Assertions, key string, val string) {
	for _, conn := range driver.conns {
		ok, err := conn.NewWithContext(driver.ctx).Set(key, val)
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

	for _, conn := range driver.conns {
		result, err := conn.NewWithContext(ctx).Ping()
		require.Error(err)
		require.False(result)

		ok, err := conn.NewWithContext(ctx).SetNX("test", "value", 10*time.Second)
		require.False(ok)
		require.Error(err)
	}

	lease := driver.NewLease("test", "", "replica1", 3*time.Second)
	require.NotNil(lease)

	err = lease.Grant(ctx)
	require.ErrorContains(err, "service unavailable")
}
