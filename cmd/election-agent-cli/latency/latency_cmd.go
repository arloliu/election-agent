package latency

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/montanaflynn/stats"
	"github.com/spf13/cobra"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	headlessSvcHost string
	metricAddr      string
	metricInterval  time.Duration
	statsPeriod     time.Duration
	probeInterval   time.Duration
	displayInterval time.Duration
)

func init() {
	LatencyCmd.Flags().StringVarP(&headlessSvcHost, "svc", "s", "", "k8s headless service dns name for probing dns latency")
	LatencyCmd.Flags().StringVarP(&metricAddr, "metric_addr", "m", "", "the prometheus metric listen address, defaults to not enable prometheus metric feature")
	LatencyCmd.Flags().DurationVarP(&probeInterval, "probe_interval", "p", 1*time.Second, "latency probe interval")
	LatencyCmd.Flags().DurationVarP(&metricInterval, "metric_interval", "t", 15*time.Second, "the prometheus metric collection interval")
	LatencyCmd.Flags().DurationVarP(&displayInterval, "display_interval", "i", 15*time.Second, "statistic display interval")
	LatencyCmd.Flags().DurationVarP(&statsPeriod, "stats_period", "d", 30*time.Second, "the latency statistic collection period")
}

var LatencyCmd = &cobra.Command{
	Use:   "latency",
	Short: "Test election agent latency",
	Long:  "Probe election agent to get latency statstic, defaults to probe liveness http endpoint, and supports to probe DNS query latency too",
	RunE:  testLatency,
}

type LatencyStats struct {
	mu           sync.Mutex
	getConnTime  time.Time
	dnsStartTime time.Time
	gotConnStats *StatsCollector
	dnsStats     *StatsCollector
	respStats    *StatsCollector
	svcDNSStats  *StatsCollector
}

func newLatencyStats(period time.Duration) *LatencyStats {
	return &LatencyStats{
		gotConnStats: NewStatsCollector(period),
		dnsStats:     NewStatsCollector(period),
		respStats:    NewStatsCollector(period),
		svcDNSStats:  NewStatsCollector(period),
	}
}

type latencyMetrics struct {
	server     *http.Server
	listenAddr string
	reg        *prometheus.Registry
	livezConn  *prometheus.GaugeVec
	livezDNS   *prometheus.GaugeVec
	livezResp  *prometheus.GaugeVec
	svcDNS     *prometheus.GaugeVec
}

func newLatencyMetrics(listenAddr string) *latencyMetrics {
	m := &latencyMetrics{
		listenAddr: listenAddr,

		reg: prometheus.NewRegistry(),

		livezConn: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "election_agent_liveness_connection_duration_seconds",
			Help: "Election agent liveness connection establishment latency",
		}, []string{"type"}),

		livezDNS: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "election_agent_liveness_dns_query_duration_seconds",
			Help: "Election agent dns query latency",
		}, []string{"type"}),

		livezResp: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "election_agent_liveness_response_duration_seconds",
			Help: "Election agent liveness response latency",
		}, []string{"type"}),

		svcDNS: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "election_agent_headless_dns_query_duration_seconds",
			Help: "Election agent headless service dns query latency",
		}, []string{"type"}),
	}
	m.reg.MustRegister(m.livezConn)
	m.reg.MustRegister(m.livezDNS)
	m.reg.MustRegister(m.livezResp)
	m.reg.MustRegister(m.svcDNS)

	return m
}

func (m *latencyMetrics) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg}))

	m.server = &http.Server{
		Addr:              m.listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 1 * time.Second,
		WriteTimeout:      1 * time.Second,
		IdleTimeout:       1 * time.Second,
		MaxHeaderBytes:    128,
	}

	fmt.Printf("[%s] Metrics http server serves on: %s\n", now(), m.listenAddr)
	return m.server.ListenAndServe()
}

func (m *latencyMetrics) Shutdown(ctx context.Context) error {
	return m.server.Shutdown(ctx)
}

func testLatency(cmd *cobra.Command, args []string) error {
	hostname, _ := cmd.Flags().GetString("host")
	agentURL, err := url.Parse(hostname)
	if err != nil {
		fmt.Printf("Invalid agent http url: %s, error: %s\n", hostname, err)
		return err
	}
	if agentURL.Path == "" || agentURL.Path == "/" {
		agentURL.Path = "/livez"
	}

	ctx := context.Background()

	fmt.Printf("[%s] Start to test latency of election agent liveness HTTP endpoint: %s\n  * probe interval: %s, display interval: %s, stats period:%s",
		now(), agentURL.String(), probeInterval, displayInterval, statsPeriod,
	)
	if headlessSvcHost != "" {
		fmt.Printf(", headless service DNS name: %s", headlessSvcHost)
	}
	fmt.Println("")

	var metrics *latencyMetrics
	if metricAddr != "" {
		metrics = newLatencyMetrics(metricAddr)
		go func() {
			_ = metrics.Start()
		}()
	}

	exitSig := make(chan os.Signal, 1)
	signal.Notify(exitSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	latStats := newLatencyStats(statsPeriod)

	probeTicker := time.NewTicker(probeInterval)
	defer probeTicker.Stop()

	metricTicker := time.NewTicker(metricInterval)
	defer metricTicker.Stop()

	if displayInterval == 0 {
		displayInterval = 1<<63 - 1
	}
	displayTicker := time.NewTicker(displayInterval)
	defer displayTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Printf("[%s] Context done\n", now())
			return nil

		case <-exitSig:
			fmt.Printf("[%s] Receive exit signal\n", now())
			if metrics != nil {
				_ = metrics.Shutdown(ctx)
			}
			return nil

		case <-probeTicker.C:
			var wg sync.WaitGroup
			var err error
			wg.Add(1)
			go func() {
				defer wg.Done()
				err = probeLivezLatency(ctx, agentURL.String(), latStats)
			}()

			if headlessSvcHost != "" {
				wg.Add(1)
				go func() {
					defer wg.Done()
					err = probeDNSLatency(ctx, headlessSvcHost, latStats)
				}()
			}
			wg.Wait()
			if err != nil {
				return err
			}

		case <-metricTicker.C:
			collecMetrics(latStats, metrics)

		case <-displayTicker.C:
			displayLivezLatency(latStats)
		}
	}
}

func latencyInfo(val time.Duration, total time.Duration) string {
	p := float64(val*100) / float64(total)
	return fmt.Sprintf("%s(%.1f%%)", val, p)
}

func displayGroupStats(title string, c *StatsCollector, t *StatsCollector) {
	fmt.Printf(" * %s\n", title)
	fmt.Printf("    Avg.: %s, Max: %s, p50: %s, p75: %s, p95: %s, p99: %s\n",
		latencyInfo(c.Mean(), t.Mean()),
		latencyInfo(c.Max(), t.Max()),
		latencyInfo(c.Percentile(50), t.Percentile(50)),
		latencyInfo(c.Percentile(75), t.Percentile(75)),
		latencyInfo(c.Percentile(95), t.Percentile(95)),
		latencyInfo(c.Percentile(95), t.Percentile(99)),
	)
}

func displayLivezLatency(ls *LatencyStats) {
	fmt.Printf("[%s] Latency Statistics (stats period: %s)\n", now(), statsPeriod)
	displayGroupStats("Liveness Connection Establishment", ls.gotConnStats, ls.respStats)
	displayGroupStats("Liveness DNS Query", ls.dnsStats, ls.respStats)
	if headlessSvcHost != "" {
		title := fmt.Sprintf("DNS Query for Headless Service: %s", headlessSvcHost)
		displayGroupStats(title, ls.svcDNSStats, ls.respStats)
	}
	fmt.Println(" * Total Response Latencies")
	fmt.Printf("    Avg.: %s, Max: %s, p50: %s, p75: %s, p95: %s, p99: %s\n",
		ls.respStats.Mean(),
		ls.respStats.Max(),
		ls.respStats.Percentile(50),
		ls.respStats.Percentile(75),
		ls.respStats.Percentile(95),
		ls.respStats.Percentile(99),
	)

	fmt.Println("")
}

func probeDNSLatency(ctx context.Context, host string, ls *LatencyStats) error {
	resolver := net.Resolver{PreferGo: true, StrictErrors: false}
	start := time.Now()
	_, err := resolver.LookupIPAddr(ctx, host)
	elapsed := time.Since(start)
	if err != nil {
		return err
	}
	ls.mu.Lock()
	ls.svcDNSStats.Collect(float64(elapsed))
	ls.mu.Unlock()

	return nil
}

func probeLivezLatency(ctx context.Context, url string, ls *LatencyStats) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	trace := &httptrace.ClientTrace{
		GetConn: func(_ string) {
			ls.mu.Lock()
			ls.getConnTime = time.Now()
			ls.mu.Unlock()
		},
		GotConn: func(_ httptrace.GotConnInfo) {
			ls.mu.Lock()
			elapsed := time.Since(ls.getConnTime)
			ls.gotConnStats.Collect(float64(elapsed))
			ls.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			ls.mu.Lock()
			elapsed := time.Since(ls.getConnTime)
			ls.respStats.Collect(float64(elapsed))
			ls.mu.Unlock()
		},
		DNSStart: func(_ httptrace.DNSStartInfo) {
			ls.mu.Lock()
			ls.dnsStartTime = time.Now()
			ls.mu.Unlock()
		},
		DNSDone: func(_ httptrace.DNSDoneInfo) {
			ls.mu.Lock()
			elapsed := time.Since(ls.dnsStartTime)
			ls.dnsStats.Collect(float64(elapsed))
			ls.mu.Unlock()
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))

	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:     1,
			MaxIdleConns:        1,
			MaxIdleConnsPerHost: 1,
		},
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Probe liveness: %s fails, status code: %d\n", url, resp.StatusCode)
	}

	return nil
}

func collecMetrics(ls *LatencyStats, metrics *latencyMetrics) {
	if metrics == nil {
		return
	}

	ls.mu.Lock()
	defer ls.mu.Unlock()

	metrics.livezConn.With(prometheus.Labels{"type": "avg"}).Set(ls.gotConnStats.Mean().Seconds())
	metrics.livezConn.With(prometheus.Labels{"type": "max"}).Set(ls.gotConnStats.Max().Seconds())
	metrics.livezConn.With(prometheus.Labels{"type": "p50"}).Set(ls.gotConnStats.Percentile(50).Seconds())
	metrics.livezConn.With(prometheus.Labels{"type": "p75"}).Set(ls.gotConnStats.Percentile(75).Seconds())
	metrics.livezConn.With(prometheus.Labels{"type": "p95"}).Set(ls.gotConnStats.Percentile(95).Seconds())
	metrics.livezConn.With(prometheus.Labels{"type": "p99"}).Set(ls.gotConnStats.Percentile(99).Seconds())

	metrics.livezDNS.With(prometheus.Labels{"type": "avg"}).Set(ls.dnsStats.Mean().Seconds())
	metrics.livezDNS.With(prometheus.Labels{"type": "max"}).Set(ls.dnsStats.Max().Seconds())
	metrics.livezDNS.With(prometheus.Labels{"type": "p50"}).Set(ls.dnsStats.Percentile(50).Seconds())
	metrics.livezDNS.With(prometheus.Labels{"type": "p75"}).Set(ls.dnsStats.Percentile(75).Seconds())
	metrics.livezDNS.With(prometheus.Labels{"type": "p95"}).Set(ls.dnsStats.Percentile(95).Seconds())
	metrics.livezDNS.With(prometheus.Labels{"type": "p99"}).Set(ls.dnsStats.Percentile(99).Seconds())

	metrics.livezResp.With(prometheus.Labels{"type": "avg"}).Set(ls.respStats.Mean().Seconds())
	metrics.livezResp.With(prometheus.Labels{"type": "max"}).Set(ls.respStats.Max().Seconds())
	metrics.livezResp.With(prometheus.Labels{"type": "p50"}).Set(ls.respStats.Percentile(50).Seconds())
	metrics.livezResp.With(prometheus.Labels{"type": "p75"}).Set(ls.respStats.Percentile(75).Seconds())
	metrics.livezResp.With(prometheus.Labels{"type": "p95"}).Set(ls.respStats.Percentile(95).Seconds())
	metrics.livezResp.With(prometheus.Labels{"type": "p99"}).Set(ls.respStats.Percentile(99).Seconds())

	if headlessSvcHost != "" {
		metrics.svcDNS.With(prometheus.Labels{"type": "avg"}).Set(ls.svcDNSStats.Mean().Seconds())
		metrics.svcDNS.With(prometheus.Labels{"type": "max"}).Set(ls.svcDNSStats.Max().Seconds())
		metrics.svcDNS.With(prometheus.Labels{"type": "p50"}).Set(ls.svcDNSStats.Percentile(50).Seconds())
		metrics.svcDNS.With(prometheus.Labels{"type": "p75"}).Set(ls.svcDNSStats.Percentile(75).Seconds())
		metrics.svcDNS.With(prometheus.Labels{"type": "p95"}).Set(ls.svcDNSStats.Percentile(95).Seconds())
		metrics.svcDNS.With(prometheus.Labels{"type": "p99"}).Set(ls.svcDNSStats.Percentile(99).Seconds())
	}
}

func now() string {
	return time.Now().Format(time.RFC3339)
}

type StatsCollector struct {
	ts      []time.Time
	rawData stats.Float64Data
	period  time.Duration
}

func NewStatsCollector(period time.Duration) *StatsCollector {
	return &StatsCollector{
		ts:      make([]time.Time, 0, 1000),
		rawData: make(stats.Float64Data, 0, 1000),
		period:  period,
	}
}

func (s *StatsCollector) Collect(data float64) {
	now := time.Now()
	if len(s.ts) > 0 && now.Sub(s.ts[0]) > s.period {
		s.ts = s.ts[1:]
		s.rawData = s.rawData[1:]
	}
	s.ts = append(s.ts, now)
	s.rawData = append(s.rawData, data)
}

func (s *StatsCollector) Mean() time.Duration {
	val, _ := s.rawData.Mean()
	return time.Duration(val)
}

func (s *StatsCollector) Max() time.Duration {
	val, _ := s.rawData.Max()
	return time.Duration(val)
}

func (s *StatsCollector) Percentile(p float64) time.Duration {
	val, _ := s.rawData.Percentile(p)
	return time.Duration(val)
}
