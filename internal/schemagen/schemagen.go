// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package schemagen builds a valid instance of any ISO 20022 message from its
// schema.
//
// AskISO's template generator covers four message types. There are 2,845. The
// difference matters to anyone working on a message that is not a payment: a
// securities settlement instruction, a corporate action notification, a
// collateral claim. For those, "here is what one looks like" has meant reading
// a 300-page message definition report.
//
// This walks the schema instead. Every mandatory element is emitted in the
// order the content model declares, every choice takes its first branch, and
// every value satisfies the facets of its type -- enumerations, patterns,
// lengths, digits, and the date and time formats. Optional elements are left
// out unless asked for, because a minimal message is the one worth reading.
//
// The result is verified, not asserted: TestEveryInstalledMessageGenerates runs
// this over every schema the user has installed and validates each result with
// AskISO's own validator.
package schemagen

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/codes"
	"github.com/sebastienrousseau/askiso/internal/xsd"
)

// Options configures a generated message.
type Options struct {
	// Optional includes elements the schema marks optional, up to Depth. The
	// default is a minimal message: everything mandatory and nothing else.
	Optional bool
	// Repeats is how many times to emit an element that may occur more than
	// once. One by default; a schema's own minimum always wins.
	Repeats int
	// MaxDepth bounds the walk. ISO 20022 nests deeply and a few types recurse.
	MaxDepth int
	// Values overrides generated content by element name, so a caller can put
	// a real amount or a real BIC into an otherwise synthetic message.
	Values map[string]string
	// ExternalCodes supplies Registration Authority values omitted from ISO
	// 20022 XSD enumerations. A matching real code replaces the shape-only
	// placeholder used when no publication is available.
	ExternalCodes *codes.ExternalSets
}

// DefaultOptions is a minimal message: mandatory elements only.
func DefaultOptions() Options {
	return Options{Repeats: 1, MaxDepth: 30}
}

// Result is a generated message and what had to be decided along the way.
type Result struct {
	// XML is the generated document.
	XML string
	// Root is the document element's name.
	Root string
	// Elements counts the elements emitted.
	Elements int
	// Notes records anything a reader should know: a choice that was taken, a
	// recursion that was stopped, a value that could not be derived.
	Notes []string
}

// Generate builds an instance of a schema's document element.
func Generate(schema *xsd.Schema, opts Options) (*Result, error) {
	if schema == nil {
		return nil, fmt.Errorf("no schema")
	}
	if opts.Repeats < 1 {
		opts.Repeats = 1
	}
	if opts.MaxDepth < 1 {
		opts.MaxDepth = 30
	}

	root, ok := schema.RootElement()
	if !ok {
		return nil, fmt.Errorf("the schema declares no global element")
	}

	g := &generator{schema: schema, opts: opts, visiting: map[string]int{}}

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	g.element(&buf, root, "/"+root.Name, 0, true)

	return &Result{
		XML:      strings.TrimRight(buf.String(), "\n"),
		Root:     root.Name,
		Elements: g.count,
		Notes:    g.notes(),
	}, nil
}

type generator struct {
	schema *xsd.Schema
	opts   Options
	// visiting counts how many times a type is open on the current path, so a
	// recursive type stops rather than looping.
	visiting map[string]int
	count    int
	seen     map[string]bool
	noteList []string
}

func (g *generator) note(format string, args ...any) {
	if g.seen == nil {
		g.seen = map[string]bool{}
	}
	msg := fmt.Sprintf(format, args...)
	if g.seen[msg] {
		return
	}
	g.seen[msg] = true
	g.noteList = append(g.noteList, msg)
}

func (g *generator) notes() []string {
	out := append([]string(nil), g.noteList...)
	sort.Strings(out)
	return out
}

// element emits one element and everything beneath it.
func (g *generator) element(buf *bytes.Buffer, el *xsd.Element, path string, depth int, isRoot bool) {
	if depth > g.opts.MaxDepth {
		g.note("stopped at %s: the document nests deeper than %d levels", path, g.opts.MaxDepth)
		return
	}
	g.count++

	indent := strings.Repeat("  ", depth)
	attrs := ""
	if isRoot && g.schema.TargetNamespace != "" {
		attrs = fmt.Sprintf(" xmlns=%q", g.schema.TargetNamespace)
	}

	ct, isComplex := g.schema.ResolveComplex(el.Type)
	if !isComplex {
		// A value element: the text is generated from the type's facets.
		value := g.value(el, path)
		fmt.Fprintf(buf, "%s<%s%s>%s</%s>\n", indent, el.Name, attrs, escape(value), el.Name)
		return
	}

	// A complex type with simple content carries a value and attributes, which
	// is how every amount in ISO 20022 is shaped: <Amt Ccy="EUR">1.00</Amt>.
	if ct.SimpleBase != "" {
		attrs += g.attributes(ct, path)
		value := g.simpleValue(ct.SimpleBase, el.Name, path)
		fmt.Fprintf(buf, "%s<%s%s>%s</%s>\n", indent, el.Name, attrs, escape(value), el.Name)
		return
	}

	attrs += g.attributes(ct, path)

	if ct.Content == nil {
		fmt.Fprintf(buf, "%s<%s%s/>\n", indent, el.Name, attrs)
		return
	}

	// A type already open on this path would recurse without end. The element
	// is emitted empty, which the schema permits only when its own content is
	// optional; the note says what happened either way.
	if g.visiting[el.Type] > 0 {
		g.note("%s repeats the type %s, so it was left empty to stop the recursion", path, el.Type)
		fmt.Fprintf(buf, "%s<%s%s/>\n", indent, el.Name, attrs)
		return
	}

	var body bytes.Buffer
	g.visiting[el.Type]++
	g.particle(&body, ct.Content, path, depth+1, false)

	// A mandatory element whose content is entirely optional comes out empty,
	// which is valid and useless: <FinInstrmId/> tells a reader nothing about
	// how a financial instrument is identified. Emitting the first thing the
	// content model offers is just as valid and shows the shape.
	if body.Len() == 0 {
		g.particle(&body, ct.Content, path, depth+1, true)
		if body.Len() > 0 {
			g.note("%s permits an empty element; its first child was emitted to show the shape", path)
		}
	}
	g.visiting[el.Type]--

	if body.Len() == 0 {
		fmt.Fprintf(buf, "%s<%s%s/>\n", indent, el.Name, attrs)
		return
	}
	fmt.Fprintf(buf, "%s<%s%s>\n%s%s</%s>\n", indent, el.Name, attrs, body.String(), indent, el.Name)
}

// particle emits a content model.
//
// forced marks a branch that has to appear because a mandatory choice selected
// it. Inside such a branch an element's own minOccurs of zero does not mean it
// can be left out: something has to satisfy the choice, and this is what was
// chosen. ISO 20022 uses that shape constantly -- <Mtg> requires one of
// Clssfctn and XtndedClssfctn, and both are declared optional.
func (g *generator) particle(buf *bytes.Buffer, p xsd.Particle, path string, depth int, forced bool) {
	switch t := p.(type) {
	case *xsd.Element:
		count := t.MinOccurs
		if count == 0 && (g.opts.Optional || forced) {
			count = 1
		}
		if count > 0 && g.opts.Repeats > count {
			count = g.opts.Repeats
			if t.MaxOccurs != xsd.Unbounded && count > t.MaxOccurs {
				count = t.MaxOccurs
			}
		}
		for i := 0; i < count; i++ {
			child := path + "/" + t.Name
			if i > 0 {
				child = fmt.Sprintf("%s/%s[%d]", path, t.Name, i+1)
			}
			g.element(buf, t, child, depth, false)
		}

	case *xsd.Sequence:
		count := t.MinOccurs
		if count == 0 {
			if !g.opts.Optional && !forced {
				return
			}
			count = 1
		}
		for i := 0; i < count; i++ {
			for _, child := range t.Particles {
				// Only the first child of a forced sequence has to appear: a
				// sequence is satisfied by its own mandatory members once it is
				// present at all.
				g.particle(buf, child, path, depth, forced && i == 0 && isFirstMandatory(t, child))
			}
		}

	case *xsd.Choice:
		if t.MinOccurs == 0 && !g.opts.Optional && !forced {
			return
		}
		if len(t.Particles) == 0 {
			return
		}
		// The first branch, always. A generated message has to be
		// reproducible, and the first branch is the one the schema author put
		// first -- which in ISO 20022 is reliably the common case: IBAN before
		// Othr, Cd before Prtry.
		if len(t.Particles) > 1 {
			g.note("%s is a choice of %d; the first branch was taken", path, len(t.Particles))
		}
		g.particle(buf, t.Particles[0], path, depth, true)

	case *xsd.Any:
		if t.MinOccurs == 0 {
			return
		}
		// A wildcard has no declared name. SupplementaryData is the only place
		// one appears, and it accepts anything.
		fmt.Fprintf(buf, "%s<SplmtryData/>\n", strings.Repeat("  ", depth))
	}
}

// isFirstMandatory reports whether a particle is the one that must carry a
// forced sequence's content: the first child, whatever its own minOccurs.
func isFirstMandatory(seq *xsd.Sequence, child xsd.Particle) bool {
	return len(seq.Particles) > 0 && seq.Particles[0] == child
}

// attributes emits a complex type's attributes.
func (g *generator) attributes(ct *xsd.ComplexType, path string) string {
	var b strings.Builder
	for _, attr := range ct.Attributes {
		if !attr.Required && !g.opts.Optional {
			continue
		}
		value := g.simpleValue(attr.Type, attr.Name, path+"/@"+attr.Name)
		fmt.Fprintf(&b, " %s=%q", attr.Name, escape(value))
	}
	return b.String()
}

// value generates the text content of an element.
func (g *generator) value(el *xsd.Element, path string) string {
	return g.simpleValue(el.Type, el.Name, path)
}

// simpleValue generates a value for a simple type, honouring an override first,
// then a name AskISO recognises, then the type's own facets.
func (g *generator) simpleValue(typeName, elementName, path string) string {
	if v, ok := g.opts.Values[elementName]; ok {
		return v
	}

	facets, base := g.schema.EffectiveFacets(typeName)

	// An enumerated type has to take one of its own codes, whatever the element
	// is called.
	if len(facets.Enumeration) > 0 {
		return preferredCode(facets.Enumeration)
	}

	// A name AskISO recognises gets a value that is not merely valid but
	// correct: a real BIC, an IBAN whose checksum works, a UUIDv4.
	if v, ok := semanticValue(elementName, base, facets); ok {
		return v
	}

	v, err := valueForFacets(base, facets)
	if err != nil {
		g.note("%s: %v", path, err)
		return ""
	}

	// A type named "...Code" with no enumeration is an external code set: the
	// Registration Authority maintains it outside the schema, so the schema
	// constrains only the shape. A single letter satisfies that and reads as
	// nothing; a four-character token reads as a code.
	if isExternalCodeType(typeName) {
		if members := g.opts.ExternalCodes.Set(typeName); len(members) > 0 {
			return members[0].Code
		}
		return codePlaceholder(facets)
	}
	return v
}

// isExternalCodeType reports whether a type is a code set the schema does not
// enumerate.
func isExternalCodeType(typeName string) bool {
	return strings.HasSuffix(typeName, "Code") && strings.Contains(typeName, "External")
}

// codePlaceholder produces something that reads as a code within the type's
// own length limits.
func codePlaceholder(f xsd.Facets) string {
	const token = "ANCH"

	width := len(token)
	if f.Length != nil {
		width = *f.Length
	} else if f.MaxLength != nil && *f.MaxLength < width {
		width = *f.MaxLength
	}
	if f.MinLength != nil && *f.MinLength > width {
		width = *f.MinLength
	}
	if width < 1 {
		width = 1
	}

	var b strings.Builder
	for len([]rune(b.String())) < width {
		b.WriteString(token)
	}
	return string([]rune(b.String())[:width])
}

// fixedValues are lexical values that satisfy their base type whatever the
// facets, because ISO 20022 places none on these types that they could break.
var fixedValues = map[string]string{
	"boolean":      "true",
	"date":         "2026-11-14",
	"time":         "09:00:00",
	"gYear":        "2026",
	"gYearMonth":   "2026-11",
	"gMonth":       "--11",
	"gDay":         "---14",
	"duration":     "P1D",
	"base64Binary": "QW5jaG9y",
	"hexBinary":    "416E63686F72",
	"anyURI":       "https://www.iso20022.org/",
}

// valueForFacets builds a value from a base type and its constraints.
func valueForFacets(base string, f xsd.Facets) (string, error) {
	b := normaliseBase(base)
	if v, ok := fixedValues[b]; ok {
		return v, nil
	}
	switch b {
	case "decimal", "float", "double":
		return decimalValue(f), nil
	case "integer", "int", "long", "short", "nonNegativeInteger", "positiveInteger":
		return integerValue(f), nil
	case "dateTime":
		return dateTimeValue(f), nil
	}
	return stringValue(f)
}

// dateTimeCandidates are equivalent instants in the two forms the catalogue's
// patterns ask for. The explicit offset comes first: CBPR_DateTime requires it
// and rejects "Z", while head.001.001.01's ISONormalisedDateTime requires "Z".
var dateTimeCandidates = []string{"2026-11-14T09:00:00+00:00", "2026-11-14T09:00:00Z"}

// dateTimeValue returns the first candidate every pattern of the type accepts.
func dateTimeValue(f xsd.Facets) string {
	for _, v := range dateTimeCandidates {
		if matchesAll(v, f.Pattern) {
			return v
		}
	}
	return dateTimeCandidates[0]
}

// matchesAll reports whether v satisfies every XSD pattern. XSD patterns are
// anchored to the whole value; one Go cannot compile counts as not matching.
func matchesAll(v string, patterns []string) bool {
	for _, p := range patterns {
		re, err := regexp.Compile(`^(?:` + p + `)$`)
		if err != nil || !re.MatchString(v) {
			return false
		}
	}
	return true
}

// stringValue satisfies the pattern and length facets of a text type.
func stringValue(f xsd.Facets) (string, error) {
	minLen := 1
	if f.MinLength != nil && *f.MinLength > minLen {
		minLen = *f.MinLength
	}
	if f.Length != nil {
		minLen = *f.Length
	}

	if len(f.Pattern) > 0 {
		// A type with several patterns has to satisfy all of them, which in
		// this catalogue never happens; the first is the constraint.
		v, err := SamplePattern(f.Pattern[0], minLen)
		if err != nil {
			return "", err
		}
		return clampLength(v, f), nil
	}

	// No pattern: a readable placeholder of the right length.
	const filler = "ASKISO SAMPLE VALUE "
	var b strings.Builder
	for len([]rune(b.String())) < minLen {
		b.WriteString(filler)
	}
	value := b.String()
	if minLen > 0 {
		value = string([]rune(value)[:minLen])
	}
	return clampLength(strings.TrimSpace(value), f), nil
}

// clampLength trims a value to maxLength, and pads it when it fell short.
func clampLength(v string, f xsd.Facets) string {
	r := []rune(v)

	if f.Length != nil {
		switch {
		case len(r) > *f.Length:
			return string(r[:*f.Length])
		case len(r) < *f.Length:
			return v + strings.Repeat("A", *f.Length-len(r))
		}
		return v
	}
	if f.MaxLength != nil && len(r) > *f.MaxLength {
		return string(r[:*f.MaxLength])
	}
	if f.MinLength != nil && len(r) < *f.MinLength {
		return v + strings.Repeat("A", *f.MinLength-len(r))
	}
	if v == "" {
		return "A"
	}
	return v
}

// decimalValue builds a number within the digit and bound facets.
func decimalValue(f xsd.Facets) string {
	total, fraction := 18, 2
	if f.TotalDigits != nil {
		total = *f.TotalDigits
	}
	if f.FractionDigits != nil {
		fraction = *f.FractionDigits
	}
	if fraction > 2 {
		fraction = 2
	}
	if fraction >= total {
		fraction = total - 1
	}
	if fraction < 0 {
		fraction = 0
	}

	// An amount reads better than an arbitrary number, so long as it fits.
	whole := "1000"
	if total-fraction < len(whole) {
		whole = strings.Repeat("1", max(1, total-fraction))
	}
	if fraction == 0 {
		return applyBounds(whole, f)
	}
	return applyBounds(whole+"."+strings.Repeat("0", fraction), f)
}

func integerValue(f xsd.Facets) string {
	digits := 1
	if f.TotalDigits != nil && *f.TotalDigits > 0 {
		digits = *f.TotalDigits
	}
	value := "1"
	if digits > 1 {
		value = "1"
	}
	return applyBounds(value, f)
}

// applyBounds keeps a number inside minInclusive and maxInclusive when the type
// declares them. Only the bounds ISO 20022 actually uses are handled: a
// non-negative floor and a percentage ceiling.
func applyBounds(v string, f xsd.Facets) string {
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return v
	}
	if f.MinInclusive != nil {
		if lo, err := strconv.ParseFloat(*f.MinInclusive, 64); err == nil && n < lo {
			return trimNumber(lo)
		}
	}
	if f.MaxInclusive != nil {
		if hi, err := strconv.ParseFloat(*f.MaxInclusive, 64); err == nil && n > hi {
			return trimNumber(hi)
		}
	}
	return v
}

func trimNumber(n float64) string {
	if n == float64(int64(n)) {
		return strconv.FormatInt(int64(n), 10)
	}
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// normaliseBase strips the xs: prefix a base type may carry.
func normaliseBase(base string) string {
	if i := strings.IndexByte(base, ':'); i >= 0 {
		return base[i+1:]
	}
	return base
}

// preferredCode picks a code from an enumeration, favouring ones that make a
// sample readable rather than the first alphabetically.
func preferredCode(codes []string) string {
	preferred := map[string]bool{
		"CRDT": true, "DEBT": true, "SHAR": true, "NORM": true, "HIGH": true,
		"CLRG": true, "TRF": true, "ADDR": true, "OPBD": true, "ACSC": true,
	}
	for _, c := range codes {
		if preferred[c] {
			return c
		}
	}
	return codes[0]
}

func escape(s string) string {
	var buf bytes.Buffer
	_ = xml.EscapeText(&buf, []byte(s))
	return buf.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
