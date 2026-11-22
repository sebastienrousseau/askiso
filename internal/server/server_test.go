// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerEndpoints(t *testing.T) {
	srv := New(Config{MaxBodyBytes: 4096})
	h := srv.Handler()

	// Healthz
	req := httptest.NewRequest(http.MethodGet, "/v1/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("healthz = %d %s", rec.Code, rec.Body.String())
	}

	// Lookup & Search
	for _, tc := range []struct {
		path string
		code int
	}{
		{"/v1/lookup?id=pacs.008.001.10", http.StatusOK},
		{"/v1/lookup", http.StatusBadRequest},
		{"/v1/search?q=pacs.008", http.StatusOK},
		{"/v1/search", http.StatusBadRequest},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.code {
			t.Errorf("%s status = %d, want %d: %s", tc.path, rec.Code, tc.code, rec.Body.String())
		}
	}

	// Dispatch & Lint
	body := `{"op":"version","payload":{}}`
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/dispatch", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("dispatch = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/dispatch", strings.NewReader(`{}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty dispatch = %d, want 400", rec.Code)
	}

	lintBody, _ := json.Marshal(map[string]string{
		"xml": `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10"><FIToFICstmrCdtTrf/></Document>`,
	})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/lint", bytes.NewReader(lintBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("lint = %d %s", rec.Code, rec.Body.String())
	}

	// Oversized request body
	tooBig := strings.Repeat("a", 5000)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/lint", strings.NewReader(tooBig)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized = %d, want 413", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/dispatch", strings.NewReader(tooBig)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized dispatch = %d, want 413", rec.Code)
	}

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// Default config + invalid ListenAndServe address.
	defSrv := New(Config{Addr: "127.0.0.1:-1"})
	if err := defSrv.ListenAndServe(); err == nil {
		t.Fatal("expected error for invalid listen addr")
	}
	emptySrv := New(Config{})
	if emptySrv.cfg.Addr != "127.0.0.1:8080" {
		t.Fatalf("default addr = %q", emptySrv.cfg.Addr)
	}
}

// A network client must never choose which directory the server reads its
// catalogue from. The client here names a catalogue holding a message the
// built-in one lacks; the server must not find it on any route.
func TestServerIgnoresClientCatalogPath(t *testing.T) {
	home := t.TempDir()
	for _, k := range []string{"HOME", "XDG_DATA_HOME", "LocalAppData"} {
		t.Setenv(k, home)
	}
	t.Setenv("ASKISO_CATALOG", "")
	clientDir := t.TempDir()
	schemas := filepath.Join(clientDir, "Payments Clearing and Settlement", "Version 11.0", "Schemas")
	if err := os.MkdirAll(schemas, 0o750); err != nil {
		t.Fatal(err)
	}
	xsd := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`
	if err := os.WriteFile(filepath.Join(schemas, "pacs.999.001.01.xsd"), []byte(xsd), 0o600); err != nil {
		t.Fatal(err)
	}

	h := New(Config{}).Handler()
	payload, _ := json.Marshal(map[string]string{"id": "pacs.999.001.01", "catalog_path": clientDir})
	body, _ := json.Marshal(map[string]any{"op": "lookup", "payload": json.RawMessage(payload)})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/dispatch", bytes.NewReader(body)))
	if rec.Code == http.StatusOK {
		t.Fatalf("lookup read the client's catalog_path: %s", rec.Body.String())
	}

	// The operator's catalogue is the one requests read.
	rec = httptest.NewRecorder()
	pinned := New(Config{CatalogPath: clientDir}).Handler()
	pinned.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/lookup?id=pacs.999.001.01", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup ignored Config.CatalogPath: %d %s", rec.Code, rec.Body.String())
	}
}
