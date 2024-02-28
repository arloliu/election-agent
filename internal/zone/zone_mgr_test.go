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
	mgr, err := newMockZoneManager(ctx, cfg)
	require.NoError(err)
	require.NotNil(mgr)

	status := &zoneStatus{
		zoomEnable:    true,
		activeZone:    "test-zone2",
		zcConnected:   true,
		peerConnected: true,
		peerStates:    []*eagrpc.AgentState{{State: agent.StandbyState, ZoomEnable: true}},
		mode:          agent.NormalMode,
		state:         agent.ActiveState,
	}
	mgr.Check(status)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// the activeZone is different, should be standby state
	status.zcConnected = true
	mgr.Check(status)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// the ZC is disconnected & peer is standby, should be standby->active
	status.zcConnected = false
	status.peerConnected = true
	mgr.Check(status)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC & peer are disconnected, should be active->standby & normal->orhpan
	status.zcConnected = false
	status.peerConnected = false
	mgr.Check(status)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// the peer back & peer is standby, should be standby->active & orhpan->normal
	status.zcConnected = false
	status.peerConnected = true
	mgr.Check(status)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC back, change to current zone, should be active->active
	status.zcConnected = true
	status.activeZone = "test-zone1"
	mgr.Check(status)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC change to another zone, should be active->standby
	status.zcConnected = true
	status.activeZone = "test-zone2"
	mgr.Check(status)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// ZC & peer are disconnected, should be standby->active & normal->orhpan
	status.zcConnected = false
	status.peerConnected = false
	mgr.Check(status)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// continuously in orhpan mode , keep active state
	mgr.Check(status)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// peer back & peer is active, should be active->standby, orphan->normal
	status.zcConnected = false
	status.peerConnected = true
	status.peerStates = []*eagrpc.AgentState{{State: agent.ActiveState, ZoomEnable: true}}
	mgr.Check(status)
	require.Equal(agent.StandbyState, status.newState)
	require.Equal(agent.NormalMode, status.newMode)
	updateStatus(status)

	// peer is disconnected again, should be standby->active, normal->orphan
	status.zcConnected = false
	status.peerConnected = false
	status.peerStates = []*eagrpc.AgentState{{State: agent.ActiveState, ZoomEnable: true}}
	mgr.Check(status)
	require.Equal(agent.ActiveState, status.newState)
	require.Equal(agent.OrphanMode, status.newMode)
	updateStatus(status)

	// ZC back, change to current zone, should be keep active, orphan->normal
	status.zcConnected = true
	status.activeZone = "test-zone1"
	mgr.Check(status)
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

func newMockZoneManager(ctx context.Context, cfg *config.Config) (ZoneManager, error) {
	driver := driver.NewMockRedisKVDriver(cfg)
	if driver == nil {
		return nil, errors.New("driver is nil")
	}

	leaseMgr := lease.NewLeaseManager(ctx, cfg, driver)
	if leaseMgr == nil {
		return nil, errors.New("lease manager is nil")
	}
	zoneMgr, err := NewZoneManager(ctx, cfg, driver, leaseMgr)
	if err != nil {
		return nil, err
	}
	return zoneMgr, nil
	// mockMgr := &MockZoneManager{}

	// mockMgr.On("Check", mock.Anything).
	// 	Run(func(args mock.Arguments) {
	// 		status, _ := args.Get(0).(*zoneStatus)
	// 		zoneMgr.Check(status)
	// 	})
	// mockMgr.On("Shutdown", mock.Anything).Return(zoneMgr.Shutdown(ctx))
	// mockMgr.On("GetAgentState").Return(zoneMgr.GetAgentState())
	// mockMgr.On("SetAgentState", mock.AnythingOfType("string")).
	// 	Return(func(state string) error {
	// 		return zoneMgr.SetAgentState(state)
	// 	})
	// mockMgr.On("GetZoomEnable").Return(zoneMgr.GetZoomEnable())
	// mockMgr.On("SetZoomEnable", mock.AnythingOfType("bool")).
	// 	Return(func(enable bool) error {
	// 		return zoneMgr.SetZoomEnable(enable)
	// 	})

	// return mockMgr, nil
}
