package metric

import (
	"sync/atomic"
	"time"
)

type AvgDuration struct {
	last     atomic.Pointer[time.Time]
	total    atomic.Int64
	count    atomic.Int64
	interval time.Duration
}

func NewAvgDuration(interval time.Duration) *AvgDuration {
	d := &AvgDuration{
		interval: interval,
	}
	d.last.Store(&time.Time{})
	return d
}

func (d *AvgDuration) Count(duration time.Duration) (time.Duration, bool) {
	now := time.Now()
	last := d.last.Load()
	if last.IsZero() {
		last = &now
		d.last.Store(last)
	}

	count := d.count.Add(1)
	total := d.total.Add(int64(duration))

	if now.After(last.Add(d.interval)) {
		d.last.Store(&time.Time{})
		d.count.Store(0)
		d.total.Store(0)
		return time.Duration(total / count), true
	}

	return 0, false
}
