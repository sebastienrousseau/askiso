// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package lsp

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sebastienrousseau/askiso/internal/xsd"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
)

func TestServerSetLoggerAndSchemaCache(t *testing.T) {
	var logBuf bytes.Buffer
	var errOut bytes.Buffer
	s := New(strings.NewReader(""), &bytes.Buffer{}, &errOut)
	s.SetLogger(slog.New(slog.NewTextHandler(&logBuf, nil)))
	s.logf("test diagnostic %d", 1)

	if !strings.Contains(logBuf.String(), "test diagnostic 1") {
		t.Errorf("structured logger missing message: %q", logBuf.String())
	}
	if !strings.Contains(errOut.String(), "askiso-lsp: test diagnostic 1") {
		t.Errorf("errOut missing message: %q", errOut.String())
	}

	dir := t.TempDir()
	xsdDir := filepath.Join(dir, "Payments Clearing and Settlement", "Version 11.0", "Schemas")
	if err := os.MkdirAll(xsdDir, 0o755); err != nil {
		t.Fatal(err)
	}
	xsdPath := filepath.Join(xsdDir, "pacs.008.001.10.xsd")
	xsdBody := `<?xml version="1.0" encoding="UTF-8"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
           targetNamespace="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10"
           elementFormDefault="qualified">
  <xs:element name="Document" type="xs:string"/>
</xs:schema>`
	if err := os.WriteFile(xsdPath, []byte(xsdBody), 0o644); err != nil {
		t.Fatal(err)
	}
	s.SetCatalogue(func() (*iso20022.Catalogue, error) {
		return iso20022.OpenCatalogue(dir)
	})

	// Seed cache to maxCachedSchemas to exercise eviction.
	for i := 0; i < maxCachedSchemas; i++ {
		s.schemas[filepath.Join(dir, "dummy", string(rune('a'+i%26)), string(rune('0'+i/26)))] = cachedSchema{schema: &xsd.Schema{}}
	}

	doc := Parse(`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10">ok</Document>`)
	schema1, msgID, ok := s.schemaFor(doc)
	if !ok || schema1 == nil || msgID != "pacs.008.001.10" {
		t.Fatalf("schemaFor first load = (%v, %q, %v)", schema1, msgID, ok)
	}
	if len(s.schemas) != 1 {
		t.Errorf("expected cache eviction to leave 1 entry, got %d", len(s.schemas))
	}

	// Second call hits cache.
	schema2, _, ok := s.schemaFor(doc)
	if !ok || schema2 != schema1 {
		t.Errorf("expected cached schema pointer %p, got %p", schema1, schema2)
	}

	// Modify file mtime/size to invalidate cache.
	if err := os.WriteFile(xsdPath, []byte(xsdBody+"\n<!-- updated -->"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(xsdPath, future, future); err != nil {
		t.Fatal(err)
	}
	schema3, _, ok := s.schemaFor(doc)
	if !ok || schema3 == schema1 {
		t.Errorf("expected re-parsed schema after mtime/size change, got %p == %p", schema3, schema1)
	}
}
