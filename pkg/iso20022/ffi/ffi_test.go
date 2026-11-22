// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package ffi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// isolate hides any catalogue installed on the developer's machine, so a test
// sees only the catalogue it names.
func isolate(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "XDG_DATA_HOME", "LocalAppData"} {
		t.Setenv(k, home)
	}
	t.Setenv("ASKISO_CATALOG", "")
}

// clientCatalogue writes a catalogue holding pacs.999.001.01, a message the
// built-in light index does not know.
func clientCatalogue(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	schemas := filepath.Join(root, "Payments Clearing and Settlement", "Version 11.0", "Schemas")
	if err := os.MkdirAll(schemas, 0o750); err != nil {
		t.Fatal(err)
	}
	xsd := `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`
	if err := os.WriteFile(filepath.Join(schemas, "pacs.999.001.01.xsd"), []byte(xsd), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// Dispatch serves in-process callers and honours catalog_path. DispatchPinned
// serves network hosts: it reads only the operator's catalogue.
func TestCatalogPathContract(t *testing.T) {
	isolate(t)
	dir := clientCatalogue(t)
	named, _ := json.Marshal(map[string]string{"id": "pacs.999.001.01", "catalog_path": dir})
	bare, _ := json.Marshal(map[string]string{"id": "pacs.999.001.01"})

	if env := decodeEnv(t, Dispatch("lookup", named)); !env.OK {
		t.Fatalf("Dispatch ignored catalog_path: %+v", env)
	}
	if env := decodeEnv(t, DispatchPinned("lookup", named, "")); env.OK {
		t.Fatalf("DispatchPinned read the payload's catalog_path: %+v", env)
	}
	if env := decodeEnv(t, DispatchPinned("lookup", bare, dir)); !env.OK {
		t.Fatalf("DispatchPinned ignored the operator's catalogue: %+v", env)
	}
}

func decodeEnv(t *testing.T, raw []byte) Envelope {
	t.Helper()
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\nraw: %s", err, raw)
	}
	return env
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDispatchOperations(t *testing.T) {
	isolate(t)
	sampleXML := `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10"><FIToFICstmrCdtTrf/></Document>`
	xsdDoc := `<?xml version="1.0" encoding="UTF-8"?><xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10"><xs:element name="Document" type="xs:string"/></xs:schema>`
	bad := []byte(`{bad`)
	empty := []byte(`{}`)

	cases := []struct {
		name    string
		op      string
		payload []byte
		wantOK  bool
	}{
		{"version", "version", nil, true},
		{"unknown operation", "unknown_op", nil, false},
		{"lookup", "lookup", []byte(`{"id":"pacs.008.001.10"}`), true},
		{"lookup empty id", "lookup", empty, false},
		{"lookup invalid json", "lookup", bad, false},
		{"search", "search", []byte(`{"query":"pacs.008"}`), true},
		{"search empty query", "search", empty, false},
		{"search invalid json", "search", bad, false},
		{"lint", "lint", mustJSON(t, map[string]string{"xml": sampleXML, "filename": "p.xml"}), true},
		{"lint with profile", "lint", mustJSON(t, map[string]string{"xml": sampleXML, "profile": "cbpr-2026"}), true},
		{"lint unknown profile", "lint", mustJSON(t, map[string]string{"xml": sampleXML, "profile": "no-such-profile"}), false},
		{"lint empty xml", "lint", empty, false},
		{"lint invalid json", "lint", bad, false},
		{"validate inline xsd", "validate", mustJSON(t, map[string]string{
			"xml":    `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10">ok</Document>`,
			"schema": xsdDoc,
		}), true},
		{"validate empty xml", "validate", empty, false},
		{"validate invalid json", "validate", bad, false},
		{"validate empty catalogue", "validate", mustJSON(t, map[string]string{"xml": sampleXML, "catalog_path": t.TempDir()}), false},
		{"translate_mt empty", "translate_mt", empty, false},
		{"translate_mt invalid json", "translate_mt", bad, false},
		{"translate_mx empty", "translate_mx", empty, false},
		{"translate_mx invalid json", "translate_mx", bad, false},
		{"classify_addresses", "classify_addresses", []byte(`{"xml":"<Document/>"}`), true},
		{"classify_addresses empty", "classify_addresses", empty, false},
		{"classify_addresses invalid json", "classify_addresses", bad, false},
	}
	for _, tc := range cases {
		env := decodeEnv(t, Dispatch(tc.op, tc.payload))
		if env.OK != tc.wantOK || env.ABI != ABIVersion {
			t.Errorf("%s: ok=%v abi=%q, want ok=%v: %+v", tc.name, env.OK, env.ABI, tc.wantOK, env)
		}
	}
}
