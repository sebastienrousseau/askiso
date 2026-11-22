// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package ffi provides a stable, JSON-envelope dispatcher over pkg/iso20022
// shared by the C-ABI (cmd/askiso-cabi), WASM, and HTTP server surfaces.
// Every language SDK (TypeScript, Python, Java) binds to this contract so
// validation, linting, and translation verdicts never drift from the Go core.
package ffi

import (
	"encoding/json"
	"fmt"
	"strings"

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
	return run(op, payload, requestedCatalog(payload))
}

// DispatchPinned executes op like Dispatch, but always reads the catalogue at
// catalogPath (empty means the default search) and never one named in the
// payload. Hosts that serve untrusted callers, such as askiso-server, use it so
// a request cannot make them read an arbitrary directory.
func DispatchPinned(op string, payload []byte, catalogPath string) []byte {
	return run(op, payload, catalogPath)
}

func run(op string, payload []byte, catalogPath string) []byte {
	data, err := dispatch(strings.TrimSpace(op), payload, catalogPath)
	if err != nil {
		return marshalEnvelope(Envelope{OK: false, ABI: ABIVersion, Error: err.Error()})
	}
	return marshalEnvelope(Envelope{OK: true, ABI: ABIVersion, Data: data})
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

// handler runs one operation. catalogPath is chosen by the entry point, never
// decoded from payload here, so DispatchPinned's guarantee holds for every op.
type handler func(payload []byte, catalogPath string) (any, error)

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

func dispatch(op string, payload []byte, catalogPath string) (any, error) {
	h, ok := handlers[op]
	if !ok {
		return nil, fmt.Errorf("unknown operation %q", op)
	}
	return h(payload, catalogPath)
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

func opVersion([]byte, string) (any, error) {
	return map[string]string{"abi": ABIVersion}, nil
}

func opLookup(payload []byte, catalogPath string) (any, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := requireField(payload, &req, "id", &req.ID); err != nil {
		return nil, err
	}
	// A missing catalogue is not an error: Lookup answers from the built-in
	// light index instead.
	cat, _ := iso20022.OpenCatalogue(catalogPath)
	return cat.Lookup(req.ID)
}

func opSearch(payload []byte, catalogPath string) (any, error) {
	var req struct {
		Query string `json:"query"`
	}
	if err := requireField(payload, &req, "query", &req.Query); err != nil {
		return nil, err
	}
	cat, _ := iso20022.OpenCatalogue(catalogPath)
	return cat.Search(req.Query)
}

func opLint(payload []byte, _ string) (any, error) {
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

func opValidate(payload []byte, catalogPath string) (any, error) {
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
	cat, err := iso20022.OpenCatalogue(catalogPath)
	if err != nil {
		return nil, err
	}
	return cat.Validate([]byte(req.XML))
}

func opTranslateMT(payload []byte, _ string) (any, error) {
	var req struct {
		MT string `json:"mt"`
	}
	if err := requireField(payload, &req, "mt", &req.MT); err != nil {
		return nil, err
	}
	return iso20022.TranslateMT([]byte(req.MT))
}

func opTranslateMX(payload []byte, _ string) (any, error) {
	var req struct {
		XML string `json:"xml"`
	}
	if err := requireField(payload, &req, "xml", &req.XML); err != nil {
		return nil, err
	}
	return iso20022.TranslateMX([]byte(req.XML))
}

func opClassifyAddresses(payload []byte, _ string) (any, error) {
	var req struct {
		XML string `json:"xml"`
	}
	if err := requireField(payload, &req, "xml", &req.XML); err != nil {
		return nil, err
	}
	return iso20022.ClassifyAddresses([]byte(req.XML))
}
