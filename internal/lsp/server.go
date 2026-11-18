// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package lsp serves AskISO as a language server for ISO 20022 XML.
//
// Someone editing a payment message wants the same answers the CLI gives, at
// the moment they type: is this IBAN's checksum right, does this element belong
// here, will this address still be accepted under CBPR+. A language
// server is how an editor asks.
//
// The transport is the Language Server Protocol's own: JSON-RPC 2.0 framed with
// Content-Length headers, over stdin and stdout. Diagnostics come from the same
// linter, schema validator and rule profiles the CLI runs, so an editor and a
// pipeline never disagree.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sebastienrousseau/askiso/internal/xsd"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
)

const maxCachedSchemas = 64

type cachedSchema struct {
	schema  *xsd.Schema
	modTime time.Time
	size    int64
}

// Server is a language server for ISO 20022 XML documents.
type Server struct {
	Name    string
	Version string
	// Profile is the scheme rule profile applied alongside the linter. The
	// client may change or disable it; cbpr-2026 remains the default as a
	// readiness check for the deferred structured-address requirement.
	Profile string

	conn   *conn
	errOut io.Writer
	logger *slog.Logger

	mu   sync.RWMutex
	docs map[string]*Document
	// schemas caches parsed XSDs by path; editor changes should validate the
	// document again, not reparse the same catalogue file on every keystroke.
	schemas map[string]cachedSchema

	// catalogue opens the user's installed schemas. Absent one, the server
	// still lints and still applies rule profiles.
	catalogue CatalogueFunc

	initialized bool
	shutdown    bool
}

// CatalogueFunc opens the user's installed schemas.
type CatalogueFunc func() (*iso20022.Catalogue, error)

// New builds a server reading from in and writing to out. Diagnostics about the
// server itself go to errOut, which must not be the same stream as out. If in
// implements io.ReadCloser, Serve owns it and closes it when stopping so that a
// blocked transport read cannot leak a goroutine.
func New(in io.Reader, out, errOut io.Writer) *Server {
	return &Server{
		Name:      "askiso-lsp",
		Version:   "dev",
		Profile:   "cbpr-2026",
		conn:      newConn(in, out),
		errOut:    errOut,
		docs:      map[string]*Document{},
		schemas:   map[string]cachedSchema{},
		catalogue: openInstalledCatalogue(),
	}
}

// SetVersion records the build version reported to the client.
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

// SetCatalogue replaces how the server reaches the user's installed schemas.
func (s *Server) SetCatalogue(open CatalogueFunc) {
	if open != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.catalogue = open
	}
}

// openInstalledCatalogue reads the user's catalogue once and caches the result.
func openInstalledCatalogue() CatalogueFunc {
	var (
		mu  sync.Mutex
		cat *iso20022.Catalogue
	)
	return func() (*iso20022.Catalogue, error) {
		mu.Lock()
		defer mu.Unlock()
		if cat != nil {
			return cat, nil
		}
		opened, err := iso20022.OpenCatalogue("")
		if err != nil {
			return nil, err
		}
		cat = opened
		return cat, nil
	}
}

// Serve handles messages until the stream ends, the client says exit, or the
// context is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.conn.canInterruptRead() {
		return s.serveBlocking(ctx)
	}

	type readResult struct {
		msg *message
		err error
	}
	reads := make(chan readResult)
	readerDone := make(chan struct{})
	stopReader := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			msg, err := s.conn.read()
			select {
			case reads <- readResult{msg: msg, err: err}:
			case <-ctx.Done():
				return
			case <-stopReader:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	defer func() {
		close(stopReader)
		_ = s.conn.interruptRead()
		<-readerDone
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case result := <-reads:
			stop, err := s.consume(result.msg, result.err)
			if stop {
				return err
			}
		}
	}
}

// serveBlocking avoids a helper goroutine for readers that cannot be closed.
// Cancellation remains observable between messages; an arbitrary io.Reader
// cannot be interrupted safely while Read itself is blocked.
func (s *Server) serveBlocking(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		stop, err := s.consume(s.conn.read())
		if stop {
			return err
		}
	}
}

func (s *Server) consume(msg *message, err error) (stop bool, result error) {
	switch {
	case errors.Is(err, io.EOF):
		return true, nil
	case err != nil:
		var pe *parseError
		if errors.As(err, &pe) {
			// A body that did not decode is answerable; the stream is still
			// synchronised because the framing was correct.
			_ = s.conn.replyError(nil, codeParse, "the request body is not valid JSON: "+pe.Error())
			return false, nil
		}
		// A framing error desynchronises the stream, so there is no
		// recovering from it.
		return true, err
	default:
		return s.handle(msg), nil
	}
}

// handle processes one message and reports whether the server should stop.
func (s *Server) handle(msg *message) (stop bool) {
	isRequest := len(msg.ID) > 0

	if msg.Method == "exit" {
		return true
	}

	if !s.initialized && msg.Method != "initialize" {
		if isRequest {
			_ = s.conn.replyError(msg.ID, codeServerNotInit, "initialize must complete first")
		}
		return false
	}
	if s.shutdown && msg.Method != "exit" {
		if isRequest {
			_ = s.conn.replyError(msg.ID, codeInvalidRequest, "the server is shutting down")
		}
		return false
	}

	switch msg.Method {
	case "initialize":
		s.initialized = true
		_ = s.conn.reply(msg.ID, s.capabilities())

	case "initialized":
		// A notification confirming the handshake; nothing to do.

	case "shutdown":
		s.shutdown = true
		_ = s.conn.reply(msg.ID, nil)

	case "textDocument/didOpen":
		s.didOpen(msg.Params)

	case "textDocument/didChange":
		s.didChange(msg.Params)

	case "textDocument/didClose":
		s.didClose(msg.Params)

	case "textDocument/didSave":
		// The document is already current; a save changes nothing.

	case "textDocument/hover":
		s.request(msg, s.hover)

	case "textDocument/completion":
		s.request(msg, s.completion)

	case "textDocument/documentSymbol":
		s.request(msg, s.documentSymbol)

	case "textDocument/diagnostic":
		s.request(msg, s.documentDiagnostic)

	case "workspace/didChangeConfiguration":
		s.didChangeConfiguration(msg.Params)

	case "$/setTrace", "$/cancelRequest":
		// Accepted and ignored.

	default:
		if isRequest {
			_ = s.conn.replyError(msg.ID, codeMethodNotFound, "no such method: "+msg.Method)
		}
	}
	return false
}

// request runs a handler that produces a result for a request, turning a
// failure into a protocol error rather than dropping the reply.
func (s *Server) request(msg *message, handler func(json.RawMessage) (any, error)) {
	result, err := handler(msg.Params)
	if err != nil {
		_ = s.conn.replyError(msg.ID, codeRequestFailed, err.Error())
		return
	}
	_ = s.conn.reply(msg.ID, result)
}

func (s *Server) capabilities() map[string]any {
	s.mu.RLock()
	name, version := s.Name, s.Version
	s.mu.RUnlock()
	return map[string]any{
		"capabilities": map[string]any{
			// Full synchronisation: ISO 20022 messages are small, and rebuilding
			// the index is cheaper than applying incremental edits correctly.
			"textDocumentSync": map[string]any{
				"openClose": true,
				"change":    1,
			},
			"hoverProvider":          true,
			"documentSymbolProvider": true,
			"completionProvider": map[string]any{
				"triggerCharacters": []string{"<"},
			},
			"diagnosticProvider": map[string]any{
				"interFileDependencies": false,
				"workspaceDiagnostics":  false,
			},
		},
		"serverInfo": map[string]any{
			"name":    name,
			"version": version,
		},
	}
}

// ---------------------------------------------------------------------------
// Document lifecycle
// ---------------------------------------------------------------------------

type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

func (s *Server) didOpen(params json.RawMessage) {
	var p struct {
		TextDocument textDocumentItem `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		s.logf("didOpen: %v", err)
		return
	}
	s.store(p.TextDocument.URI, p.TextDocument.Text)
	s.publish(p.TextDocument.URI)
}

func (s *Server) didChange(params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		s.logf("didChange: %v", err)
		return
	}
	if len(p.ContentChanges) == 0 {
		return
	}
	// Full synchronisation: the last change carries the whole document.
	s.store(p.TextDocument.URI, p.ContentChanges[len(p.ContentChanges)-1].Text)
	s.publish(p.TextDocument.URI)
}

func (s *Server) didClose(params json.RawMessage) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}

	s.mu.Lock()
	delete(s.docs, p.TextDocument.URI)
	s.mu.Unlock()

	// Clearing the diagnostics matters: an editor keeps showing the last set
	// otherwise, on a file that is no longer open.
	_ = s.conn.notify("textDocument/publishDiagnostics", map[string]any{
		"uri":         p.TextDocument.URI,
		"diagnostics": []Diagnostic{},
	})
}

func (s *Server) didChangeConfiguration(params json.RawMessage) {
	var p struct {
		Settings struct {
			AskISO struct {
				Profile string `json:"profile"`
			} `json:"askiso"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	if p.Settings.AskISO.Profile != "" {
		if _, err := iso20022.CheckProfile([]byte("<Document/>"), p.Settings.AskISO.Profile, ""); err != nil {
			s.logf("ignoring unknown rule profile %q", p.Settings.AskISO.Profile)
			return
		}
	}

	s.mu.Lock()
	s.Profile = p.Settings.AskISO.Profile
	s.mu.Unlock()

	// The setting changes every verdict, so every open document is rechecked.
	s.mu.RLock()
	uris := make([]string, 0, len(s.docs))
	for uri := range s.docs {
		uris = append(uris, uri)
	}
	s.mu.RUnlock()

	sort.Strings(uris)
	for _, uri := range uris {
		s.publish(uri)
	}
}

func (s *Server) store(uri, text string) {
	doc := Parse(text)
	s.mu.Lock()
	s.docs[uri] = doc
	s.mu.Unlock()
}

func (s *Server) document(uri string) (*Document, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	doc, ok := s.docs[uri]
	return doc, ok
}

func (s *Server) logf(format string, args ...any) {
	// A failure to log is not worth propagating: the stream it would report on
	// is the one that just failed.
	msg := fmt.Sprintf(format, args...)
	s.mu.RLock()
	logger := s.logger
	s.mu.RUnlock()
	if logger != nil {
		logger.Warn(msg)
	}
	_, _ = fmt.Fprintf(s.errOut, "askiso-lsp: %s\n", msg)
}

// ---------------------------------------------------------------------------
// Schema access
// ---------------------------------------------------------------------------

// schemaFor loads the schema a document's namespace names, when the user has it
// installed. Everything that needs it degrades rather than failing.
func (s *Server) schemaFor(doc *Document) (*xsd.Schema, string, bool) {
	msgID, err := iso20022.MessageIDFromInstance([]byte(doc.Text))
	if err != nil {
		return nil, "", false
	}
	s.mu.RLock()
	open := s.catalogue
	s.mu.RUnlock()
	cat, err := open()
	if err != nil {
		return nil, msgID, false
	}
	path, err := cat.SchemaPath(msgID)
	if err != nil {
		return nil, msgID, false
	}
	info, statErr := os.Stat(path)
	s.mu.RLock()
	cached, ok := s.schemas[path]
	s.mu.RUnlock()
	if ok && cached.schema != nil && statErr == nil && cached.modTime.Equal(info.ModTime()) && cached.size == info.Size() {
		return cached.schema, msgID, true
	}
	schema, err := xsd.ParseFile(path)
	if err != nil {
		s.logf("parsing %s: %v", path, err)
		return nil, msgID, false
	}
	s.mu.Lock()
	if len(s.schemas) >= maxCachedSchemas {
		clear(s.schemas)
	}
	var entry cachedSchema
	entry.schema = schema
	if statErr == nil {
		entry.modTime = info.ModTime()
		entry.size = info.Size()
	}
	s.schemas[path] = entry
	s.mu.Unlock()
	return schema, msgID, true
}

func (s *Server) documentDiagnostic(params json.RawMessage) (any, error) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.TextDocument.URI == "" {
		return nil, errors.New("textDocument.uri is required")
	}
	doc, ok := s.document(p.TextDocument.URI)
	if !ok {
		return nil, fmt.Errorf("document is not open: %s", p.TextDocument.URI)
	}
	return map[string]any{"kind": "full", "items": s.diagnose(doc)}, nil
}

// trimNamespace removes a namespace prefix from an element name.
func trimNamespace(name string) string {
	if i := strings.IndexByte(name, ':'); i >= 0 {
		return name[i+1:]
	}
	return name
}
