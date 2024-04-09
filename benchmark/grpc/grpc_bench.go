package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"election-agent/benchmark/bench"
	"election-agent/internal/config"
	"election-agent/internal/logging"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	eagrpc "election-agent/proto/election_agent/v1"
)

var (
	host        string
	concurrency int
	iterations  int
)

func init() {
	if err := config.Init(); err != nil {
		logging.Fatalw("Failed to initiliaze configuration", "error", err)
		os.Exit(1)
	}

	logging.Init()

	flag.StringVar(&host, "h", "localhost:8080", "The gRPC target address")
	flag.IntVar(&concurrency, "c", 50, "The number of concurrent clients")
	flag.IntVar(&iterations, "i", 1000, "The number of iterations per client")
}

func main() {
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	client := newBenchmarkClient(ctx, host, concurrency)
	benchmark := bench.NewBenchmark(ctx, cancel, client, concurrency, iterations)

	benchmark.Start()
	benchmark.Stop()
}

type grpcBenchmarkClient struct {
	ctx     context.Context
	n       int
	target  string
	conns   []*grpc.ClientConn
	clients []eagrpc.ElectionClient
	creqs1  []*eagrpc.CampaignRequest
	creqs2  []*eagrpc.CampaignRequest
	ereqs   []*eagrpc.ExtendElectedTermRequest
}

func newBenchmarkClient(ctx context.Context, host string, n int) *grpcBenchmarkClient {
	client := &grpcBenchmarkClient{
		ctx:     ctx,
		n:       n,
		target:  host,
		conns:   make([]*grpc.ClientConn, n),
		clients: make([]eagrpc.ElectionClient, n),
		creqs1:  make([]*eagrpc.CampaignRequest, n),
		creqs2:  make([]*eagrpc.CampaignRequest, n),
		ereqs:   make([]*eagrpc.ExtendElectedTermRequest, n),
	}

	fmt.Printf("Establishing %d gRPC client connections to target %s\n", n, client.target)
	for i := 0; i < n; i++ {
		client.creqs1[i] = &eagrpc.CampaignRequest{
			Election:  fmt.Sprintf("bench_election%d", i),
			Candidate: fmt.Sprintf("bench_client%d", i),
			Term:      bench.BenchmarkTTL,
		}
		client.creqs2[i] = &eagrpc.CampaignRequest{
			Election:  fmt.Sprintf("bench_election%d", i),
			Candidate: fmt.Sprintf("bench_client%d_other", i),
			Term:      bench.BenchmarkTTL,
		}
		client.ereqs[i] = &eagrpc.ExtendElectedTermRequest{
			Election: fmt.Sprintf("bench_election%d", i),
			Leader:   fmt.Sprintf("bench_client%d", i),
			Term:     bench.BenchmarkTTL,
			Retries:  3,
		}
	}

	return client
}

func (c *grpcBenchmarkClient) Setup() error {
	errs, ctx := errgroup.WithContext(c.ctx)
	for i := 0; i < c.n; i++ {
		idx := i
		errs.Go(func() error {
			conn, err := grpc.DialContext(ctx, c.target,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
			)
			if err != nil {
				return err
			}

			c.conns[idx] = conn
			c.clients[idx] = eagrpc.NewElectionClient(conn)

			return nil
		})
	}

	if err := errs.Wait(); err != nil {
		return err
	}

	return nil
}

func (c *grpcBenchmarkClient) Quit(idx int) {
	client := c.clients[idx]
	rreq := &eagrpc.ResignRequest{Election: fmt.Sprintf("bench_election%d", idx), Leader: fmt.Sprintf("bench_client%d", idx)}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	val, err := client.Resign(ctx, rreq)
	if err != nil {
		fmt.Printf("Client %d resign got error: %s\n", idx, err.Error())
	}
	if val != nil && !val.Value {
		fmt.Printf("Client %d resign failed\n", idx)
	}
	_ = c.conns[idx].Close()
}

func (c *grpcBenchmarkClient) CampaignSuccess(idx int, iteration int) error {
	client := c.clients[idx]
	req := c.creqs1[idx]
	ret, err := client.Campaign(c.ctx, req)
	if err != nil {
		code := status.Code(err)
		if code == codes.Canceled {
			return nil
		}
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), got error: %w", idx, iteration, err)
	}

	if !ret.Elected {
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), expected to successed\n", idx, iteration)
	}

	return nil
}

func (c *grpcBenchmarkClient) CampaignFail(idx int, iteration int) error {
	client := c.clients[idx]
	req := c.creqs2[idx]
	ret, err := client.Campaign(c.ctx, req)
	if err != nil {
		code := status.Code(err)
		if code == codes.Canceled {
			return nil
		}
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), got error: %w", idx, iteration, err)
	}
	if ret.Elected {
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), expected to fail\n", idx, iteration)
	}

	return nil
}

func (c *grpcBenchmarkClient) ExtendElectedTerm(idx int, iteration int) error {
	client := c.clients[idx]
	req := c.ereqs[idx]

	ret, err := client.ExtendElectedTerm(c.ctx, req)
	if err != nil {
		code := status.Code(err)
		if code == codes.Canceled {
			return err
		}
		return fmt.Errorf("ExtendElectedTerm failed(client: %d, iteration: %d), got error: %w", idx, iteration, err)
	}

	if !ret.Value {
		return fmt.Errorf("ExtendElectedTerm failed(client: %d, iteration: %d)", idx, iteration)
	}

	return nil
}
