package api

import (
	"context"
	"errors"
	"os"
	"testing"

	"election-agent/internal/config"
	"election-agent/internal/driver"
	"election-agent/internal/kube"
	"election-agent/internal/lease"
	"election-agent/internal/logging"
	"election-agent/internal/metric"
	"election-agent/internal/zone"
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
	driver := driver.NewMockRedisKVDriver(cfg)
	if driver == nil {
		return nil, errors.New("failed to create mock redis leader driver")
	}

	metricMgr := metric.NewMetricManager(cfg)
	mgr := lease.NewLeaseManager(ctx, cfg, metricMgr, driver)

	var kubeClient kube.KubeClient
	if cfg.Kube.Enable {
		var err error
		kubeClient, err = kube.NewKubeClient(ctx, cfg)
		if err != nil {
			return nil, err
		}
	}
	zoneMgr, err := zone.NewZoneManager(ctx, cfg, driver, mgr, metricMgr)
	if err != nil {
		return nil, err
	}

	apiServer := NewServer(ctx, cfg, mgr, zoneMgr, kubeClient)
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
