package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/api"
	"election-agent/internal/config"
	"election-agent/internal/driver"
	"election-agent/internal/kube"
	"election-agent/internal/lease"
	"election-agent/internal/logging"
	"election-agent/internal/metric"
	"election-agent/internal/zone"
)

func init() {
	if err := config.Init(); err != nil {
		os.Exit(1)
	}

	logging.Init()

	agent.AutoSetProcsMem()
}

func main() {
	var err error
	var kvDriver driver.KVDriver

	ctx := context.Background()
	cfg := config.GetDefault()

	switch cfg.Driver {
	case "redis":
		kvDriver, err = driver.NewRedisKVDriver(ctx, cfg)
	default:
		logging.Fatalw("Unsupported driver", "driver", cfg.Driver)
	}
	if kvDriver == nil || err != nil {
		logging.Fatalw("Failed to initialize key-value driver", "error", err)
	}

	var kubeClient kube.KubeClient
	if cfg.Kube.Enable {
		kubeClient, err = kube.NewKubeClient(ctx, cfg)
		if err != nil {
			logging.Warnw("Failed to initialize k8s client", "error", err)
		}
	}

	metricMgr := metric.NewMetricManager(cfg)

	leaseMgr := lease.NewLeaseManager(ctx, cfg, metricMgr, kvDriver)

	zoneMgr, err := zone.NewZoneManager(ctx, cfg, kvDriver, leaseMgr, metricMgr)
	if err != nil {
		logging.Fatalw("Failed to initialize zone manager", "error", err.Error())
		return
	}

	logging.Infow("Start zone manager", "env", cfg.Env, "enable", cfg.Zone.Enable, "check_interval", cfg.Zone.CheckInterval)
	err = zoneMgr.Start()
	if err != nil {
		logging.Fatalw("Zone manager failed to start", "error", err.Error())
		return
	}

	apiServer := api.NewServer(ctx, cfg, leaseMgr, zoneMgr, kubeClient)

	logging.Infow("Start election agent", "env", cfg.Env)
	err = apiServer.Start()
	if err != nil {
		logging.Fatalw("API server failed to start", "error", err.Error())
		return
	}

	exitSig := make(chan os.Signal, 1)
	signal.Notify(exitSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	<-exitSig

	exitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if err := apiServer.Shutdown(exitCtx); err != nil {
		logging.Fatalw("Failed to shutdown API server",
			"error", err.Error(),
		)
	}

	if err := zoneMgr.Shutdown(exitCtx); err != nil {
		logging.Fatalw("Failed to shutdown API server",
			"error", err.Error(),
		)
	}

	logging.Info("Election agent has been shutdowned")
}
