// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package schemagen

import (
	"bytes"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/xsd"
)

func TestPatternParserNestedFailures(t *testing.T) {
	for _, pattern := range []string{`(A|\`, `)`, `[A-\`} {
		if _, err := parsePattern(pattern); err == nil {
			t.Errorf("malformed pattern %q was accepted", pattern)
		}
	}
}

func TestCharacterClassFallbackSelection(t *testing.T) {
	negated := &charClass{negated: true, ranges: []runeRange{{0x20, 0x7D}}}
	if got := negated.pick(); got != '~' {
		t.Fatalf("negated fallback picked %q, want '~'", got)
	}
	nonPrintable := &charClass{ranges: []runeRange{{1, 2}}}
	if got := nonPrintable.pick(); got != 1 {
		t.Fatalf("non-printable fallback picked %d", got)
	}
}

func TestGeneratorParticleSkipBranches(t *testing.T) {
	g := &generator{opts: Options{}, schema: &xsd.Schema{}, visiting: map[string]int{}}
	var buf bytes.Buffer
	g.particle(&buf, &xsd.Sequence{MinOccurs: 0, Particles: []xsd.Particle{&xsd.Element{Name: "A"}}}, "/Document", 1, false)
	g.particle(&buf, &xsd.Choice{MinOccurs: 0, Particles: []xsd.Particle{&xsd.Element{Name: "A"}}}, "/Document", 1, false)
	g.particle(&buf, &xsd.Choice{MinOccurs: 1}, "/Document", 1, false)
	g.particle(&buf, &xsd.Any{MinOccurs: 0}, "/Document", 1, false)
	if buf.Len() != 0 {
		t.Fatalf("optional/empty particles emitted %q", buf.String())
	}
}

func TestGeneratorEmptyComplexAndInvalidPatternValue(t *testing.T) {
	schema := &xsd.Schema{
		TargetNamespace: "urn:test",
		Elements:        map[string]*xsd.Element{"Document": {Name: "Document", Type: "Empty"}},
		ElementOrder:    []string{"Document"},
		ComplexTypes:    map[string]*xsd.ComplexType{"Empty": {Name: "Empty"}},
		SimpleTypes:     map[string]*xsd.SimpleType{},
	}
	result, err := Generate(schema, Options{})
	if err != nil || !strings.Contains(result.XML, "<Document") || !strings.Contains(result.XML, "/>") {
		t.Fatalf("empty complex type generation = %+v, %v", result, err)
	}

	bad := &xsd.SimpleType{Name: "Bad", Base: "xs:string", Facets: xsd.Facets{Pattern: []string{"["}}}
	schema.SimpleTypes["Bad"] = bad
	g := &generator{schema: schema, opts: Options{Values: map[string]string{}}, visiting: map[string]int{}}
	if got := g.simpleValue("Bad", "Value", "/Value"); got != "" || len(g.notes()) == 0 {
		t.Fatalf("unusable pattern value = %q, notes %v", got, g.notes())
	}
}

func TestFacetAndSemanticFallbacks(t *testing.T) {
	value, err := valueForFacets("xs:unknownType", xsd.Facets{})
	if err != nil || value == "" {
		t.Fatalf("unknown simple base fallback = %q, %v", value, err)
	}
	if _, ok := semanticValue("UnrecognisedElement", "string", xsd.Facets{}); ok {
		t.Fatal("unknown element unexpectedly received a semantic value")
	}
}

func TestEmptyPatternSamplesNothing(t *testing.T) {
	got, err := SamplePattern("", 0)
	if err != nil || got != "" {
		t.Fatalf("empty pattern sample = %q, %v", got, err)
	}
}

// head.001.001.01's ISONormalisedDateTime requires the "Z" form, while
// CBPR_DateTime requires an explicit offset. A generated dateTime has to
// satisfy whichever pattern its own type declares.
func TestDateTimeValueFollowsItsPattern(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pattern []string
		want    string
	}{
		{"no pattern", nil, "2026-11-14T09:00:00+00:00"},
		{"ISONormalisedDateTime", []string{".*Z"}, "2026-11-14T09:00:00Z"},
		{"CBPR_DateTime", []string{`.*(\+|-)((0[0-9])|(1[0-3])):[0-5][0-9]`}, "2026-11-14T09:00:00+00:00"},
	} {
		got, err := valueForFacets("xs:dateTime", xsd.Facets{Pattern: tc.pattern})
		if err != nil || got != tc.want {
			t.Errorf("%s: got %q, %v; want %q", tc.name, got, err, tc.want)
		}
	}
}
