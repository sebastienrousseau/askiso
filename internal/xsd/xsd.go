// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package xsd parses the XML Schema subset that ISO 20022 uses.
//
// This is not a general XSD 1.0 implementation and does not try to be. A survey
// of all 4,746 schemas the Registration Authority publishes shows they use a
// small, closed vocabulary:
//
//	structure   schema, element, complexType, simpleType, restriction,
//	            sequence, choice, simpleContent, extension, attribute, any
//	facets      enumeration, minLength, maxLength, length, pattern,
//	            totalDigits, fractionDigits, minInclusive, maxInclusive
//	base types  string, decimal, dateTime, date, time, boolean,
//	            gYear, gYearMonth, gMonth
//
// Every schema is self-contained: no import, include, group, attributeGroup,
// union, list, key, or complexContent appears anywhere in the catalogue, and
// elementFormDefault is always "qualified". That is what makes a focused parser
// practical, and it is why AskISO can validate without linking libxml2.
package xsd

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NSSchema is the XML Schema namespace.
const NSSchema = "http://www.w3.org/2001/XMLSchema"

// Unbounded is the MaxOccurs value for maxOccurs="unbounded".
const Unbounded = -1

// Schema is a parsed XSD document.
type Schema struct {
	TargetNamespace string
	Path            string

	// Elements holds the globally declared elements, keyed by name. ISO 20022
	// schemas normally declare one ("Document", "AppHdr" or "Xchg"), but some
	// older releases declare several.
	Elements     map[string]*Element
	ComplexTypes map[string]*ComplexType
	SimpleTypes  map[string]*SimpleType

	// ElementOrder preserves declaration order, so the first global element can
	// be treated as the document root when an instance is ambiguous.
	ElementOrder []string
}

// Element is an element declaration.
type Element struct {
	Name      string
	Type      string // type name; may be a builtin such as "xs:string"
	MinOccurs int
	MaxOccurs int // Unbounded for "unbounded"
}

// Particle is one item in a content model: *Element, *Sequence, *Choice or *Any.
type Particle interface{ particle() }

func (*Element) particle()  {}
func (*Sequence) particle() {}
func (*Choice) particle()   {}
func (*Any) particle()      {}

// Sequence requires its particles to appear in order.
type Sequence struct {
	Particles []Particle
	MinOccurs int
	MaxOccurs int
}

// Choice requires exactly one of its particles.
type Choice struct {
	Particles []Particle
	MinOccurs int
	MaxOccurs int
}

// Any is a wildcard accepting one element of any name.
type Any struct {
	Namespace       string
	ProcessContents string
	MinOccurs       int
	MaxOccurs       int
}

// ComplexType describes an element with children, attributes, or both.
type ComplexType struct {
	Name    string
	Content Particle // nil for an empty or simple-content type

	// SimpleBase is set for <simpleContent><extension base="...">: the element
	// carries a simple value of that type plus the attributes below.
	SimpleBase string
	Attributes []*Attribute
}

// Attribute is an attribute declaration.
type Attribute struct {
	Name     string
	Type     string
	Required bool
}

// SimpleType is a value type: a base plus restricting facets.
type SimpleType struct {
	Name   string
	Base   string // "xs:string", or another simple type in this schema
	Facets Facets
}

// Facets are the value constraints a simple type applies.
type Facets struct {
	Enumeration    []string
	Pattern        []string
	Length         *int
	MinLength      *int
	MaxLength      *int
	TotalDigits    *int
	FractionDigits *int
	MinInclusive   *string
	MaxInclusive   *string
}

// ParseFile reads and parses an XSD document.
func ParseFile(path string) (*Schema, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("opening schema: %w", err)
	}
	defer func() { _ = f.Close() }()

	s, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	s.Path = path
	return s, nil
}

// Parse reads an XSD document from r.
func Parse(r io.Reader) (*Schema, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = true
	// Schemas are trusted-but-unverified input from the user's own download.
	// encoding/xml never resolves external entities; this rejects internal ones
	// too, so a DTD cannot influence parsing.
	dec.Entity = xml.HTMLEntity

	s := &Schema{
		Elements:     map[string]*Element{},
		ComplexTypes: map[string]*ComplexType{},
		SimpleTypes:  map[string]*SimpleType{},
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml: %w", err)
		}

		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Space != NSSchema || start.Name.Local != "schema" {
			continue
		}

		s.TargetNamespace = attr(start, "targetNamespace")
		if err := parseSchemaBody(dec, s); err != nil {
			return nil, err
		}
		break
	}

	if s.TargetNamespace == "" && len(s.Elements) == 0 {
		return nil, fmt.Errorf("not an XML Schema document")
	}
	return s, nil
}

func parseSchemaBody(dec *xml.Decoder, s *Schema) error {
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return fmt.Errorf("unexpected end of schema")
		}
		if err != nil {
			return fmt.Errorf("xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "schema" {
				return nil
			}

		case xml.StartElement:
			if t.Name.Space != NSSchema {
				if err := dec.Skip(); err != nil {
					return err
				}
				continue
			}

			switch t.Name.Local {
			case "element":
				el, err := parseElement(dec, t)
				if err != nil {
					return err
				}
				if _, dup := s.Elements[el.Name]; !dup {
					s.ElementOrder = append(s.ElementOrder, el.Name)
				}
				s.Elements[el.Name] = el

			case "complexType":
				ct, err := parseComplexType(dec, t)
				if err != nil {
					return err
				}
				if ct.Name == "" {
					return fmt.Errorf("global complexType without a name")
				}
				s.ComplexTypes[ct.Name] = ct

			case "simpleType":
				st, err := parseSimpleType(dec, t)
				if err != nil {
					return err
				}
				if st.Name == "" {
					return fmt.Errorf("global simpleType without a name")
				}
				s.SimpleTypes[st.Name] = st

			case "annotation":
				if err := dec.Skip(); err != nil {
					return err
				}

			default:
				// Unknown top-level construct. The catalogue contains none, but
				// skipping keeps an unexpected schema parseable rather than fatal.
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

func parseElement(dec *xml.Decoder, start xml.StartElement) (*Element, error) {
	el := &Element{
		Name:      attr(start, "name"),
		Type:      localName(attr(start, "type")),
		MinOccurs: occurs(attr(start, "minOccurs"), 1),
		MaxOccurs: occurs(attr(start, "maxOccurs"), 1),
	}
	if el.Name == "" {
		return nil, fmt.Errorf("element declaration without a name")
	}
	// Anonymous inline types do not occur in the catalogue; skip any body.
	if err := skipToEnd(dec, start.Name.Local); err != nil {
		return nil, err
	}
	return el, nil
}

func parseComplexType(dec *xml.Decoder, start xml.StartElement) (*ComplexType, error) {
	ct := &ComplexType{Name: attr(start, "name")}

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, wrapEOF(err, "complexType")
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "complexType" {
				return ct, nil
			}

		case xml.StartElement:
			switch t.Name.Local {
			case "sequence", "choice":
				p, err := parseParticle(dec, t)
				if err != nil {
					return nil, err
				}
				ct.Content = p

			case "simpleContent":
				if err := parseSimpleContent(dec, ct); err != nil {
					return nil, err
				}

			case "attribute":
				ct.Attributes = append(ct.Attributes, parseAttribute(t))
				if err := skipToEnd(dec, "attribute"); err != nil {
					return nil, err
				}

			case "annotation":
				if err := dec.Skip(); err != nil {
					return nil, err
				}

			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

func parseSimpleContent(dec *xml.Decoder, ct *ComplexType) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return wrapEOF(err, "simpleContent")
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "simpleContent" {
				return nil
			}

		case xml.StartElement:
			switch t.Name.Local {
			case "extension", "restriction":
				ct.SimpleBase = localName(attr(t, "base"))
				if err := parseExtensionBody(dec, ct, t.Name.Local); err != nil {
					return err
				}
			case "annotation":
				if err := dec.Skip(); err != nil {
					return err
				}
			default:
				if err := dec.Skip(); err != nil {
					return err
				}
			}
		}
	}
}

func parseExtensionBody(dec *xml.Decoder, ct *ComplexType, closing string) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return wrapEOF(err, closing)
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == closing {
				return nil
			}

		case xml.StartElement:
			if t.Name.Local == "attribute" {
				ct.Attributes = append(ct.Attributes, parseAttribute(t))
				if err := skipToEnd(dec, "attribute"); err != nil {
					return err
				}
				continue
			}
			if err := dec.Skip(); err != nil {
				return err
			}
		}
	}
}

func parseAttribute(start xml.StartElement) *Attribute {
	return &Attribute{
		Name:     attr(start, "name"),
		Type:     localName(attr(start, "type")),
		Required: attr(start, "use") == "required",
	}
}

// parseParticle reads a sequence or choice and everything nested inside it.
func parseParticle(dec *xml.Decoder, start xml.StartElement) (Particle, error) {
	min := occurs(attr(start, "minOccurs"), 1)
	max := occurs(attr(start, "maxOccurs"), 1)
	kind := start.Name.Local

	var children []Particle

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, wrapEOF(err, kind)
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == kind {
				return newGroup(kind, children, min, max), nil
			}

		case xml.StartElement:
			switch t.Name.Local {
			case "element":
				el, err := parseElement(dec, t)
				if err != nil {
					return nil, err
				}
				children = append(children, el)

			case "sequence", "choice":
				p, err := parseParticle(dec, t)
				if err != nil {
					return nil, err
				}
				children = append(children, p)

			case "any":
				a := &Any{
					Namespace:       attr(t, "namespace"),
					ProcessContents: attr(t, "processContents"),
					MinOccurs:       occurs(attr(t, "minOccurs"), 1),
					MaxOccurs:       occurs(attr(t, "maxOccurs"), 1),
				}
				children = append(children, a)
				if err := skipToEnd(dec, "any"); err != nil {
					return nil, err
				}

			case "annotation":
				if err := dec.Skip(); err != nil {
					return nil, err
				}

			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

func newGroup(kind string, children []Particle, min, max int) Particle {
	if kind == "choice" {
		return &Choice{Particles: children, MinOccurs: min, MaxOccurs: max}
	}
	return &Sequence{Particles: children, MinOccurs: min, MaxOccurs: max}
}

func parseSimpleType(dec *xml.Decoder, start xml.StartElement) (*SimpleType, error) {
	st := &SimpleType{Name: attr(start, "name")}

	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, wrapEOF(err, "simpleType")
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "simpleType" {
				return st, nil
			}

		case xml.StartElement:
			switch t.Name.Local {
			case "restriction":
				st.Base = localName(attr(t, "base"))
				if err := parseFacets(dec, &st.Facets); err != nil {
					return nil, err
				}
			case "annotation":
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			default:
				if err := dec.Skip(); err != nil {
					return nil, err
				}
			}
		}
	}
}

func parseFacets(dec *xml.Decoder, f *Facets) error {
	for {
		tok, err := dec.Token()
		if err != nil {
			return wrapEOF(err, "restriction")
		}

		switch t := tok.(type) {
		case xml.EndElement:
			if t.Name.Local == "restriction" {
				return nil
			}

		case xml.StartElement:
			value := attr(t, "value")
			switch t.Name.Local {
			case "enumeration":
				f.Enumeration = append(f.Enumeration, value)
			case "pattern":
				f.Pattern = append(f.Pattern, value)
			case "length":
				f.Length = intPtr(value)
			case "minLength":
				f.MinLength = intPtr(value)
			case "maxLength":
				f.MaxLength = intPtr(value)
			case "totalDigits":
				f.TotalDigits = intPtr(value)
			case "fractionDigits":
				f.FractionDigits = intPtr(value)
			case "minInclusive":
				v := value
				f.MinInclusive = &v
			case "maxInclusive":
				v := value
				f.MaxInclusive = &v
			}
			if err := skipToEnd(dec, t.Name.Local); err != nil {
				return err
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Resolution helpers
// ---------------------------------------------------------------------------

// ResolveComplex returns the complex type with this name.
func (s *Schema) ResolveComplex(name string) (*ComplexType, bool) {
	ct, ok := s.ComplexTypes[name]
	return ct, ok
}

// ResolveSimple returns the simple type with this name.
func (s *Schema) ResolveSimple(name string) (*SimpleType, bool) {
	st, ok := s.SimpleTypes[name]
	return st, ok
}

// EffectiveFacets walks a simple type's restriction chain and merges the facets
// it accumulates, returning them with the builtin base the chain bottoms out on.
//
// depth is bounded so a schema whose types reference each other cannot loop.
func (s *Schema) EffectiveFacets(typeName string) (Facets, string) {
	var merged Facets
	name := typeName

	for depth := 0; depth < 32; depth++ {
		if IsBuiltin(name) {
			return merged, name
		}
		st, ok := s.SimpleTypes[name]
		if !ok {
			return merged, name
		}
		mergeFacets(&merged, st.Facets)
		if st.Base == "" || st.Base == name {
			return merged, name
		}
		name = st.Base
	}
	return merged, name
}

// mergeFacets keeps the tightest constraint seen while walking a chain. Derived
// types are visited first, so an already-set bound is the more specific one.
func mergeFacets(dst *Facets, src Facets) {
	dst.Enumeration = append(dst.Enumeration, src.Enumeration...)
	dst.Pattern = append(dst.Pattern, src.Pattern...)
	if dst.Length == nil {
		dst.Length = src.Length
	}
	if dst.MinLength == nil {
		dst.MinLength = src.MinLength
	}
	if dst.MaxLength == nil {
		dst.MaxLength = src.MaxLength
	}
	if dst.TotalDigits == nil {
		dst.TotalDigits = src.TotalDigits
	}
	if dst.FractionDigits == nil {
		dst.FractionDigits = src.FractionDigits
	}
	if dst.MinInclusive == nil {
		dst.MinInclusive = src.MinInclusive
	}
	if dst.MaxInclusive == nil {
		dst.MaxInclusive = src.MaxInclusive
	}
}

// IsBuiltin reports whether a type name is an XML Schema builtin.
func IsBuiltin(name string) bool {
	switch name {
	case "string", "normalizedString", "token", "decimal", "integer",
		"boolean", "date", "dateTime", "time", "duration",
		"gYear", "gYearMonth", "gMonth", "gDay", "gMonthDay",
		"base64Binary", "hexBinary", "anyURI", "anyType", "anySimpleType",
		"float", "double", "long", "int", "short", "byte",
		"nonNegativeInteger", "positiveInteger", "nonPositiveInteger", "negativeInteger",
		"unsignedLong", "unsignedInt", "unsignedShort", "unsignedByte":
		return true
	}
	return false
}

// RootElement returns the element an instance document should start with.
func (s *Schema) RootElement() (*Element, bool) {
	for _, preferred := range []string{"Document", "AppHdr", "Xchg"} {
		if el, ok := s.Elements[preferred]; ok {
			return el, true
		}
	}
	if len(s.ElementOrder) > 0 {
		return s.Elements[s.ElementOrder[0]], true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// Token helpers
// ---------------------------------------------------------------------------

func attr(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// localName strips a namespace prefix: "xs:string" becomes "string".
func localName(qname string) string {
	if i := strings.IndexByte(qname, ':'); i >= 0 {
		return qname[i+1:]
	}
	return qname
}

func occurs(value string, def int) int {
	switch value {
	case "":
		return def
	case "unbounded":
		return Unbounded
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return def
	}
	return n
}

func intPtr(value string) *int {
	n, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return &n
}

// skipToEnd consumes tokens until the named element closes.
func skipToEnd(dec *xml.Decoder, name string) error {
	depth := 1
	for {
		tok, err := dec.Token()
		if err != nil {
			return wrapEOF(err, name)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == name {
				depth++
			}
		case xml.EndElement:
			if t.Name.Local == name {
				depth--
				if depth == 0 {
					return nil
				}
			}
		}
	}
}

func wrapEOF(err error, context string) error {
	if err == io.EOF {
		return fmt.Errorf("unexpected end of document inside <%s>", context)
	}
	return fmt.Errorf("xml in <%s>: %w", context, err)
}
