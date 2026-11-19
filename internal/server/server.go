// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package server implements the askiso-server HTTP daemon wrapping pkg/iso20022
// and pkg/iso20022/ffi for fintechs, banks, and event-streaming sidecars.
package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/sebastienrousseau/askiso/pkg/iso20022/ffi"
)

const (
	defaultMaxBodyBytes = 8 << 20 // 8 MiB
	readHeaderTimeout   = 5 * time.Second
	readTimeout         = 30 * time.Second
	writeTimeout        = 30 * time.Second
	idleTimeout         = 60 * time.Second
)

// Config configures an askiso-server HTTP instance.
type Config struct {
	Addr         string
	Version      string
	MaxBodyBytes int64
	Logger       *slog.Logger
}

// Server serves AskISO over HTTP with bounded request sizes and timeouts.
type Server struct {
	cfg    Config
	srv    *http.Server
	logger *slog.Logger
}

// New constructs a Server with safe defaults.
func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:8080"
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.MaxBodyBytes <= 0 {
		cfg.MaxBodyBytes = defaultMaxBodyBytes
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &Server{cfg: cfg, logger: logger}
	s.srv = &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
	return s
}

// Handler returns the HTTP handler for testing or embedding.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.handleHealthz)
	mux.HandleFunc("POST /v1/dispatch", s.handleDispatch)
	mux.HandleFunc("POST /v1/lint", s.handleOp("lint"))
	mux.HandleFunc("POST /v1/validate", s.handleOp("validate"))
	mux.HandleFunc("POST /v1/translate/mt", s.handleOp("translate_mt"))
	mux.HandleFunc("POST /v1/translate/mx", s.handleOp("translate_mx"))
	mux.HandleFunc("POST /v1/classify-addresses", s.handleOp("classify_addresses"))
	mux.HandleFunc("GET /v1/lookup", s.handleLookup)
	mux.HandleFunc("GET /v1/search", s.handleSearch)
	return mux
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	s.logger.Info("starting askiso-server", "addr", s.cfg.Addr, "version", s.cfg.Version)
	return s.srv.ListenAndServe()
}

// Shutdown gracefully stops the HTTP server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": s.cfg.Version,
		"abi":     ffi.ABIVersion,
	})
}

func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	body, ok := s.readBody(w, r)
	if !ok {
		return
	}
	var req struct {
		Op      string          `json:"op"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Op) == "" {
		writeJSON(w, http.StatusBadRequest, ffi.Envelope{
			OK:    false,
			ABI:   ffi.ABIVersion,
			Error: "op and valid JSON body are required",
		})
		return
	}
	s.writeFFI(w, ffi.Dispatch(req.Op, req.Payload))
}

func (s *Server) handleOp(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := s.readBody(w, r)
		if !ok {
			return
		}
		s.writeFFI(w, ffi.Dispatch(op, body))
	}
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	payload, _ := json.Marshal(map[string]string{"id": id})
	s.writeFFI(w, ffi.Dispatch("lookup", payload))
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	payload, _ := json.Marshal(map[string]string{"query": q})
	s.writeFFI(w, ffi.Dispatch("search", payload))
}

func (s *Server) readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	limited := http.MaxBytesReader(w, r.Body, s.cfg.MaxBodyBytes)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, ffi.Envelope{
			OK:    false,
			ABI:   ffi.ABIVersion,
			Error: err.Error(),
		})
		return nil, false
	}
	return body, true
}

func (s *Server) writeFFI(w http.ResponseWriter, raw []byte) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	var env struct {
		OK bool `json:"ok"`
	}
	status := http.StatusOK
	if err := json.Unmarshal(raw, &env); err == nil && !env.OK {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
