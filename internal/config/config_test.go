package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestConfigFromEnv(t *testing.T) {
	require := require.New(t)
	t.Setenv("EA_ENV", "development")
	t.Setenv("EA_LOG_LEVEL", "debug")
	t.Setenv("EA_NAME", "test")
	t.Setenv("EA_KEY_PREFIX", "test")
	t.Setenv("EA_DEFAULT_STATE", "standby")
	t.Setenv("EA_STATE_CACHE_TTL", "1s")

	t.Setenv("EA_KUBE_ENABLE", "false")
	t.Setenv("EA_KUBE_IN_CLUSTER", "false")

	t.Setenv("EA_GRPC_ENABLE", "false")
	t.Setenv("EA_GRPC_PORT", "1234")

	t.Setenv("EA_HTTP_ENABLE", "false")
	t.Setenv("EA_HTTP_PORT", "2345")

	t.Setenv("EA_METRIC_ENABLE", "false")
	t.Setenv("EA_METRIC_REQUEST_DURATION_BUCKETS", "0.01,0.02,0.03,0.04")

	t.Setenv("EA_LEASE_CACHE", "true")
	t.Setenv("EA_LEASE_CACHE_SIZE", "1234")
	t.Setenv("EA_LEASE_TIMEOUT", "1200ms")

	t.Setenv("EA_ZONE_ENABLE", "true")
	t.Setenv("EA_ZONE_NAME", "zone1")
	t.Setenv("EA_ZONE_STATE_KEY_PREFIX", "test")
	t.Setenv("EA_ZONE_CHECK_INTERVAL", "1s")
	t.Setenv("EA_ZONE_CHECK_TIMEOUT", "1s")
	t.Setenv("EA_ZONE_COORDINATOR_URL", "localhost:9000")
	t.Setenv("EA_ZONE_COORDINATOR_TIMEOUT", "3s")
	t.Setenv("EA_ZONE_PEER_URLS", "localhost:8082")
	t.Setenv("EA_ZONE_PEER_TIMEOUT", "500ms")

	t.Setenv("EA_REDIS_MODE", "cluster")
	t.Setenv("EA_REDIS_URLS", "redis://c1r1?addr=c1r2&addr=c1r3,redis://c2r1?addr=c2r2&addr=c2r3,redis://c3r1?addr=c3r2&addr=c3r3")
	t.Setenv("EA_REDIS_PRIMARY", "1")
	t.Setenv("EA_REDIS_OPERATION_TIMEOUT", "1s")
	t.Setenv("EA_REDIS_MASTER", "test")

	err := Init()
	require.NoError(err)

	verifyConfig(t)
}

func TestConfigFromYAML(t *testing.T) {
	yamlData := `
env: development
log_level: debug
name: test
key_prefix: test
default_state: standby
state_cache_ttl: 1s

kube:
    enable: false
    in_cluster: false

grpc:
    enable: false
    port: 1234

http:
    enable: false
    port: 2345

metric:
    enable: false
    request_duration_buckets:
        - 0.01
        - 0.02
        - 0.03
        - 0.04

lease:
    cache: true
    cache_size: 1234
    lease_timeout: 1200ms

zone:
    enable: true
    name: zone1
    state_key_prefix: test
    check_interval: 1s
    check_timeout: 1s
    coordinator_url: localhost:9000
    coordinator_timeout: 3s
    peer_urls:
        - localhost:8082
    peer_timeout: 500ms

redis:
    mode: cluster
    urls:
        - redis://c1r1?addr=c1r2&addr=c1r3
        - redis://c2r1?addr=c2r2&addr=c2r3
        - redis://c3r1?addr=c3r2&addr=c3r3
    primary: 1
    operation_timeout: 1s
    master: test
`

	require := require.New(t)

	file, err := os.CreateTemp("", "test_config")
	require.NoError(err)
	require.NotNil(file)

	_, err = file.Write([]byte(yamlData))
	require.NoError(err)

	t.Setenv("EA_CONFIG_FILE", file.Name())

	err = Init()
	require.NoError(err)

	verifyConfig(t)
}

func verifyConfig(t *testing.T) {
	require := require.New(t)

	cfg := GetDefault()

	require.Equal("development", cfg.Env)
	require.Equal("debug", cfg.LogLevel)
	require.Equal("test", cfg.Name)
	require.Equal("test", cfg.KeyPrefix)
	require.Equal("standby", cfg.DefaultState)
	require.Equal(time.Second, cfg.StateCacheTTL)

	require.False(cfg.Kube.Enable)
	require.False(cfg.Kube.InCluster)

	require.False(cfg.GRPC.Enable)
	require.Equal(1234, cfg.GRPC.Port)

	require.False(cfg.HTTP.Enable)
	require.Equal(2345, cfg.HTTP.Port)

	require.False(cfg.Metric.Enable)
	require.Equal([]float64{0.01, 0.02, 0.03, 0.04}, cfg.Metric.RequestDurationBuckets)

	require.Equal("cluster", cfg.Redis.Mode)
	require.Equal([]string{"redis://c1r1?addr=c1r2&addr=c1r3", "redis://c2r1?addr=c2r2&addr=c2r3", "redis://c3r1?addr=c3r2&addr=c3r3"}, cfg.Redis.URLs)

	require.True(cfg.Lease.Cache)
	require.Equal(1234, cfg.Lease.CacheSize)

	require.True(cfg.Zone.Enable)
	require.Equal("zone1", cfg.Zone.Name)
	require.Equal("test", cfg.Zone.StateKeyPrefix)
	require.Equal(time.Second, cfg.Zone.CheckInterval)
	require.Equal(time.Second, cfg.Zone.CheckTimeout)
	require.Equal("localhost:9000", cfg.Zone.CoordinatorURL)
	require.Equal(3*time.Second, cfg.Zone.CoordinatorTimeout)
	require.Equal([]string{"localhost:8082"}, cfg.Zone.PeerURLs)
	require.Equal(500*time.Millisecond, cfg.Zone.PeerTimeout)
}
