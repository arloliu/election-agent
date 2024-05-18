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
	"golang.org/x/sync/errgroup"
)

// RedisKVDriver implements KVDriver interface
type RedisKVDriver struct {
	ctx        context.Context
	cfg        *config.Config
	connShards redlock.ConnShards
	mode       string
	rlock      *redlock.RedLock
	mu         sync.Mutex
	hasher     maphash.Hasher[string]
	hostname   string

	stateKey      string
	modeKey       string
	activeZoneKey string
	zoneEnableKey string
}

var _ KVDriver = (*RedisKVDriver)(nil)

func NewRedisKVDriver(ctx context.Context, cfg *config.Config) (*RedisKVDriver, error) {
	switch cfg.Driver {
	case "goredis":
		redlock.RegisterCreateConnectionsFunc(redlock.CreateGoredisConnections)
	case "rueidis":
		redlock.RegisterCreateConnectionsFunc(redlock.CreateRueidisConnections)
	}

	connShards, err := redlock.CreateConnections(ctx, cfg)
	if err != nil {
		return nil, err
	}

	idx := cfg.Redis.Primary
	if idx < 0 || idx >= len(connShards[0]) {
		errMsg := fmt.Sprintf("The primary index %d is out of range", idx)
		logging.Error(errMsg)
		return nil, errors.New(errMsg)
	}

	inst := &RedisKVDriver{
		ctx:           ctx,
		cfg:           cfg,
		connShards:    connShards,
		mode:          agent.NormalMode,
		rlock:         redlock.New(connShards...),
		hasher:        maphash.NewHasher[string](),
		hostname:      os.Getenv("HOSTNAME"),
		stateKey:      cfg.AgentInfoKey(agent.StateKey),
		modeKey:       cfg.AgentInfoKey(agent.ModeKey),
		activeZoneKey: cfg.AgentInfoKey(agent.ActiveZoneKey),
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

	logging.Infow("Key-value driver",
		"driver", cfg.Driver,
		"mode", cfg.Redis.Mode,
		"primary", cfg.Redis.Primary,
		"operation_timeout", cfg.Redis.OpearationTimeout,
		"backend_count", inst.rlock.GetAllConnCount(),
	)
	return inst, err
}

func (rd *RedisKVDriver) ConnectionCount() int {
	return rd.rlock.GetAllConnCount()
}

func (rd *RedisKVDriver) RebuildConnections() error {
	newConnGroups, err := redlock.CreateConnections(rd.ctx, rd.cfg)
	if err != nil {
		return err
	}

	rd.mu.Lock()
	rd.connShards = newConnGroups
	rd.mu.Unlock()

	var connShards []redlock.ConnGroup
	if rd.mode == agent.OrphanMode {
		connShards = []redlock.ConnGroup{rd.connShards[rd.cfg.Redis.Primary]}
	} else {
		connShards = rd.connShards
	}
	rd.rlock.SetConnShards(connShards)
	return nil
}

func (rd *RedisKVDriver) LeaseID(name string, kind string, holder string, ttl time.Duration) uint64 {
	return rd.hasher.Hash(rd.cfg.LeaseKey(name, kind) + holder + strconv.FormatInt(int64(ttl), 16))
}

func (rd *RedisKVDriver) GetHolder(ctx context.Context, name string, kind string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout)
	defer cancel()
	return rd.rlock.Get(ctx, rd.cfg.LeaseKey(name, kind))
}

func (rd *RedisKVDriver) GetHolders(ctx context.Context, kind string) ([]Holder, error) {
	ctx, cancel := context.WithTimeout(ctx, rd.cfg.Redis.OpearationTimeout*2)
	defer cancel()

	type holderInfo struct {
		val   string
		count int
	}

	holders := make([]Holder, 0)
	holderMap := make(map[string]*holderInfo, 0)
	match := rd.cfg.LeaseKindPrefix(kind) + "*"

	var mu sync.Mutex
	for _, connGroup := range rd.rlock.GetConnShards() {
		errs, ctx := errgroup.WithContext(ctx)
		for _, conn := range connGroup {
			conn := conn
			errs.Go(func() error {
				var cursor uint64

				holderSet := make(map[string]struct{}, 0)
				for {
					var err error
					var keys []string
					keys, cursor, err = conn.Scan(ctx, cursor, match, 50)
					if err != nil {
						return err
					}

					if len(keys) == 0 {
						if cursor == 0 {
							break
						}
						continue
					}
					var vals []string
					vals, err = conn.MGet(ctx, keys...)
					if err != nil {
						return err
					}

					mu.Lock()
					for i, key := range keys {
						val := vals[i]
						if val == "" {
							continue
						}
						if _, ok := holderSet[key]; ok {
							continue
						}

						holderSet[key] = struct{}{}
						if _, ok := holderMap[key]; !ok {
							holderMap[key] = &holderInfo{val: val, count: 1}
						} else {
							holderMap[key].count++
						}
					}
					mu.Unlock()

					if cursor == 0 {
						break
					}
				}
				return nil
			})
		}
		if err := errs.Wait(); err != nil {
			return holders, err
		}
	}

	quorum := rd.rlock.GetQuorum()
	for key, info := range holderMap {
		if info.count >= quorum {
			holders = append(holders, Holder{Election: key, Name: info.val})
		}
	}
	return holders, nil
}

func (rd *RedisKVDriver) NewMutex(name string, kind string, holder string, ttl time.Duration) Mutex {
	return rd.rlock.NewMutex(
		rd.cfg.LeaseKey(name, kind),
		holder,
		redlock.WithExpiry(ttl),
		redlock.WithTries(1),
		redlock.WithTimeoutFactor(0.2),
	)
}

func (rd *RedisKVDriver) Shutdown(ctx context.Context) error {
	for _, connShards := range rd.connShards {
		for _, conn := range connShards {
			conn.Close(ctx)
		}
	}
	return nil
}

func (rd *RedisKVDriver) GetActiveZone() (string, error) {
	return rd.Get(rd.ctx, rd.activeZoneKey)
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
	status := &agent.Status{State: agent.UnavailableState, Mode: agent.UnknownMode}
	replies, err := rd.MGet(rd.ctx, rd.stateKey, rd.modeKey, rd.activeZoneKey)
	if err != nil || len(replies) != 3 {
		logging.Debugw("driver: GetAgentStatus got error", "state", status.State, "mode", status.Mode, "error", err)
		return status, nil
	}

	if !rd.cfg.Zone.Enable {
		status.State = rd.cfg.DefaultState
		status.Mode = agent.NormalMode
	} else {
		status.State = replies[0]
		status.Mode = replies[1]
		status.ActiveZone = replies[2]
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

	_, err := rd.MSet(rd.ctx, rd.stateKey, status.State, rd.modeKey, status.Mode, rd.activeZoneKey, status.ActiveZone)
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
		connShards := []redlock.ConnGroup{rd.connShards[rd.cfg.Redis.Primary]}
		rd.rlock.SetConnShards(connShards)
		return
	}

	rd.rlock.SetConnShards(rd.connShards)
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

func (rd *RedisKVDriver) IsUnhealthy(err error) bool {
	if err == nil {
		return false
	}

	merr := getMultiError(err)
	if merr == nil {
		return false
	}

	quorum := rd.rlock.GetQuorum()
	if merr.Len() < quorum {
		return false
	}

	n := 0
	for _, err := range merr.Errors {
		if isNetOpError(err) {
			n++
		}
	}
	return n >= quorum
}
