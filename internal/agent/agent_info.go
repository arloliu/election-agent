package agent

import (
	"sync/atomic"
	"time"
)

const (
	ActiveState      = "active"
	StandbyState     = "standby"
	UnavailableState = "unavailable" // when all backend nodes failed
)

var ValidStates = []string{ActiveState, StandbyState, UnavailableState}

const (
	NormalMode  = "normal"
	OrphanMode  = "orphan"
	UnknownMode = "unknown" // when all backend nodes failed
)

var ValidModes = []string{NormalMode, OrphanMode, UnknownMode}

const (
	StateKey = "state"
	ModeKey  = "mode"
)

type State struct {
	val  atomic.Value
	ttl  time.Duration
	last time.Time
}

func NewState(state string, ttl time.Duration) *State {
	s := State{ttl: ttl, last: time.Now()}
	s.Store(state)
	return &s
}

func (s *State) Load() string {
	if s.Expired() {
		s.val.Store("")
		return ""
	}
	return s.val.Load().(string)
}

func (s *State) Store(val string) {
	s.last = time.Now()
	s.val.Store(val)
}

func (s *State) Expired() bool {
	return s.last.Add(s.ttl).Before(time.Now())
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
	}
	return ActiveState
}
