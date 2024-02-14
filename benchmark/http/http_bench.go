package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"election-agent/internal/api"
	"election-agent/internal/config"
	"election-agent/internal/logging"
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

	flag.StringVar(&host, "h", "http://localhost:8081", "The HTTP service address")
	flag.IntVar(&concurrency, "c", 50, "The number of concurrent clients")
	flag.IntVar(&iterations, "i", 1000, "The number of iterations per client")
}

func main() {
	flag.Parse()

	ctx := context.Background()
	fmt.Printf("Start benchmarking, %d clients, %d iterations per client...\n", concurrency, iterations)

	var wg sync.WaitGroup
	wg.Add(concurrency)
	start := time.Now()
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			url := fmt.Sprintf("%s/election/bench_election%d", host, idx)
			name := fmt.Sprintf("client%d", idx)
			cbody := []byte(fmt.Sprintf(`{"candidate":"%s","term":1000}`, name))
			ebody := []byte(fmt.Sprintf(`{"leader":"%s","term":1000}`, name))
			for j := 0; j < iterations; j++ {
				if j == 0 {
					creq, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(cbody))
					resp, err := http.DefaultClient.Do(creq)
					if err != nil {
						logging.Fatalw("campaign fail", "idx", idx, "iter", j, "err", err, "resp", resp)
					}
					body, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					result := api.CampaignResult{}
					_ = json.Unmarshal(body, &result)
					if !result.Elected || result.Leader != name {
						logging.Fatalw("campaign fail", "idx", idx, "iter", j, "err", err, "result", result)
					}
				} else {
					ereq, _ := http.NewRequestWithContext(ctx, http.MethodPatch, url, bytes.NewBuffer(ebody))
					resp, err := http.DefaultClient.Do(ereq)
					if err != nil {
						logging.Fatalw("extend fail", "idx", idx, "iter", j, "err", err)
					}
					resp.Body.Close()
					if resp.StatusCode != http.StatusOK {
						logging.Fatalw("extend fail", "idx", idx, "iter", j)
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
}
