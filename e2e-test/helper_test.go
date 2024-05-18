package e2etest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"election-agent/internal/agent"

	"go.uber.org/multierr"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

//nolint:unused
func enableService(ctx context.Context, cfg *envconf.Config, name string) error {
	return controlService(ctx, cfg, name, true)
}

//nolint:unused
func disableService(ctx context.Context, cfg *envconf.Config, name string) error {
	return controlService(ctx, cfg, name, false)
}

//nolint:unused
func controlService(ctx context.Context, cfg *envconf.Config, name string, enable bool) error {
	client := cfg.Client()
	svc := corev1.Service{}
	err := client.Resources().Get(ctx, name, cfg.Namespace(), &svc)
	if err != nil {
		return err
	}

	if enable {
		log.Printf("Enabling service %s...\n", name)
		delete(svc.Spec.Selector, "disable")
	} else {
		log.Printf("Disabling service %s...\n", name)
		svc.Spec.Selector["disable"] = name
	}

	if err := client.Resources().Update(ctx, &svc); err != nil {
		return err
	}

	success := false
	for i := 0; i < 20; i++ {
		if err := client.Resources().Get(ctx, name, cfg.Namespace(), &svc); err != nil {
			return err
		}

		if enable {
			if _, ok := svc.Spec.Selector["disable"]; !ok {
				success = true
				break
			}
		} else {
			if val, ok := svc.Spec.Selector["disable"]; ok && val == name {
				success = true
				break
			}
		}
		time.Sleep(time.Second)
	}

	if success {
		if enable {
			log.Printf("# Service %s enabled\n", name)
		} else {
			log.Printf("# Service %s disabled\n", name)
		}
	} else {
		if enable {
			return fmt.Errorf("Failed to enable %s service, err:%w", name, err)
		} else {
			return fmt.Errorf("Failed to disable %s service, err:%w", name, err)
		}
	}

	return nil
}

func scaleDeployment(ctx context.Context, cfg *envconf.Config, name string, replicas int32) error {
	client := cfg.Client()
	deployment := appsv1.Deployment{}
	if err := client.Resources().Get(ctx, name, cfg.Namespace(), &deployment); err != nil {
		return err
	}

	if *deployment.Spec.Replicas == replicas {
		log.Printf("%s deployment replicas %d is the same, no needs to scale\n", name, replicas)
		return nil
	}

	deployment.Spec.Replicas = &replicas
	log.Printf("Scaling %s deployment replicas to %d...\n", name, replicas)
	if err := client.Resources().Update(ctx, &deployment); err != nil {
		return err
	}

	if err := waitDeploymentScaled(cfg, &deployment, replicas); err != nil {
		return err
	}

	return nil
}

func updateActiveZone(ctx context.Context, cfg *envconf.Config, zone string) error {
	client := cfg.Client()
	configmap := corev1.ConfigMap{}
	err := client.Resources().Get(ctx, "zone-coordinator-config", cfg.Namespace(), &configmap)
	if err != nil {
		return err
	}
	if configmap.Data["ZC_ZONE"] == zone {
		log.Printf("Active zone is %s and not changed, no needs to update\n", zone)
		return nil
	}
	configmap.Data["ZC_ZONE"] = zone

	log.Printf("Update zone-coordinator-config ZC_ZONE to %s\n", zone)
	if err := client.Resources().Update(ctx, &configmap); err != nil {
		return err
	}

	deployment := appsv1.Deployment{}
	if err := client.Resources().Get(ctx, "zone-coordinator", cfg.Namespace(), &deployment); err != nil {
		return err
	}

	var replicas int32 = 0
	deployment.Spec.Replicas = &replicas
	log.Printf("Scale down zone-coordinator replicas to %d\n", replicas)
	if err := client.Resources().Update(ctx, &deployment); err != nil {
		return err
	}

	if err := waitDeploymentScaled(cfg, &deployment, 0); err != nil {
		return err
	}

	// get latest deployment
	if err := client.Resources().Get(ctx, "zone-coordinator", cfg.Namespace(), &deployment); err != nil {
		return err
	}

	replicas = 1
	deployment.Spec.Replicas = &replicas
	log.Printf("Scale up zone-coordinator replicas to %d\n", replicas)
	if err := client.Resources().Update(ctx, &deployment); err != nil {
		return err
	}

	if err := waitDeploymentScaled(cfg, &deployment, 1); err != nil {
		return err
	}

	return waitActiveZone(ctx, cfg, zone, activeZoneTimeout)
}

func waitActiveZone(ctx context.Context, cfg *envconf.Config, zone string, timeout time.Duration) error {
	elapsed := time.Now().Add(timeout)
	for {
		activeZone, _ := utilGetActiveZone(ctx, cfg)
		if activeZone == zone {
			log.Printf("# Current active zone: %s\n", activeZone)
			return nil
		} else {
			log.Printf("Active zone mismatch, %s:%s\n", zone, activeZone)
		}

		if time.Now().After(elapsed) {
			return fmt.Errorf("! Wait active zone %s timeout: %s", zone, timeout.String())
		}
		time.Sleep(time.Second)
	}
}

func waitDeploymentAvailable(cfg *envconf.Config, deployName string) error {
	client := cfg.Client()
	log.Printf("Waiting for %s to be available...", deployName)
	if err := wait.For(
		conditions.New(client.Resources()).DeploymentAvailable(deployName, cfg.Namespace()),
		wait.WithImmediate(),
		wait.WithTimeout(1*time.Minute),
		wait.WithInterval(1*time.Second),
	); err != nil {
		log.Printf("Timedout while waiting for %s deployment: %s", deployName, err)
		return err
	}

	log.Printf("# Deployment %s is available", deployName)

	return nil
}

func waitDeploymentScaled(cfg *envconf.Config, deployment *appsv1.Deployment, expectedReplicas int32) error {
	client := cfg.Client()

	scaleFetcher := func(object k8s.Object) int32 {
		return object.(*appsv1.Deployment).Status.ReadyReplicas
	}
	log.Printf("Waiting for deployment %s to be scaled to %d...", deployment.ObjectMeta.Name, expectedReplicas)
	if err := wait.For(
		conditions.New(client.Resources()).ResourceScaled(deployment, scaleFetcher, expectedReplicas),
		wait.WithImmediate(),
		wait.WithTimeout(1*time.Minute),
		wait.WithInterval(1*time.Second),
	); err != nil {
		log.Printf("! Timedout while waiting for %s to be scaled, err: %s", deployment.ObjectMeta.Name, err.Error())
		return err
	}

	log.Printf("# Deployment %s has been scaled to %d", deployment.ObjectMeta.Name, expectedReplicas)
	return nil
}

func scaleStatefulSet(ctx context.Context, cfg *envconf.Config, name string, replicas int32) error {
	client := cfg.Client()
	sts := appsv1.StatefulSet{}
	if err := client.Resources().Get(ctx, name, cfg.Namespace(), &sts); err != nil {
		return err
	}

	if *sts.Spec.Replicas == replicas {
		log.Printf("%s deployment replicas %d is the same, no needs to scale\n", name, replicas)
		return nil
	}

	sts.Spec.Replicas = &replicas
	log.Printf("Scaling %s StatefulSet replicas to %d...\n", name, replicas)
	if err := client.Resources().Update(ctx, &sts); err != nil {
		return err
	}

	if err := waitStatefulSetScaled(cfg, &sts, replicas); err != nil {
		return err
	}

	return nil
}

func waitStatefulSetScaled(cfg *envconf.Config, sts *appsv1.StatefulSet, expectedReplicas int32) error {
	client := cfg.Client()

	scaleFetcher := func(object k8s.Object) int32 {
		return object.(*appsv1.StatefulSet).Status.ReadyReplicas
	}
	log.Printf("Waiting for StatefulSet %s to be scaled to %d...", sts.ObjectMeta.Name, expectedReplicas)
	if err := wait.For(
		conditions.New(client.Resources()).ResourceScaled(sts, scaleFetcher, expectedReplicas),
		wait.WithImmediate(),
		wait.WithTimeout(1*time.Minute),
		wait.WithInterval(1*time.Second),
	); err != nil {
		log.Printf("! Timedout while waiting for %s to be scaled, err: %s", sts.ObjectMeta.Name, err.Error())
		return err
	}

	log.Printf("# StatefulSet %s has been scaled to %d", sts.ObjectMeta.Name, expectedReplicas)
	return nil
}

func waitStatefulSetAvailable(ctx context.Context, cfg *envconf.Config, stsName string, expectedReplicas int32) error {
	client := cfg.Client()
	log.Printf("Waiting for %s StatefulSet to be available...", stsName)
	sts := appsv1.StatefulSet{}
	if err := client.Resources().Get(ctx, stsName, cfg.Namespace(), &sts); err != nil {
		return err
	}

	return waitStatefulSetScaled(cfg, &sts, expectedReplicas)
}

func agentStatusIsWithTimes(ctx context.Context, cfg *envconf.Config, agentHost string, state string, mode string, times int) bool {
	log.Printf("Ensure agent statues, expect agent %s state: %s, mode: %s, timeout: %s with %d times\n", agentHost, state, mode, stateChangeTimeout, times)
	for i := 0; i < times; i++ {
		status, err := utilGetAgentStatus(ctx, cfg, agentHost)
		if err != nil {
			log.Printf("  ! Unable to get agent status, error: %s\n", err.Error())
			return false
		}

		for j, s := range status {
			if s.State != state || s.Mode != mode {
				log.Printf("  ! Unmatched[%d], agent %s expected/actual state: %s/%s, mode: %s/%s, activeZone: %s zc: %t, peer: %t\n",
					j, agentHost, state, s.State, mode, s.Mode, s.ActiveZone, s.ZcConnected, s.PeerConnected)
				return false
			} else {
				log.Printf("  * Matched[%d], agent %s expected/actual state: %s/%s, mode: %s/%s, activeZone: %s zc: %t, peer: %t\n",
					j, agentHost, state, s.State, mode, s.Mode, s.ActiveZone, s.ZcConnected, s.PeerConnected)
			}
		}
		time.Sleep(time.Second)
	}

	return true
}

func agentStatusIs(ctx context.Context, cfg *envconf.Config, name string, state string, mode string) error {
	elapsed := time.Now().Add(stateChangeTimeout)
	agentHost := headlessSvcGRPCHost(cfg, name)
	log.Printf("Expect agent %s state: %s, mode: %s, timeout: %s\n", agentHost, state, mode, stateChangeTimeout)
	for {
		status, err := utilGetAgentStatus(ctx, cfg, agentHost)
		if err != nil {
			log.Printf("!  Wait agent %s state change got err: %s\n", agentHost, err.Error())
			if time.Now().After(elapsed) {
				return fmt.Errorf("! Wait agent %s state change timeout, got error: %w", agentHost, err)
			}
			time.Sleep(time.Second)
		}

		matched := true
		for i, s := range status {
			if s.State != state || s.Mode != mode {
				log.Printf("  * Unmatched[%d], agent %s expected/actual state: %s/%s, mode: %s/%s, activeZone: %s zc: %t, peer: %t\n",
					i, agentHost, state, s.State, mode, s.Mode, s.ActiveZone, s.ZcConnected, s.PeerConnected)
				matched = false
			} else {
				log.Printf("  * Matched[%d], agent %s expected/actual state: %s/%s, mode: %s/%s, activeZone: %s zc: %t, peer: %t\n",
					i, agentHost, state, s.State, mode, s.Mode, s.ActiveZone, s.ZcConnected, s.PeerConnected)
			}
		}

		times := rand.Intn(4) + 3
		if matched && agentStatusIsWithTimes(ctx, cfg, agentHost, state, mode, times) {
			log.Printf("# All Matched(count: %d), agent %s state: %s, mode: %s\n", len(status), agentHost, state, mode)
			return nil
		}
		if time.Now().After(elapsed) {
			return fmt.Errorf("! Wait agent %s state change timeout.", agentHost)
		}
		time.Sleep(time.Second)
	}
}

func simulateAgent(ctx context.Context, cfg *envconf.Config, name string, state string) error {
	agentHost := svcGRPCHost(cfg, name)
	stdout, err := utilSimluateResign(ctx, cfg, agentHost, "e2e-test-election", simNumClients, 2)
	if err != nil {
		return err
	} else {
		log.Printf("# Simulate resign %s output:\n%s\n", agentHost, stdout)
	}

	stdout, err = utilSimluate(ctx, cfg, agentHost, state, simDuration, "e2e-test-election", simNumClients, 2)
	if err == nil {
		log.Printf("# Simulate %s output:\n%s\n", agentHost, stdout)
	}
	return err
}

func simulateTwoAgents(ctx context.Context, cfg *envconf.Config, z1State string, z2State string) error {
	var wg sync.WaitGroup
	var simErr error

	z1AgentHost := svcGRPCHost(cfg, z1AgentName)
	z2AgentHost := svcGRPCHost(cfg, z2AgentName)

	var resignAgentHost string
	if z1State == agent.ActiveState {
		resignAgentHost = z1AgentHost
	} else if z2State == agent.ActiveState {
		resignAgentHost = z2AgentHost
	}

	if resignAgentHost != "" {
		_, err := utilSimluateResign(ctx, cfg, resignAgentHost, "e2e-test-election", simNumClients, 2)
		if err != nil {
			return err
		} else {
			log.Printf("# Simulate  %s resign successfully", resignAgentHost)
		}
	}

	wg.Add(2)
	go func() {
		_, err := utilSimluate(ctx, cfg, z1AgentHost, z1State, simDuration, "e2e-test-election", simNumClients, 2)
		if err != nil {
			simErr = multierr.Append(simErr, err)
		} else {
			log.Printf("# Simulate %s election in %s state successfully", z1AgentHost, z1State)
		}
		wg.Done()
	}()

	go func() {
		_, err := utilSimluate(ctx, cfg, z2AgentHost, z2State, simDuration, "e2e-test-election", simNumClients, 2)
		if err != nil {
			simErr = multierr.Append(simErr, err)
		} else {
			log.Printf("# Simulate %s election in %s state successfully", z2AgentHost, z2State)
		}
		wg.Done()
	}()

	wg.Wait()

	return simErr
}

func genLabelSelector(labels map[string]string) string {
	pairs := make([]string, 0, len(labels))
	for k, v := range labels {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, ",")
}

func getPods(ctx context.Context, cfg *envconf.Config, deployName string) (*corev1.PodList, error) {
	client := cfg.Client()
	deployment := appsv1.Deployment{}
	if err := client.Resources().Get(ctx, deployName, cfg.Namespace(), &deployment); err != nil {
		return nil, err
	}

	labelSelector := genLabelSelector(deployment.Spec.Selector.MatchLabels)
	pods := &corev1.PodList{}
	if err := client.Resources(cfg.Namespace()).List(ctx, pods, resources.WithLabelSelector(labelSelector)); err != nil {
		return nil, err
	}
	return pods, nil
}

func podExec(ctx context.Context, cfg *envconf.Config, deployName string, containerName string, command []string) ([]byte, []byte, error) {
	pods, err := getPods(ctx, cfg, deployName)
	if err != nil {
		return nil, nil, err
	}
	podName := pods.Items[0].Name

	var stdout, stderr bytes.Buffer
	if err := cfg.Client().Resources().ExecInPod(ctx, cfg.Namespace(), podName, containerName, command, &stdout, &stderr); err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}

	return stdout.Bytes(), stderr.Bytes(), nil
}

func utilPodExec(ctx context.Context, cfg *envconf.Config, cmd []string) ([]byte, error) {
	now := time.Now()
	stdout, stderr, err := podExec(ctx, cfg, "election-agent-util", "election-agent-util", cmd)
	elapsed := time.Since(now)
	if err != nil {
		return nil, fmt.Errorf("Failed to exec command: %s\n  * elapsed:%s\n  * stdout:\n %s\n  * stderr:\n %s\n  * err:\n %w\n",
			strings.Join(cmd, " "), elapsed, string(stdout), string(stderr), err)
	}
	log.Printf("Execute pod successed, elapsed: %s, cmd: %s\n", elapsed, strings.Join(cmd, " "))
	return stdout, nil
}

func utilGetActiveZone(ctx context.Context, cfg *envconf.Config) (string, error) {
	// cmd := []string{"curl", fmt.Sprintf("http://%s.%s.svc", zcName, cfg.Namespace())}
	cmd := []string{"election-agent-cli", "control", "get-active-zone", fmt.Sprintf("http://%s.%s.svc", zcName, cfg.Namespace())}
	stdout, err := utilPodExec(ctx, cfg, cmd)
	if err != nil {
		return "", err
	}

	return strings.Trim(string(stdout), "\n"), nil
}

type agentStatus struct {
	State         string `json:"state"`
	Mode          string `json:"mode"`
	ActiveZone    string `json:"active_zone"`
	ZcConnected   bool   `json:"zc_connected"`
	PeerConnected bool   `json:"peer_connected"`
	Target        string `json:"target"`
}

func utilGetAgentStatus(ctx context.Context, cfg *envconf.Config, host string) ([]*agentStatus, error) {
	cmd := []string{"election-agent-cli", "--host", host, "control", "get-status"}
	status := []*agentStatus{}
	stdout, err := utilPodExec(ctx, cfg, cmd)
	if err != nil {
		return status, err
	}

	if err := json.Unmarshal(stdout, &status); err != nil {
		return status, fmt.Errorf("failed to decode agent status, err: %w", err)
	}

	return status, nil
}

func utilSimluate(ctx context.Context, cfg *envconf.Config, host string, state string, duration time.Duration, election string, numClients int, candidates int) (string, error) {
	log.Printf("Simulate %s state with prefix %s on %s, number of client: %d, candidates: %d, duration: %s\n",
		state, election, host, numClients, candidates, duration.String())
	cmd := []string{
		"election-agent-cli", "--host", host, "simulate",
		"--state", state,
		"--duration", duration.String(),
		"--election", election,
		"--num", fmt.Sprint(numClients),
		"--candidates", fmt.Sprint(candidates),
		"--camp_timeout", "3s",
		"--extend_timeout", "3s",
		"--term", "10000",
	}
	stdout, err := utilPodExec(ctx, cfg, cmd)
	if err != nil {
		return string(stdout), err
	}

	return string(stdout), nil
}

func utilSimluateResign(ctx context.Context, cfg *envconf.Config, host string, election string, numClients int, candidates int) (string, error) {
	log.Printf("Resign all elections by prefix %s on %s, number of client: %d, candidates: %d\n",
		election, host, numClients, candidates)
	cmd := []string{
		"election-agent-cli", "--host", host, "simulate",
		"--resign",
		"--election", election,
		"--num", fmt.Sprint(numClients),
		"--candidates", fmt.Sprint(candidates),
	}
	stdout, err := utilPodExec(ctx, cfg, cmd)
	if err != nil {
		return string(stdout), err
	}

	return string(stdout), nil
}

func svcGRPCHost(cfg *envconf.Config, name string) string {
	return fmt.Sprintf("%s.%s.svc:443", name, cfg.Namespace())
}

func headlessSvcGRPCHost(cfg *envconf.Config, name string) string {
	return fmt.Sprintf("%s-headless.%s.svc:443", name, cfg.Namespace())
}
