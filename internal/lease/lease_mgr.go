package lease

import (
	"context"
	"time"

	"election-agent/internal/config"

	lru "github.com/hashicorp/golang-lru/v2"
)

type LeaseManager struct {
	ctx    context.Context
	cfg    *config.Config
	driver LeaseDriver
	cache  *lru.TwoQueueCache[uint64, Lease]
}

func NewLeaseManager(ctx context.Context, cfg *config.Config, driver LeaseDriver) *LeaseManager {
	var cache *lru.TwoQueueCache[uint64, Lease]
	if cfg.Lease.Cache {
		size := cfg.Lease.CacheSize
		if size == 0 {
			size = 4192
		}
		cache, _ = lru.New2Q[uint64, Lease](size)
	}

	return &LeaseManager{
		ctx:    ctx,
		cfg:    cfg,
		driver: driver,
		cache:  cache,
	}
}

func (lm *LeaseManager) GetLease(ctx context.Context, name string, holder string, ttl time.Duration) Lease {
	if lm.cfg.Lease.Cache {
		id := lm.driver.LeaseID(name, holder, ttl)
		if lease, ok := lm.cache.Get(id); ok {
			return lease
		}
		lease := lm.driver.NewLease(name, holder, ttl)
		lm.cache.Add(id, lease)

		return lease
	}

	return lm.driver.NewLease(name, holder, ttl)
}

func (lm *LeaseManager) GrantLease(ctx context.Context, name string, holder string, ttl time.Duration) error {
	lease := lm.GetLease(ctx, name, holder, ttl)
	return lease.Grant(ctx)
}

func (lm *LeaseManager) RevokeLease(ctx context.Context, name string, holder string) error {
	lease := lm.GetLease(ctx, name, holder, 0)
	if lm.cfg.Lease.Cache {
		lm.cache.Remove(lease.ID())
	}
	return lease.Revoke(ctx)
}

func (lm *LeaseManager) ExtendLease(ctx context.Context, name string, holder string, ttl time.Duration) error {
	lease := lm.GetLease(ctx, name, holder, ttl)
	return lease.Extend(ctx)
}

func (lm *LeaseManager) GetLeaseHolder(ctx context.Context, name string) (string, error) {
	return lm.driver.GetHolder(ctx, name)
}

func (lm *LeaseManager) Ready() bool {
	// TODO: implement the readiness check here
	return true
}
