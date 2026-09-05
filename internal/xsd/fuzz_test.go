// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package xsd_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sebastienrousseau/askiso/internal/xsd"
)

// A schema is a file the user downloaded. It is not hostile, but it is not
// verified either, and a parser that panics on a truncated or malformed one
// takes the whole tool down. These targets assert the only property that
// matters here: the parser returns, either a schema or an error.
//
//	go test ./internal/xsd/ -fuzz FuzzParse -fuzztime 60s

// parseBudget bounds a single parse in the fuzzer. Real schemas are a few
// hundred kilobytes and parse in milliseconds; anything past this is a
// pathological shape worth keeping as a regression input.
const parseBudget = 2 * time.Second

func FuzzParse(f *testing.F) {
	f.Add(`<?xml version="1.0"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:a" xmlns="urn:a">
  <xs:element name="Document" type="Document"/>
  <xs:complexType name="Document">
    <xs:sequence><xs:element name="MsgId" type="Max35Text"/></xs:sequence>
  </xs:complexType>
  <xs:simpleType name="Max35Text">
    <xs:restriction base="xs:string"><xs:maxLength value="35"/></xs:restriction>
  </xs:simpleType>
</xs:schema>`)

	// The shapes most likely to break a hand-written parser: unbalanced tags,
	// unknown constructs, absurd occurrence counts, and deep nesting.
	f.Add(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"><xs:element name="A"`)
	f.Add(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="u">
  <xs:complexType name="T"><xs:choice maxOccurs="99999999999999999999">
    <xs:element name="A" minOccurs="-5" type="xs:string"/></xs:choice></xs:complexType></xs:schema>`)
	f.Add(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="u">
  <xs:simpleType name="T"><xs:restriction base="xs:string">
    <xs:maxLength value="not-a-number"/><xs:pattern value="[["/>
  </xs:restriction></xs:simpleType></xs:schema>`)
	f.Add(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="u">
  <xs:complexType name="T"><xs:sequence><xs:any/></xs:sequence></xs:complexType>
  <xs:complexType name="U"><xs:simpleContent><xs:extension base="xs:decimal">
    <xs:attribute name="Ccy" type="xs:string" use="required"/>
  </xs:extension></xs:simpleContent></xs:complexType></xs:schema>`)
	f.Add("")
	f.Add("<")

	f.Fuzz(func(t *testing.T, data string) {
		if len(data) > 1<<20 {
			return
		}
		// Returning is not enough: a schema is untrusted input, and a parse
		// that takes seconds on a megabyte is a way to stall the validator.
		started := time.Now()
		schema, err := xsd.Parse(strings.NewReader(data))
		if elapsed := time.Since(started); elapsed > parseBudget {
			t.Fatalf("Parse took %v on %d bytes; budget is %v", elapsed, len(data), parseBudget)
		}
		if err != nil {
			if schema != nil {
				t.Fatalf("Parse returned both a schema and an error: %v", err)
			}
			return
		}
		if schema == nil {
			t.Fatal("Parse returned neither a schema nor an error")
		}

		// Everything a caller may reach for has to be safe on whatever parsed.
		if _, ok := schema.RootElement(); ok {
			for name := range schema.ComplexTypes {
				_, _ = schema.ResolveComplex(name)
			}
			for name := range schema.SimpleTypes {
				_, _ = schema.ResolveSimple(name)
				_, _ = schema.EffectiveFacets(name)
			}
		}
		if len(schema.ElementOrder) > len(schema.Elements) {
			t.Fatalf("ElementOrder has %d entries for %d elements",
				len(schema.ElementOrder), len(schema.Elements))
		}
	})
}

// FuzzStructuredSchema explores occurrence, compositor and facet combinations
// while guaranteeing syntactically valid XSD. The same schema must parse
// deterministically and preserve every generated semantic constraint.
func FuzzStructuredSchema(f *testing.F) {
	f.Add(uint16(35), false, false, uint8(1))
	f.Add(uint16(1), true, true, uint8(3))
	f.Add(^uint16(0), false, true, uint8(255))

	f.Fuzz(func(t *testing.T, rawMax uint16, optional, choice bool, rawEnums uint8) {
		maxLength := int(rawMax%256) + 1
		minOccurs := 1
		if optional {
			minOccurs = 0
		}
		enumCount := int(rawEnums%8) + 1

		var enums strings.Builder
		for i := range enumCount {
			fmt.Fprintf(&enums, `<xs:enumeration value="C%02d"/>`, i)
		}
		particle := fmt.Sprintf(`<xs:element name="Value" type="Code" minOccurs="%d"/>`, minOccurs)
		if choice {
			particle = `<xs:choice><xs:element name="A" type="Code"/><xs:element name="B" type="Code"/></xs:choice>`
		}

		source := fmt.Sprintf(`<xs:schema xmlns:xs="%s" targetNamespace="urn:fuzz">
  <xs:element name="Document" type="DocumentType"/>
  <xs:complexType name="DocumentType"><xs:sequence>%s</xs:sequence></xs:complexType>
  <xs:simpleType name="Code"><xs:restriction base="xs:string">
    <xs:maxLength value="%d"/>%s
  </xs:restriction></xs:simpleType>
</xs:schema>`, xsd.NSSchema, particle, maxLength, enums.String())

		first, err := xsd.Parse(strings.NewReader(source))
		if err != nil {
			t.Fatalf("generated schema rejected: %v\n%s", err, source)
		}
		second, err := xsd.Parse(strings.NewReader(source))
		if err != nil {
			t.Fatalf("deterministic reparse failed: %v", err)
		}
		root, ok := first.RootElement()
		if !ok || root.Name != "Document" || root.Type != "DocumentType" {
			t.Fatalf("root declaration lost: %#v", root)
		}
		facets, base := first.EffectiveFacets("Code")
		if base != "string" || facets.MaxLength == nil || *facets.MaxLength != maxLength {
			t.Fatalf("facet mismatch: base=%q facets=%+v", base, facets)
		}
		if len(facets.Enumeration) != enumCount {
			t.Fatalf("enumerations=%d want %d", len(facets.Enumeration), enumCount)
		}
		facets2, base2 := second.EffectiveFacets("Code")
		if base2 != base || facets2.MaxLength == nil || *facets2.MaxLength != *facets.MaxLength ||
			strings.Join(facets2.Enumeration, "\x00") != strings.Join(facets.Enumeration, "\x00") {
			t.Fatalf("nondeterministic parse: first=%+v second=%+v", facets, facets2)
		}
	})
}
