package api

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"election-agent/internal/config"
	"election-agent/internal/kube"
	"election-agent/internal/lease"
	"election-agent/internal/logging"
	"election-agent/internal/zone"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"

	eagrpc "election-agent/proto/election_agent/v1"

	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

type Server struct {
	ctx context.Context
	cfg *config.Config

	httpRouter        *gin.Engine
	httpServer        *http.Server
	grpcListener      net.Listener
	grpcServer        *grpc.Server
	grpcHealthService *health.Server
	leaseMgr          *lease.LeaseManager
	zoneMgr           zone.ZoneManager
	kubeClient        kube.KubeClient
	ready             chan bool
}

func NewServer(ctx context.Context, cfg *config.Config, leaseMgr *lease.LeaseManager, zoneMgr zone.ZoneManager, kubeClient kube.KubeClient) *Server {
	readyChanSize := 0
	if cfg.GRPC.Enable {
		readyChanSize++
	}
	if cfg.HTTP.Enable {
		readyChanSize++
	}

	return &Server{
		ctx:        ctx,
		cfg:        cfg,
		leaseMgr:   leaseMgr,
		zoneMgr:    zoneMgr,
		kubeClient: kubeClient,
		ready:      make(chan bool, readyChanSize),
	}
}

func (srv *Server) Start() error {
	if srv.cfg.GRPC.Enable {
		if err := srv.startGRPC(); err != nil {
			return err
		}
	}

	if srv.cfg.HTTP.Enable {
		if err := srv.startHTTP(); err != nil {
			return err
		}
	}

	return nil
}

func (srv *Server) startGRPC() error {
	var err error
	listenAddr := fmt.Sprintf(":%d", srv.cfg.GRPC.Port)
	srv.grpcListener, err = net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	srv.grpcServer = grpc.NewServer()
	electionService := newElectionGRPCService(srv.cfg, srv.leaseMgr, srv.kubeClient)
	controlService := newControlGRPCService(srv.cfg, srv.leaseMgr, srv.zoneMgr)
	eagrpc.RegisterElectionServer(srv.grpcServer, electionService)
	eagrpc.RegisterControlServer(srv.grpcServer, controlService)

	srv.grpcHealthService = health.NewServer()
	if srv.grpcHealthService == nil {
		logging.Error("Failed to initialize gRPC health service")
		return errors.New("Failed to initialize gRPC health service")
	}

	srv.grpcHealthService.SetServingStatus("", healthgrpc.HealthCheckResponse_NOT_SERVING)
	srv.grpcHealthService.SetServingStatus("livez", healthgrpc.HealthCheckResponse_NOT_SERVING)
	srv.grpcHealthService.SetServingStatus("readyz", healthgrpc.HealthCheckResponse_NOT_SERVING)

	healthgrpc.RegisterHealthServer(srv.grpcServer, srv.grpcHealthService)

	go func() {
		srv.ready <- true

		srv.grpcHealthService.SetServingStatus("", healthgrpc.HealthCheckResponse_SERVING)
		srv.grpcHealthService.SetServingStatus("liveness", healthgrpc.HealthCheckResponse_SERVING)
		srv.grpcHealthService.SetServingStatus("readiness", healthgrpc.HealthCheckResponse_SERVING)

		logging.Infow("gRPC service serves on", "addr", listenAddr)
		err := srv.grpcServer.Serve(srv.grpcListener)
		if err != nil {
			srv.ready <- false
			logging.Errorw("gRPC service got error", "error", err.Error())
		}
	}()

	return nil
}

func (srv *Server) startHTTP() error {
	if srv.cfg.IsProdEnv() {
		gin.SetMode(gin.ReleaseMode)
	} else if srv.cfg.IsTestEnv() {
		gin.SetMode(gin.TestMode)
	} else {
		gin.SetMode(gin.DebugMode)
		gin.ForceConsoleColor()
	}

	srv.httpRouter = gin.New()

	httpService := newElectionHTTPService(srv.ctx, srv.cfg, srv.leaseMgr, srv.kubeClient)
	httpService.MountHandlers(srv.httpRouter)

	srv.httpRouter.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		if err, ok := recovered.(string); ok {
			logging.Errorw("HTTP server receive panic", "error", err, "stack", string(debug.Stack()))
			c.String(http.StatusInternalServerError, fmt.Sprintf("error: %s", err))
		}
		c.AbortWithStatus(http.StatusInternalServerError)
	}))

	listenAddr := fmt.Sprintf(":%d", srv.cfg.HTTP.Port)
	srv.httpServer = &http.Server{
		Addr:              listenAddr,
		Handler:           srv.httpRouter,
		ReadHeaderTimeout: 2 * time.Second,
	}

	go func() {
		srv.ready <- true

		logging.Infow("HTTP service serves on", "addr", listenAddr)
		err := srv.httpServer.ListenAndServe()
		if err != nil {
			srv.ready <- false
			if errors.Is(err, http.ErrServerClosed) {
				logging.Info("HTTP API server closed")
			} else {
				logging.Errorw("API service got error", "error", err.Error())
			}
		}
	}()

	return nil
}

func (srv *Server) Shutdown(ctx context.Context) error {
	if srv.cfg.GRPC.Enable {
		srv.grpcServer.GracefulStop()
	}

	if srv.cfg.HTTP.Enable {
		if err := srv.httpServer.Shutdown(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (srv *Server) WaitReady() bool {
	readyCount := 0
	readyChanSize := 0
	if srv.cfg.GRPC.Enable {
		readyChanSize++
	}
	if srv.cfg.HTTP.Enable {
		readyChanSize++
	}

	ctx, cancel := context.WithTimeout(srv.ctx, 3*time.Second)
	defer cancel()
	for {
		select {
		case v, ok := <-srv.ready:
			if ok {
				logging.Debugw("WaitReady receives ready signal", "val", v)
				readyCount++
				if readyCount >= readyChanSize {
					return v
				}
			}
		case <-ctx.Done():
			return false
		}
	}
}
