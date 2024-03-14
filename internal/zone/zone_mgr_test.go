package zone

import (
	"context"
	"errors"
	"os"
	"testing"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/driver"
	"election-agent/internal/lease"
	"election-agent/internal/logging"
	eagrpc "election-agent/proto/election_agent/v1"

	mock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	config.Default = &config.Config{
		Env:      "test",
		LogLevel: "info",
	}

	logger := logging.Init()
	if logger == nil {
		panic(errors.New("Failed to initial logger"))
	}

	exitCode := m.Run()
	os.Exit(exitCode)
}

func TestZoneManager_BacicChecks(t *testing.T) {
	require := require.New(t)

	ctx := context.TODO()
	cfg := &config.Config{
		Name:         "test-election-agent",
		DefaultState: "active",
		KeyPrefix:    "test_agent",
		Redis:        config.RedisConfig{},
		Zone: config.ZoneConfig{
			Enable:         true,
			Name:           "test-zone1",
			CoordinatorURL: "http://fake",
			PeerURLs:       []string{"fake_peer"},
		},
	}
	m, err := newMockZoneManager(ctx, cfg)
	require.NoError(err)
	require.NotNil(m)

	status := &zoneStatus{
		zoomEnable:    true,
		activeZone:    "test-zone2",
		zcConnected:   true,
		peerConnected: true,
		peerStatus:    []*eagrpc.AgentStatus{{State: agent.StandbyState, ZoomEnable: true}},
		mode:          agent.NormalMode,
		state:         agent.ActiveState,
	}
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// the activeZone is different, should be standby state
	status.zcConnected = true
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// the ZC is disconnected & peer is standby, should be standby->active
	status.zcConnected = false
	status.peerConnected = true
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC & peer are disconnected, should be active->standby & normal->orhpan
	status.zcConnected = false
	status.peerConnected = false
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// the peer back & peer is standby, should be standby->active & orhpan->normal
	status.zcConnected = false
	status.peerConnected = true
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC back, change to current zone, should be active->active
	status.zcConnected = true
	status.activeZone = "test-zone1"
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC change to another zone, should be active->standby
	status.zcConnected = true
	status.activeZone = "test-zone2"
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC & peer are disconnected, should be standby->active & normal->orhpan
	status.zcConnected = false
	status.peerConnected = false
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// continuously in orhpan mode , keep active state
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// peer back & peer is active, should be active->standby, orphan->normal
	status.zcConnected = false
	status.peerConnected = true
	status.peerStatus = []*eagrpc.AgentStatus{{State: agent.ActiveState, Mode: agent.NormalMode, ZoomEnable: true}}
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// peer is disconnected again, should be standby->active, normal->orphan
	status.zcConnected = false
	status.peerConnected = false
	status.peerStatus = []*eagrpc.AgentStatus{{State: agent.ActiveState, Mode: agent.NormalMode, ZoomEnable: true}}
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// ZC back, change to current zone, should be keep active, orphan->normal
	status.zcConnected = true
	status.activeZone = "test-zone1"
	Check(status, cfg, m.kvDriver, m.mockZm, m.lm)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)
}

func updateStatus(status *zoneStatus) {
	status.state = status.newState
	status.mode = status.newMode
	status.newState = ""
	status.newMode = ""
}

type mockComponent struct {
	zm       ZoneManager
	mockZm   *MockZoneManager
	lm       *lease.LeaseManager
	kvDriver lease.KVDriver
}

func newMockZoneManager(ctx context.Context, cfg *config.Config) (*mockComponent, error) {
	m := mockComponent{}

	m.kvDriver = driver.NewMockRedisKVDriver(cfg)
	if m.kvDriver == nil {
		return nil, errors.New("m.kvDriver is nil")
	}

	m.lm = lease.NewLeaseManager(ctx, cfg, m.kvDriver)
	if m.lm == nil {
		return nil, errors.New("lease manager is nil")
	}

	var err error
	m.zm, err = NewZoneManager(ctx, cfg, m.kvDriver, m.lm)
	if err != nil {
		return nil, err
	}
	// return zoneMgr, nil
	mockMgr := &MockZoneManager{}

	mockMgr.On("SetPeerStatus", mock.AnythingOfType("*election_agent_v1.AgentStatus")).Return(nil)
	mockMgr.On("SetAgentState", mock.AnythingOfType("string")).
		Return(func(state string) error {
			return m.zm.SetAgentState(state)
		})

	mockMgr.On("SetAgentMode", mock.AnythingOfType("string")).
		Return(func(mode string) error {
			return m.zm.SetAgentMode(mode)
		})

	mockMgr.On("SetOpearationMode", mock.AnythingOfType("string")).Return()
	mockMgr.On("GetZoomEnable").
		Return(func() (bool, error) {
			return m.zm.GetZoomEnable()
		})
	mockMgr.On("SetZoomEnable", mock.AnythingOfType("bool")).
		Return(func(enable bool) error {
			return m.zm.SetZoomEnable(enable)
		})

	m.mockZm = mockMgr
	return &m, nil
}
