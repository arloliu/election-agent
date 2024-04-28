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

type candidates struct {
	mu sync.Mutex
	m  []*electionCandidate
}

func (c *candidates) members() []*electionCandidate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m
}

func (c *candidates) setActive(idx int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, m := range c.m {
		if i == idx {
			m.active = true
		} else {
			m.active = false
		}
	}
}

type simulateClient struct {
	ctx      context.Context
	peers    []*candidates
	reqCount atomic.Int64
}

type electionCandidate struct {
	conn   *grpc.ClientConn
	client eagrpc.ElectionClient
	active bool
}

func newSimulateClient(ctx context.Context) (*simulateClient, error) {
	inst := &simulateClient{
		ctx:   ctx,
		peers: make([]*candidates, numClients),
	}

	svcConfig := `{
		"methodConfig": [{
			"name": [{"service": "grpc.election_agent.v1.Control"}, {"service": "grpc.election_agent.v1.Election"}],
			"waitForReady": true,
			"timeout": "3s",
			"retryPolicy": {
				"maxAttempts": 10,
				"initialBackoff": "0.1s",
				"maxBackoff": "1s",
				"backoffMultiplier": 2.0,
				"retryableStatusCodes": [ "UNAVAILABLE" ]
			}
		}]}`

	for i := 0; i < numClients; i++ {
		inst.peers[i] = &candidates{m: make([]*electionCandidate, numCandidates)}
		activeIdx := rand.Intn(numCandidates) //nolint:gosec
		for j := 0; j < numCandidates; j++ {
			conn, err := grpc.DialContext(ctx, Hostname,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultServiceConfig(svcConfig),
			)
			if err != nil {
				return nil, err
			}

			inst.peers[i].m[j] = &electionCandidate{
				conn:   conn,
				client: eagrpc.NewElectionClient(conn),
				active: j == activeIdx,
			}
		}
	}

	return inst, nil
}

func (s *simulateClient) shutdown() {
	fmt.Printf("Shutdown %d peers\n", len(s.peers)*numCandidates)
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
						ctx, cancel := context.WithTimeout(s.ctx, reqTimeout)
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

	if err != nil && status.Code(err) == codes.DeadlineExceeded {
		return nil
	}

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

func (s *simulateClient) peerCampaign() error {
	errs, _ := errgroup.WithContext(s.ctx)
	for i, peers := range s.peers {
		i := i
		peers := peers
		errs.Go(func() error {
			for j, c := range peers.members() {
				var err error
				for retries := 0; retries < 3; retries++ {
					err = func() error {
						ctx, cancel := context.WithTimeout(s.ctx, reqTimeout)
						defer cancel()

						return s.peerCampaignAction(ctx, c, i, j, retries)
					}()
					if err == nil {
						break
					}

					time.Sleep(100 * time.Millisecond)
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

func (s *simulateClient) peerCampaignAction(ctx context.Context, c *electionCandidate, i int, j int, retries int) error { //nolint:cyclop
	info := fmt.Sprintf("(peer: %d, idx: %d, retries: %d)", i, j, retries)

	if c.active {
		req := extendReqN(i, j)
		ret, err := c.client.ExtendElectedTerm(ctx, req)
		if err != nil && status.Code(err) == codes.DeadlineExceeded {
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
		if err != nil && status.Code(err) == codes.DeadlineExceeded {
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

func simulateClients(cmd *cobra.Command, args []string) error {
	var duration time.Duration
	if leaderResign {
		duration = 30 * time.Second
		fmt.Printf("Resign all elections, clients: %d, candidates: %d\n", numClients, numCandidates)
	} else {
		duration = simDuration
		fmt.Printf("Simulate %s state has started, clients: %d, candidates: %d, duration: %s, req_timeout:%s, forever:%t\n",
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

	start := time.Now()
	// choose active clients and campaign first
	if err := clients.chooseLeader(); err != nil {
		return err
	}
	elapsed := time.Since(start)
	rps := float64(numClients) / elapsed.Seconds()
	fmt.Printf("[%s] Handover %d leaders took %s, %.1f rps\n", time.Now().Format(time.RFC3339), numClients, elapsed.String(), rps)

	// start to simluate
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)

	cycle := 0
	cycleStart := time.Now()
	for {
		select {
		case <-ticker.C:
			if displayRPS {
				cycle++
				if cycle%10 == 0 {
					cycle = 0
					count := clients.reqCount.Swap(0)
					rps := float64(count) / time.Since(cycleStart).Seconds()
					cycleStart = time.Now()
					fmt.Printf("[%s] peer election, %.1f rps\n", time.Now().Format(time.RFC3339), rps)
				}
			}

			err := clients.peerCampaign()
			if err != nil {
				fmt.Printf("[%s] peer campaign fail, error:%s\n", time.Now().Format(time.RFC3339), err.Error())
				if !forever {
					ticker.Stop()
					clients.shutdown()
					return err
				}
			}
		case <-ctx.Done():
			ticker.Stop()
			clients.shutdown()
			fmt.Printf("Simulation done\n")
			return nil
		case <-exitSig:
			fmt.Println("Receive exit signal")
			ticker.Stop()
			clients.shutdown()
			return nil
		}
	}
}
