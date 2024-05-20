package lease

import (
	"context"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/driver"
	"election-agent/internal/metric"

	lru "github.com/hashicorp/golang-lru/v2"
)

type LeaseManager struct {
	ctx       context.Context
	cfg       *config.Config
	state     *agent.State
	metricMgr *metric.MetricManager
	driver    driver.KVDriver
	cache     *lru.TwoQueueCache[uint64, Lease]
}

func NewLeaseManager(ctx context.Context, cfg *config.Config, metricMgr *metric.MetricManager, drv driver.KVDriver) *LeaseManager {
	var cache *lru.TwoQueueCache[uint64, Lease]
	if cfg.Lease.Cache {
		size := cfg.Lease.CacheSize
		if size == 0 {
			size = 4192
		}
		cache, _ = lru.New2Q[uint64, Lease](size)
	}

	mgr := &LeaseManager{
		ctx:       ctx,
		cfg:       cfg,
		state:     agent.NewState(cfg.DefaultState, cfg.StateCacheTTL),
		metricMgr: metricMgr,
		driver:    drv,
		cache:     cache,
	}
	mgr.state.Store(cfg.DefaultState)

	return mgr
}

func (lm *LeaseManager) MetricManager() *metric.MetricManager {
	return lm.metricMgr
}

func (lm *LeaseManager) getLease(name string, kind string, holder string, ttl time.Duration) Lease {
	if lm.cfg.Lease.Cache {
		id := lm.driver.LeaseID(name, kind, holder, ttl)
		if lease, ok := lm.cache.Get(id); ok {
			return lease
		}
		lease := lm.newLease(name, kind, holder, ttl)
		lm.cache.Add(id, lease)

		return lease
	}

	return lm.newLease(name, kind, holder, ttl)
}

func (lm *LeaseManager) newLease(name string, kind string, holder string, ttl time.Duration) Lease {
	return NewLease(name, kind, holder, ttl, lm.driver)
}

type GrantLeaseResult struct {
	Name   string
	Kind   string
	Holder string
	TTL    time.Duration
}

func (lm *LeaseManager) GrantLease(ctx context.Context, name string, kind string, candidate string, ttl time.Duration) (result *GrantLeaseResult, err error) {
	start := time.Now()
	defer func() {
		status := true
		if err != nil && IsUnavailableError(err) {
			status = false
		}
		lm.postHook("campaign", status, start)
	}()

	if err = lm.preHook("campaign"); err != nil {
		lease := lm.getLease(name, kind, candidate, ttl)
		result = &GrantLeaseResult{Name: name, TTL: ttl, Kind: lease.Kind()}
		return
	}

	result, err = lm.grantLease(ctx, name, kind, candidate, ttl)
	return
}

func (lm *LeaseManager) grantLease(ctx context.Context, name string, kind string, holder string, ttl time.Duration) (*GrantLeaseResult, error) {
	lease := lm.getLease(name, kind, holder, ttl)
	result := &GrantLeaseResult{Name: name, TTL: ttl, Kind: lease.Kind()}
	err := lease.Grant(ctx)
	if err != nil {
		curHolder, err2 := lm.GetLeaseHolder(ctx, name, kind)
		if err2 != nil {
			return result, err
		}
		result.Holder = curHolder
		return result, err
	}

	result.Holder = holder
	return result, nil
}

func (lm *LeaseManager) RevokeLease(ctx context.Context, name string, kind string, holder string) (err error) {
	start := time.Now()
	defer func() { lm.postHook("resign", err == nil, start) }()

	if err = lm.preHook("resign"); err != nil {
		return
	}

	lease := lm.getLease(name, kind, holder, 0)
	if lm.cfg.Lease.Cache {
		lm.cache.Remove(lease.ID())
	}
	err = lease.Revoke(ctx)
	return
}

func (lm *LeaseManager) ExtendLease(ctx context.Context, name string, kind string, holder string, ttl time.Duration) (err error) {
	start := time.Now()
	defer func() { lm.postHook("extend_elected_term", err == nil, start) }()

	if err = lm.preHook("extend_elected_term"); err != nil {
		return
	}

	lease := lm.getLease(name, kind, holder, ttl)
	err = lease.Extend(ctx)
	return
}

func (lm *LeaseManager) HandoverLease(ctx context.Context, name string, kind string, holder string, ttl time.Duration) (err error) {
	start := time.Now()
	defer func() { lm.postHook("handover", err == nil, start) }()

	if err = lm.preHook("handover"); err != nil {
		return
	}

	lease := lm.newLease(name, kind, holder, ttl)
	err = lease.Handover(ctx, holder)
	return
}

func (lm *LeaseManager) GetLeaseHolder(ctx context.Context, name string, kind string) (holder string, err error) {
	start := time.Now()
	defer func() { lm.postHook("get_leader", err == nil, start) }()

	if err = lm.preHook("get_leader"); err != nil {
		return
	}

	holder, err = lm.driver.GetHolder(ctx, name, kind)
	return
}

func (lm *LeaseManager) GetLeaseHolders(ctx context.Context, kind string) (holders []driver.Holder, err error) {
	start := time.Now()
	defer func() { lm.postHook("list_leaders", err == nil, start) }()

	if err = lm.preHook("list_leaders"); err != nil {
		return
	}

	holders, err = lm.driver.GetHolders(ctx, kind)
	return
}

func (lm *LeaseManager) preHook(action string) error {
	lm.metricMgr.RequestBegin(action)

	state := lm.getState()
	if state == agent.UnavailableState {
		lm.metricMgr.IncAgentUnavailableState()
		return ErrServiceUnavalable
	} else if state == agent.StandbyState {
		return ErrAgentStandby
	}
	return nil
}

func (lm *LeaseManager) postHook(action string, status bool, start time.Time) {
	lm.metricMgr.RequestFinish(action, status, time.Since(start))
}

func (lm *LeaseManager) getState() string {
	if !lm.cfg.Zone.Enable {
		return lm.cfg.DefaultState
	}

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
