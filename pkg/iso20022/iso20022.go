// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package iso20022 is AskISO's public API for working with ISO 20022 financial
// messages. The CLI, the WebAssembly build, and any Go service that imports this
// package all run exactly the same code, so a message linted in the browser gets
// the same verdict as one linted in a terminal or a CI pipeline.
//
// # Light mode and full mode
//
// Everything here works with no setup. AskISO embeds an index of the published
// standard -- every message identifier, the message set that publishes it, and
// the Registration Authority's download location -- so lookup, search,
// generation, linting, conversion, code lookup and MT/MX cross-reference need no
// files on disk. That is light mode, and it is what the browser build runs.
//
// Reading schema text needs a catalogue the user downloaded from
// https://www.iso20022.org/. AskISO redistributes no specification content.
// Open one with OpenCatalogue; the API reports Installed=false rather than
// failing when a schema is absent, and names the download that would supply it.
package iso20022

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/catalog"
	"github.com/sebastienrousseau/askiso/internal/cbprworkspace"
	"github.com/sebastienrousseau/askiso/internal/codes"
	"github.com/sebastienrousseau/askiso/internal/converter"
	"github.com/sebastienrousseau/askiso/internal/diff"
	"github.com/sebastienrousseau/askiso/internal/flow"
	"github.com/sebastienrousseau/askiso/internal/generator"
	"github.com/sebastienrousseau/askiso/internal/graph"
	"github.com/sebastienrousseau/askiso/internal/linter"
	"github.com/sebastienrousseau/askiso/internal/registry"
	"github.com/sebastienrousseau/askiso/internal/rules"
	"github.com/sebastienrousseau/askiso/internal/schemagen"
	"github.com/sebastienrousseau/askiso/internal/swift"
	"github.com/sebastienrousseau/askiso/internal/translator"
	"github.com/sebastienrousseau/askiso/internal/validator"
	"github.com/sebastienrousseau/askiso/internal/xsd"
)

// Re-exported types, so callers need not reach into internal packages.
type (
	// GeneratorOptions configures synthetic message generation.
	GeneratorOptions = generator.Options
	// LintResult is the outcome of a business-rule check.
	LintResult = linter.Result
	// LintIssue is a single rule violation.
	LintIssue = linter.Issue
	// Severity classifies a lint issue.
	Severity = linter.IssueSeverity
	// CodeItem is an ISO 20022 external code definition.
	CodeItem = codes.CodeItem
	// ExternalCodeSets is a locally imported Registration Authority publication.
	ExternalCodeSets = codes.ExternalSets
	// LifecycleChain is a linked four-stage payment flow.
	LifecycleChain = flow.LifecycleChain
	// Mapping is a SWIFT MT to ISO 20022 MX cross-reference.
	Mapping = translator.Mapping
	// CBPRPack is a locally compiled, content-minimised CBPR+ rule pack.
	CBPRPack = rules.CBPRPack
	// CBPRPackSource is disclosure-safe provenance for one local source file.
	CBPRPackSource = rules.CBPRPackSource
	// CBPRPackConstraint is one compiled local cardinality or lexical rule.
	CBPRPackConstraint = rules.CBPRPackConstraint
	// CBPRPackInfo is disclosure-safe provenance attached to a local pack result.
	CBPRPackInfo = rules.CBPRPackInfo
	// CBPRPackHit is one extractive result from a private local PDF.
	CBPRPackHit = rules.CBPRPackHit
	// CBPRWorkspaceOptions selects private local CBPR+ inputs and output.
	CBPRWorkspaceOptions = cbprworkspace.Options
	// CBPRWorkspaceManifest describes a private local CBPR+ workspace.
	CBPRWorkspaceManifest = cbprworkspace.Manifest
	// CBPRWorkspaceRuntime is the verified executable state of a workspace.
	CBPRWorkspaceRuntime = cbprworkspace.Runtime
	// CBPRVerification is a content-free local conformance-suite result.
	CBPRVerification = cbprworkspace.Verification
	// CBPRSampleExport reports locally exported synthetic positive fixtures.
	CBPRSampleExport = cbprworkspace.SampleExport
	// CBPRSampleExportOptions selects the local wrapper for exported fixtures.
	CBPRSampleExportOptions = cbprworkspace.SampleExportOptions
	// CBPRNegativeExport reports locally exported synthetic rejection fixtures.
	CBPRNegativeExport = cbprworkspace.NegativeExport
	// CBPRReviewChecklist inventories independently sourced evidence still needed.
	CBPRReviewChecklist = cbprworkspace.ReviewChecklist
	// CBPRSampleAudit is a content-free sample intake assessment.
	CBPRSampleAudit = cbprworkspace.SampleAudit
	// CBPRAnonymisationReport records local transformations by hash only.
	CBPRAnonymisationReport = cbprworkspace.AnonymisationReport
	// CBPRSampleAttestation pins a human review claim to sample hashes.
	CBPRSampleAttestation = cbprworkspace.SampleAttestation
	// CBPROverlay is an operator-authored conditional rule overlay.
	CBPROverlay = rules.CBPROverlay
	// CBPRReleaseDiff is a content-free comparison of two local release exports.
	CBPRReleaseDiff = cbprworkspace.ReleaseDiff
	// CBPRConformanceOptions selects strict local release-gate requirements.
	CBPRConformanceOptions = cbprworkspace.ConformanceOptions
	// CBPRConformanceReport is a content-free strict release-gate result.
	CBPRConformanceReport = cbprworkspace.ConformanceReport
	// CBPRExternalEvidence pins an independently obtained verdict.
	CBPRExternalEvidence = cbprworkspace.ExternalEvidence
	// CBPRSourceSearch is an in-memory multi-format local evidence result.
	CBPRSourceSearch = cbprworkspace.SearchResult
	// MessageSet is a message set as published by the Registration Authority.
	MessageSet = registry.Set
)

// Severity values.
const (
	SeverityError   = linter.SeverityError
	SeverityWarning = linter.SeverityWarning
	SeverityInfo    = linter.SeverityInfo
)

// MessageInfo describes one message definition.
//
// Installed reports whether the schema is available locally. When it is false,
// Sets names the message sets that publish it, each carrying a DownloadURL.
type MessageInfo struct {
	ID          string       `json:"id"`
	BaseCode    string       `json:"base_code"`
	Domain      string       `json:"domain"`
	DomainName  string       `json:"domain_name"`
	Installed   bool         `json:"installed"`
	Category    string       `json:"category,omitempty"`
	Version     string       `json:"version,omitempty"`
	SchemaPath  string       `json:"schema_path,omitempty"`
	SamplePath  string       `json:"sample_path,omitempty"`
	ReportPaths []string     `json:"report_paths,omitempty"`
	Sets        []MessageSet `json:"message_sets,omitempty"`
}

// domainNames maps a business area prefix to a human label.
var domainNames = map[string]string{
	"acmt": "Account Management",
	"admi": "Administration",
	"auth": "Regulatory Reporting",
	"caaa": "Card Payments - Acceptor to Acquirer",
	"caam": "ATM Management",
	"cain": "Acquirer to Issuer Card Messages",
	"camt": "Cash Management",
	"casp": "Card Retailer Protocol",
	"catm": "Card Terminal Management",
	"catp": "ATM Interface",
	"colr": "Collateral Management",
	"fxtr": "Foreign Exchange Trade",
	"head": "Business Application Header",
	"pacs": "Payments Clearing and Settlement",
	"pain": "Payments Initiation",
	"reda": "Reference Data",
	"remt": "Remittance Advice",
	"secl": "Securities Clearing",
	"seev": "Securities Events",
	"semt": "Securities Management",
	"sese": "Securities Settlement",
	"setr": "Securities Trade",
	"tsmt": "Trade Services Management",
	"trck": "Payment Tracking",
}

// DomainName returns a human label for a business area prefix such as "pacs".
func DomainName(domain string) string {
	if n, ok := domainNames[strings.ToLower(domain)]; ok {
		return n
	}
	return strings.ToUpper(domain)
}

// Catalogue is an optional set of schemas the user downloaded from the
// Registration Authority. The zero value is valid and means light mode.
type Catalogue struct {
	idx *catalog.Index
}

// OpenCatalogue loads a catalogue. Pass an empty path to search the conventional
// locations. A nil Catalogue is valid everywhere and behaves as light mode.
func OpenCatalogue(path string) (*Catalogue, error) {
	idx, err := catalog.LoadResolved(path)
	if err != nil {
		return nil, err
	}
	return &Catalogue{idx: idx}, nil
}

// Root reports where the catalogue was loaded from.
func (c *Catalogue) Root() string {
	if c == nil || c.idx == nil {
		return ""
	}
	return c.idx.RootDir
}

// Installed reports how many message definitions are available locally.
func (c *Catalogue) Installed() int {
	if c == nil || c.idx == nil {
		return 0
	}
	return len(c.idx.Messages)
}

// CatalogueLocations lists the directories searched for a catalogue, in order.
func CatalogueLocations() []string { return catalog.DefaultDirs() }

// ErrNoCatalogue means no catalogue could be located.
var ErrNoCatalogue = catalog.ErrNotFound

// Standard reports the whole published standard: every message identifier and
// the sets that publish them. This is embedded and always available.
func Standard() (*registry.Registry, error) { return registry.Load() }

// Lookup describes one message. It never fails for a real identifier, whether or
// not a catalogue is installed; the Installed field reports which.
//
// c may be nil.
func (c *Catalogue) Lookup(id string) (MessageInfo, error) {
	q := strings.ToLower(strings.TrimSpace(id))

	if c != nil && c.idx != nil {
		if m, ok := c.idx.MessageMap[q]; ok {
			return fromCatalogue(m), nil
		}
	}

	reg, err := registry.Load()
	if err != nil {
		return MessageInfo{}, err
	}
	m, ok := reg.Lookup(q)
	if !ok {
		return MessageInfo{}, fmt.Errorf("no ISO 20022 message matches %q", id)
	}
	return MessageInfo{
		ID:         m.ID,
		BaseCode:   m.BaseCode,
		Domain:     m.Domain,
		DomainName: DomainName(m.Domain),
		Installed:  false,
		Sets:       reg.SetsFor(m.ID),
	}, nil
}

// Search ranks messages by relevance. With a catalogue it searches what is
// installed; without one it searches the whole published standard.
//
// c may be nil.
func (c *Catalogue) Search(query string) ([]MessageInfo, error) {
	if c != nil && c.idx != nil {
		hits := c.idx.Search(query)
		if len(hits) > 0 {
			out := make([]MessageInfo, len(hits))
			for i, m := range hits {
				out[i] = fromCatalogue(m)
			}
			return out, nil
		}
		// An installed catalogue must not narrow what search can find. The
		// catalogue index knows only identifiers and folder names; the embedded
		// registry also knows the published message sets, so a keyword the
		// installed files cannot answer still gets an answer.
	}

	reg, err := registry.Load()
	if err != nil {
		return nil, err
	}
	hits := reg.Search(query)
	out := make([]MessageInfo, len(hits))
	for i, m := range hits {
		out[i] = MessageInfo{
			ID:         m.ID,
			BaseCode:   m.BaseCode,
			Domain:     m.Domain,
			DomainName: DomainName(m.Domain),
			Installed:  false,
			Sets:       reg.SetsFor(m.ID),
		}
	}
	return out, nil
}

// ErrSchemaNotInstalled means a real message has no locally available schema.
var ErrSchemaNotInstalled = errors.New("schema not installed")

// SchemaPath returns the local XSD path for a message.
//
// When the schema is absent it returns ErrSchemaNotInstalled wrapped with the
// message sets that publish it, so callers can tell the user what to download.
//
// c may be nil.
func (c *Catalogue) SchemaPath(id string) (string, error) {
	info, err := c.Lookup(id)
	if err != nil {
		return "", err
	}
	if info.Installed && info.SchemaPath != "" {
		return info.SchemaPath, nil
	}
	return "", &SchemaMissingError{ID: info.ID, Sets: info.Sets}
}

// SchemaMissingError names the downloads that would supply a missing schema.
type SchemaMissingError struct {
	ID   string
	Sets []MessageSet
}

// Error names the missing schema and, when the registry knows one, the
// message set to download and where the Registration Authority hosts it.
func (e *SchemaMissingError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: schema not installed", e.ID)
	if len(e.Sets) > 0 {
		fmt.Fprintf(&b, "; download %s from %s", e.Sets[0].String(), e.Sets[0].DownloadURL())
	}
	return b.String()
}

// Unwrap returns ErrSchemaNotInstalled, so errors.Is matches every
// missing-schema error whatever message it names.
func (e *SchemaMissingError) Unwrap() error { return ErrSchemaNotInstalled }

// DomainCounts reports how many message definitions each business area holds.
// With a catalogue it counts what is installed; otherwise the whole standard.
//
// c may be nil.
func (c *Catalogue) DomainCounts() (map[string]int, error) {
	if c != nil && c.idx != nil {
		counts := map[string]int{}
		for _, m := range c.idx.Messages {
			counts[strings.ToLower(m.BaseCode[:4])]++
		}
		return counts, nil
	}
	reg, err := registry.Load()
	if err != nil {
		return nil, err
	}
	return reg.Domains(), nil
}

// MessageSets lists every message set the Registration Authority publishes,
// sorted by name. Always available.
func MessageSets() ([]MessageSet, error) {
	reg, err := registry.Load()
	if err != nil {
		return nil, err
	}
	out := append([]MessageSet(nil), reg.Sets...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func fromCatalogue(m catalog.Message) MessageInfo {
	info := MessageInfo{
		ID:          m.ID,
		BaseCode:    m.BaseCode,
		Domain:      strings.ToLower(m.BaseCode[:4]),
		DomainName:  DomainName(m.BaseCode[:4]),
		Installed:   true,
		Category:    m.Category,
		Version:     m.Version,
		SchemaPath:  m.XSDPath,
		SamplePath:  m.XMLSamplePath,
		ReportPaths: m.MDRPaths,
	}

	// Naming the publishing message set is useful whether or not the schema is
	// installed: it says where the specification came from, and it is what a
	// colleague needs in order to obtain the same files.
	if reg, err := registry.Load(); err == nil {
		info.Sets = reg.SetsFor(m.ID)
	}
	return info
}

// ---------------------------------------------------------------------------
// Catalogue-free operations. These are the whole of light mode, and the whole of
// what the browser build exposes.
// ---------------------------------------------------------------------------

// Lint checks an ISO 20022 XML document against business rules: IBAN mod-97,
// BIC structure, ISO 4217 currency precision, UETR format, and temporal sanity.
func Lint(xmlData []byte, filename string) (*LintResult, error) {
	return linter.Lint(xmlData, filename)
}

// Generate produces a synthetic ISO 20022 message.
func Generate(opts GeneratorOptions) (string, error) { return generator.Generate(opts) }

// DefaultGeneratorOptions returns sensible defaults for a message type.
func DefaultGeneratorOptions(msgType string) GeneratorOptions {
	return generator.DefaultOptions(msgType)
}

// GenerateLifecycle builds a linked pain.001 -> pacs.008 -> pacs.002 -> camt.053
// transaction chain sharing one UETR and end-to-end identifier.
func GenerateLifecycle(opts GeneratorOptions) (*LifecycleChain, error) {
	return flow.GenerateLifecycle(opts)
}

// XMLToJSON converts an ISO 20022 XML document to JSON.
func XMLToJSON(xmlData []byte) ([]byte, error) { return converter.XMLToJSON(xmlData) }

// JSONToXML converts JSON back to ISO 20022 XML.
//
// Element order is not currently preserved; ISO 20022 complex types are
// xs:sequence, so round-tripped output may not validate. See the project's
// known limitations.
func JSONToXML(jsonData []byte) ([]byte, error) { return converter.JSONToXML(jsonData) }

// LookupCode searches the external code sets by code, name, or description.
func LookupCode(query string) []CodeItem { return codes.Lookup(query) }

// AllCodes returns every external code AskISO knows.
func AllCodes() []CodeItem { return codes.GetAllCodes() }

// ExternalCode is one code from the Registration Authority's external code set
// publication.
type ExternalCode = codes.ExternalCode

// ReadExternalCodeSets parses a local Registration Authority XLSX or JSON
// publication without storing or uploading it.
func ReadExternalCodeSets(path string) (*ExternalCodeSets, error) {
	return codes.ImportExternalSets(path)
}

// ExternalCodes returns the external code sets the user imported into this
// catalogue, or nil when they have imported none.
//
// Most ISO 20022 code sets are enumerated in the schemas. The rest are
// maintained separately on a quarterly cycle and referenced by name only.
// AskISO redistributes that publication no more than it redistributes the
// schemas: ImportExternalCodes reads the file the user downloaded.
func (c *Catalogue) ExternalCodes() []ExternalCode {
	if c == nil || c.idx == nil {
		return nil
	}
	sets := codes.ExternalSetsFor(c.idx.RootDir)
	if sets == nil {
		return nil
	}
	return sets.Codes
}

// LookupExternalCode finds a code in the imported publication.
func (c *Catalogue) LookupExternalCode(code string) []ExternalCode {
	if c == nil || c.idx == nil {
		return nil
	}
	return codes.ExternalSetsFor(c.idx.RootDir).Lookup(code)
}

// ImportExternalCodes reads a Registration Authority external code set
// publication and stores it beside this catalogue. Both the spreadsheet and the
// JSON form are accepted.
func (c *Catalogue) ImportExternalCodes(path string) (int, error) {
	if c == nil || c.idx == nil {
		return 0, fmt.Errorf("the external code sets are stored beside a catalogue, and none is open")
	}

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		return 0, err
	}
	if _, err := codes.SaveExternalSets(c.idx.RootDir, sets); err != nil {
		return 0, err
	}
	codes.ForgetExternalSets(c.idx.RootDir)
	return sets.Total(), nil
}

// TranslateSWIFT cross-references a SWIFT MT or ISO 20022 MX identifier.
func TranslateSWIFT(code string) (Mapping, bool) { return translator.Lookup(code) }

// AllMappings returns the complete MT/MX cross-reference matrix.
func AllMappings() []Mapping { return translator.GetAllMappings() }

// Conversion is the outcome of translating a SWIFT MT message: the generated
// ISO 20022 document, plus a report of what happened to every source field.
type Conversion = swift.Conversion

// FieldReport records how one MT field fared in a conversion.
type FieldReport = swift.FieldReport

// MTMessage is a parsed SWIFT MT message.
type MTMessage = swift.Message

// ParseMT reads a SWIFT MT message without converting it.
func ParseMT(data []byte) (*MTMessage, error) { return swift.Parse(data) }

// TranslateMT converts a SWIFT MT message to its ISO 20022 equivalent.
//
// MT103 becomes pacs.008, MT202 becomes pacs.009, and MT940 becomes camt.053.
// The conversion is lossy in both directions and the losses are the point: the
// returned report names every field that was carried, shortened, inferred, or
// dropped, so nothing disappears without being accounted for.
//
// MT parties carry unstructured addresses, which CBPR+ stops accepting once
// the deferred structured-address requirement takes effect. Converted messages will fail
// CheckProfile with the cbpr-2026 profile until the addresses are enriched.
func TranslateMT(data []byte) (*Conversion, error) {
	m, err := swift.Parse(data)
	if err != nil {
		return nil, err
	}
	return swift.Convert(m)
}

// TranslatableMT lists the MT message types TranslateMT can convert.
func TranslatableMT() []string { return swift.Supported() }

// TranslateMX converts an ISO 20022 message to its SWIFT MT equivalent.
//
// This is the direction people need during coexistence, and the one that loses
// what matters: a structured address flattens into free text, a purpose code
// and a legal entity identifier have nowhere to go, and a 35-character
// reference is cut to the 16 an MT field allows. Every one of those appears in
// the report.
func TranslateMX(document []byte) (*Conversion, error) { return swift.ConvertMX(document) }

// TranslatableMX lists the ISO 20022 messages TranslateMX can convert.
func TranslatableMX() []string { return swift.SupportedMX() }

// ValidateIBAN checks an IBAN against the ISO 13616 mod-97 algorithm.
func ValidateIBAN(iban string) (bool, string) { return linter.ValidateIBAN(iban) }

// ValidateBIC checks a Business Identifier Code against ISO 9362.
func ValidateBIC(bic string) (bool, string) { return linter.ValidateBIC(bic) }

// ValidateUETR checks a unique end-to-end transaction reference (RFC 4122 v4).
func ValidateUETR(uetr string) (bool, string) { return linter.ValidateUETR(uetr) }

// SequenceDiagram renders a payment flow as a diagram.
// format is "mermaid" or "ascii".
func SequenceDiagram(msgType, preset, format string) string {
	if strings.EqualFold(format, "ascii") {
		return graph.GenerateASCII(msgType, preset)
	}
	return graph.GenerateMermaid(msgType, preset)
}

// ---------------------------------------------------------------------------
// Schema validation
// ---------------------------------------------------------------------------

// SchemaError is one XML Schema violation.
type SchemaError = validator.Error

// SchemaResult is the outcome of validating a document against its schema.
type SchemaResult = validator.Result

// StreamThreshold is the size above which ValidateStream is worth reaching for.
// Below it the document costs less to hold than the machinery to avoid holding
// it; above it a statement can be larger than the memory available.
const StreamThreshold = 8 << 20 // 8 MiB

// ValidateStream checks an instance read from r against the schema at
// schemaPath, without holding the document in memory.
//
// The verdict is identical to ValidateFile's -- that equivalence is asserted
// against every sample message in an installed catalogue -- but the cost is
// roughly 120 bytes per transaction rather than the whole document. A camt.053
// covering a corporate's month can be validated on a machine that could not
// hold it.
func ValidateStream(r io.Reader, schemaPath string) (*SchemaResult, error) {
	return ValidateStreamWithExternalCodes(r, schemaPath, nil)
}

// ValidateStreamWithExternalCodes validates a stream and enforces locally
// supplied external code values in addition to the XSD.
func ValidateStreamWithExternalCodes(r io.Reader, schemaPath string, external *ExternalCodeSets) (*SchemaResult, error) {
	schema, err := xsd.ParseFile(schemaPath)
	if err != nil {
		return nil, err
	}
	return validator.ValidateReaderWithExternalSets(r, schema, external), nil
}

// ValidateFile checks an instance against the schema at schemaPath.
func ValidateFile(instance []byte, schemaPath string) (*SchemaResult, error) {
	return ValidateFileWithExternalCodes(instance, schemaPath, nil)
}

// ValidateFileWithExternalCodes validates buffered XML and enforces locally
// supplied external code values in addition to the XSD.
func ValidateFileWithExternalCodes(instance []byte, schemaPath string, external *ExternalCodeSets) (*SchemaResult, error) {
	schema, err := xsd.ParseFile(schemaPath)
	if err != nil {
		return nil, err
	}
	return validator.ValidateWithExternalSets(instance, schema, external), nil
}

// ValidateAgainst checks an instance against a schema supplied as bytes. This is
// what the browser build uses, where the user pastes both.
func ValidateAgainst(instance, schemaDoc []byte) (*SchemaResult, error) {
	schema, err := xsd.Parse(bytes.NewReader(schemaDoc))
	if err != nil {
		return nil, err
	}
	return validator.Validate(instance, schema), nil
}

// Validate checks an instance against the schema for its own namespace, taken
// from the catalogue. c may be nil, in which case the error names the download.
func (c *Catalogue) Validate(instance []byte) (*SchemaResult, error) {
	msgID, err := MessageIDFromInstance(instance)
	if err != nil {
		return nil, err
	}
	path, err := c.SchemaPath(msgID)
	if err != nil {
		return nil, err
	}
	return ValidateFileWithExternalCodes(instance, path, codes.ExternalSetsFor(c.idx.RootDir))
}

// SchemaGenOptions configures a message generated from its schema.
type SchemaGenOptions = schemagen.Options

// SchemaGenResult is a generated message and the decisions behind it.
type SchemaGenResult = schemagen.Result

// DefaultSchemaGenOptions is a minimal message: every mandatory element and
// nothing else.
func DefaultSchemaGenOptions() SchemaGenOptions { return schemagen.DefaultOptions() }

// HasTemplate reports whether a message type has a hand-written template.
// Everything else is generated from its schema, which needs one installed.
func HasTemplate(msgType string) bool { return generator.HasTemplate(msgType) }

// TemplateTypes lists the message types Generate can build without a catalogue.
func TemplateTypes() []string { return generator.TemplateTypes() }

// GenerateFromSchema builds a valid instance of any installed message by
// walking its schema.
//
// Where Generate covers four message types from templates, this covers all
// 2,845: every mandatory element in the order the content model declares, the
// first branch of every choice, and values that satisfy each type's own facets.
// Generated messages validate against their schema and lint clean, which is
// asserted across the whole catalogue rather than claimed.
func (c *Catalogue) GenerateFromSchema(msgID string, opts SchemaGenOptions) (*SchemaGenResult, error) {
	path, err := c.SchemaPath(msgID)
	if err != nil {
		return nil, err
	}
	schema, err := xsd.ParseFile(path)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", msgID, err)
	}
	return schemagen.Generate(schema, opts)
}

// SchemaDiff is a structural comparison of two schemas.
type SchemaDiff = diff.Report

// SchemaChange is one difference found by Diff.
type SchemaChange = diff.Change

// Diff compares two installed schemas and classifies every structural
// difference as breaking or compatible.
//
// The direction matters: from is the version a message was built against, to is
// the one it must now satisfy. A change is breaking when a message that
// satisfied the old schema can be rejected by the new one, or when a receiver
// loses a field it used to get.
//
// Both schemas must be installed; Diff returns a *SchemaMissingError naming the
// download when one is not.
func (c *Catalogue) Diff(fromID, toID string) (*SchemaDiff, error) {
	fromPath, err := c.SchemaPath(fromID)
	if err != nil {
		return nil, err
	}
	toPath, err := c.SchemaPath(toID)
	if err != nil {
		return nil, err
	}

	fromSchema, err := xsd.ParseFile(fromPath)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", fromID, err)
	}
	toSchema, err := xsd.ParseFile(toPath)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", toID, err)
	}
	return diff.Compare(fromSchema, toSchema, fromID, toID), nil
}

// isoNamespace matches the ISO 20022 namespace form, which carries the message
// identifier: urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10
var isoNamespace = regexp.MustCompile(`^urn:iso:std:iso:20022:tech:xsd:([a-z]{4}\.\d{3}\.\d{3}\.\d{2})$`)

// MessageIDFromInstance reads the message identifier from a document's
// namespace declaration.
func MessageIDFromInstance(instance []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(instance))
	var firstISO string
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("could not determine the message type: malformed XML: %w", err)
		}
		switch token := tok.(type) {
		case xml.StartElement:
			depth++
			match := isoNamespace.FindStringSubmatch(token.Name.Space)
			if match == nil {
				continue
			}
			id := match[1]
			if firstISO == "" {
				firstISO = id
			}
			// An application header and Document can be siblings in an envelope.
			// Prefer the business Document rather than accidentally validating the
			// envelope as head.001.
			if token.Name.Local == "Document" || depth == 1 {
				return id, nil
			}
		case xml.EndElement:
			depth--
		}
	}
	if firstISO != "" {
		return firstISO, nil
	}
	return "", errors.New("could not determine the message type: no ISO 20022 namespace declaration found")
}

// ---------------------------------------------------------------------------
// Scheme rule profiles
// ---------------------------------------------------------------------------

// RuleFinding is one scheme-level violation.
type RuleFinding = rules.Finding

// RuleResult is the outcome of applying a profile.
type RuleResult = rules.Result

// RuleProfiles lists the available rule profiles.
func RuleProfiles() []string { return rules.Names() }

// DescribeProfile returns a profile's description.
func DescribeProfile(name string) string { return rules.Describe(name) }

// CheckProfile applies a named scheme rule profile to a message.
//
// Scheme rules sit above schema validity: a message can be perfectly valid XML
// and still be rejected by a clearing system. The message type is read from the
// document's own namespace so exemptions apply correctly.
func CheckProfile(instance []byte, profile, filename string) (*RuleResult, error) {
	p, err := rules.Get(profile)
	if err != nil {
		return nil, err
	}
	root, err := converter.Parse(instance)
	if err != nil {
		return nil, err
	}
	msgID, _ := MessageIDFromInstance(instance)
	return rules.Run(p, root, msgID, filename), nil
}

// CompileCBPRPack reads an authorised local PDF or directory and compiles its
// supported restrictions in memory. It performs no network access and does not
// persist or embed the source documents.
func CompileCBPRPack(source string) (*CBPRPack, error) {
	return rules.CompileCBPRPack(source)
}

// WriteCBPRPack writes a user-requested reusable local pack with owner-only
// permissions. Applications should not redistribute it without the necessary
// rights to the source standards material.
func WriteCBPRPack(path string, pack *CBPRPack) error {
	return rules.WriteCBPRPack(path, pack)
}

// CheckCBPRPack applies the embedded live CBPR+ profile plus a user-supplied
// local pack. Source may be a PDF directory or a compiled .cbpr-pack.json file.
func CheckCBPRPack(instance []byte, source, filename string) (*RuleResult, error) {
	pack, err := rules.CompileCBPRPack(source)
	if err != nil {
		return nil, err
	}
	base, err := rules.Get("cbpr-plus")
	if err != nil {
		return nil, err
	}
	profile, err := pack.Augment(base)
	if err != nil {
		return nil, err
	}
	root, err := converter.Parse(instance)
	if err != nil {
		return nil, err
	}
	msgID, _ := MessageIDFromInstance(instance)
	return rules.Run(profile, root, msgID, filename), nil
}

// SearchCBPRPack performs extractive full-text retrieval against private local
// PDFs. It never calls AskISO's OpenAI, Ollama, or other model-provider paths.
func SearchCBPRPack(source, query string, limit int) ([]CBPRPackHit, error) {
	return rules.SearchCBPRPack(source, query, limit)
}

// SearchCBPRSources searches local PDF, JSON, XML/XSD, and XLSX files without
// persisting extracts or invoking a model or network provider.
func SearchCBPRSources(source, query string, limit int) (*CBPRSourceSearch, error) {
	return cbprworkspace.SearchLocalSources(source, query, limit)
}

// ImportCBPRWorkspace indexes user-entitled artefacts and writes only
// content-minimised derived files into a private local workspace.
func ImportCBPRWorkspace(options CBPRWorkspaceOptions) (*CBPRWorkspaceManifest, error) {
	return cbprworkspace.Import(options)
}

// LoadCBPRWorkspace verifies and loads a private workspace manifest.
func LoadCBPRWorkspace(path string) (*CBPRWorkspaceManifest, error) {
	return cbprworkspace.LoadManifest(path)
}

// LoadCBPRWorkspaceRuntime verifies the manifest and loads its private compiled
// rules and external-code index without accessing the original source folder.
func LoadCBPRWorkspaceRuntime(path string) (*CBPRWorkspaceRuntime, error) {
	return cbprworkspace.LoadRuntime(path)
}

// VerifyCBPRWorkspace runs the workspace's pinned local sample suite.
func VerifyCBPRWorkspace(source, workspace string) (*CBPRVerification, error) {
	return cbprworkspace.Verify(source, workspace)
}

// ExportCBPRValidSamples writes one AskISO-generated positive BAH-plus-payload
// fixture per executable Usage Guideline into the private source tree.
func ExportCBPRValidSamples(source, workspace, output string) (*CBPRSampleExport, error) {
	return cbprworkspace.ExportValidSamples(source, workspace, output)
}

// ExportCBPRValidSamplesWithOptions exports fixtures using an envelope,
// FINplus request-payload, or DataPDU transport template.
func ExportCBPRValidSamplesWithOptions(source, workspace, output string, options CBPRSampleExportOptions) (*CBPRSampleExport, error) {
	return cbprworkspace.ExportValidSamplesWithOptions(source, workspace, output, options)
}

// ExportCBPRInvalidSamples derives multiple validated rejection scenarios from
// AskISO-generated positive fixtures. They remain synthetic evidence.
func ExportCBPRInvalidSamples(source, workspace, output string) (*CBPRNegativeExport, error) {
	return cbprworkspace.ExportNegativeSamples(source, workspace, output)
}

// WriteCBPRReviewChecklist writes the 31-positive/31-negative review inventory.
func WriteCBPRReviewChecklist(workspace, output, createdAt string) (*CBPRReviewChecklist, error) {
	return cbprworkspace.WriteReviewChecklist(workspace, output, createdAt)
}

// AuditCBPRSamples checks provenance, duplicates, pairing and common live-data patterns.
func AuditCBPRSamples(source, workspace string) (*CBPRSampleAudit, error) {
	return cbprworkspace.AuditSamples(source, workspace)
}

// AnonymiseCBPRSamples creates clearly labelled local copies with common IBAN
// and long-account-number shapes replaced.
func AnonymiseCBPRSamples(source, workspace, output string) (*CBPRAnonymisationReport, error) {
	return cbprworkspace.AnonymiseSamples(source, workspace, output)
}

// WriteCBPRSampleAttestation records an explicit human independent-review
// assertion. AskISO cannot establish that assertion itself.
func WriteCBPRSampleAttestation(source, workspace, output, reviewer, provider, reviewedAt string, acknowledge bool) (*CBPRSampleAttestation, error) {
	return cbprworkspace.WriteSampleAttestation(source, workspace, output, reviewer, provider, reviewedAt, acknowledge)
}

// WriteCBPRExternalEvidence records a content-free externally obtained verdict.
func WriteCBPRExternalEvidence(workspace, output, provider, testedAt string, cases int, passed, acknowledge bool) (*CBPRExternalEvidence, error) {
	return cbprworkspace.WriteExternalEvidence(workspace, output, provider, testedAt, cases, passed, acknowledge)
}

// CompileCBPROverlay validates an explicitly authored non-XSD rule overlay.
func CompileCBPROverlay(path string) (*CBPRPack, error) { return rules.CompileCBPROverlay(path) }

// CompareCBPRReleaseSources compares two local entitled exports by hashes and identifiers.
func CompareCBPRReleaseSources(fromSource, toSource, fromRelease, toRelease string) (*CBPRReleaseDiff, error) {
	return cbprworkspace.CompareReleaseSources(fromSource, toSource, fromRelease, toRelease)
}

// CheckCBPRConformance runs the strict local workspace, sample, code-release,
// permission, entitlement, and optional external-evidence gate.
func CheckCBPRConformance(options CBPRConformanceOptions) (*CBPRConformanceReport, error) {
	return cbprworkspace.CheckConformance(options)
}

// SARIF renders rule results as a SARIF 2.1.0 log.
//
// SARIF is what a code-scanning pipeline reads, so this is the shape a rule
// finding takes when it leaves AskISO for GitHub, GitLab or a build server.
// The browser build calls it too, through the same code, so a report downloaded
// from the website matches `askiso lint --format sarif` on the same message --
// byte for byte, apart from the artifact URI, which names whatever file the
// caller passed.
func SARIF(results ...*RuleResult) (string, error) {
	var buf bytes.Buffer
	if err := rules.WriteSARIF(&buf, results...); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// ClassifyAddresses reports the shape of every postal address in a message,
// which is the question the deferred CBPR+ structured-address change turns on.
func ClassifyAddresses(instance []byte) (map[string]string, error) {
	root, err := converter.Parse(instance)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, addr := range rules.FindAll(root, "PstlAdr") {
		out[addr.Path] = string(rules.Classify(addr.Node))
	}
	return out, nil
}
