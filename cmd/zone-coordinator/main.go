package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"election-agent/internal/agent"
	"election-agent/internal/config"
	"election-agent/internal/logging"
	"election-agent/internal/zc"
)

func init() {
	env := os.Getenv("ZC_ENV")
	if env == "" {
		env = "production"
	}
	logLevel := os.Getenv("ZC_LOG_LEVEL")
	if logLevel == "" {
		logLevel = "info"
	}

	config.Default = &config.Config{Env: env, LogLevel: logLevel}
	logging.Init()

	agent.AutoSetProcsMem()
}

func main() {
	portStr := os.Getenv("ZC_PORT")
	if portStr == "" {
		portStr = "80"
	}

	n, err := strconv.ParseInt(portStr, 10, 32)
	if err != nil {
		logging.Fatalw("The `ZC_PORT` environment variable needs to be a number", "error", err)
	}
	port := int(n)

	zone := os.Getenv("ZC_ZONE")
	if zone == "" {
		logging.Fatal("The `ZC_ZONE` environment variable can't be empty")
	}

	version := os.Getenv("ZC_VERSION")
	// set default zc version to v2
	if version == "" {
		version = "v2"
	}
	if version != "v1" && version != "v2" {
		logging.Fatalw("The `ZC_VERSION` environment variable must be v1 or v2")
	}

	logging.Infow("Environment variable", "ZC_ZONE", zone, "ZC_PORT", port, "ZC_VERSION", version)

	server, err := zc.NewServer(port, zone, version)
	if err != nil {
		logging.Fatalw("Failed to parse v2 zone string", "error", err)
	}

	processed := make(chan struct{})
	go func() {
		exitSig := make(chan os.Signal, 1)
		signal.Notify(exitSig, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)
		<-exitSig

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			logging.Fatalw("Failed to shutdown zone coordinator", "error", err)
		}
		close(processed)
	}()

	err = server.Start()
	if !errors.Is(err, http.ErrServerClosed) {
		logging.Fatalw("Server got error", "error", err)
		os.Exit(1)
	}

	logging.Info("Zone coordinator has been shutdowned")
	<-processed
}
