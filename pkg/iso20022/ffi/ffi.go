// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package ffi provides a stable, JSON-envelope dispatcher over pkg/iso20022
// shared by the C-ABI (cmd/askiso-cabi), WASM, and HTTP server surfaces.
// Every language SDK (TypeScript, Python, Java) binds to this contract so
// validation, linting, and translation verdicts never drift from the Go core.
package ffi

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/catalog"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
)

// ABIVersion identifies the JSON envelope contract version.
const ABIVersion = "askiso-abi/v1"

// Envelope is the deterministic JSON response returned by every FFI call.
type Envelope struct {
	OK    bool   `json:"ok"`
	ABI   string `json:"abi"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// Dispatch executes a named operation with a JSON payload and returns a JSON
// Envelope. It never panics and always returns valid JSON.
//
// Dispatch honours a "catalog_path" field in the payload, so it is for
// in-process callers that already own the filesystem (the C-ABI library, WASM).
// A host that takes payloads from the network uses DispatchPinned instead.
func Dispatch(op string, payload []byte) []byte {
	return run(op, payload, &source{path: requestedCatalog(payload)})
}

// DispatchPinned executes op like Dispatch, but always reads the catalogue at
// catalogPath (empty means the default search) and never one named in the
// payload. Hosts that serve untrusted callers, such as askiso-server, use it so
// a request cannot make them read an arbitrary directory.
//
// Its responses also keep the host's filesystem private: schema, sample and
// report paths are relative to the catalogue root, the root never appears in
// an error, and a missing catalogue is reported without the directories that
// were searched.
func DispatchPinned(op string, payload []byte, catalogPath string) []byte {
	return run(op, payload, &source{path: catalogPath, private: true})
}

// source is the catalogue an operation reads, as chosen by the entry point.
type source struct {
	path    string
	private bool   // hide host paths in the response
	root    string // set once the catalogue is open
}

func (src *source) open() (*iso20022.Catalogue, error) {
	cat, err := iso20022.OpenCatalogue(src.path)
	if cat != nil {
		src.root = cat.Root()
	}
	return cat, err
}

func run(op string, payload []byte, src *source) []byte {
	data, err := dispatch(strings.TrimSpace(op), payload, src)
	if err != nil {
		return marshalEnvelope(Envelope{OK: false, ABI: ABIVersion, Error: src.errorText(err)})
	}
	if src.private {
		data = relativize(data, src.root)
	}
	return marshalEnvelope(Envelope{OK: true, ABI: ABIVersion, Data: data})
}

// errorText is err's message, without host paths when src is private.
func (src *source) errorText(err error) string {
	if !src.private {
		return err.Error()
	}
	var notFound *catalog.NotFoundError
	if errors.As(err, &notFound) {
		return "no ISO 20022 catalogue is installed on this server"
	}
	msg := err.Error()
	if src.root != "" {
		msg = strings.ReplaceAll(msg, src.root+string(filepath.Separator), "")
		msg = strings.ReplaceAll(msg, src.root, ".")
	}
	return msg
}

// relativize rewrites the paths in lookup and search results relative to root.
func relativize(data any, root string) any {
	switch v := data.(type) {
	case iso20022.MessageInfo:
		return relInfo(v, root)
	case []iso20022.MessageInfo:
		out := make([]iso20022.MessageInfo, len(v))
		for i, m := range v {
			out[i] = relInfo(m, root)
		}
		return out
	}
	return data
}

func relInfo(m iso20022.MessageInfo, root string) iso20022.MessageInfo {
	m.SchemaPath = relPath(m.SchemaPath, root)
	m.SamplePath = relPath(m.SamplePath, root)
	if m.ReportPaths != nil {
		reports := make([]string, len(m.ReportPaths))
		for i, p := range m.ReportPaths {
			reports[i] = relPath(p, root)
		}
		m.ReportPaths = reports
	}
	return m
}

// relPath is p relative to root in forward-slash form. A path outside root, or
// one that cannot be made relative, is reduced to its file name.
func relPath(p, root string) string {
	if p == "" {
		return ""
	}
	if root != "" {
		if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(p)
}

// requestedCatalog reads the optional catalog_path field. A payload that is not
// valid JSON yields "", and the operation reports the decode error itself.
func requestedCatalog(payload []byte) string {
	var req struct {
		CatalogPath string `json:"catalog_path"`
	}
	_ = decodePayload(payload, &req)
	return req.CatalogPath
}

func marshalEnvelope(env Envelope) []byte {
	out, err := json.Marshal(env)
	if err != nil {
		return []byte(`{"ok":false,"abi":"askiso-abi/v1","error":"marshal response failed"}`)
	}
	return out
}

func decodePayload(payload []byte, dst any) error {
	if len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, dst); err != nil {
		return fmt.Errorf("invalid JSON payload: %w", err)
	}
	return nil
}

// handler runs one operation. src is chosen by the entry point, never decoded
// from payload here, so DispatchPinned's guarantee holds for every op.
type handler func(payload []byte, src *source) (any, error)

var handlers = map[string]handler{
	"version":            opVersion,
	"lookup":             opLookup,
	"search":             opSearch,
	"lint":               opLint,
	"validate":           opValidate,
	"translate_mt":       opTranslateMT,
	"translate_mx":       opTranslateMX,
	"classify_addresses": opClassifyAddresses,
}

func dispatch(op string, payload []byte, src *source) (any, error) {
	h, ok := handlers[op]
	if !ok {
		return nil, fmt.Errorf("unknown operation %q", op)
	}
	return h(payload, src)
}

// requireField decodes payload into dst and checks that the named field is set.
func requireField(payload []byte, dst any, name string, value *string) error {
	if err := decodePayload(payload, dst); err != nil {
		return err
	}
	if strings.TrimSpace(*value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func opVersion([]byte, *source) (any, error) {
	return map[string]string{"abi": ABIVersion}, nil
}

func opLookup(payload []byte, src *source) (any, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := requireField(payload, &req, "id", &req.ID); err != nil {
		return nil, err
	}
	// A missing catalogue is not an error: Lookup answers from the built-in
	// light index instead.
	cat, _ := src.open()
	return cat.Lookup(req.ID)
}

func opSearch(payload []byte, src *source) (any, error) {
	var req struct {
		Query string `json:"query"`
	}
	if err := requireField(payload, &req, "query", &req.Query); err != nil {
		return nil, err
	}
	cat, _ := src.open()
	return cat.Search(req.Query)
}

func opLint(payload []byte, _ *source) (any, error) {
	var req struct {
		XML      string `json:"xml"`
		Filename string `json:"filename,omitempty"`
		Profile  string `json:"profile,omitempty"`
	}
	if err := requireField(payload, &req, "xml", &req.XML); err != nil {
		return nil, err
	}
	lintRes, err := iso20022.Lint([]byte(req.XML), req.Filename)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Profile) == "" {
		return map[string]any{"lint": lintRes}, nil
	}
	ruleRes, err := iso20022.CheckProfile([]byte(req.XML), req.Profile, req.Filename)
	if err != nil {
		return nil, err
	}
	return map[string]any{"lint": lintRes, "profile": ruleRes}, nil
}

func opValidate(payload []byte, src *source) (any, error) {
	var req struct {
		XML    string `json:"xml"`
		Schema string `json:"schema,omitempty"`
	}
	if err := requireField(payload, &req, "xml", &req.XML); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Schema) != "" {
		return iso20022.ValidateAgainst([]byte(req.XML), []byte(req.Schema))
	}
	cat, err := src.open()
	if err != nil {
		return nil, err
	}
	return cat.Validate([]byte(req.XML))
}

func opTranslateMT(payload []byte, _ *source) (any, error) {
	var req struct {
		MT string `json:"mt"`
	}
	if err := requireField(payload, &req, "mt", &req.MT); err != nil {
		return nil, err
	}
	return iso20022.TranslateMT([]byte(req.MT))
}

func opTranslateMX(payload []byte, _ *source) (any, error) {
	var req struct {
		XML string `json:"xml"`
	}
	if err := requireField(payload, &req, "xml", &req.XML); err != nil {
		return nil, err
	}
	return iso20022.TranslateMX([]byte(req.XML))
}

func opClassifyAddresses(payload []byte, _ *source) (any, error) {
	var req struct {
		XML string `json:"xml"`
	}
	if err := requireField(payload, &req, "xml", &req.XML); err != nil {
		return nil, err
	}
	return iso20022.ClassifyAddresses([]byte(req.XML))
}
