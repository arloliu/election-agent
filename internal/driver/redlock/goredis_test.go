//go:build goredis

package redlock

import (
	"election-agent/internal/config"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGoredis_parseRedisURLs_single(t *testing.T) {
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

func TestGoredis_parseRedisURLs_cluster(t *testing.T) {
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
