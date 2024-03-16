package driver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"testing"
	"time"

	"election-agent/internal/config"
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

func TestRedisKVDriver_parseRedisURLs_single(t *testing.T) {
	require := require.New(t)
	cfg := config.Config{
		Redis: config.RedisConfig{
			Mode: "single",
			URLs: []string{"redis://user:pass@rs1:6789/1", "redis://user:pass@rs2:6790/2", "redis://user:pass@rs3:6791/3"},
		},
	}

	opts, err := parseRedisURLs(&cfg)
	require.NoError(err)
	require.NotNil(opts)
	require.Len(opts, 3)

	for i, o := range opts {
		require.Len(o.Addrs, 1)
		require.Equal([]string{fmt.Sprintf("rs%d:%d", i+1, 6789+i)}, o.Addrs)
		require.Equal("user", o.Username)
		require.Equal("pass", o.Password)
		require.Equal(o.DB, i+1)
	}
}

func TestRedisKVDriver_parseRedisURLs_cluster(t *testing.T) {
	require := require.New(t)
	cfg := config.Config{
		Redis: config.RedisConfig{
			Mode: "cluster",
			URLs: []string{
				"redis://user:pass@c1r1:6379?addr=c1r2:6379&addr=c1r3:6379",
				"redis://user:pass@c2r1:6379?addr=c2r2:6379&addr=c2r3:6379",
				"redis://user:pass@c3r1:6379?addr=c3r2:6379&addr=c3r3:6379",
			},
		},
	}

	opts, err := parseRedisURLs(&cfg)
	require.NoError(err)
	require.NotNil(opts)
	require.Len(opts, 3)

	for i, o := range opts {
		require.Len(o.Addrs, 3)
		for j, addr := range o.Addrs {
			require.Equal(fmt.Sprintf("c%dr%d:6379", i+1, j+1), addr)
		}
		require.Equal("user", o.Username)
		require.Equal("pass", o.Password)
	}
}

func TestRedisKVDriver_isRedisUnhealthy(t *testing.T) {
	require := require.New(t)

	rd := &RedisKVDriver{
		pools:  []RedisPool{nil, nil, nil},
		quorum: 2,
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

func TestRedisKVDriver_getMostFreqVal(t *testing.T) {
	require := require.New(t)

	strs := make([]string, 0, 1000)
	for i := 0; i < 400; i++ {
		strs = append(strs, "item1")
	}
	for i := 0; i < 600; i++ {
		strs = append(strs, "item2")
	}

	rand.Shuffle(len(strs), func(i, j int) {
		strs[i], strs[j] = strs[j], strs[i]
	})

	val, freq := getMostFreqVal(strs)
	require.Equal("item2", val)
	require.Equal(600, freq)

	strs = make([]string, 0, 5)
	for i := 0; i < 3; i++ {
		strs = append(strs, "")
	}
	for i := 0; i < 2; i++ {
		strs = append(strs, "item")
	}

	val, freq = getMostFreqVal(strs)
	require.Equal("", val)
	require.Equal(3, freq)
}

func TestRedisKVDriver_mocks(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	mockPool := NewMockRedisPoolWithConn()

	conn, _ := mockPool.Get(ctx)
	require.NotNil(conn)

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

func TestRedisKVDriver_redisGetAsync(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	driver := NewMockRedisKVDriver(&config.Config{})

	poolSet(driver, require, "key1", "val1")
	val, err := driver.redisGetAsync(ctx, "key1", true)
	require.NoError(err)
	require.Equal("val1", val)

	val, err = driver.redisGetAsync(ctx, "key2", true)
	require.NoError(err)
	require.Equal("", val)
}

func TestRedisKVDriver_redisMGetAsync(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	driver := NewMockRedisKVDriver(&config.Config{})

	poolSet(driver, require, "key1", "val1")
	poolSet(driver, require, "key2", "val2")
	vals, err := driver.redisMGetAsync(ctx, "key1", "key2")
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
	require.Equal(3, n)

	vals, err := driver.redisMGetAsync(ctx, "key1", "key2")
	require.NoError(err)
	require.Equal("val1", vals[0])
	require.Equal("val2", vals[1])
}

func poolSet(driver *RedisKVDriver, require *require.Assertions, key string, val string) {
	for _, pool := range driver.pools {
		conn, err := pool.Get(driver.ctx)
		require.NoError(err)
		require.NotNil(conn)
		ok, err := conn.Set(key, val)
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

	for _, client := range driver.clients {
		_, err := client.Ping(ctx).Result()
		require.Error(err)

		ok, err := client.SetNX(ctx, "test", "value", 10*time.Second).Result()
		require.Error(err)
		require.False(ok)
	}

	lease := driver.NewLease("test", "replica1", 3*time.Second)
	require.NotNil(lease)

	err = lease.Grant(ctx)
	require.ErrorContains(err, "service unavailable")
}
