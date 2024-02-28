package api

import (
	"context"
	"testing"

	"election-agent/internal/config"
	"election-agent/internal/logging"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	eagrpc "election-agent/proto/election_agent/v1"
)

func TestGRPCService(t *testing.T) {
	require := require.New(t)
	ctx := context.TODO()
	cfg := config.Config{
		Env:          "test",
		LogLevel:     "debug",
		Name:         "test_election_agent",
		DefaultState: "active",
		KeyPrefix:    "test_agent",
		GRPC:         config.GRPCConfig{Enable: true, Port: 18080},
		HTTP:         config.HTTPConfig{Enable: false},
		Lease: config.LeaseConfig{
			CacheSize: 8192,
		},
		Zone: config.ZoneConfig{
			Enable: false,
			Name:   "zone1",
		},
	}

	server, err := startMockServer(ctx, &cfg)
	require.NoError(err)
	require.NotNil(server)
	defer server.Shutdown(ctx) //nolint:errcheck

	conn, err := grpc.Dial("127.0.0.0:18080", grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(err)
	require.NotNil(conn)
	defer conn.Close()

	require.NoError(err)
	client := eagrpc.NewElectionClient(conn)

	cReq1 := &eagrpc.CampaignRequest{Election: "election1", Candidate: "client1", Term: 4000}
	eReq1 := &eagrpc.ExtendElectedTermRequest{Election: "election1", Leader: "client1", Term: 4000}
	cReq2 := &eagrpc.CampaignRequest{Election: "election1", Candidate: "client2", Term: 4000}
	campaignResult, err := client.Campaign(ctx, cReq1)
	require.NoError(err)
	require.Equal(true, campaignResult.Elected)

	for i := 0; i < 100; i++ {
		campaignResult, err := client.Campaign(ctx, cReq2)
		require.NoError(err)
		require.Equal(false, campaignResult.Elected)

		extendResult, err := client.ExtendElectedTerm(ctx, eReq1)
		require.NoError(err)
		require.Equal(true, extendResult.Value)

		leaderReq, err := client.GetLeader(ctx, &eagrpc.GetLeaderRequest{Election: "election1"})
		require.NoError(err)
		require.Equal("client1", leaderReq.Value)
	}

	rReq1 := &eagrpc.ResignRequest{Election: "election1", Leader: "client1"}
	resignResult, err := client.Resign(ctx, rReq1)
	require.NoError(err)
	require.True(resignResult.Value)

	campaignResult, err = client.Campaign(ctx, cReq2)
	require.NoError(err)
	require.Equal(true, campaignResult.Elected)

	campaignResult, err = client.Campaign(ctx, cReq1)
	require.NoError(err)
	require.Equal(false, campaignResult.Elected)
}

func BenchmarkGRPCService(b *testing.B) {
	ctx := context.TODO()
	cfg := config.Config{
		Env:       "test",
		LogLevel:  "warning",
		KeyPrefix: "test_agent",
		GRPC:      config.GRPCConfig{Enable: true, Port: 18080},
		Lease: config.LeaseConfig{
			CacheSize: 8192,
		},
	}
	config.Default = &cfg
	logging.Init()

	server, err := startMockServer(ctx, &cfg)
	if err != nil {
		b.FailNow()
	}

	defer server.Shutdown(ctx) //nolint:errcheck

	conn, err := grpc.Dial("127.0.0.0:18080", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		b.FailNow()
	}

	client := eagrpc.NewElectionClient(conn)
	req := &eagrpc.CampaignRequest{Election: "bechmark_election", Candidate: "client1", Term: 1000}

	b.ResetTimer()
	for i := 0; i <= b.N; i++ {
		_, err := client.Campaign(ctx, req)
		if err != nil {
			b.FailNow()
		}
	}
	b.StopTimer()
}
