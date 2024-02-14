package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"election-agent/internal/api"
	"election-agent/internal/config"
	"election-agent/internal/kube"
	"election-agent/internal/lease"
	"election-agent/internal/logging"

	"github.com/KimMachineGun/automemlimit/memlimit"
	"github.com/pbnjay/memory"
	"go.uber.org/automaxprocs/maxprocs"
)

func init() {
	if err := config.Init(); err != nil {
		os.Exit(1)
	}

	logging.Init()

	// Automatically set GOMAXPROCS based on the number of processors
	undo, err := maxprocs.Set()
	defer undo()
	if err != nil {
		logging.Warnw("Failed to set GOMAXPROCS", "error", err)
	}
	logging.Infow("CPU information", "GOMAXPROCS", runtime.GOMAXPROCS(0), "NumCPU", runtime.NumCPU())

	// Automatically set GOMEMLIMIT based on cgroup memory setting or system total memory
	_, ok := os.LookupEnv("GOMEMLIMIT")
	if ok {
		mem := debug.SetMemoryLimit(-1)
		logging.Infow("Set memory limit by GOMEMLIMIT environment variable", "mem", memByteToStr(mem))
	} else {
		sysTotalMem := memory.TotalMemory()
		limit, err := memlimit.FromCgroup()
		if err == nil && limit < sysTotalMem {
			mem, _ := memlimit.SetGoMemLimit(0.9)
			logging.Infow("Set memory limit by cgroup", "mem", memByteToStr(mem), "system_total_mem", memByteToStr(sysTotalMem))
		} else {
			mem := int64(float64(sysTotalMem) * 0.9)
			debug.SetMemoryLimit(mem)
			logging.Infow("Set memory limit by system total memory", "mem", memByteToStr(mem), "system_total_mem", memByteToStr(sysTotalMem))
		}
	}
}

func memByteToStr[T int64 | uint64](v T) string {
	return fmt.Sprintf("%d MB", uint64(v)/1048576)
}

func main() {
	var err error
	var leaseDriver lease.LeaseDriver

	ctx := context.Background()
	cfg := config.GetDefault()

	switch cfg.Driver {
	case "redis":
		leaseDriver, err = lease.NewRedisLeaseDriver(ctx, cfg)
	default:
		logging.Fatalw("Unsupported driver", "driver", cfg.Driver)
	}
	if leaseDriver == nil || err != nil {
		logging.Fatalw("Failed to initialize lease driver", "error", err)
	}

	var kubeClient kube.KubeClient
	if cfg.Kube.Enable {
		kubeClient, err = kube.NewKubeClient(ctx, cfg)
		if err != nil {
			logging.Warnw("Failed to initialize k8s client", "error", err)
		}
	}
	leaseMgr := lease.NewLeaseManager(ctx, cfg, leaseDriver)

	apiServer := api.NewServer(ctx, cfg, leaseMgr, kubeClient)

	logging.Infow("Start election agent", "env", cfg.Env)
	err = apiServer.Start()
	if err != nil {
		logging.Fatalw("API server failed to start",
			"error", err.Error(),
		)
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

	logging.Info("Election agent has been shutdowned")
}
