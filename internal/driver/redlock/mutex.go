package redlock

import (
	"context"
	"encoding/base64"
	"errors"
	"math/rand"
	"time"

	"election-agent/internal/logging"

	cryptorand "crypto/rand"
)

const (
	minRetryDelayMilliSec = 50
	maxRetryDelayMilliSec = 250
)

type DelayFunc func(tries int) time.Duration

var defDelayFun = func(tries int) time.Duration {
	randDelay := rand.Intn(maxRetryDelayMilliSec - minRetryDelayMilliSec) //nolint:gosec
	return time.Duration(randDelay+minRetryDelayMilliSec) * time.Millisecond
}

type Mutex struct {
	name   string
	expiry time.Duration

	tries     int
	delayFunc DelayFunc

	driftFactor   float64
	timeoutFactor float64
	opTimeout     time.Duration
	value         string
	randomValue   bool
	until         time.Time

	getConns func(string) ([]Conn, int)
}

// Name returns mutex name (i.e. the Redis key).
func (m *Mutex) Name() string {
	return m.name
}

// Value returns the current random value. The value will be empty until a lock is acquired (or WithValue option is used).
func (m *Mutex) Value() string {
	return m.value
}

// Until returns the time of validity of acquired lock. The value will be zero value until a lock is acquired.
func (m *Mutex) Until() time.Time {
	return m.until
}

// TryLockContext only attempts to lock m once and returns immediately regardless of success or failure without retrying.
func (m *Mutex) TryLockContext(ctx context.Context) error {
	return m.lockContext(ctx, 1)
}

// LockContext locks m. In case it returns an error on failure, you may retry to acquire the lock by calling this method again.
func (m *Mutex) LockContext(ctx context.Context) error {
	return m.lockContext(ctx, m.tries)
}

// lockContext locks m. In case it returns an error on failure, you may retry to acquire the lock by calling this method again.
func (m *Mutex) lockContext(ctx context.Context, tries int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	var value string

	if m.randomValue {
		value, err = genValue()
		if err != nil {
			return err
		}
	} else {
		value = m.value
	}

	conns, quorum := m.getConns(m.name)

	var timer *time.Timer
	for i := 0; i < tries; i++ {
		if i != 0 {
			if timer == nil {
				timer = time.NewTimer(m.delayFunc(i))
			} else {
				timer.Reset(m.delayFunc(i))
			}

			select {
			case <-ctx.Done():
				timer.Stop()
				// Exit early if the context is done.
				return ErrFailed
			case <-timer.C:
				// Fall-through when the delay timer completes.
			}
		}

		start := time.Now()
		n, err := func() (int, error) {
			ctx, cancel := context.WithTimeout(ctx, m.opTimeout)
			defer cancel()
			return actStatusOpAsync(conns, quorum, true, func(conn Conn) (bool, error) {
				return m.acquire(ctx, conn, value)
			})
		}()

		now := time.Now()
		until := now.Add(m.expiry - now.Sub(start) - time.Duration(int64(float64(m.expiry)*m.driftFactor)))
		if n >= quorum && now.Before(until) {
			m.value = value
			m.until = until
			return nil
		}

		if i == tries-1 && err != nil {
			return err
		}
	}

	return ErrFailed
}

// UnlockContext unlocks m and returns the status of unlock.
func (m *Mutex) UnlockContext(ctx context.Context) (bool, error) {
	conns, quorum := m.getConns(m.name)
	n, err := func() (int, error) {
		ctx, cancel := context.WithTimeout(ctx, m.opTimeout)
		defer cancel()
		return actStatusOpAsync(conns, quorum, true, func(conn Conn) (bool, error) {
			return m.release(ctx, conn, m.value)
		})
	}()
	if n < quorum {
		return false, err
	}
	return true, nil
}

// ExtendContext resets the mutex's expiry and returns the status of expiry extension.
func (m *Mutex) ExtendContext(ctx context.Context) (bool, error) {
	start := time.Now()
	conns, quorum := m.getConns(m.name)

	n, err := func() (int, error) {
		ctx, cancel := context.WithTimeout(ctx, m.opTimeout)
		defer cancel()
		return actStatusOpAsync(conns, quorum, true, func(conn Conn) (bool, error) {
			return m.touch(ctx, conn, m.value, int(m.expiry/time.Millisecond))
		})
	}()
	if err != nil && !errors.Is(err, context.Canceled) {
		logging.Warnw("Mutex.ExtendContext got error", "name", m.name, "value", m.value, "err", err, "n", n, "quorum", quorum, "ttl", int(m.expiry/time.Millisecond))
	}

	if n < quorum {
		return false, err
	}
	now := time.Now()
	until := now.Add(m.expiry - now.Sub(start) - time.Duration(int64(float64(m.expiry)*m.driftFactor)))
	if now.Before(until) {
		m.until = until
		return true, nil
	}
	return false, ErrExtendFailed
}

// HandoverContext set a new holder of mutex and return the status.
func (m *Mutex) HandoverContext(ctx context.Context, holder string) (bool, error) {
	start := time.Now()
	conns, quorum := m.getConns(m.name)

	n, err := actStatusOpAsync(conns, quorum, false, func(conn Conn) (bool, error) {
		return m.handover(ctx, conn, holder, int(m.expiry/time.Millisecond))
	})
	if err != nil {
		logging.Warnw("Mutex.HandoverContext got error", "name", m.name, "holder", holder, "err", err, "n", n, "quorum", quorum, "ttl", int(m.expiry/time.Millisecond))
	}

	if n < quorum {
		return false, err
	}

	now := time.Now()
	until := now.Add(m.expiry - now.Sub(start) - time.Duration(int64(float64(m.expiry)*m.driftFactor)))
	if now.Before(until) {
		m.until = until
		return true, nil
	}
	return false, ErrHandoverFailed
}

func genValue() (string, error) {
	b := make([]byte, 16)
	_, err := cryptorand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
