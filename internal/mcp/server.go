// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package mcp serves AskISO over the Model Context Protocol.
//
// An assistant asked about an ISO 20022 message has two options: recall what it
// read about the standard, or check. This server is the second option. It
// exposes the same engine the CLI runs -- the linter, the validator, the rule
// profiles, the MT converter, the registry -- as tools an agent can call, so an
// answer about a message comes from the specification rather than from memory.
//
// The transport is newline-delimited JSON-RPC 2.0 over stdin and stdout, which
// is what the protocol's stdio transport specifies. Nothing is written to
// stdout except protocol messages; diagnostics go to stderr, because a stray
// line on stdout corrupts the stream.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
)

// ProtocolVersion is the revision of the Model Context Protocol this server
// implements. A client asking for a different revision is answered with this
// one, which the specification allows: the client then decides whether to
// continue.
const ProtocolVersion = "2025-06-18"

// Server speaks MCP over a pair of streams.
type Server struct {
	// Name and Version identify the server to the client.
	Name    string
	Version string

	in io.Reader
	// inCloser is present when Serve can interrupt a blocked transport read.
	// New transfers ownership of an io.ReadCloser to Serve.
	inCloser io.Closer
	out      io.Writer
	// errOut receives diagnostics. It must never be the same stream as out.
	errOut io.Writer
	logger *slog.Logger

	tools  []Tool
	byName map[string]Tool
	// catalogue opens the user's installed schemas. SetCatalogue replaces it.
	catalogue CatalogueFunc
	mu        sync.RWMutex
	writeMu   sync.Mutex

	// initializeReceived and initialized distinguish the initialize response
	// from the client's subsequent notifications/initialized boundary. MCP does
	// not enter its operation phase until both have occurred.
	initializeReceived bool
	initialized        bool
}

// New builds a server reading requests from in and writing replies to out.
// Diagnostics go to errOut, which must be a different stream from out.
func New(in io.Reader, out, errOut io.Writer) *Server {
	s := &Server{
		Name:    "askiso",
		Version: "dev",
		in:      in,
		out:     out,
		errOut:  errOut,
		byName:  map[string]Tool{},
	}
	if closer, ok := in.(io.Closer); ok {
		s.inCloser = closer
	}
	s.catalogue = openInstalledCatalogue()
	s.register(defaultTools(s.catalogue)...)
	return s
}

// SetCatalogue replaces how the server reaches the user's installed schemas.
// Call it before serving; it rebuilds the tools that read them.
func (s *Server) SetCatalogue(open CatalogueFunc) {
	if open == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.catalogue = open
	s.tools = nil
	s.byName = map[string]Tool{}
	s.registerLocked(defaultTools(open)...)
}

// SetVersion records the build version reported during the handshake.
func (s *Server) SetVersion(v string) {
	if v != "" {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.Version = v
	}
}

// SetLogger attaches an optional structured logger for server diagnostics.
func (s *Server) SetLogger(l *slog.Logger) {
	s.mu.Lock()
	s.logger = l
	s.mu.Unlock()
}

func (s *Server) logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.mu.RLock()
	logger := s.logger
	s.mu.RUnlock()
	if logger != nil {
		logger.Warn(msg)
	}
	_, _ = fmt.Fprintf(s.errOut, "askiso-mcp: %s\n", msg)
}

func (s *Server) register(tools ...Tool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registerLocked(tools...)
}

func (s *Server) registerLocked(tools ...Tool) {
	for _, t := range tools {
		if _, dup := s.byName[t.Name]; dup {
			continue
		}
		s.byName[t.Name] = t
		s.tools = append(s.tools, t)
	}
}

// Tools lists the registered tools in declaration order. The returned slice is
// detached so callers cannot mutate the registry through slice aliasing.
func (s *Server) Tools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tool, len(s.tools))
	for i, tool := range s.tools {
		out[i] = tool
		out[i].Schema = cloneJSONObject(tool.Schema)
	}
	return out
}

func cloneJSONObject(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = cloneJSONValue(value)
	}
	return out
}

func cloneJSONValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneJSONObject(value)
	case []any:
		out := make([]any, len(value))
		for i := range value {
			out[i] = cloneJSONValue(value[i])
		}
		return out
	case []string:
		return append([]string(nil), value...)
	default:
		return value
	}
}

// ---------------------------------------------------------------------------
// JSON-RPC framing
// ---------------------------------------------------------------------------

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC 2.0 reserved error codes.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// maxLine bounds a single request. The protocol has no framing header, so a
// client that never sends a newline must not be able to exhaust memory.
const maxLine = 8 << 20 // 8 MiB

// Serve reads requests until the input ends or the context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 0, 64<<10), maxLine)
	if s.inCloser == nil {
		return s.serveBlocking(ctx, scanner)
	}

	lines := make(chan []byte)
	done := make(chan error, 1)
	readerDone := make(chan struct{})
	stopReader := make(chan struct{})
	go func() {
		defer close(readerDone)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			select {
			case lines <- line:
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-stopReader:
				return
			}
		}
		done <- scanner.Err()
	}()
	defer func() {
		close(stopReader)
		_ = s.inCloser.Close()
		<-readerDone
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-done:
			return scannerResult(err)
		case line := <-lines:
			if len(trimSpace(line)) == 0 {
				continue
			}
			s.handleLine(ctx, line)
		}
	}
}

// serveBlocking performs no asynchronous read for a reader that cannot be
// closed. That preserves the zero-leak guarantee; cancellation is checked
// between records because io.Reader provides no general interruption contract.
func (s *Server) serveBlocking(ctx context.Context, scanner *bufio.Scanner) error {
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		line := scanner.Bytes()
		if len(trimSpace(line)) != 0 {
			s.handleLine(ctx, line)
		}
	}
	return scannerResult(scanner.Err())
}

func scannerResult(err error) error {
	if errors.Is(err, bufio.ErrTooLong) {
		return fmt.Errorf("a request exceeded the %d-byte limit", maxLine)
	}
	return err
}

func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n'
}

func (s *Server) handleLine(ctx context.Context, line []byte) {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		s.fail(nil, codeParse, "the request is not valid JSON", err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		s.fail(req.ID, codeInvalidRequest, `"jsonrpc" must be "2.0"`, nil)
		return
	}

	// A request without an id is a notification: it is acted on, but never
	// answered.
	notification := len(req.ID) == 0

	result, rpcErr := s.dispatch(ctx, req)
	if notification {
		return
	}
	if rpcErr != nil {
		s.write(response{JSONRPC: "2.0", ID: req.ID, Error: rpcErr})
		return
	}
	s.write(response{JSONRPC: "2.0", ID: req.ID, Result: result})
}

func (s *Server) dispatch(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.initialize(req.Params)

	case "notifications/initialized", "initialized":
		if !s.completeInitialization() {
			return nil, &rpcError{Code: codeInvalidRequest, Message: "initialize must complete first"}
		}
		return map[string]any{}, nil

	case "ping":
		return map[string]any{}, nil

	case "tools/list":
		if !s.isInitialized() {
			return nil, &rpcError{Code: codeInvalidRequest, Message: "initialize must complete first"}
		}
		return map[string]any{"tools": s.toolDescriptors()}, nil

	case "tools/call":
		if !s.isInitialized() {
			return nil, &rpcError{Code: codeInvalidRequest, Message: "initialize must complete first"}
		}
		return s.callTool(ctx, req.Params)

	case "notifications/cancelled", "notifications/progress":
		// Nothing here is long-running enough to cancel or report on.
		return map[string]any{}, nil
	}

	return nil, &rpcError{Code: codeMethodNotFound, Message: "no such method: " + req.Method}
}

func (s *Server) initialize(params json.RawMessage) (any, *rpcError) {
	// The client's requested version is read but not required to match: the
	// specification says to answer with a version this server supports and let
	// the client decide.
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &p)
	}

	s.mu.Lock()
	s.initializeReceived = true
	s.initialized = false
	name, version := s.Name, s.Version
	s.mu.Unlock()
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{"listChanged": false},
		},
		"serverInfo": map[string]any{
			"name":    name,
			"version": version,
		},
		"instructions": instructions,
	}, nil
}

func (s *Server) completeInitialization() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initializeReceived {
		return false
	}
	s.initialized = true
	return true
}

func (s *Server) isInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

const instructions = `AskISO answers questions about ISO 20022 from the specification rather than from memory.

Use askiso_lint and askiso_check_profile before telling anyone a message is correct: the
linter checks IBAN checksums, BIC structure, currency precision and UETR format, and the
cbpr-2026 profile checks the CBPR+ structured-address rules. Swift deferred the
14 November 2026 cutover on 27 August 2026 and confirms replacement timing by December,
so state that the requirement stands rather than quoting a date that has moved.

askiso_validate and askiso_diff read the user's own downloaded schemas and report
Installed=false rather than guessing when a schema is absent. Everything else works with
no files on disk.

AskISO redistributes no specification content. Schemas come from the user's own download
from iso20022.org.`

func (s *Server) write(resp response) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		_, _ = fmt.Fprintf(s.errOut, "askiso-mcp: could not encode a reply: %v\n", err)
		return
	}
	data = append(data, '\n')
	if _, err := s.out.Write(data); err != nil {
		_, _ = fmt.Fprintf(s.errOut, "askiso-mcp: could not write a reply: %v\n", err)
	}
}

func (s *Server) fail(id json.RawMessage, code int, message string, data any) {
	s.write(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message, Data: data}})
}
