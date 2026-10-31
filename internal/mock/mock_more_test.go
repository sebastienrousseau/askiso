// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package mock

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sebastienrousseau/askiso/internal/generator"
	"go.uber.org/goleak"
)

func validPayment(t *testing.T) string {
	t.Helper()
	xml, err := generator.Generate(generator.DefaultOptions("pacs.008"))
	if err != nil {
		t.Fatal(err)
	}
	return xml
}

func TestHandlePaymentAcceptsCleanMessage(t *testing.T) {
	s := NewServer(0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(validPayment(t)))

	s.handlePayment(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "pacs.002") {
		t.Errorf("a settlement confirmation should be a pacs.002:\n%s", rec.Body.String())
	}
}

func TestHandlePaymentJSONNegotiation(t *testing.T) {
	s := NewServer(0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(validPayment(t)))
	req.Header.Set("Accept", "application/json")

	s.handlePayment(rec, req)

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON: %v\n%s", err, rec.Body.String())
	}
	// ACSC (AcceptedSettlementCompleted) is the pacs.002 status for a payment
	// that has actually settled; ACCP only means the profile was accepted.
	if payload["status"] != "ACSC" {
		t.Errorf("status = %v, want ACSC", payload["status"])
	}
}

func TestHandlePaymentRejectsBadBusinessRules(t *testing.T) {
	bad := strings.Replace(validPayment(t), "DE89", "DE00", 1)

	s := NewServer(0)
	rec := httptest.NewRecorder()
	s.handlePayment(rec, httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(bad)))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "RJCT") {
		t.Errorf("a rejection should carry RJCT:\n%s", rec.Body.String())
	}

	// The JSON form of the same rejection.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(bad))
	req.Header.Set("Accept", "application/json")
	s.handlePayment(rec, req)

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("expected JSON: %v", err)
	}
	if payload["status"] != "RJCT" {
		t.Errorf("status = %v, want RJCT", payload["status"])
	}
}

func TestHandlePaymentRejectsBadRequests(t *testing.T) {
	s := NewServer(0)

	rec := httptest.NewRecorder()
	s.handlePayment(rec, httptest.NewRequest(http.MethodGet, "/v1/payments", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET should be rejected, got %d", rec.Code)
	}
	if rec.Header().Get("Allow") != http.MethodPost {
		t.Errorf("Allow = %q, want POST", rec.Header().Get("Allow"))
	}

	rec = httptest.NewRecorder()
	s.handlePayment(rec, httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader("")))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an empty body should be rejected, got %d", rec.Code)
	}
}

func TestHandleAccountStatement(t *testing.T) {
	s := NewServer(0)

	rec := httptest.NewRecorder()
	s.handleAccountStatement(rec,
		httptest.NewRequest(http.MethodGet, "/v1/accounts/DE89370400440532013000", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "camt.053") {
		t.Errorf("a statement should be a camt.053:\n%s", body)
	}
	if !strings.Contains(body, "DE89370400440532013000") {
		t.Error("the statement should carry the requested account")
	}

	rec = httptest.NewRecorder()
	s.handleAccountStatement(rec, httptest.NewRequest(http.MethodGet, "/v1/accounts/", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a missing account should be rejected, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleAccountStatement(rec,
		httptest.NewRequest(http.MethodGet, "/v1/accounts/DE00BAD", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("an invalid IBAN should be rejected as a client error, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.handleAccountStatement(rec,
		httptest.NewRequest(http.MethodPost, "/v1/accounts/DE89370400440532013000", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST statement should be rejected, got %d", rec.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	s := NewServer(0)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("health should return JSON: %v", err)
	}
	if payload["status"] != "UP" {
		t.Errorf("status = %v, want UP", payload["status"])
	}
}

func TestHealthRejectsUnsupportedMethods(t *testing.T) {
	rec := httptest.NewRecorder()
	NewServer(0).handleHealth(rec, httptest.NewRequest(http.MethodPost, "/v1/health", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != http.MethodGet {
		t.Errorf("status/Allow = %d/%q, want 405/GET", rec.Code, rec.Header().Get("Allow"))
	}
}

// Start binds a real port and serves the routes end to end.
func TestStartServesRoutes(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	s := NewServer(port)
	done := make(chan error, 1)
	go func() { done <- s.Start() }()
	t.Cleanup(func() { _ = s.Srv.Close() })

	base := "http://127.0.0.1:" + itoa(port)
	var resp *http.Response
	for i := 0; i < 50; i++ {
		resp, err = http.Get(base + "/v1/health")
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("server never came up: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "UP") {
		t.Errorf("health check failed: %s", body)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Hardening
// ---------------------------------------------------------------------------

func TestServerHasTimeoutsAndBindsLoopback(t *testing.T) {
	s := NewServer(8080)

	if s.Srv.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout must be set; without it a slow client can hold a connection open")
	}
	if s.Srv.ReadTimeout == 0 || s.Srv.WriteTimeout == 0 || s.Srv.IdleTimeout == 0 {
		t.Errorf("every timeout should be set: %+v", s.Srv)
	}
	if !strings.HasPrefix(s.Addr(), "127.0.0.1:") {
		t.Errorf("the default bind should be loopback, got %q", s.Addr())
	}

	// An explicit host is honoured.
	if got := NewServerWith(Options{Host: "0.0.0.0", Port: 9}).Addr(); got != "0.0.0.0:9" {
		t.Errorf("explicit host = %q", got)
	}
}

func TestOversizedBodyRejected(t *testing.T) {
	s := NewServer(0)
	rec := httptest.NewRecorder()

	big := strings.NewReader(strings.Repeat("x", maxBodySize+1024))
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", big)
	s.handlePayment(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rec.Code)
	}
}

// The rail used to answer AC01 for every failure, which teaches the wrong
// reason-code semantics. Each rule class now maps to its own code.
func TestRejectionReasonMatchesTheFault(t *testing.T) {
	base := validPayment(t)

	cases := map[string]struct {
		mutate func(string) string
		want   string
	}{
		"bad IBAN":     {func(s string) string { return strings.Replace(s, "DE89", "DE00", 1) }, "AC01"},
		"bad BIC":      {func(s string) string { return strings.Replace(s, "DEUTDEDDXXX", "NOTABIC", 1) }, "RC01"},
		"bad currency": {func(s string) string { return strings.Replace(s, `Ccy="EUR">5000.00`, `Ccy="EUR">5000.12345`, 1) }, "AM11"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := tc.mutate(base)
			if body == base {
				t.Skip("mutation did not apply to this fixture")
			}

			s := NewServer(0)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(body))
			req.Header.Set("Accept", "application/json")
			s.handlePayment(rec, req)

			var payload map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatalf("expected JSON: %v", err)
			}
			if payload["reason"] != tc.want {
				t.Errorf("reason = %v, want %s", payload["reason"], tc.want)
			}
		})
	}
}

func TestMalformedXMLIsRejectedAsFF01(t *testing.T) {
	s := NewServer(0)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader("<not-closed>"))
	req.Header.Set("Accept", "application/json")
	s.handlePayment(rec, req)

	var payload map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	if payload["reason"] != "FF01" {
		t.Errorf("reason = %v, want FF01", payload["reason"])
	}
}

// ---------------------------------------------------------------------------
// Scenarios
// ---------------------------------------------------------------------------

func TestScenarios(t *testing.T) {
	if len(Scenarios()) == 0 {
		t.Fatal("no scenarios registered")
	}

	cases := map[Scenario]struct {
		wantStatus int
		wantIn     string
	}{
		ScenarioRejectAC01: {http.StatusUnprocessableEntity, "AC01"},
		ScenarioRejectAC04: {http.StatusUnprocessableEntity, "AC04"},
		ScenarioDelayed:    {http.StatusOK, "PDNG"},
		ScenarioReturn:     {http.StatusOK, "ACSC"},
		ScenarioNormal:     {http.StatusOK, "ACSC"},
	}

	for sc, want := range cases {
		t.Run(scenarioName(sc), func(t *testing.T) {
			s := NewServerWith(Options{Scenario: sc})
			rec := httptest.NewRecorder()
			s.handlePayment(rec,
				httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(validPayment(t))))

			if rec.Code != want.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, want.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), want.wantIn) {
				t.Errorf("body should contain %q:\n%s", want.wantIn, rec.Body.String())
			}
		})
	}
}

func TestScenarioReportedByHealth(t *testing.T) {
	s := NewServerWith(Options{Scenario: ScenarioDelayed})
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["scenario"] != string(ScenarioDelayed) {
		t.Errorf("scenario = %v", payload["scenario"])
	}

	plain := httptest.NewRecorder()
	NewServer(0).handleHealth(plain, httptest.NewRequest(http.MethodGet, "/v1/health", nil))
	_ = json.Unmarshal(plain.Body.Bytes(), &payload)
	if payload["scenario"] != "normal" {
		t.Errorf("an unset scenario should read as normal, got %v", payload["scenario"])
	}
}

func TestReturnScenarioAnnouncesFollowUp(t *testing.T) {
	s := NewServerWith(Options{Scenario: ScenarioReturn})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(validPayment(t)))
	req.Header.Set("Accept", "application/json")
	s.handlePayment(rec, req)

	var payload map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &payload)
	followUp, ok := payload["follow_up"].(map[string]any)
	if !ok || followUp["message_type"] != "pacs.004.001.11" {
		t.Fatalf("the return scenario should announce a retrievable pacs.004: %v", payload)
	}
	href, _ := followUp["href"].(string)
	returnRec := httptest.NewRecorder()
	s.handleReturn(returnRec, httptest.NewRequest(http.MethodGet, href, nil))
	if returnRec.Code != http.StatusOK || !strings.Contains(returnRec.Body.String(), "pacs.004.001.11") {
		t.Errorf("follow-up %q did not return a pacs.004: %d\n%s", href, returnRec.Code, returnRec.Body.String())
	}
}

func TestReturnEndpointRejectsInvalidRequests(t *testing.T) {
	normal := NewServer(0)
	rec := httptest.NewRecorder()
	normal.handleReturn(rec, httptest.NewRequest(http.MethodGet, "/v1/returns/1", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("normal scenario status = %d, want 404", rec.Code)
	}

	returns := NewServerWith(Options{Scenario: ScenarioReturn})
	for _, tc := range []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/v1/returns/1", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/returns/", http.StatusBadRequest},
		{http.MethodGet, "/v1/returns/one/two", http.StatusBadRequest},
	} {
		rec = httptest.NewRecorder()
		returns.handleReturn(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}

func TestPaymentReferencesAreUnique(t *testing.T) {
	s := NewServer(0)
	refs := make(map[string]bool)
	for i := 0; i < 20; i++ {
		rec := httptest.NewRecorder()
		s.handlePayment(rec,
			httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(validPayment(t))))
		body := rec.Body.String()
		start := strings.Index(body, "<MsgId>")
		end := strings.Index(body, "</MsgId>")
		if start < 0 || end <= start {
			t.Fatalf("response has no MsgId: %s", body)
		}
		ref := body[start+len("<MsgId>") : end]
		if refs[ref] {
			t.Fatalf("duplicate response reference %q", ref)
		}
		refs[ref] = true
	}
}

func TestPaymentReferencesAreUniqueUnderContention(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	s := NewServer(0)
	payment := validPayment(t)

	const requests = 256
	refs := make(chan string, requests)
	errs := make(chan string, requests)
	var wg sync.WaitGroup
	for range requests {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			s.handlePayment(rec, httptest.NewRequest(http.MethodPost, "/v1/payments", strings.NewReader(payment)))
			body := rec.Body.String()
			start, end := strings.Index(body, "<MsgId>"), strings.Index(body, "</MsgId>")
			if rec.Code != http.StatusOK || start < 0 || end <= start {
				errs <- body
				return
			}
			refs <- body[start+len("<MsgId>") : end]
		}()
	}
	wg.Wait()
	close(refs)
	close(errs)
	for body := range errs {
		t.Fatalf("invalid concurrent payment response: %s", body)
	}
	seen := make(map[string]struct{}, requests)
	for ref := range refs {
		if _, duplicate := seen[ref]; duplicate {
			t.Fatalf("duplicate concurrent reference %q", ref)
		}
		seen[ref] = struct{}{}
	}
	if len(seen) != requests {
		t.Fatalf("references=%d want %d", len(seen), requests)
	}
}

func TestAccountStatementIgnoresTrailingPath(t *testing.T) {
	s := NewServer(0)
	rec := httptest.NewRecorder()
	s.handleAccountStatement(rec,
		httptest.NewRequest(http.MethodGet, "/v1/accounts/DE89370400440532013000/statement", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "DE89370400440532013000") {
		t.Error("the account should be carried into the statement")
	}
}

func TestShutdownIsClean(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	s := NewServer(port)
	done := make(chan error, 1)
	go func() { done <- s.Start() }()

	// Wait for it to bind.
	for i := 0; i < 100; i++ {
		if resp, err := http.Get("http://127.0.0.1:" + itoa(port) + "/v1/health"); err == nil {
			_ = resp.Body.Close()
			break
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// A clean shutdown must not be reported as a failure.
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Start should return nil after a clean shutdown, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Start did not return after Shutdown")
	}
}

// The sequence seed comes from the clock; a clock before 1970 must start the
// sequence at zero rather than wrap a negative UnixNano into a huge value.
func TestClockSeed(t *testing.T) {
	if got := clockSeed(time.Unix(0, -1)); got != 0 {
		t.Errorf("clockSeed before the epoch = %d, want 0", got)
	}
	if got := clockSeed(time.Unix(0, 42)); got != 42 {
		t.Errorf("clockSeed(42ns) = %d, want 42", got)
	}
}
