package lease

import (
	"context"
	"errors"
	"fmt"
	"time"

	"election-agent/internal/driver"
	"election-agent/internal/driver/redlock"
)

// RedisLease implements Lease interface
type RedisLease struct {
	mutex  driver.Mutex
	driver driver.KVDriver
	kind   string
	id     uint64
}

var _ Lease = (*RedisLease)(nil)

func NewLease(name string, kind string, holder string, ttl time.Duration, drv driver.KVDriver) Lease {
	if kind == "" {
		kind = "default"
	}

	lease := &RedisLease{
		id:     drv.LeaseID(name, kind, holder, ttl),
		kind:   kind,
		mutex:  drv.NewMutex(name, kind, holder, ttl),
		driver: drv,
	}

	return lease
}

func (rl *RedisLease) ID() uint64 {
	return rl.id
}

func (rl *RedisLease) Kind() string {
	return rl.kind
}

func (rl *RedisLease) Grant(ctx context.Context) error {
	err := rl.mutex.TryLockContext(ctx)
	if err == nil {
		return nil
	}

	if rl.driver.IsUnhealthy(err) {
		return &UnavailableError{Err: err}
	}

	return err
}

func (rl *RedisLease) Revoke(ctx context.Context) error {
	_, err := rl.mutex.UnlockContext(ctx)
	if err != nil {
		if rl.driver.IsUnhealthy(err) {
			return &UnavailableError{Err: err}
		}

		if errors.Is(err, redlock.ErrLockAlreadyExpired) {
			return nil
		}

		val, _ := rl.driver.GetHolder(ctx, rl.mutex.Name(), rl.kind)
		return fmt.Errorf("Failed to revoke lease %s, expected value:%s, actual value:%s, error: %w\n", rl.mutex.Name(), rl.mutex.Value(), val, err)
	}
	return nil
}

func (rl *RedisLease) Extend(ctx context.Context) error {
	ok, err := rl.mutex.ExtendContext(ctx)
	if err != nil {
		if rl.driver.IsUnhealthy(err) {
			return &UnavailableError{Err: err}
		}

		rlTakenError := &redlock.TakenError{}
		if errors.As(err, &rlTakenError) {
			e, _ := err.(*redlock.TakenError) //nolint:errorlint
			return &TakenError{Nodes: e.Nodes}
		}

		return &ExtendFailError{Lease: rl.mutex.Name(), Err: err}
	}
	if !ok {
		return &NonexistError{Lease: rl.mutex.Name()}
	}

	return nil
}

func (rl *RedisLease) Handover(ctx context.Context, holder string) error {
	ok, err := rl.mutex.HandoverContext(ctx, holder)
	if err != nil {
		if rl.driver.IsUnhealthy(err) {
			return &UnavailableError{Err: err}
		}

		rlTakenError := &redlock.TakenError{}
		if errors.As(err, &rlTakenError) {
			e, _ := err.(*redlock.TakenError) //nolint:errorlint
			return &TakenError{Nodes: e.Nodes}
		}

		return &HandoverFailError{Lease: rl.mutex.Name(), Holder: holder, Err: err}
	}

	if !ok {
		return &HandoverFailError{Lease: rl.mutex.Name(), Holder: holder}
	}

	return nil
}
