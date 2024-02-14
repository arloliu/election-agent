package lease

import (
	"context"
	"time"
)

type Lease interface {
	ID() uint64
	Grant(ctx context.Context) error
	Revoke(ctx context.Context) error
	Extend(ctx context.Context) error
}

type LeaseDriver interface {
	LeaseID(name string, holder string, ttl time.Duration) uint64
	NewLease(name string, holder string, ttl time.Duration) Lease
	GetHolder(ctx context.Context, name string) (string, error)
	Shutdown(ctx context.Context) error
}
