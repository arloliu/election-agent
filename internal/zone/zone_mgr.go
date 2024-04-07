package zone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/driver"
	"election-agent/internal/lease"
	"election-agent/internal/logging"
	"election-agent/internal/metric"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	eagrpc "election-agent/proto/election_agent/v1"
)

type ZoneManager interface {
	Start() error
	Shutdown(ctx context.Context) error
	GetActiveZone() (string, error)
	GetPeerStatus() ([]*eagrpc.AgentStatus, error)
	SetPeerStatus(status *eagrpc.AgentStatus) error

	GetAgentStatus() (*agent.Status, error)
	SetAgentStatus(status *agent.Status) error

	SetOperationMode(mode string)
}

var _ ZoneManager = (*zoneManager)(nil)

type zoneManager struct {
	ctx         context.Context
	cfg         *config.Config
	mode        string
	driver      driver.KVDriver
	leaseMgr    *lease.LeaseManager
	ticker      *time.Ticker
	peerClients []eagrpc.ControlClient
	zcClient    *http.Client
	metricMgr   *metric.MetricManager
	reconnected bool
}

type zoneStatus struct {
	zoneEnable    bool
	zcConnected   bool
	peerConnected bool
	activeZone    string
	mode          string
	newMode       string
	state         string
	newState      string
	peerStatus    []*eagrpc.AgentStatus
}

func NewZoneManager(ctx context.Context, cfg *config.Config, driver driver.KVDriver, leaseMgr *lease.LeaseManager, metricMgr *metric.MetricManager) (*zoneManager, error) {
	mgr := &zoneManager{
		ctx:         ctx,
		cfg:         cfg,
		mode:        agent.NormalMode,
		driver:      driver,
		leaseMgr:    leaseMgr,
		peerClients: make([]eagrpc.ControlClient, 0, len(cfg.Zone.PeerURLs)),
		metricMgr:   metricMgr,
	}

	if !cfg.Zone.Enable {
		return mgr, nil
	}

	// check zone configuration
	if cfg.Name == "" {
		return nil, errors.New("The config `name` is empty")
	}
	if cfg.Zone.Name == "" {
		logging.Errorw("zone config", "val", cfg.Zone)
		return nil, errors.New("The config `zone.name` is empty")
	}
	if cfg.Zone.CoordinatorURL == "" {
		return nil, errors.New("The config `zone.coordinator_url` is empty")
	}
	if len(cfg.Zone.PeerURLs) == 0 {
		return nil, errors.New("The config `zone.peer_urls` is empty")
	}

	mgr.zcClient = &http.Client{
		Timeout: cfg.Zone.CoordinatorTimeout,
	}

	peerClients, err := mgr.createPeerClients()
	if err != nil {
		return nil, err
	}
	mgr.peerClients = peerClients

	curStatus, err := mgr.GetAgentStatus()
	if err != nil {
		return nil, err
	}

	if curStatus.State == agent.UnavailableState {
		logging.Warn("Initial zone manager in unavailable state")
	}

	// initial status when the default setting of zone is enabled
	if mgr.cfg.Zone.Enable {
		mgr.cfg.Zone.Enable = curStatus.ZoneEnable
		mgr.leaseMgr.SetStateCache(curStatus.State)
	}

	return mgr, nil
}

func (zm *zoneManager) Start() error {
	zm.ticker = time.NewTicker(zm.cfg.Zone.CheckInterval)
	go func() {
		for {
			select {
			case <-zm.ticker.C:
				zm.checkStatus()

			case <-zm.ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (zm *zoneManager) checkStatus() {
	status := zm.getZoneStatus()
	Check(status, zm.cfg, zm.driver, zm, zm.leaseMgr)
}

func (zm *zoneManager) Shutdown(ctx context.Context) error {
	if zm.ticker != nil {
		zm.ticker.Stop()
	}
	return nil
}

func (zm *zoneManager) GetMode() string {
	return zm.mode
}

func (zm *zoneManager) SetMode(mode string) {
	zm.mode = mode
}

func (zm *zoneManager) GetActiveZone() (string, error) {
	if !zm.cfg.Zone.Enable {
		return zm.cfg.Zone.Name, nil
	}

	ctx, cancel := context.WithTimeout(zm.ctx, zm.cfg.Zone.CheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zm.cfg.Zone.CoordinatorURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-Type", "text/plain")
	req.Header.Add("User-Agent", "Election Agent")

	resp, err := zm.zcClient.Do(req)
	if err != nil {
		return "", err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	return string(body), nil
}

func (zm *zoneManager) GetPeerStatus() ([]*eagrpc.AgentStatus, error) {
	var mu sync.Mutex
	statuses := make([]*eagrpc.AgentStatus, 0, len(zm.peerClients))
	eg := errgroup.Group{}

	// recreate new peer clients to avoid retry lagging
	peerClients, err := zm.createPeerClients()
	if err != nil {
		return nil, err
	}
	zm.peerClients = peerClients

	for _, client := range zm.peerClients {
		client := client
		eg.Go(func() error {
			ctx, cancel := context.WithTimeout(zm.ctx, zm.cfg.Zone.PeerTimeout)
			defer cancel()

			status, err := client.GetStatus(ctx, &eagrpc.Empty{})
			if err != nil {
				return err
			}
			if status.State == agent.UnavailableState {
				return nil
			}

			mu.Lock()
			defer mu.Unlock()
			statuses = append(statuses, status)

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, err
	}

	return statuses, nil
}

func (zm *zoneManager) SetPeerStatus(status *eagrpc.AgentStatus) error {
	var n atomic.Int32
	eg := errgroup.Group{}
	for _, client := range zm.peerClients {
		client := client
		eg.Go(func() error {
			ctx, cancel := context.WithTimeout(zm.ctx, zm.cfg.Zone.PeerTimeout)
			defer cancel()

			result, err := client.SetStatus(ctx, status)
			if err != nil {
				logging.Warnw("Failed to set peer state",
					"agent", zm.cfg.Name,
					"zone", zm.cfg.Zone.Name,
					"mode", status.Mode,
					"state", status.State,
					"zone_enable", status.ZoneEnable,
					"err", err,
				)
				return err
			}
			if result.Value {
				n.Add(1)
			}

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}

	return nil
}

func (zm *zoneManager) GetAgentStatus() (*agent.Status, error) {
	return zm.driver.GetAgentStatus()
}

func (zm *zoneManager) SetAgentStatus(status *agent.Status) error {
	return zm.driver.SetAgentStatus(status)
}

func (zm *zoneManager) SetOperationMode(mode string) {
	zm.driver.SetOperationMode(mode)
}

func (zm *zoneManager) createPeerClients() ([]eagrpc.ControlClient, error) {
	peerClients := make([]eagrpc.ControlClient, 0, len(zm.cfg.Zone.PeerURLs))
	retryPolicy := `{
		"methodConfig": [{
			"name": [{"service": "grpc.election_agent.v1.Control"}],
			"waitForReady": true,
			"timeout": "0.5s",
			"retryPolicy": {
				"maxAttempts": 3,
				"initialBackoff": "0.1s",
				"maxBackoff": "1s",
				"backoffMultiplier": 2.0,
				"retryableStatusCodes": [ "UNAVAILABLE" ]
			}
		}]}`

	for _, peerURL := range zm.cfg.Zone.PeerURLs {
		conn, err := grpc.DialContext(zm.ctx, peerURL,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithDefaultServiceConfig(retryPolicy),
		)
		if err != nil {
			return nil, err
		}
		peerClients = append(peerClients, eagrpc.NewControlClient(conn))
	}

	return peerClients, nil
}

func (zm *zoneManager) getZoneStatus() *zoneStatus {
	var err error
	status := &zoneStatus{zoneEnable: zm.cfg.Zone.Enable, mode: agent.UnknownMode, zcConnected: false, peerConnected: true}

	n, _ := zm.driver.Ping(zm.ctx)
	disconectedBackends := len(zm.cfg.Redis.URLs) - n
	if disconectedBackends == 0 {
		zm.reconnected = false
	} else if !zm.reconnected {
		// try to rebuild connections only once when some redis backends are disconnected
		logging.Infow("Redis backends are disconnected, rebuild connections in driver", "disconnected_backends", disconectedBackends)
		if err := zm.driver.RebuildConnections(); err != nil {
			logging.Warnw("Failed to rebuild backend connections", "error", err)
		}
		zm.reconnected = true
	}

	kvStatus, err := zm.driver.GetAgentStatus()
	if err != nil {
		logging.Warnw("Failed to get current agent status",
			"agent", zm.cfg.Name,
			"zone", zm.cfg.Zone.Name,
			"error", err,
		)
	}
	status.state = kvStatus.State
	status.mode = kvStatus.Mode
	status.zoneEnable = kvStatus.ZoneEnable

	if !status.zoneEnable {
		return status
	}

	status.activeZone, err = zm.GetActiveZone()
	if err == nil || status.activeZone != "" {
		status.zcConnected = true
	}

	status.peerStatus, _ = zm.GetPeerStatus()
	if len(status.peerStatus) == 0 {
		status.peerConnected = false
	}

	if zm.metricMgr.Enabled() {
		// check and set metrics in goroutine to prevent getZoneStatus from taking too long to execute
		go func(peerConnected int, zcConnected bool) {
			zm.metricMgr.SetDisconnectedRedisBackends(disconectedBackends)
			zm.metricMgr.SetDisconnectedPeers(len(zm.cfg.Zone.PeerURLs) - peerConnected)
			zm.metricMgr.IsZCConnected(zcConnected)
		}(len(status.peerStatus), status.zcConnected)
	}

	return status
}

// Check zone and peers status and set proper agent state and mode.
// This method is seperated from zoneManager to support unit testing.
func Check(status *zoneStatus, cfg *config.Config, kvDriver driver.KVDriver, zm ZoneManager, lm *lease.LeaseManager) {
	// only update the local state cache when agent is in the standalone mode
	if !status.zoneEnable {
		lm.SetStateCache(status.state)
		return
	}

	var err error

	if status.zcConnected {
		status.newMode = agent.NormalMode

		if status.state == agent.UnavailableState {
			status.newState = agent.UnavailableState
		} else if cfg.Zone.Name == status.activeZone {
			status.newState = agent.ActiveState
		} else {
			status.newState = agent.StandbyState
		}
	} else if status.peerConnected { // failed to connect to zone coordinator, but some peers are alive
		status.newMode = agent.NormalMode

		peerActive := false
		for _, peerState := range status.peerStatus {
			if peerState.State == agent.ActiveState {
				peerActive = true
				break
			}
		}

		if status.state == agent.UnavailableState {
			status.newState = agent.UnavailableState
		} else if peerActive {
			status.newState = agent.StandbyState
		} else {
			status.newState = agent.ActiveState
		}
	} else { // orphan mode
		status.newMode = agent.OrphanMode

		switch status.state {
		case agent.UnavailableState:
			status.newState = agent.UnavailableState
		case agent.EmptyState:
			status.newState = agent.ActiveState
		default:
			if status.mode != agent.OrphanMode {
				status.newState = agent.FlipState(status.state)
			} else {
				status.newState = status.state
			}
		}
	}

	// 1. set operation mode
	zm.SetOperationMode(status.newMode)

	// 2. update agent state & mode
	if status.mode != status.newMode {
		logging.Infow("Change agent mode",
			"agent", cfg.Name,
			"zone", cfg.Zone.Name,
			"old_mode", status.mode,
			"new_mode", status.newMode,
		)
	}

	logging.Debugw("SetAgentStatus",
		"state", status.state, "newState", status.newState, "mode", status.mode, "newMode", status.newMode,
		"hostname", os.Getenv("HOSTNAME"),
	)
	err = zm.SetAgentStatus(&agent.Status{State: status.newState, Mode: status.newMode, ZoneEnable: status.zoneEnable})
	if err != nil {
		logging.Errorw("Failed to update agent status",
			"agent", cfg.Name,
			"zone", cfg.Zone.Name,
			"state", status.state,
			"newState", status.newState,
			"mode", status.mode,
			"newMode", status.newMode,
			"error", err,
		)
	}

	// 3. notify lease manager to update agent state cache
	lm.SetStateCache(status.newState)

	// 4. notify agent peers to enter standby mode if this agent becomes active one
	if status.newState == agent.ActiveState && status.peerConnected {
		err := zm.SetPeerStatus(&eagrpc.AgentStatus{State: agent.StandbyState, Mode: agent.NormalMode, ZoneEnable: true})
		if err != nil {
			logging.Errorw("Failed to notify agent peers to enter standby mode",
				"agent", cfg.Name,
				"zone", cfg.Zone.Name,
				"error", err,
			)
		}
	}

	logging.Debugw("Zone status",
		"zoneEnable", status.zoneEnable,
		"zcConnected", status.zcConnected,
		"peerConnected", status.peerConnected,
		"state", status.state+"->"+status.newState,
		"mode", fmt.Sprintf("%s->%s", status.mode, status.newMode),
		"activeZone", status.activeZone,
	)
}
