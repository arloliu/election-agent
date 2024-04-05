package metric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAvgDuration(t *testing.T) {
	require := require.New(t)

	interval := 1 * time.Millisecond
	d := NewAvgDuration(interval)
	var avg time.Duration
	var ok bool

	for i := 0; i < 10; i++ {
		avg, ok = d.Count(10 * time.Microsecond)
		require.False(ok)
		require.Equal(time.Duration(0), avg)
		avg, ok = d.Count(10 * time.Microsecond)
		require.False(ok)
		require.Equal(time.Duration(0), avg)
		avg, ok = d.Count(10 * time.Microsecond)
		require.False(ok)
		require.Equal(time.Duration(0), avg)

		time.Sleep(interval)
		avg, ok = d.Count(30 * time.Microsecond)
		require.True(ok)
		require.Equal(15*time.Microsecond, avg)
	}
}

func BenchmarkAvgDuration(b *testing.B) {
	d := NewAvgDuration(time.Second)

	b.ResetTimer()
	for i := 0; i <= b.N; i++ {
		d.Count(time.Duration(i) * time.Microsecond)
	}
	b.StopTimer()
}
