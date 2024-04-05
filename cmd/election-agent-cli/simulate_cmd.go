package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/signal"
	"strings"
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
	interval      int
	electionTerm  int
	simDuration   time.Duration
	simState      string
	leaderResign  bool
)

func init() {
	rootCmd.AddCommand(simulateCmd)
	simulateCmd.Flags().BoolVarP(&leaderResign, "resign", "r", false, "resign all election and exit")
	simulateCmd.Flags().IntVarP(&numClients, "num", "n", 100, "number of clients")
	simulateCmd.Flags().IntVarP(&numCandidates, "candidates", "c", 2, "number of candidates")
	simulateCmd.Flags().StringVarP(&electionName, "election", "e", "sim_election", "election name")
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

type simulateClient struct {
	ctx   context.Context
	peers [][]*electionCandidate
}

type electionCandidate struct {
	conn   *grpc.ClientConn
	client eagrpc.ElectionClient
	active bool
}

func newSimulateClient(ctx context.Context) (*simulateClient, error) {
	inst := &simulateClient{
		ctx:   ctx,
		peers: make([][]*electionCandidate, numClients),
	}

	svcConfig := `{
		"methodConfig": [{
			"name": [{"service": "grpc.election_agent.v1.Control"}],
			"waitForReady": true,
			"timeout": "10s",
			"retryPolicy": {
				"maxAttempts": 10,
				"initialBackoff": "0.1s",
				"maxBackoff": "1s",
				"backoffMultiplier": 2.0,
				"retryableStatusCodes": [ "UNAVAILABLE" ]
			}
		}]}`

	for i := 0; i < numClients; i++ {
		inst.peers[i] = make([]*electionCandidate, numCandidates)
		activeIdx := rand.Intn(numCandidates) //nolint:gosec
		for j := 0; j < numCandidates; j++ {
			conn, err := grpc.DialContext(ctx, Hostname,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
				grpc.WithDefaultServiceConfig(svcConfig),
			)
			if err != nil {
				return nil, err
			}

			inst.peers[i][j] = &electionCandidate{
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
	for i, peer := range s.peers {
		for j, c := range peer {
			if c.active {
				_, _ = c.client.Resign(s.ctx, resignReqN(i, j))
			}
			_ = c.conn.Close()
		}
	}
}

func campReqN(p int, n int) *eagrpc.CampaignRequest {
	return &eagrpc.CampaignRequest{
		Election:  fmt.Sprintf("%s-%d", electionName, p),
		Candidate: fmt.Sprintf("client-%d-%d", p, n),
		Term:      int32(electionTerm),
	}
}

func extendReqN(p int, n int) *eagrpc.ExtendElectedTermRequest {
	return &eagrpc.ExtendElectedTermRequest{
		Election: fmt.Sprintf("%s-%d", electionName, p),
		Leader:   fmt.Sprintf("client-%d-%d", p, n),
		Term:     int32(electionTerm),
	}
}

func resignReqN(p int, n int) *eagrpc.ResignRequest {
	return &eagrpc.ResignRequest{
		Election: fmt.Sprintf("%s-%d", electionName, p),
		Leader:   fmt.Sprintf("client-%d-%d", p, n),
	}
}

func (s *simulateClient) resignLeaders() error {
	errs, _ := errgroup.WithContext(s.ctx)
	for i, peers := range s.peers {
		i := i
		peers := peers
		errs.Go(func() error {
			for j, c := range peers {
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
			for j, c := range peers {
				if !c.active {
					continue
				}
				req := campReqN(i, j)
				ret, err := c.client.Campaign(s.ctx, req)

				switch simState {
				case agent.ActiveState:
					if err != nil {
						return fmt.Errorf("Active candidate should campaign successed (peer: %d, idx: %d), got error: %w", i, j, err)
					}
					if !ret.Elected {
						return fmt.Errorf("Active candidate should campaign successed, (peer: %d, idx: %d), leader: %s", i, j, ret.Leader)
					}
				case agent.StandbyState:
					if err != nil {
						return fmt.Errorf("Active candidate should campaign fail but not got error (peer: %d, idx: %d), error: %w", i, j, err)
					}
					if ret.Elected {
						return fmt.Errorf("Active candidate should campaign fail (peer: %d, idx: %d), leader: %s", i, j, ret.Leader)
					}
				case agent.UnavailableState:
					if err == nil {
						return fmt.Errorf("Active candidate should campaign fail and got error, elected: %t(peer: %d, idx: %d)", ret.Elected, i, j)
					} else if status.Code(err) != codes.FailedPrecondition {
						return fmt.Errorf("Active candidate should campaign fail and got unavailable status code (peer: %d, idx: %d), error: %w", i, j, err)
					}
				}
			}

			return nil
		})
	}

	return errs.Wait()
}

func (s *simulateClient) peerCampaign() error { //nolint:cyclop
	errs, _ := errgroup.WithContext(s.ctx)
	for i, peers := range s.peers {
		i := i
		peers := peers
		errs.Go(func() error {
			for j, c := range peers {
				if c.active {
					req := extendReqN(i, j)
					ret, err := c.client.ExtendElectedTerm(s.ctx, req)

					switch simState {
					case agent.ActiveState:
						if err != nil {
							return fmt.Errorf("Active candidate should extend elected term successed (peer: %d, idx: %d), got error: %w", i, j, err)
						}
						if !ret.Value {
							return fmt.Errorf("Active candidate should extend elected term successed (peer: %d, idx: %d)", i, j)
						}
					case agent.StandbyState:
						if err != nil {
							return fmt.Errorf("Active candidate should extend elected term fail but not got error (peer: %d, idx: %d), error: %w", i, j, err)
						} else if ret.Value {
							return fmt.Errorf("Active candidate should extend elected term fail (peer: %d, idx: %d)", i, j)
						}
					case agent.UnavailableState:
						if err == nil {
							return fmt.Errorf("Active candidate should extend elected term fail and got error (peer: %d, idx: %d)", i, j)
						} else if status.Code(err) != codes.FailedPrecondition {
							return fmt.Errorf("Active candidate should extend elected term fail and got unavailable status code (peer: %d, idx: %d)", i, j)
						}
					}
				} else {
					req := campReqN(i, j)
					ret, err := c.client.Campaign(s.ctx, req)

					switch simState {
					case agent.ActiveState:
						if ret != nil && ret.Elected {
							return fmt.Errorf("Inactive candidate should campaign term fail (peer: %d, idx: %d), got error: %w", i, j, err)
						}
					case agent.StandbyState:
						if err != nil {
							return fmt.Errorf("Inactive candidate should campaign fail but not got error (peer: %d, idx: %d), error: %w", i, j, err)
						} else if ret.Elected {
							return fmt.Errorf("Inactive candidate should campaign fail (peer: %d, idx: %d)", i, j)
						}
					case agent.UnavailableState:
						if err == nil {
							return fmt.Errorf("Inactive candidate should campaign term fail and got error (peer: %d, idx: %d)", i, j)
						} else if status.Code(err) != codes.FailedPrecondition {
							return fmt.Errorf("Inactive candidate should campaign term fail and got unavailable status code (peer: %d, idx: %d)", i, j)
						}
					}
				}
			}

			return nil
		})
	}

	return errs.Wait()
}

func simulateClients(cmd *cobra.Command, args []string) error {
	var duration time.Duration
	if leaderResign {
		duration = 30 * time.Second
		fmt.Printf("Resign all elections, clients: %d, candidates: %d\n", numClients, numCandidates)
	} else {
		duration = simDuration
		fmt.Printf("Simulate %s state has started, clients: %d, candidates: %d, duration: %s\n", simState, numClients, numCandidates, duration.String())
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
	if err := clients.chooseLeader(); err != nil {
		return err
	}

	// start to simluate
	ticker := time.NewTicker(time.Duration(interval) * time.Millisecond)
	for {
		select {
		case <-ticker.C:
			err := clients.peerCampaign()
			if err != nil {
				ticker.Stop()
				clients.shutdown()
				return err
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
