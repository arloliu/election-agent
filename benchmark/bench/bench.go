package bench

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"
)

const BenchmarkTTL int32 = 3000

type BenchmarkClient interface {
	Setup() error
	Quit(idx int)
	CampaignSuccess(idx int, iteration int) error
	CampaignFail(idx int, iteration int) error
	ExtendElectedTerm(idx int, iteration int) error
}

type Benchmark struct {
	ctx         context.Context
	cancel      context.CancelFunc
	client      BenchmarkClient
	count       atomic.Int32
	start       time.Time
	last        time.Time
	ticker      *time.Ticker
	lastExtern  []time.Time
	concurrency int
	iterations  int
}

func NewBenchmark(ctx context.Context, cancel context.CancelFunc, client BenchmarkClient, concurrency int, iterations int) *Benchmark {
	return &Benchmark{
		ctx:         ctx,
		cancel:      cancel,
		client:      client,
		concurrency: concurrency,
		iterations:  iterations,
		lastExtern:  make([]time.Time, concurrency),
	}
}

func (b *Benchmark) Start() {
	b.ticker = time.NewTicker(3 * time.Second)
	ch := make(chan error, 1)
	exitSig := make(chan os.Signal, 1)
	signal.Notify(exitSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		err := b.startAsync()
		ch <- err
	}()

	for {
		select {
		case <-b.ticker.C:
			b.reportRPS(false)

		case err := <-ch:
			if err != nil {
				fmt.Printf("Benchmark got err: %s\n", err.Error())
			}
			b.ticker.Stop()
			b.cancel()
			return

		case <-exitSig:
			b.ticker.Stop()
			b.cancel()
			return
		}
	}
}

func (b *Benchmark) startAsync() error {
	err := b.client.Setup()
	if err != nil {
		return err
	}

	fmt.Printf("Start benchmarking, %d clients, %d iterations per client...\n", b.concurrency, b.iterations)

	errs, _ := errgroup.WithContext(b.ctx)
	b.start = time.Now()
	b.last = b.start
	for n := 0; n < b.concurrency; n++ {
		idx := n
		errs.Go(func() error {
			for i := 0; i < b.iterations; i++ {
				select {
				case <-b.ctx.Done():
					return nil
				default:
					if i == 0 {
						b.count.Add(1)
						err := b.client.CampaignSuccess(idx, i)
						if err != nil {
							return err
						}
						continue
					}

					b.count.Add(1)
					err := b.client.ExtendElectedTerm(idx, i)
					if err != nil {
						return err
					}
					b.lastExtern[idx] = time.Now()

					b.count.Add(1)
					err = b.client.CampaignFail(idx, i)
					if err != nil {
						return err
					}
				}
			}
			return nil
		})
	}

	if err := errs.Wait(); err != nil {
		return err
	}

	b.reportRPS(true)

	return nil
}

func (b *Benchmark) reportRPS(finised bool) {
	end := time.Now()
	elapsed := end.Sub(b.start)
	rps := int64(b.count.Swap(0)) * 1000 / end.Sub(b.last).Milliseconds()
	b.last = end
	if finised {
		fmt.Printf("Benchmark finished, Elasped: %s, %d reqs/sec\n", elapsed, rps)
	} else {
		elapsedStr := time.Unix(0, 0).UTC().Add(elapsed).Format("04:05.00")
		fmt.Printf("  * Elasped: %s, %d reqs/sec\n", elapsedStr, rps)
	}
}

func (b *Benchmark) Stop() {
	fmt.Printf("Closing %d benchmark clients...\n", b.concurrency)
	var wg sync.WaitGroup
	wg.Add(b.concurrency)
	count := atomic.Int32{}
	for i := 0; i < b.concurrency; i++ {
		go func(idx int) {
			expire := b.lastExtern[idx].Add(time.Duration(BenchmarkTTL) * time.Millisecond)
			if time.Now().Before(expire) {
				count.Add(1)
				b.client.Quit(idx)
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
	fmt.Printf("Benchmark %d clients closed\n", count.Load())
}
