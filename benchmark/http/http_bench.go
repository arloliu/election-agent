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
	"time"

	"election-agent/benchmark/bench"
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
	ctx, cancel := context.WithCancel(context.Background())
	client := newBenchmarkClient(ctx, host, concurrency)
	benchmark := bench.NewBenchmark(ctx, cancel, client, concurrency, iterations)

	benchmark.Start()
	benchmark.Stop()
}

type httpBenchmarkClient struct {
	ctx     context.Context
	n       int
	client  *http.Client
	reqURLs []string
	creqs1  [][]byte
	creqs2  [][]byte
	ereqs   [][]byte
}

func newBenchmarkClient(ctx context.Context, host string, n int) *httpBenchmarkClient {
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:     n,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     60 * time.Second,
		},
	}

	client := &httpBenchmarkClient{
		ctx:     ctx,
		n:       n,
		client:  httpClient,
		reqURLs: make([]string, n),
		creqs1:  make([][]byte, n),
		creqs2:  make([][]byte, n),
		ereqs:   make([][]byte, n),
	}

	for i := 0; i < n; i++ {
		client.reqURLs[i] = fmt.Sprintf("%s/election/bench_election%d", host, i)
		client.creqs1[i] = []byte(fmt.Sprintf(`{"candidate":"bench_client%d","term":%d}`, i, bench.BenchmarkTTL))
		client.creqs2[i] = []byte(fmt.Sprintf(`{"candidate":"bench_client%d_other","term":%d}`, i, bench.BenchmarkTTL))
		client.ereqs[i] = []byte(fmt.Sprintf(`{"leader":"bench_client%d","term":%d}`, i, bench.BenchmarkTTL))
	}
	return client
}

func (c *httpBenchmarkClient) Setup() error {
	return nil
}

func (c *httpBenchmarkClient) Quit(idx int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	reqBody := []byte(fmt.Sprintf(`{"leader":"bench_client%d"}`, idx))
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, c.reqURLs[idx], bytes.NewBuffer(reqBody))
	resp, err := c.client.Do(req)
	if err != nil {
		fmt.Printf("Client %d resign got error: %s\n", idx, err.Error())
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errResp := api.ErrorResponse{}
		_ = json.Unmarshal(body, &errResp)
		fmt.Printf("Client %d resign failed, status code=%d, resp=%s\n", idx, resp.StatusCode, errResp.Message)
	}
}

func (c *httpBenchmarkClient) CampaignSuccess(idx int, iteration int) error {
	reqBody := c.creqs1[idx]
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodPost, c.reqURLs[idx], bytes.NewBuffer(reqBody))
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), got error: %w", idx, iteration, err)
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	result := api.CampaignResult{}
	_ = json.Unmarshal(body, &result)
	if !result.Elected {
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), expected to successed\n", idx, iteration)
	}

	return nil
}

func (c *httpBenchmarkClient) CampaignFail(idx int, iteration int) error {
	reqBody := c.creqs2[idx]
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodPost, c.reqURLs[idx], bytes.NewBuffer(reqBody))
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), got error: %w", idx, iteration, err)
	}

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	result := api.CampaignResult{}
	_ = json.Unmarshal(body, &result)
	if result.Elected {
		return fmt.Errorf("Campaign failed(client: %d, iteration: %d), expected to fail\n", idx, iteration)
	}

	return nil
}

func (c *httpBenchmarkClient) ExtendElectedTerm(idx int, iteration int) error {
	reqBody := c.ereqs[idx]
	req, _ := http.NewRequestWithContext(c.ctx, http.MethodPatch, c.reqURLs[idx], bytes.NewBuffer(reqBody))
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("ExtendElectedTerm failed(client: %d, iteration: %d), got error: %w", idx, iteration, err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ExtendElectedTerm failed(client: %d, iteration: %d)", idx, iteration)
	}

	return nil
}
