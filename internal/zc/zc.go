package zc

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"election-agent/internal/logging"
)

type Server struct {
	server   *http.Server
	port     int
	zone     []byte
	statusOk []byte
}

func NewServer(port int, zone string) *Server {
	return &Server{
		port:     port,
		zone:     []byte(zone),
		statusOk: []byte(`{"status", "ok"}`),
	}
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(s.index))
	mux.Handle("/livez", http.HandlerFunc(s.livez))
	mux.Handle("/readyz", http.HandlerFunc(s.readyz))

	listenAddr := fmt.Sprintf(":%d", s.port)
	s.server = &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 1 * time.Second,
		WriteTimeout:      1 * time.Second,
		IdleTimeout:       1 * time.Second,
		MaxHeaderBytes:    128,
	}

	logging.Infow("Zone coordinator serves on", "addr", listenAddr)
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.zone)
}

func (s *Server) livez(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.statusOk)
}

func (s *Server) readyz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.statusOk)
}
