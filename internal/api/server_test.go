package api

import (
	"context"
	"errors"
	"os"
	"testing"

	"election-agent/internal/config"
	"election-agent/internal/kube"
	"election-agent/internal/lease"
	"election-agent/internal/logging"
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

func startMockServer(ctx context.Context, cfg *config.Config) (*Server, error) {
	driver := lease.NewMockRedisLeaseDriver(cfg)
	if driver == nil {
		return nil, errors.New("failed to create mock redis leader driver")
	}

	mgr := lease.NewLeaseManager(ctx, cfg, driver)
	kubeClient, err := kube.NewKubeClient(ctx, cfg)
	if err != nil {
		return nil, err
	}

	apiServer := NewServer(ctx, cfg, mgr, kubeClient)
	if apiServer == nil {
		return nil, errors.New("failed to create API server")
	}

	err = apiServer.Start()
	if err != nil {
		return nil, err
	}

	if !apiServer.WaitReady() {
		return nil, errors.New("failed to wait API server be ready")
	}

	return apiServer, nil
}
