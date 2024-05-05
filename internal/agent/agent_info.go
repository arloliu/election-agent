package agent

import (
	"sync"
	"sync/atomic"
	"time"

	pb "election-agent/proto/election_agent/v1"
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
	ActiveZoneKey = "active_zone"
	ZoneEnableKey = "zone_enable"
)

type Status struct {
	State         string
	Mode          string
	ActiveZone    string
	ZcConnected   bool
	PeerConnected bool
	mu            sync.Mutex
}

func (s *Status) Store(status *Status) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.State = status.State
	s.Mode = status.Mode
	s.ActiveZone = status.ActiveZone
	s.ZcConnected = status.ZcConnected
	s.PeerConnected = status.PeerConnected
}

func (s *Status) Load() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{State: s.State, Mode: s.Mode, ActiveZone: s.ActiveZone, ZcConnected: s.ZcConnected, PeerConnected: s.PeerConnected}
}

func (s *Status) Proto() *pb.AgentStatus {
	return &pb.AgentStatus{State: s.State, Mode: s.Mode, ActiveZone: s.ActiveZone, ZcConnected: s.ZcConnected, PeerConnected: s.PeerConnected}
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
	last atomic.Pointer[time.Time]
	ttl  time.Duration
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
