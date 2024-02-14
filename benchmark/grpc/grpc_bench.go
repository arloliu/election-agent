package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"election-agent/internal/config"
	"election-agent/internal/logging"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

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

	flag.StringVar(&host, "h", "localhost:8080", "The gRPC service address")
	flag.IntVar(&concurrency, "c", 50, "The number of concurrent clients")
	flag.IntVar(&iterations, "i", 1000, "The number of iterations per client")
}

func main() {
	flag.Parse()

	ctx := context.Background()

	var wg sync.WaitGroup
	conns := make([]*grpc.ClientConn, concurrency)
	clients := make([]eagrpc.ElectionClient, concurrency)

	fmt.Printf("Start creating gRPC %d clients to server %s...\n", concurrency, host)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			conn, err := grpc.Dial(host, grpc.WithTransportCredentials(insecure.NewCredentials()))
			if err != nil {
				logging.Fatalw("Connect to grpc server fail", "err", err.Error())
				return
			}
			conns[idx] = conn
			clients[idx] = eagrpc.NewElectionClient(conn)
			wg.Done()
		}(i)
	}
	wg.Wait()

	fmt.Printf("Start benchmarking, %d iterations per client...\n", iterations)
	wg.Add(concurrency)
	start := time.Now()
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			client := clients[idx]
			creq := &eagrpc.CampaignRequest{Election: fmt.Sprintf("election%d", idx), Candidate: fmt.Sprintf("client%d", idx), Term: 3000}
			ereq := &eagrpc.ExtendElectedTermRequest{Election: fmt.Sprintf("election%d", idx), Leader: fmt.Sprintf("client%d", idx), Term: 3000}
			for j := 0; j < iterations; j++ {
				if j == 0 {
					ret, err := client.Campaign(ctx, creq)
					if err != nil {
						logging.Fatalw("Campaign failed", "req", creq, "ret", ret, "iter", j, "err", err)
						os.Exit(1)
					}
					if !ret.Elected {
						logging.Fatalw("Campaign failed", "req", creq, "ret", ret, "iter", j)
						os.Exit(1)
					}
				} else {
					ret, err := client.ExtendElectedTerm(ctx, ereq)
					if err != nil || !ret.Value {
						logging.Fatalw("ExtendElectedTerm failed", "req", ereq, "iter", j, "err", err.Error())
						os.Exit(1)
					}
				}
			}
			wg.Done()
		}(i)
	}
	wg.Wait()

	elapsed := time.Since(start)
	rps := int64(concurrency*iterations*1000) / elapsed.Milliseconds()
	fmt.Printf("Elasped: %s, RPS: %d reqs/sec\n", elapsed, rps)

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			client := clients[idx]
			rreq := &eagrpc.ResignRequest{Election: fmt.Sprintf("election%d", idx), Leader: fmt.Sprintf("client%d", idx)}
			_, _ = client.Resign(ctx, rreq)
			_ = conns[idx].Close()
			wg.Done()
		}(i)
	}
	wg.Wait()
}
