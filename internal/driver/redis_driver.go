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
	"election-agent/internal/logging"

	"github.com/dolthub/maphash"
)

// RedisKVDriver implements KVDriver interface
type RedisKVDriver struct {
	ctx      context.Context
	cfg      *config.Config
	conns    []redlock.Conn
	mode     string
	rlock    *redlock.RedLock
	mu       sync.Mutex
	hasher   maphash.Hasher[string]
	hostname string

	stateKey      string
	modeKey       string
	zoneEnableKey string
}

var _ KVDriver = (*RedisKVDriver)(nil)

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
		ctx:           ctx,
		cfg:           cfg,
		conns:         conns,
		mode:          agent.NormalMode,
		rlock:         redlock.New(conns...),
		hasher:        maphash.NewHasher[string](),
		hostname:      os.Getenv("HOSTNAME"),
		stateKey:      cfg.AgentInfoKey(agent.StateKey),
		modeKey:       cfg.AgentInfoKey(agent.ModeKey),
		zoneEnableKey: cfg.AgentInfoKey(agent.ZoneEnableKey),
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

func (rd *RedisKVDriver) ConnectionCount() int {
	conns, _ := rd.rlock.GetConns()
	return len(conns)
}

func (rd *RedisKVDriver) RebuildConnections() error {
	newConns, err := redlock.CreateConnections(rd.ctx, rd.cfg)
	if err != nil {
		return err
	}

	rd.mu.Lock()
	rd.conns = newConns
	rd.mu.Unlock()

	var conns []redlock.Conn
	if rd.mode == agent.OrphanMode {
		conns = []redlock.Conn{rd.conns[rd.cfg.Redis.Primary]}
	} else {
		conns = rd.conns
	}
	rd.rlock.SetConns(conns)
	return nil
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

func (rd *RedisKVDriver) NewMutex(name string, kind string, holder string, ttl time.Duration) Mutex {
	if kind == "" {
		kind = "default"
	}

	return rd.rlock.NewMutex(
		rd.leaseKey(name, kind),
		holder,
		redlock.WithExpiry(ttl),
		redlock.WithTries(1),
		redlock.WithTimeoutFactor(0.2),
	)
}

func (rd *RedisKVDriver) Shutdown(ctx context.Context) error {
	for _, conn := range rd.conns {
		conn.Close(ctx)
	}
	return nil
}

func (rd *RedisKVDriver) GetAgentState() (string, error) {
	state, err := rd.Get(rd.ctx, rd.stateKey)
	logging.Debugw("driver GetAgentState", "state", state, "err", err)
	if err != nil {
		return agent.UnavailableState, nil
	}

	if state == agent.UnavailableState {
		// change state to "empty" state when the previous "unavailable" value receieved
		_, _ = rd.Set(rd.ctx, rd.stateKey, agent.EmptyState)
		return agent.EmptyState, nil
	}

	if state == "" {
		return agent.EmptyState, nil
	}

	if !slices.Contains(agent.ValidStates, state) {
		return state, fmt.Errorf("The retrieved agent state '%s' is invalid", state)
	}

	logging.Debugw("driver: GetAgentState", "state", state, "key", rd.stateKey, "hostname", rd.hostname)
	return state, nil
}

func (rd *RedisKVDriver) SetAgentMode(mode string) error {
	logging.Debugw("driver: SetAgentMode", "mode", mode, "hostname", rd.hostname)
	_, err := rd.Set(rd.ctx, rd.modeKey, mode)
	return err
}

func (rd *RedisKVDriver) GetAgentStatus() (*agent.Status, error) {
	status := &agent.Status{State: agent.UnavailableState, Mode: agent.UnknownMode, ZoneEnable: false}
	replies, err := rd.MGet(rd.ctx, rd.stateKey, rd.modeKey, rd.zoneEnableKey)
	if err != nil || len(replies) != 3 {
		status.ZoneEnable = rd.cfg.Zone.Enable
		logging.Debugw("driver: GetAgentStatus got error", "state", status.State, "mode", status.Mode, "zoneEnable", status.ZoneEnable, "error", err)
		return status, nil
	}

	// set default zoom enable settting when the zoom enable value is empty
	var zoneEnable bool
	if replies[2] == "" {
		zoneEnable = rd.cfg.Zone.Enable
	} else {
		zoneEnable = redlock.IsBoolStrTrue(replies[2])
	}
	if !rd.cfg.Zone.Enable {
		status.State = rd.cfg.DefaultState
		status.Mode = agent.NormalMode
		status.ZoneEnable = false
	} else {
		status.State = replies[0]
		status.Mode = replies[1]
		status.ZoneEnable = zoneEnable
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

	return status, nil
}

func (rd *RedisKVDriver) SetAgentStatus(status *agent.Status) error {
	if status.State == agent.UnavailableState {
		return nil
	}

	_, err := rd.MSet(rd.ctx, rd.stateKey, status.State, rd.modeKey, status.Mode, rd.zoneEnableKey, status.ZoneEnable)
	return err
}

func (rd *RedisKVDriver) SetOperationMode(mode string) {
	rd.mu.Lock()

	if rd.mode == mode {
		rd.mu.Unlock()
		return
	}

	rd.mode = mode
	rd.mu.Unlock()

	if mode == agent.OrphanMode {
		conns := []redlock.Conn{rd.conns[rd.cfg.Redis.Primary]}
		rd.rlock.SetConns(conns)
		return
	}

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

func (rd *RedisKVDriver) IsUnhealthy(err error) bool {
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
