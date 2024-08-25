package zc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"election-agent/internal/logging"
)

type Server struct {
	server   *http.Server
	port     int
	version  string
	zone     []byte
	zoneMap  map[string][]string
	statusOk []byte
}

func NewServer(port int, zone string, version string) (*Server, error) {
	ver := strings.ToLower(version)
	srv := &Server{
		port:     port,
		version:  ver,
		statusOk: []byte(`{"status", "ok"}`),
	}

	if ver == "v2" {
		zoneMap, err := parseZoneString(zone)
		if err != nil {
			return nil, err
		}
		srv.zoneMap = zoneMap
		srv.zone = []byte(zoneMap["default"][0])
	} else {
		srv.zone = []byte(strings.TrimSpace(zone))
	}

	return srv, nil
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(s.handlerV1))
	if s.version == "v2" {
		mux.Handle("/v1", http.HandlerFunc(s.handlerV1))
		mux.Handle("/v2", http.StripPrefix("/v2", http.HandlerFunc(s.handlerV2)))
		mux.Handle("/v2/", http.StripPrefix("/v2", http.HandlerFunc(s.handlerV2)))
	}
	mux.Handle("/livez", http.HandlerFunc(s.livez))
	mux.Handle("/readyz", http.HandlerFunc(s.readyz))

	listenAddr := fmt.Sprintf(":%d", s.port)
	s.server = &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 1 * time.Second,
		WriteTimeout:      1 * time.Second,
		IdleTimeout:       1 * time.Second,
		MaxHeaderBytes:    1024,
	}

	logging.Infow("Zone coordinator serves on", "addr", listenAddr)
	return s.server.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *Server) handlerV1(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("X-Api-Version", s.version)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.zone)
}

func (s *Server) handlerV2(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Api-Version", s.version)

	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		jsonData, err := json.Marshal(s.zoneMap)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(jsonData)
		return
	}

	zones, ok := s.zoneMap[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	jsonData, err := json.Marshal(zones)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(jsonData)
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

func parseZoneString(zone string) (map[string][]string, error) {
	zones := make(map[string][]string)
	zone = strings.TrimSpace(zone)
	hasDefault := false

	for _, pair := range strings.Split(zone, ";") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			return nil, errors.New("contains empty key-value pair")
		}

		tokens := strings.SplitN(pair, ":", 2)
		if len(tokens) != 2 {
			return nil, errors.New("invalid format: missing ':' separator")
		}

		key := strings.TrimSpace(tokens[0])
		if key == "" {
			return nil, errors.New("contains empty key")
		}
		if key == "default" {
			hasDefault = true
		}

		values := strings.Split(tokens[1], ",")
		for i, val := range values {
			values[i] = strings.TrimSpace(val)
			if values[i] == "" {
				return nil, errors.New("contains empty value")
			}
		}
		zones[key] = values
	}

	if !hasDefault {
		return nil, errors.New("needs to have default zone")
	}

	return zones, nil
}
