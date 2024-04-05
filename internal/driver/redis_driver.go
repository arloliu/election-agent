package driver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/driver/redlock"
	"election-agent/internal/lease"
	"election-agent/internal/logging"

	"github.com/dolthub/maphash"
)

// RedisLease implements Lease interface
type RedisLease struct {
	id     uint64
	kind   string
	mu     *redlock.Mutex
	driver *RedisKVDriver
}

var _ lease.Lease = (*RedisLease)(nil)

func (rl *RedisLease) ID() uint64 {
	return rl.id
}

func (rl *RedisLease) Kind() string {
	return rl.kind
}

func (rl *RedisLease) Grant(ctx context.Context) error {
	err := rl.mu.TryLockContext(ctx)
	if err == nil {
		return nil
	}

	if rl.driver.isRedisUnhealthy(err) {
		return &lease.UnavailableError{Err: err}
	}

	return err
}

func (rl *RedisLease) Revoke(ctx context.Context) error {
	_, err := rl.mu.UnlockContext(ctx)
	if err != nil {
		if rl.driver.isRedisUnhealthy(err) {
			return &lease.UnavailableError{Err: err}
		}

		if errors.Is(err, redlock.ErrLockAlreadyExpired) {
			return nil
		}

		val, _ := rl.driver.GetHolder(ctx, rl.mu.Name(), rl.kind)
		return fmt.Errorf("Failed to revoke lease %s, expected value:%s, actual value:%s, error: %w\n", rl.mu.Name(), rl.mu.Value(), val, err)
	}
	return nil
}

func (rl *RedisLease) Extend(ctx context.Context) error {
	ok, err := rl.mu.ExtendContext(ctx)
	if err != nil {
		if rl.driver.isRedisUnhealthy(err) {
			return &lease.UnavailableError{Err: err}
		}

		TakenError := &redlock.TakenError{}
		if errors.As(err, &TakenError) {
			e, _ := err.(*redlock.TakenError) //nolint:errorlint
			return &lease.TakenError{Nodes: e.Nodes}
		}

		return &lease.ExtendFailError{Lease: rl.mu.Name(), Err: err}
	}
	if !ok {
		return &lease.NonexistError{Lease: rl.mu.Name()}
	}

	return nil
}

func (rl *RedisLease) Handover(ctx context.Context, holder string) error {
	ok, err := rl.mu.HandoverContext(ctx, holder)
	if err != nil {
		if rl.driver.isRedisUnhealthy(err) {
			return &lease.UnavailableError{Err: err}
		}

		TakenError := &redlock.TakenError{}
		if errors.As(err, &TakenError) {
			e, _ := err.(*redlock.TakenError) //nolint:errorlint
			return &lease.TakenError{Nodes: e.Nodes}
		}

		return &lease.HandoverFailError{Lease: rl.mu.Name(), Holder: holder, Err: err}
	}

	if !ok {
		return &lease.HandoverFailError{Lease: rl.mu.Name(), Holder: holder}
	}

	return nil
}

// RedisKVDriver implements KVDriver interface
type RedisKVDriver struct {
	ctx         context.Context
	cfg         *config.Config
	originConns []redlock.Conn
	conns       []redlock.Conn
	rlock       *redlock.RedLock
	mu          sync.Mutex
	hasher      maphash.Hasher[string]
	hostname    string
}

var _ lease.KVDriver = (*RedisKVDriver)(nil)

func NewRedisKVDriver(ctx context.Context, cfg *config.Config) (*RedisKVDriver, error) {
	conns, err := redlock.CreateConnections(ctx, cfg)
	if err != nil {
		return nil, err
	}

	idx := cfg.Redis.Primary
	if idx < 0 || idx >= len(conns) {
		errMsg := fmt.Sprintf("The primary index %d is out of range", idx)
		logging.Error(errMsg)
		return nil, errors.New(errMsg)
	}

	inst := &RedisKVDriver{
		ctx:         ctx,
		cfg:         cfg,
		originConns: conns,
		conns:       conns,
		rlock:       redlock.New(conns...),
		hasher:      maphash.NewHasher[string](),
		hostname:    os.Getenv("HOSTNAME"),
	}

	if cfg.Zone.Enable {
		state, stateErr := inst.GetAgentState()
		if stateErr != nil {
			return inst, stateErr
		}
		if state != agent.UnavailableState {
			err = inst.SetAgentMode(agent.NormalMode)
		} else {
			logging.Warn("Initial key-value driver in unavailable state")
		}
	} else {
		_ = inst.SetAgentStatus(&agent.Status{State: agent.ActiveState, Mode: agent.NormalMode})
	}

	return inst, err
}

func (rd *RedisKVDriver) LeaseID(name string, kind string, holder string, ttl time.Duration) uint64 {
	if kind == "" {
		kind = "default"
	}
	return rd.hasher.Hash(name + kind + holder + strconv.FormatInt(int64(ttl), 16))
}

func (rd *RedisKVDriver) GetHolder(ctx context.Context, name string, kind string) (string, error) {
	if kind == "" {
		kind = "default"
	}
	key := rd.leaseKey(name, kind)
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.Get(ctx, key)
}

func (rd *RedisKVDriver) NewLease(name string, kind string, holder string, ttl time.Duration) lease.Lease {
	rd.mu.Lock()
	defer rd.mu.Unlock()
	if kind == "" {
		kind = "default"
	}

	lease := &RedisLease{
		id:   rd.LeaseID(name, kind, holder, ttl),
		kind: kind,
		mu: rd.rlock.NewMutex(
			rd.leaseKey(name, kind),
			holder,
			redlock.WithExpiry(ttl),
			redlock.WithTries(1),
			redlock.WithTimeoutFactor(0.2),
			redlock.WithShuffleConns(true),
		),
		driver: rd,
	}

	return lease
}

func (rd *RedisKVDriver) Shutdown(ctx context.Context) error {
	for _, conn := range rd.conns {
		conn.Close(ctx)
	}
	return nil
}

func (rd *RedisKVDriver) GetAgentState() (string, error) {
	state, err := rd.Get(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey))
	logging.Debugw("driver GetAgentState", "state", state, "err", err)
	if err != nil {
		return agent.UnavailableState, nil
	}

	if state == agent.UnavailableState {
		// change state to "empty" state when the previous "unavailable" value receieved
		_, _ = rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), agent.EmptyState)
		return agent.EmptyState, nil
	}

	if state == "" {
		return agent.EmptyState, nil
	}

	if !slices.Contains(agent.ValidStates, state) {
		return state, fmt.Errorf("The retrieved agent state '%s' is invalid", state)
	}

	logging.Debugw("driver: GetAgentState", "state", state, "key", rd.cfg.AgentInfoKey(agent.StateKey), "hostname", rd.hostname)
	return state, nil
}

func (rd *RedisKVDriver) SetAgentState(state string) error {
	if state == agent.UnavailableState {
		return nil
	}

	logging.Debugw("driver: SetAgentState", "state", state, "key", rd.cfg.AgentInfoKey(agent.StateKey), "hostname", rd.hostname)
	_, err := rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.StateKey), state)
	return err
}

func (rd *RedisKVDriver) GetAgentMode() (string, error) {
	mode, err := rd.Get(rd.ctx, rd.cfg.AgentInfoKey(agent.ModeKey))
	if err != nil || mode == "" {
		return agent.UnknownMode, err
	}

	return mode, err
}

func (rd *RedisKVDriver) SetAgentMode(mode string) error {
	logging.Debugw("driver: SetAgentMode", "mode", mode, "conns", len(rd.conns), "hostname", rd.hostname)
	_, err := rd.Set(rd.ctx, rd.cfg.AgentInfoKey(agent.ModeKey), mode)
	return err
}

func (rd *RedisKVDriver) GetAgentStatus() (*agent.Status, error) {
	stateKey := rd.cfg.AgentInfoKey(agent.StateKey)
	modeKey := rd.cfg.AgentInfoKey(agent.ModeKey)
	zoneEnableKey := rd.cfg.AgentInfoKey(agent.ZoneEnableKey)

	status := &agent.Status{State: agent.UnavailableState, Mode: agent.UnknownMode, ZoomEnable: false}
	replies, err := rd.MGet(rd.ctx, stateKey, modeKey, zoneEnableKey)
	if err != nil || len(replies) != 3 {
		status.ZoomEnable = rd.cfg.Zone.Enable
		logging.Debugw("driver: GetAgentStatus got error", "state", status.State, "mode", status.Mode, "zoomEnable", status.ZoomEnable, "error", err)
		return status, nil
	}

	// set default zoom enable settting when the zoom enable value is empty
	var zoomEnable bool
	if replies[2] == "" {
		zoomEnable = rd.cfg.Zone.Enable
	} else {
		zoomEnable = redlock.IsBoolStrTrue(replies[2])
	}
	if !rd.cfg.Zone.Enable {
		status.State = rd.cfg.DefaultState
		status.Mode = agent.NormalMode
		status.ZoomEnable = false
	} else {
		status.State = replies[0]
		status.Mode = replies[1]
		status.ZoomEnable = zoomEnable
	}

	if status.State == agent.UnavailableState || status.State == "" {
		// change state to "empty" state when the previous "unavailable" value receieved
		status.State = agent.EmptyState
	}

	if status.Mode == "" {
		status.Mode = agent.UnknownMode
	}

	if !slices.Contains(agent.ValidStates, status.State) && status.State != agent.EmptyState {
		return status, fmt.Errorf("The retrieved agent state '%s' is invalid", status.State)
	}

	if !slices.Contains(agent.ValidModes, status.Mode) {
		return status, fmt.Errorf("The retrieved agent mode '%s' is invalid", status.Mode)
	}

	logging.Debugw("driver: GetAgentStatus", "state", status.State, "mode", status.Mode, "zoomEnable", status.ZoomEnable)

	return status, nil
}

func (rd *RedisKVDriver) SetAgentStatus(status *agent.Status) error {
	if status.State == agent.UnavailableState {
		return nil
	}

	stateKey := rd.cfg.AgentInfoKey(agent.StateKey)
	modeKey := rd.cfg.AgentInfoKey(agent.ModeKey)
	zoneEnableKey := rd.cfg.AgentInfoKey(agent.ZoneEnableKey)

	_, err := rd.MSet(rd.ctx, stateKey, status.State, modeKey, status.Mode, zoneEnableKey, status.ZoomEnable)
	return err
}

func (rd *RedisKVDriver) SetOpearationMode(mode string) {
	if mode == agent.OrphanMode {
		if len(rd.conns) == 1 {
			return
		}

		rd.rlock.SetConns([]redlock.Conn{rd.conns[rd.cfg.Redis.Primary]})
		return
	}

	if len(rd.conns) > 1 {
		return
	}
	rd.conns = rd.originConns
	rd.rlock.SetConns(rd.conns)
}

func (rd *RedisKVDriver) Ping(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.Ping(ctx)
}

func (rd *RedisKVDriver) Get(ctx context.Context, key string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.Get(ctx, key)
}

func (rd *RedisKVDriver) GetSetBool(ctx context.Context, key string, defVal bool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	val, err := rd.rlock.Get(ctx, key)
	if val == "" {
		_, err := rd.rlock.SetBool(ctx, key, defVal)
		if err != nil {
			return defVal, err
		}
	}
	return redlock.IsBoolStrTrue(val), err
}

func (rd *RedisKVDriver) MGet(ctx context.Context, keys ...string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.MGet(ctx, keys...)
}

func (rd *RedisKVDriver) Set(ctx context.Context, key string, value string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.Set(ctx, key, value)
}

func (rd *RedisKVDriver) SetBool(ctx context.Context, key string, value bool) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.SetBool(ctx, key, value)
}

func (rd *RedisKVDriver) MSet(ctx context.Context, pairs ...any) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.MSet(ctx, pairs...)
}

func (rd *RedisKVDriver) leaseKey(lease string, kind string) string {
	if kind == "" {
		kind = "default"
	}
	return rd.cfg.KeyPrefix + "/lease/" + kind + "/" + lease
}

func (rd *RedisKVDriver) isRedisUnhealthy(err error) bool {
	if err == nil {
		return false
	}

	merr := getMultiError(err)
	if merr == nil {
		return false
	}

	conns, quorum := rd.rlock.GetConns()
	if merr.Len() < quorum {
		return false
	}

	n := 0
	for _, err := range merr.Errors {
		if isNetOpError(err) {
			n++
		}
	}
	return n > len(conns)-quorum
}
