package metric

import (
	"net/http"
	"time"

	"election-agent/internal/config"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	StatusSuccess = "success"
	StatusFail    = "fail"
)

type MetricManager struct {
	cfg               *config.Config
	reg               *prometheus.Registry
	reqDuration       *prometheus.HistogramVec
	reqInflights      *prometheus.GaugeVec
	agentUnavails     prometheus.Counter
	zcDisconnected    prometheus.Gauge
	peersDisconnected prometheus.Gauge
	redisDisconnected prometheus.Gauge
}

func NewMetricManager(cfg *config.Config) *MetricManager {
	constLabels := prometheus.Labels{"agent": cfg.Name}
	if cfg.Zone.Name != "" {
		constLabels["zone"] = cfg.Zone.Name
	}

	m := &MetricManager{
		cfg: cfg,
		reg: prometheus.NewRegistry(),
		reqDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:        "election_agent_request_duration_seconds",
				Help:        "Distribution of election request latencies",
				Buckets:     cfg.Metric.RequestDurationBuckets,
				ConstLabels: constLabels,
			},
			[]string{"action", "status"},
		),
		reqInflights: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name:        "election_agent_requests_in_flight",
				Help:        "Current in-flight requests",
				ConstLabels: constLabels,
			},
			[]string{"action"},
		),
		agentUnavails: prometheus.NewCounter(prometheus.CounterOpts{
			Name:        "election_agent_unavailable_total",
			Help:        "The number of unavailable state errors returned by the election agent",
			ConstLabels: constLabels,
		}),
		zcDisconnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "election_agent_zone_coordinator_is_disconnected",
			Help:        "Whether the zone coordinator is disconnected, the value should be 0 or 1",
			ConstLabels: constLabels,
		}),
		peersDisconnected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name:        "election_agent_disconnected_peers",
			Help:        "The number of currently disconnected peers",
			ConstLabels: constLabels,
		}),
		redisDisconnected: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        "election_agent_disconnected_redis_backends",
				Help:        "The number of currently disconnected redis backends",
				ConstLabels: constLabels,
			},
		),
	}

	if !cfg.Metric.Enable {
		return m
	}

	m.reg.MustRegister(m.reqDuration)
	m.reg.MustRegister(m.reqInflights)
	m.reg.MustRegister(m.agentUnavails)
	m.reg.MustRegister(m.zcDisconnected)
	m.reg.MustRegister(m.peersDisconnected)
	m.reg.MustRegister(m.redisDisconnected)
	return m
}

func (m *MetricManager) Enabled() bool {
	return m.cfg.Metric.Enable
}

func (m *MetricManager) RequestBegin(action string) {
	if !m.cfg.Metric.Enable {
		return
	}

	m.reqInflights.With(prometheus.Labels{"action": action}).Inc()
}

func (m *MetricManager) RequestFinish(action string, status bool, duration time.Duration) {
	if !m.cfg.Metric.Enable {
		return
	}

	m.reqInflights.With(prometheus.Labels{"action": action}).Dec()

	var statusStr string
	if status {
		statusStr = StatusSuccess
	} else {
		statusStr = StatusFail
	}
	m.reqDuration.With(prometheus.Labels{"action": action, "status": statusStr}).Observe(duration.Seconds())
}

func (m *MetricManager) IncAgentUnavailableState() {
	if !m.cfg.Metric.Enable {
		return
	}

	m.agentUnavails.Inc()
}

func (m *MetricManager) IsZCConnected(connected bool) {
	if !m.cfg.Metric.Enable {
		return
	}

	var val float64 = 0.0
	if !connected {
		val = 1.0
	}
	m.zcDisconnected.Set(val)
}

func (m *MetricManager) SetDisconnectedPeers(num int) {
	if !m.cfg.Metric.Enable {
		return
	}

	m.peersDisconnected.Set(float64(num))
}

func (m *MetricManager) SetDisconnectedRedisBackends(num int) {
	if !m.cfg.Metric.Enable {
		return
	}

	m.redisDisconnected.Set(float64(num))
}

func (m *MetricManager) HTTPHandler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}
