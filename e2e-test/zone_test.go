package e2etest

import (
	"context"
	"math/rand/v2"
	"testing"

	"election-agent/internal/agent"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

const (
	z1AgentName   = "election-agent-z1"
	z2AgentName   = "election-agent-z2"
	zcName        = "zone-coordinator"
	agentReplicas = 3
)

func TestZoneSwitch(t *testing.T) { //nolint:gocyclo,cyclop
	f1 := features.New("zone-test1").
		Assess("active zone is z1", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := waitActiveZone(ctx, cfg, "z1", activeZoneTimeout); err != nil {
				t.Fatal(err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.ActiveState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z2AgentName, agent.StandbyState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := simulateTwoAgents(ctx, cfg, agent.ActiveState, agent.StandbyState); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).Feature()

	f2 := features.New("zone-test2").
		Assess("active zone is z1, agents to zone-coordinator disconnected", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.ActiveState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}
			if err := agentStatusIs(ctx, cfg, z2AgentName, agent.StandbyState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := scaleDeployment(ctx, cfg, zcName, 0); err != nil {
				t.Fatalf("failed to scale down %s deployment, err:%s", zcName, err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.ActiveState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}
			if err := agentStatusIs(ctx, cfg, z2AgentName, agent.StandbyState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := simulateTwoAgents(ctx, cfg, agent.ActiveState, agent.StandbyState); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, zcName, 1); err != nil {
				t.Fatalf("failed to scale up %s deployment, err:%s", zcName, err.Error())
			}

			return ctx
		}).Feature()

	f3 := features.New("zone-test3").
		Assess("active zone is z1, election-agent-z2 down", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, z2AgentName, 0); err != nil {
				t.Fatalf("failed to scale down %s deployment, err:%s", z2AgentName, err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.ActiveState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			_, err := utilGetAgentStatus(ctx, cfg, svcGRPCHost(cfg, z2AgentName))
			if err == nil {
				t.Fatal("election-agent-z2 should not be connectable")
			}

			if err := simulateAgent(ctx, cfg, z1AgentName, agent.ActiveState); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, z2AgentName, agentReplicas); err != nil {
				t.Fatalf("failed to scale up %s deployment, err:%s", z2AgentName, err.Error())
			}

			return ctx
		}).Feature()

	f4 := features.New("zone-test4").
		Assess("active zone switched to z2", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := updateActiveZone(ctx, cfg, "z2"); err != nil {
				t.Fatalf("failed to update active zone, err:%s", err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.StandbyState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}
			if err := agentStatusIs(ctx, cfg, z2AgentName, agent.ActiveState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := simulateTwoAgents(ctx, cfg, agent.StandbyState, agent.ActiveState); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := updateActiveZone(ctx, cfg, "z1"); err != nil {
				t.Fatalf("failed to update active zone, err:%s", err.Error())
			}

			return ctx
		}).Feature()

	f5 := features.New("zone-test5").
		Assess("active zone is z1, both zone-coordinator and election-agent-z2 down", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.ActiveState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := scaleDeployment(ctx, cfg, zcName, 0); err != nil {
				t.Fatalf("failed to scale down %s deployment, err:%s", zcName, err.Error())
			}
			if err := scaleDeployment(ctx, cfg, z2AgentName, 0); err != nil {
				t.Fatalf("failed to scale down %s deployment, err:%s", z2AgentName, err.Error())
			}

			t.Log("Check Z1 agent status")

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.StandbyState, agent.OrphanMode); err != nil {
				t.Fatal(err.Error())
			}

			_, err := utilGetAgentStatus(ctx, cfg, svcGRPCHost(cfg, z2AgentName))
			if err == nil {
				t.Fatal("election-agent-z2 should not be connectable")
			}

			if err := simulateAgent(ctx, cfg, z1AgentName, agent.StandbyState); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, zcName, 1); err != nil {
				t.Fatalf("failed to scale up %s deployment, err:%s", zcName, err.Error())
			}
			if err := scaleDeployment(ctx, cfg, z2AgentName, agentReplicas); err != nil {
				t.Fatalf("failed to scale up %s deployment, err:%s", z2AgentName, err.Error())
			}

			return ctx
		}).Feature()

	f6 := features.New("zone-test6").
		Assess("active zone is z2, both zone-coordinator and election-agent-z2 down", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := updateActiveZone(ctx, cfg, "z2"); err != nil {
				t.Fatalf("failed to update active zone, err:%s", err.Error())
			}

			if err := scaleDeployment(ctx, cfg, zcName, 0); err != nil {
				t.Fatalf("failed to scale down %s deployment, err:%s", zcName, err.Error())
			}
			if err := scaleDeployment(ctx, cfg, z2AgentName, 0); err != nil {
				t.Fatalf("failed to scale down %s deployment, err:%s", z2AgentName, err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.ActiveState, agent.OrphanMode); err != nil {
				t.Fatal(err.Error())
			}

			_, err := utilGetAgentStatus(ctx, cfg, svcGRPCHost(cfg, z2AgentName))
			if err == nil {
				t.Fatal("election-agent-z2 should not be connectable")
			}

			if err := simulateAgent(ctx, cfg, z1AgentName, agent.ActiveState); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, zcName, 1); err != nil {
				t.Fatalf("failed to scale up %s deployment, err:%s", zcName, err.Error())
			}
			if err := scaleDeployment(ctx, cfg, z2AgentName, agentReplicas); err != nil {
				t.Fatalf("failed to scale up %s deployment, err:%s", z2AgentName, err.Error())
			}

			if err := updateActiveZone(ctx, cfg, "z1"); err != nil {
				t.Fatalf("failed to update active zone, err:%s", err.Error())
			}

			return ctx
		}).Feature()

	f7 := features.New("zone-test7").
		Assess("active zone is z1, all redis down", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, "redis-1", 0); err != nil {
				t.Fatalf("failed to scale down redis-1 deployment, err:%s", err.Error())
			}
			if err := scaleDeployment(ctx, cfg, "redis-2", 0); err != nil {
				t.Fatalf("failed to scale down redis-2 deployment, err:%s", err.Error())
			}
			if err := scaleDeployment(ctx, cfg, "redis-3", 0); err != nil {
				t.Fatalf("failed to scale down redis-3 deployment, err:%s", err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.UnavailableState, agent.UnknownMode); err != nil {
				t.Fatal(err.Error())
			}
			if err := agentStatusIs(ctx, cfg, z2AgentName, agent.UnavailableState, agent.UnknownMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := simulateTwoAgents(ctx, cfg, agent.UnavailableState, agent.UnavailableState); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, "redis-1", 1); err != nil {
				t.Fatalf("failed to scale up redis-1 deployment, err:%s", err.Error())
			}
			if err := scaleDeployment(ctx, cfg, "redis-2", 1); err != nil {
				t.Fatalf("failed to scale up redis-2 deployment, err:%s", err.Error())
			}
			if err := scaleDeployment(ctx, cfg, "redis-3", 1); err != nil {
				t.Fatalf("failed to scale up redis-3 deployment, err:%s", err.Error())
			}

			return ctx
		}).
		Feature()

	f8 := features.New("zone-test8").
		Assess("active zone is z2, redis-1 down, then redis-2 down", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := updateActiveZone(ctx, cfg, "z2"); err != nil {
				t.Fatalf("failed to update active zone, err:%s", err.Error())
			}

			if err := scaleDeployment(ctx, cfg, "redis-1", 0); err != nil {
				t.Fatalf("failed to scale down redis-1 deployment, err:%s", err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.StandbyState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}
			if err := agentStatusIs(ctx, cfg, z2AgentName, agent.ActiveState, agent.NormalMode); err != nil {
				t.Fatal(err.Error())
			}

			if err := simulateTwoAgents(ctx, cfg, agent.StandbyState, agent.ActiveState); err != nil {
				t.Fatal(err.Error())
			}

			if err := scaleDeployment(ctx, cfg, "redis-2", 0); err != nil {
				t.Fatalf("failed to scale down redis-2 deployment, err:%s", err.Error())
			}

			if err := agentStatusIs(ctx, cfg, z1AgentName, agent.UnavailableState, agent.UnknownMode); err != nil {
				t.Fatal(err.Error())
			}
			if err := agentStatusIs(ctx, cfg, z2AgentName, agent.UnavailableState, agent.UnknownMode); err != nil {
				t.Fatal(err.Error())
			}

			return ctx
		}).
		Teardown(func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := scaleDeployment(ctx, cfg, "redis-1", 1); err != nil {
				t.Fatalf("failed to scale up redis-1 deployment, err:%s", err.Error())
			}
			if err := scaleDeployment(ctx, cfg, "redis-2", 1); err != nil {
				t.Fatalf("failed to scale up redis-2 deployment, err:%s", err.Error())
			}
			if err := updateActiveZone(ctx, cfg, "z1"); err != nil {
				t.Fatalf("failed to update active zone, err:%s", err.Error())
			}

			return ctx
		}).
		Feature()

	testFeatures := []features.Feature{f1, f2, f3, f4, f5, f6, f7, f8}
	rand.Shuffle(len(testFeatures), func(i, j int) {
		testFeatures[i], testFeatures[j] = testFeatures[j], testFeatures[i]
	})

	// test features
	testEnv.Test(t, testFeatures...)
}
