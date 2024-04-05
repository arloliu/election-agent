package metric

import (
	"testing"
	"time"

	"election-agent/internal/config"
)

func BenchmarkRequestDuration(b *testing.B) {
	cfg := config.Config{
		HTTP:   config.HTTPConfig{Enable: true},
		Metric: config.MetricConfig{Enable: true},
	}
	m := NewMetricManager(&cfg)
	b.ResetTimer()
	for i := 0; i <= b.N; i++ {
		m.RequestFinish("campaign", true, time.Millisecond)
	}
	b.StopTimer()
}
