package zone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/lease"
	"election-agent/internal/logging"

	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	eagrpc "election-agent/proto/election_agent/v1"
)

const ZoneEnableKey = "zone_enable"

type ZoneManager interface {
	Start() error
	Shutdown(ctx context.Context) error
	GetActiveZone() (string, error)
	GetPeerStatus() ([]*eagrpc.AgentStatus, error)
	SetPeerStatus(status *eagrpc.AgentStatus) error
	GetAgentState() (string, error)
	SetAgentState(state string) error
	GetAgentMode() (string, error)
	SetAgentMode(mode string) error
	GetZoomEnable() (bool, error)
	SetZoomEnable(enable bool) error
}

var _ ZoneManager = (*zoneManager)(nil)

type zoneManager struct {
	ctx         context.Context
	cfg         *config.Config
	mode        string
	driver      lease.KVDriver
	mutex       lease.Mutex
	leaseMgr    *lease.LeaseManager
	ticker      *time.Ticker
	peerClients []eagrpc.ControlClient
	zcClient    *http.Client
	zcReq       *http.Request
}

type zoneStatus struct {
	zoomEnable    bool
	zcConnected   bool
	peerConnected bool
	activeZone    string
	mode          string
	newMode       string
	state         string
	newState      string
	peerStatus    []*eagrpc.AgentStatus
}

func NewZoneManager(ctx context.Context, cfg *config.Config, driver lease.KVDriver, leaseMgr *lease.LeaseManager) (*zoneManager, error) {
	mgr := &zoneManager{
		ctx:         ctx,
		cfg:         cfg,
		mode:        agent.NormalMode,
		driver:      driver,
		leaseMgr:    leaseMgr,
		peerClients: make([]eagrpc.ControlClient, 0, len(cfg.Zone.PeerURLs)),
	}

	curState, err := mgr.GetAgentState()
	if err != nil {
		return nil, err
	}

	if curState != agent.UnavailableState {
		if err := mgr.SetAgentState(cfg.DefaultState); err != nil {
			return nil, err
		}
		if err := mgr.SetZoomEnable(cfg.Zone.Enable); err != nil {
			return nil, err
		}
	} else {
		logging.Warn("Initial zone manager in unavailable state")
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

	mgr.mutex = driver.NewMutex(cfg.Name, cfg.Zone.CheckTimeout)

	mgr.zcClient = &http.Client{
		Timeout: cfg.Zone.CoordinatorTimeout,
	}

	req, err := http.NewRequest(http.MethodGet, cfg.Zone.CoordinatorURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", "text/plain")
	req.Header.Add("User-Agent", "Election Agent")
	mgr.zcReq = req

	peerClients, err := mgr.createPeerClients()
	if err != nil {
		return nil, err
	}
	mgr.peerClients = peerClients

	return mgr, nil
}

func (zm *zoneManager) Start() error {
	if !zm.cfg.Zone.Enable {
		return nil
	}

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
	err := zm.mutex.Lock(zm.ctx)
	if err != nil {
		return
	}

	defer func() {
		for i := 0; i < 3; i++ {
			ok, err := zm.mutex.Unlock(zm.ctx)
			if ok && err == nil {
				return
			}
		}
	}()

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

	resp, err := zm.zcClient.Do(zm.zcReq)
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
					"zoom_enable", status.ZoomEnable,
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

func (zm *zoneManager) GetAgentState() (string, error) {
	return zm.driver.GetAgentState()
}

func (zm *zoneManager) SetAgentState(state string) error {
	zm.leaseMgr.SetStateCache(state)
	return zm.driver.SetAgentState(state)
}

func (zm *zoneManager) GetAgentMode() (string, error) {
	return zm.driver.GetAgentMode()
}

func (zm *zoneManager) SetAgentMode(mode string) error {
	return zm.driver.SetAgentMode(mode)
}

func (zm *zoneManager) GetZoomEnable() (bool, error) {
	enable, err := zm.driver.GetSetBool(zm.ctx, zm.cfg.AgentInfoKey(ZoneEnableKey), zm.cfg.Zone.Enable, false)
	if err != nil {
		return zm.cfg.Zone.Enable, err
	}
	return enable, nil
}

func (zm *zoneManager) SetZoomEnable(enable bool) error {
	_, err := zm.driver.SetBool(zm.ctx, zm.cfg.AgentInfoKey(ZoneEnableKey), enable, false)
	zm.cfg.Zone.Enable = enable
	return err
}

func (zm *zoneManager) createPeerClients() ([]eagrpc.ControlClient, error) {
	peerClients := make([]eagrpc.ControlClient, 0, len(zm.cfg.Zone.PeerURLs))
	retryPolicy := `{
		"methodConfig": [{
			"name": [{"service": "grpc.election_agent.v1.Control"}],
			"waitForReady": true,
			"timeout": "0.5s",
			"retryPolicy": {
				"maxAttempts": 1,
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
	status := &zoneStatus{zoomEnable: false, zcConnected: false, peerConnected: true}

	status.state, err = zm.GetAgentState()
	if err != nil {
		logging.Warnw("Failed to get current agent state",
			"agent", zm.cfg.Name,
			"zone", zm.cfg.Zone.Name,
			"error", err,
		)
	}
	unavailableState := status.state == agent.UnavailableState

	status.zoomEnable, err = zm.GetZoomEnable()
	if err != nil && !unavailableState {
		logging.Warnw("Failed to get zoom enable",
			"agent", zm.cfg.Name,
			"zone", zm.cfg.Zone.Name,
			"error", err,
		)
	}

	if !status.zoomEnable {
		return status
	}

	status.activeZone, err = zm.GetActiveZone()
	if err == nil || status.activeZone != "" {
		status.zcConnected = true
	}

	status.mode, err = zm.driver.GetAgentMode()
	if err != nil && !unavailableState {
		logging.Warnw("Failed to get agent mode",
			"agent", zm.cfg.Name,
			"zone", zm.cfg.Zone.Name,
			"error", err,
		)
	}

	status.peerStatus, _ = zm.GetPeerStatus()
	if len(status.peerStatus) == 0 {
		status.peerConnected = false
	}

	return status
}

// Check zone and peers status and set proper agent state and mode.
// This method is seperated from zoneManager to support unit testing.
func Check(status *zoneStatus, cfg *config.Config, kvDriver lease.KVDriver, zm ZoneManager, lm *lease.LeaseManager) { //nolint:cyclop
	if !status.zoomEnable {
		return
	}

	var err error
	unavailableState := status.state == agent.UnavailableState

	if status.zcConnected {
		status.newMode = agent.NormalMode

		if unavailableState {
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

		if unavailableState {
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

	// 1. change agent mode
	if status.mode != status.newMode {
		logging.Infow("Change agent mode",
			"agent", cfg.Name,
			"zone", cfg.Zone.Name,
			"old_mode", status.mode,
			"new_mode", status.newMode,
		)
		// change the mode stored in kv store to update the mode of other agent replicas.
		// other agent replicas will use `GetAgentMode` to retreive the mode.
		err = zm.SetAgentMode(status.newMode)
		if err != nil && !unavailableState {
			logging.Errorw("Failed to set agent mode",
				"agent", cfg.Name,
				"zone", cfg.Zone.Name,
				"mode", status.newMode,
				"error", err,
			)
		}
	}

	// 2. notify agent peers to enter standby mode if this agent becomes active one
	if status.newState == agent.ActiveState && status.peerConnected {
		err := zm.SetPeerStatus(&eagrpc.AgentStatus{State: agent.StandbyState, Mode: agent.NormalMode, ZoomEnable: true})
		if err != nil {
			logging.Errorw("Failed to notify agent peers to enter standby mode",
				"agent", cfg.Name,
				"zone", cfg.Zone.Name,
				"error", err,
			)
		}
	}

	// 3. update agent state
	err = zm.SetAgentState(status.newState)
	if err != nil && !unavailableState {
		logging.Errorw("Failed to update agent state",
			"agent", cfg.Name,
			"zone", cfg.Zone.Name,
			"state", status.newState,
			"error", err,
		)
	}

	// 4. notify lease manager to update agent state cache
	lm.SetStateCache(status.newState)

	logging.Debugw("Zoom status",
		"zoomEnable", status.zoomEnable,
		"zcConnected", status.zcConnected,
		"peerConnected", status.peerConnected,
		"state", status.state+"->"+status.newState,
		"mode", fmt.Sprintf("%s->%s", status.mode, status.newMode),
		"activeZone", status.activeZone,
	)
}
