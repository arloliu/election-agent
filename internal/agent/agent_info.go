package agent

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	ActiveState      = "active"
	StandbyState     = "standby"
	UnavailableState = "unavailable" // when all backend nodes failed
	EmptyState       = "empty"       // when the value in all backend nodes are empty
)

var ValidStates = []string{ActiveState, StandbyState, UnavailableState}

const (
	NormalMode  = "normal"
	OrphanMode  = "orphan"
	UnknownMode = "unknown" // when all backend nodes failed
)

var ValidModes = []string{NormalMode, OrphanMode, UnknownMode}

const (
	StateKey      = "state"
	ModeKey       = "mode"
	ZoneEnableKey = "zone_enable"
)

type Status struct {
	mu    sync.Mutex
	State string
	Mode  string
}

func (s *Status) Store(status *Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	val := status.Load()
	s.State = val.State
	s.Mode = val.Mode
}

func (s *Status) Load() Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	return Status{State: s.State, Mode: s.Mode}
}

// local agent status instance
var localStatus Status = Status{State: UnavailableState, Mode: UnknownMode}

func SetLocalStatus(status *Status) {
	localStatus.Store(status)
}

func GetLocalStatus() Status {
	return localStatus.Load()
}

type State struct {
	val  atomic.Value
	ttl  time.Duration
	last atomic.Pointer[time.Time]
}

func NewState(state string, ttl time.Duration) *State {
	s := State{ttl: ttl}
	now := time.Now()
	s.last.Store(&now)
	s.Store(state)
	return &s
}

func (s *State) Load() string {
	return s.val.Load().(string)
}

func (s *State) Store(val string) {
	now := time.Now()
	s.last.Store(&now)
	s.val.Store(val)
}

func (s *State) Expired() bool {
	if s.ttl == time.Duration(0) {
		return true
	}
	return s.last.Load().Add(s.ttl).Before(time.Now())
}

func (s *State) IsActive() bool {
	return s.val.Load().(string) == ActiveState
}

func (s *State) IsStandby() bool {
	return s.val.Load().(string) == StandbyState
}

func (s *State) IsUnavailable() bool {
	return s.val.Load().(string) == UnavailableState
}

func FlipState(state string) string {
	if state == ActiveState {
		return StandbyState
	} else if state == StandbyState {
		return ActiveState
	}
	return state
}
