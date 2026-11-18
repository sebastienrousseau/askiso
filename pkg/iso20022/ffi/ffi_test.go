// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package ffi

import (
	"encoding/json"
	"testing"
)

func decodeEnv(t *testing.T, raw []byte) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nraw: %s", err, raw)
	}
	return env
}

func TestDispatchOperations(t *testing.T) {
	if env := decodeEnv(t, Dispatch("version", nil)); !env.OK || env.ABI != ABIVersion {
		t.Fatalf("version = %+v", env)
	}
	if env := decodeEnv(t, Dispatch("unknown_op", nil)); env.OK {
		t.Fatalf("expected error for unknown operation")
	}

	// Lookup and search in light mode.
	if env := decodeEnv(t, Dispatch("lookup", []byte(`{"id":"pacs.008.001.10"}`))); !env.OK {
		t.Fatalf("lookup failed: %+v", env)
	}
	if env := decodeEnv(t, Dispatch("lookup", []byte(`{}`))); env.OK {
		t.Fatalf("expected error for empty lookup id")
	}
	if env := decodeEnv(t, Dispatch("lookup", []byte(`{bad`))); env.OK {
		t.Fatalf("expected error for invalid json")
	}
	if env := decodeEnv(t, Dispatch("search", []byte(`{"query":"pacs.008"}`))); !env.OK {
		t.Fatalf("search failed: %+v", env)
	}
	if env := decodeEnv(t, Dispatch("search", []byte(`{}`))); env.OK {
		t.Fatalf("expected error for empty search query")
	}
	if env := decodeEnv(t, Dispatch("search", []byte(`{bad`))); env.OK {
		t.Fatalf("expected error for invalid search json")
	}

	// Lint (with and without profile).
	sampleXML := `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10"><FIToFICstmrCdtTrf/></Document>`
	lintPayload, _ := json.Marshal(map[string]string{"xml": sampleXML, "filename": "p.xml"})
	if env := decodeEnv(t, Dispatch("lint", lintPayload)); !env.OK {
		t.Fatalf("lint failed: %+v", env)
	}
	lintProfilePayload, _ := json.Marshal(map[string]string{"xml": sampleXML, "profile": "cbpr-2026"})
	if env := decodeEnv(t, Dispatch("lint", lintProfilePayload)); !env.OK {
		t.Fatalf("lint with profile failed: %+v", env)
	}
	lintBadProfile, _ := json.Marshal(map[string]string{"xml": sampleXML, "profile": "no-such-profile"})
	if env := decodeEnv(t, Dispatch("lint", lintBadProfile)); env.OK {
		t.Fatalf("expected error for unknown profile")
	}
	if env := decodeEnv(t, Dispatch("lint", []byte(`{}`))); env.OK {
		t.Fatalf("expected error for empty xml")
	}
	if env := decodeEnv(t, Dispatch("lint", []byte(`{bad`))); env.OK {
		t.Fatalf("expected error for invalid lint json")
	}

	// Validate against inline XSD.
	xsdDoc := `<?xml version="1.0" encoding="UTF-8"?><xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10"><xs:element name="Document" type="xs:string"/></xs:schema>`
	valPayload, _ := json.Marshal(map[string]string{
		"xml":    `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10">ok</Document>`,
		"schema": xsdDoc,
	})
	if env := decodeEnv(t, Dispatch("validate", valPayload)); !env.OK {
		t.Fatalf("validate failed: %+v", env)
	}
	if env := decodeEnv(t, Dispatch("validate", []byte(`{}`))); env.OK {
		t.Fatalf("expected error for empty validate xml")
	}
	if env := decodeEnv(t, Dispatch("validate", []byte(`{bad`))); env.OK {
		t.Fatalf("expected error for invalid validate json")
	}
	valBadCat, _ := json.Marshal(map[string]string{"xml": sampleXML, "catalog_path": t.TempDir()})
	if env := decodeEnv(t, Dispatch("validate", valBadCat)); env.OK {
		t.Fatalf("expected error for empty catalogue")
	}

	// Translate MT / MX & ClassifyAddresses.
	if env := decodeEnv(t, Dispatch("translate_mt", []byte(`{}`))); env.OK {
		t.Fatalf("expected error for empty mt")
	}
	if env := decodeEnv(t, Dispatch("translate_mt", []byte(`{bad`))); env.OK {
		t.Fatalf("expected error for invalid mt json")
	}
	if env := decodeEnv(t, Dispatch("translate_mx", []byte(`{}`))); env.OK {
		t.Fatalf("expected error for empty mx")
	}
	if env := decodeEnv(t, Dispatch("translate_mx", []byte(`{bad`))); env.OK {
		t.Fatalf("expected error for invalid mx json")
	}
	if env := decodeEnv(t, Dispatch("classify_addresses", []byte(`{"xml":"<Document/>"}`))); !env.OK {
		t.Fatalf("classify_addresses failed: %+v", env)
	}
	if env := decodeEnv(t, Dispatch("classify_addresses", []byte(`{}`))); env.OK {
		t.Fatalf("expected error for empty classify_addresses xml")
	}
	if env := decodeEnv(t, Dispatch("classify_addresses", []byte(`{bad`))); env.OK {
		t.Fatalf("expected error for invalid classify_addresses json")
	}
}
