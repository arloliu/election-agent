package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	require := require.New(t)
	t.Setenv("EA_ENV", "development")
	t.Setenv("EA_LOG_LEVEL", "debug")

	t.Setenv("EA_GRPC_ENABLE", "false")
	t.Setenv("EA_GRPC_PORT", "1234")

	t.Setenv("EA_HTTP_ENABLE", "false")
	t.Setenv("EA_HTTP_PORT", "2345")

	t.Setenv("EA_REDIS_MODE", "cluster")
	t.Setenv("EA_REDIS_URLS", "redis://c1r1?addr=c1r2&addr=c1r3,redis://c2r1?addr=c2r2&addr=c2r3,redis://c3r1?addr=c3r2&addr=c3r3")

	t.Setenv("EA_LEASE_CACHE", "true")
	t.Setenv("EA_LEASE_CACHE_SIZE", "1234")
	t.Setenv("EA_LEASE_TIMEOUT", "1200ms")

	t.Setenv("EA_KUBE_IN_CLUSTER", "false")

	err := Init()
	require.NoError(err)

	cfg := GetDefault()

	require.Equal("development", cfg.Env)
	require.Equal("debug", cfg.LogLevel)

	require.Equal(false, cfg.GRPC.Enable)
	require.Equal(1234, cfg.GRPC.Port)

	require.Equal(false, cfg.HTTP.Enable)
	require.Equal(2345, cfg.HTTP.Port)

	require.Equal("cluster", cfg.Redis.Mode)
	require.Equal([]string{"redis://c1r1?addr=c1r2&addr=c1r3", "redis://c2r1?addr=c2r2&addr=c2r3", "redis://c3r1?addr=c3r2&addr=c3r3"}, cfg.Redis.URLs)

	require.Equal(true, cfg.Lease.Cache)
	require.Equal(1234, cfg.Lease.CacheSize)

	require.Equal(false, cfg.Kube.InCluster)
}
