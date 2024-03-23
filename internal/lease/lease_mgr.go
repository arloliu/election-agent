package lease

import (
	"context"
	"sync"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"

	lru "github.com/hashicorp/golang-lru/v2"
)

type LeaseManager struct {
	ctx    context.Context
	cfg    *config.Config
	state  *agent.State
	driver KVDriver
	cache  *lru.TwoQueueCache[uint64, Lease]
	mu     sync.Mutex
}

func NewLeaseManager(ctx context.Context, cfg *config.Config, driver KVDriver) *LeaseManager {
	var cache *lru.TwoQueueCache[uint64, Lease]
	if cfg.Lease.Cache {
		size := cfg.Lease.CacheSize
		if size == 0 {
			size = 4192
		}
		cache, _ = lru.New2Q[uint64, Lease](size)
	}

	mgr := &LeaseManager{
		ctx:    ctx,
		cfg:    cfg,
		state:  agent.NewState(cfg.DefaultState, cfg.StateCacheTTL),
		driver: driver,
		cache:  cache,
	}
	mgr.state.Store(cfg.DefaultState)

	return mgr
}

func (lm *LeaseManager) GetLease(ctx context.Context, name string, kind string, holder string, ttl time.Duration) Lease {
	if lm.cfg.Lease.Cache {
		id := lm.driver.LeaseID(name, kind, holder, ttl)
		if lease, ok := lm.cache.Get(id); ok {
			return lease
		}
		lease := lm.driver.NewLease(name, kind, holder, ttl)
		lm.cache.Add(id, lease)

		return lease
	}

	return lm.driver.NewLease(name, kind, holder, ttl)
}

func (lm *LeaseManager) GrantLease(ctx context.Context, name string, kind string, holder string, ttl time.Duration) error {
	state := lm.GetState()
	if state == agent.UnavailableState {
		return ErrServiceUnavalable
	} else if state == agent.StandbyState {
		return ErrAgentStandby
	}

	lease := lm.GetLease(ctx, name, kind, holder, ttl)
	return lease.Grant(ctx)
}

func (lm *LeaseManager) RevokeLease(ctx context.Context, name string, kind string, holder string) error {
	state := lm.GetState()
	if state == agent.UnavailableState {
		return ErrServiceUnavalable
	} else if state == agent.StandbyState {
		return ErrAgentStandby
	}

	lease := lm.GetLease(ctx, name, kind, holder, 0)
	if lm.cfg.Lease.Cache {
		lm.cache.Remove(lease.ID())
	}
	return lease.Revoke(ctx)
}

func (lm *LeaseManager) ExtendLease(ctx context.Context, name string, kind string, holder string, ttl time.Duration) error {
	state := lm.GetState()
	if state == agent.UnavailableState {
		return ErrServiceUnavalable
	} else if state == agent.StandbyState {
		return ErrAgentStandby
	}

	lease := lm.GetLease(ctx, name, kind, holder, ttl)
	return lease.Extend(ctx)
}

func (lm *LeaseManager) GetLeaseHolder(ctx context.Context, name string, kind string) (string, error) {
	return lm.driver.GetHolder(ctx, name, kind)
}

func (lm *LeaseManager) GetState() string {
	if !lm.cfg.Zone.Enable {
		return agent.ActiveState
	}

	lm.mu.Lock()
	defer lm.mu.Unlock()

	if lm.state.Expired() {
		state, err := lm.driver.GetAgentState()
		if err != nil {
			lm.state.Store(agent.UnavailableState)
			return agent.UnavailableState
		}
		lm.state.Store(state)
	}
	return lm.state.Load()
}

func (lm *LeaseManager) SetStateCache(state string) {
	lm.state.Store(state)
}

func (lm *LeaseManager) Ready() bool {
	// TODO: implement the readiness check here
	return true
}
