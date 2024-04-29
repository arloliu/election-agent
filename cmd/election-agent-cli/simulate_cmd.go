package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"election-agent/internal/agent"
	eagrpc "election-agent/proto/election_agent/v1"
)

var (
	numClients    int
	numCandidates int
	electionName  string
	electionKind  string
	interval      int
	electionTerm  int
	simDuration   time.Duration
	reqTimeout    time.Duration
	simState      string
	leaderResign  bool
	forever       bool
	displayRPS    bool
)

var finished atomic.Bool

func init() {
	rootCmd.AddCommand(simulateCmd)
	simulateCmd.Flags().BoolVarP(&leaderResign, "resign", "r", false, "resign all election and exit")
	simulateCmd.Flags().DurationVarP(&reqTimeout, "timeout", "o", 1*time.Second, "election request timeout")
	simulateCmd.Flags().BoolVarP(&forever, "forever", "f", false, "simulate forever even error occurs")
	simulateCmd.Flags().BoolVarP(&displayRPS, "display", "p", false, "display request per seconds information")
	simulateCmd.Flags().IntVarP(&numClients, "num", "n", 100, "number of clients")
	simulateCmd.Flags().IntVarP(&numCandidates, "candidates", "c", 2, "number of candidates")
	simulateCmd.Flags().StringVarP(&electionName, "election", "e", "sim_election", "election name")
	simulateCmd.Flags().StringVarP(&electionKind, "kind", "k", "default", "election kind")
	simulateCmd.Flags().IntVarP(&interval, "interval", "i", 1000, "election interval, milliseconds")
	simulateCmd.Flags().IntVarP(&electionTerm, "term", "t", 3000, "election term, milliseconds")
	simulateCmd.Flags().DurationVarP(&simDuration, "duration", "d", 10*time.Second, "simulation duration")
	simulateCmd.Flags().StringVarP(&simState, "state", "s", agent.ActiveState, fmt.Sprintf("simulation state, possible values: %s", strings.Join(agent.ValidStates, ", ")))
}

var simulateCmd = &cobra.Command{
	Use:   "simulate",
	Short: "Simulate client election",
	Long:  "Simulate clients to campaign elections",
	RunE:  simulateClients,
}

type electionCandidate struct {
	conn   *grpc.ClientConn
	client eagrpc.ElectionClient
	active bool
}

type candidatePeers struct {
	mu          sync.Mutex
	candaidates []*electionCandidate
}

func (c *candidatePeers) member(idx int) *electionCandidate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.candaidates[idx]
}

func (c *candidatePeers) members() []*electionCandidate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.candaidates
}

func (c *candidatePeers) setActive(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.candaidates {
		if i == idx {
			m.active = true
		} else {
			m.active = false
		}
	}
}

type simulateClient struct {
	ctx      context.Context
	peers    []*candidatePeers
	reqCount atomic.Int64
}

func newSimulateClient(ctx context.Context) (*simulateClient, error) {
	inst := &simulateClient{
		ctx:   ctx,
		peers: make([]*candidatePeers, numClients),
	}

	svcConfig := `{
		"methodConfig": [{
			"name": [{"service": "grpc.election_agent.v1.Control"}, {"service": "grpc.election_agent.v1.Election"}],
			"waitForReady": true,
			"timeout": "1s",
			"retryPolicy": {
				"maxAttempts": 10,
				"initialBackoff": "0.1s",
				"maxBackoff": "1s",
				"backoffMultiplier": 2.0,
				"retryableStatusCodes": [ "UNAVAILABLE" ]
			}
		}]}`

	for i := 0; i < numClients; i++ {
		inst.peers[i] = &candidatePeers{candaidates: make([]*electionCandidate, numCandidates)}
		activeIdx := rand.Intn(numCandidates) //nolint:gosec
		for j := 0; j < numCandidates; j++ {
			conn, err := grpc.DialContext(ctx, Hostname,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultServiceConfig(svcConfig),
			)
			if err != nil {
				return nil, err
			}

			inst.peers[i].candaidates[j] = &electionCandidate{
				conn:   conn,
				client: eagrpc.NewElectionClient(conn),
				active: j == activeIdx,
			}
		}
	}

	return inst, nil
}

func (s *simulateClient) shutdown() {
	fmt.Printf("[%s] Shutdown %d peers\n", now(), len(s.peers)*numCandidates)
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	for i, peer := range s.peers {
		for j, c := range peer.members() {
			if c.active {
				_, _ = c.client.Resign(ctx, resignReqN(i, j))
			}
			_ = c.conn.Close()
		}
	}
}

func handoverReqN(p int, n int) *eagrpc.HandoverRequest {
	return &eagrpc.HandoverRequest{
		Election: fmt.Sprintf("%s-%d", electionName, p),
		Leader:   fmt.Sprintf("client-%d-%d", p, n),
		Term:     int32(electionTerm),
		Kind:     electionKind,
	}
}

func campReqN(p int, n int) *eagrpc.CampaignRequest {
	return &eagrpc.CampaignRequest{
		Election:  fmt.Sprintf("%s-%d", electionName, p),
		Candidate: fmt.Sprintf("client-%d-%d", p, n),
		Kind:      electionKind,
		Term:      int32(electionTerm),
	}
}

func extendReqN(p int, n int) *eagrpc.ExtendElectedTermRequest {
	return &eagrpc.ExtendElectedTermRequest{
		Election: fmt.Sprintf("%s-%d", electionName, p),
		Leader:   fmt.Sprintf("client-%d-%d", p, n),
		Kind:     electionKind,
		Term:     int32(electionTerm),
		Retries:  3,
	}
}

func resignReqN(p int, n int) *eagrpc.ResignRequest {
	return &eagrpc.ResignRequest{
		Election: fmt.Sprintf("%s-%d", electionName, p),
		Leader:   fmt.Sprintf("client-%d-%d", p, n),
		Kind:     electionKind,
	}
}

func (s *simulateClient) resignLeaders() error {
	errs, _ := errgroup.WithContext(s.ctx)
	for i, peers := range s.peers {
		i := i
		peers := peers
		errs.Go(func() error {
			for j, c := range peers.members() {
				if _, err := c.client.Resign(s.ctx, resignReqN(i, j)); err != nil {
					code := status.Code(err)
					if code != codes.NotFound && code != codes.FailedPrecondition {
						return err
					}
				}
			}

			return nil
		})
	}

	return errs.Wait()
}

func (s *simulateClient) chooseLeader() error {
	errs, _ := errgroup.WithContext(s.ctx)

	chooseLeaderTimeout := time.Duration(2) * reqTimeout
	for i, peers := range s.peers {
		i := i
		peers := peers
		errs.Go(func() error {
			for j, c := range peers.members() {
				if !c.active {
					continue
				}

				var err error
				for retries := 0; retries < 3; retries++ {
					err = func() error {
						ctx, cancel := context.WithTimeout(s.ctx, chooseLeaderTimeout)
						defer cancel()
						return s.chooseLeaderAction(ctx, c.client, i, j, retries)
					}()
					if err == nil {
						break
					}
				}
				if err != nil {
					return err
				}
			}

			return nil
		})
	}

	return errs.Wait()
}

func (s *simulateClient) chooseLeaderAction(ctx context.Context, client eagrpc.ElectionClient, i int, j int, retries int) error {
	ret, err := client.Handover(ctx, handoverReqN(i, j))
	info := fmt.Sprintf("(peer: %d, idx: %d, retries: %d)", i, j, retries)

	switch simState {
	case agent.ActiveState:
		if err != nil {
			return fmt.Errorf("Active candidate should handover successed %s, got error: %w", info, err)
		}
		if !ret.Value {
			return fmt.Errorf("Active candidate should handover successed, %s", info)
		}
	case agent.StandbyState:
		if err != nil {
			return fmt.Errorf("Active candidate should handover fail but not got error %s, error: %w", info, err)
		}
		if ret.Value {
			return fmt.Errorf("Active candidate should handover fail %s", info)
		}
	case agent.UnavailableState:
		if err == nil {
			return fmt.Errorf("Active candidate should handover fail and got error %s", info)
		} else if status.Code(err) != codes.FailedPrecondition {
			return fmt.Errorf("Active candidate should handover fail and got unavailable status code %s, error: %w", info, err)
		}
	}
	return nil
}

func (s *simulateClient) peerCampaign(ctx context.Context, i int, j int) error {
	var err error
	client := s.peers[i].member(j)
	for retries := 0; retries < 3; retries++ {
		err = func() error {
			cctx, cancel := context.WithTimeout(ctx, reqTimeout)
			defer cancel()

			return s.peerCampaignAction(cctx, client, i, j)
		}()
		if err == nil {
			break
		}

		time.Sleep(100 * time.Millisecond)
	}
	return err
}

func (s *simulateClient) peerCampaignAction(ctx context.Context, c *electionCandidate, i int, j int) error { //nolint:cyclop
	info := fmt.Sprintf("(peer: %d, candidate: %d)", i, j)

	if c.active {
		req := extendReqN(i, j)
		ret, err := c.client.ExtendElectedTerm(ctx, req)
		// discards error when the simulation finished
		if err != nil && finished.Load() && (status.Code(err) == codes.DeadlineExceeded || status.Code(err) == codes.Canceled) {
			return nil
		}

		s.reqCount.Add(1)

		switch simState {
		case agent.ActiveState:
			if err != nil {
				return fmt.Errorf("Active candidate should extend elected term successed %s, got error: %w", info, err)
			}
			if !ret.Value {
				return fmt.Errorf("Active candidate should extend elected term successed %s", info)
			}
		case agent.StandbyState:
			if err != nil {
				return fmt.Errorf("Active candidate should extend elected term fail but not got error %s, error: %w", info, err)
			} else if ret.Value {
				return fmt.Errorf("Active candidate should extend elected term fail %s", info)
			}
		case agent.UnavailableState:
			if err == nil {
				return fmt.Errorf("Active candidate should extend elected term fail and got error %s", info)
			} else if status.Code(err) != codes.FailedPrecondition {
				return fmt.Errorf("Active candidate should extend elected term fail and got unavailable status code, actual status code: %d %s", status.Code(err), info)
			}
		}
	} else {
		req := campReqN(i, j)
		ret, err := c.client.Campaign(ctx, req)
		// discards error when the simulation finished
		if err != nil && finished.Load() && (status.Code(err) == codes.DeadlineExceeded || status.Code(err) == codes.Canceled) {
			return nil
		}

		s.reqCount.Add(1)

		switch simState {
		case agent.ActiveState:
			if ret != nil && ret.Elected {
				s.peers[i].setActive(j)
				if err != nil {
					return fmt.Errorf("Inactive candidate should campaign term fail, switch to active %s, got error: %w", info, err)
				} else {
					return fmt.Errorf("Inactive candidate should campaign term fail, switch to active %s", info)
				}
			}
		case agent.StandbyState:
			if err != nil {
				return fmt.Errorf("Inactive candidate should campaign fail but not got error %s, error: %w", info, err)
			} else if ret.Elected {
				return fmt.Errorf("Inactive candidate should campaign fail %s", info)
			}
		case agent.UnavailableState:
			if err == nil {
				return fmt.Errorf("Inactive candidate should campaign term fail and got error %s", info)
			} else if status.Code(err) != codes.FailedPrecondition {
				return fmt.Errorf("Inactive candidate should extend elected term fail and got unavailable status code, actual status code: %d %s", status.Code(err), info)
			}
		}
	}

	return nil
}

func (s *simulateClient) simulateWorker(ctx context.Context, i int, j int, resultChan chan<- error) {
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	defer ticker.Stop()

	for {
		err := s.peerCampaign(ctx, i, j)
		if err != nil {
			if forever {
				fmt.Printf("[%s] Got error: %s\n", now(), err)
			} else {
				resultChan <- err
				return
			}
		}

		select {
		case <-ctx.Done():
			resultChan <- nil
			return
		case <-ticker.C:
			continue
		}
	}
}

func now() string {
	return time.Now().Format(time.RFC3339)
}

func simulateClients(cmd *cobra.Command, args []string) error {
	var duration time.Duration
	if leaderResign {
		duration = 30 * time.Second
		fmt.Printf("Resign all elections, clients: %d, candidates: %d\n", numClients, numCandidates)
	} else {
		duration = simDuration
		fmt.Printf("Simulate %s state has started, clients: %d, candidates: %d, duration: %s, req_timeout: %s, forever: %t\n",
			simState, numClients, numCandidates, duration.String(), reqTimeout.String(), forever)
	}

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	clients, err := newSimulateClient(ctx)
	if err != nil {
		return err
	}

	exitSig := make(chan os.Signal, 1)
	signal.Notify(exitSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	if leaderResign {
		if err := clients.resignLeaders(); err != nil {
			return err
		}
		return nil
	}

	// choose active clients and campaign first
	start := time.Now()
	if err := clients.chooseLeader(); err != nil {
		fmt.Printf("[%s] Got error:%s\n", now(), err)
		return err
	}
	elapsed := time.Since(start)
	rps := float64(numClients) / elapsed.Seconds()
	fmt.Printf("[%s] Handover %d leaders took %s, RPS: %.1f \n", now(), numClients, elapsed.String(), rps)

	resultChan := make(chan error)

	// start simulation
	for i := 0; i < numClients; i++ {
		for j := 0; j < numCandidates; j++ {
			go clients.simulateWorker(ctx, i, j, resultChan)
		}
	}

	if displayRPS {
		displayTicker := time.NewTicker(2 * time.Second)
		defer displayTicker.Stop()
		cycleStart := time.Now()
		go func() {
			for range displayTicker.C {
				if displayRPS {
					count := clients.reqCount.Swap(0)
					rps := float64(count) / time.Since(cycleStart).Seconds()
					cycleStart = time.Now()
					fmt.Printf("[%s] Election RPS: %.1f\n", now(), rps)
				}
			}
		}()
	}

	for i := 0; i < numClients*numCandidates; i++ {
		select {
		case err := <-resultChan:
			if err != nil {
				return err
			}
		case <-ctx.Done():
			finished.Store(true)
			clients.shutdown()
			fmt.Printf("[%s] Simulation done\n", now())
			return nil
		case <-exitSig:
			finished.Store(true)
			fmt.Printf("[%s] Receive exit signal\n", now())
			cancel()
			clients.shutdown()
			return nil
		}
	}

	return nil
}
