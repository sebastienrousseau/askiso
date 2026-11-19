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
func Dispatch(op string, payload []byte) []byte {
	data, err := dispatch(strings.TrimSpace(op), payload)
	if err != nil {
		return marshalEnvelope(Envelope{OK: false, ABI: ABIVersion, Error: err.Error()})
	}
	return marshalEnvelope(Envelope{OK: true, ABI: ABIVersion, Data: data})
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

func dispatch(op string, payload []byte) (any, error) {
	switch op {
	case "version":
		return map[string]string{"abi": ABIVersion}, nil
	case "lookup":
		var req struct {
			ID          string `json:"id"`
			CatalogPath string `json:"catalog_path,omitempty"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.ID) == "" {
			return nil, fmt.Errorf("id is required")
		}
		cat, _ := iso20022.OpenCatalogue(req.CatalogPath)
		return cat.Lookup(req.ID)
	case "search":
		var req struct {
			Query       string `json:"query"`
			CatalogPath string `json:"catalog_path,omitempty"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.Query) == "" {
			return nil, fmt.Errorf("query is required")
		}
		cat, _ := iso20022.OpenCatalogue(req.CatalogPath)
		return cat.Search(req.Query)
	case "lint":
		var req struct {
			XML      string `json:"xml"`
			Filename string `json:"filename,omitempty"`
			Profile  string `json:"profile,omitempty"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.XML) == "" {
			return nil, fmt.Errorf("xml is required")
		}
		lintRes, err := iso20022.Lint([]byte(req.XML), req.Filename)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.Profile) != "" {
			ruleRes, err := iso20022.CheckProfile([]byte(req.XML), req.Profile, req.Filename)
			if err != nil {
				return nil, err
			}
			return map[string]any{"lint": lintRes, "profile": ruleRes}, nil
		}
		return map[string]any{"lint": lintRes}, nil
	case "validate":
		var req struct {
			XML         string `json:"xml"`
			Schema      string `json:"schema,omitempty"`
			CatalogPath string `json:"catalog_path,omitempty"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.XML) == "" {
			return nil, fmt.Errorf("xml is required")
		}
		if strings.TrimSpace(req.Schema) != "" {
			return iso20022.ValidateAgainst([]byte(req.XML), []byte(req.Schema))
		}
		cat, err := iso20022.OpenCatalogue(req.CatalogPath)
		if err != nil {
			return nil, err
		}
		return cat.Validate([]byte(req.XML))
	case "translate_mt":
		var req struct {
			MT string `json:"mt"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.MT) == "" {
			return nil, fmt.Errorf("mt is required")
		}
		return iso20022.TranslateMT([]byte(req.MT))
	case "translate_mx":
		var req struct {
			XML string `json:"xml"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.XML) == "" {
			return nil, fmt.Errorf("xml is required")
		}
		return iso20022.TranslateMX([]byte(req.XML))
	case "classify_addresses":
		var req struct {
			XML string `json:"xml"`
		}
		if err := decodePayload(payload, &req); err != nil {
			return nil, err
		}
		if strings.TrimSpace(req.XML) == "" {
			return nil, fmt.Errorf("xml is required")
		}
		return iso20022.ClassifyAddresses([]byte(req.XML))
	default:
		return nil, fmt.Errorf("unknown operation %q", op)
	}
}
