// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package cbprworkspace builds a private, content-minimised index over CBPR+
// artefacts held by the user. It never copies source files and has no network
// client: every operation is an explicit local filesystem operation.
package cbprworkspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sebastienrousseau/askiso/internal/atomicfile"
	"github.com/sebastienrousseau/askiso/internal/codes"
	"github.com/sebastienrousseau/askiso/internal/rules"
	"github.com/sebastienrousseau/askiso/internal/schemagen"
	"github.com/sebastienrousseau/askiso/internal/validator"
	"github.com/sebastienrousseau/askiso/internal/xsd"
)

const (
	ManifestFormat = "askiso-cbpr-workspace/v1"
	SuiteFormat    = "askiso-cbpr-conformance-suite/v1"
	ManifestFile   = "manifest.json"
	SuiteFile      = "conformance-suite.json"
	GeneratedDir   = "generated-samples"
	maxFiles       = 4096
	maxSourceSize  = 128 << 20
)

var (
	messageNamespaceRE = regexp.MustCompile(`^urn:iso:std:iso:20022:tech:xsd:([a-z]{4}\.[0-9]{3}\.[0-9]{3}\.[0-9]{2})$`)
	usageIdentifierRE  = regexp.MustCompile(`(?i)swift\.cbprplus(?:\.[a-z0-9]+)+`)
)

// workspaceStateMu makes the individually atomic generated files one logical
// snapshot for callers in this process. The manifest is published last, while
// readers hold a shared lock across every file it pins.
var workspaceStateMu sync.RWMutex

var (
	discoverSource      = discover
	compilePackSource   = compilePack
	compilePDFPack      = rules.CompileCBPRPack
	writeCompiledPack   = rules.WriteCBPRPack
	importExternalCodes = codes.ImportExternalSets
	saveExternalCodes   = codes.SaveExternalSets
	writeWorkspaceJSON  = writeJSON
	protectWorkspace    = os.Chmod
	generateFromSchema  = schemagen.Generate
)

// Options selects local inputs and a private output directory.
type Options struct {
	Source        string
	Workspace     string
	Release       string
	ExternalCodes string
	// ExternalCodesAsOf selects the newest publication not later than this
	// YYYY-MM-DD date when ExternalCodes names a directory.
	ExternalCodesAsOf string
	// RuleOverlay is an optional operator-authored conditional-rule JSON file.
	RuleOverlay string
	// GenerateSamples derives positive and negative validator evidence from
	// each local XSD. Generated messages stay in the private workspace.
	GenerateSamples bool
	// EntitlementAcknowledged records the operator's assertion that they are
	// authorised to process the selected private artefacts. AskISO cannot
	// determine licence rights itself.
	EntitlementAcknowledged bool
}

// SourceFile records provenance without storing content or an absolute path.
type SourceFile struct {
	Path             string   `json:"path"`
	Kind             string   `json:"kind"`
	SHA256           string   `json:"sha256"`
	Size             int64    `json:"size"`
	MessageID        string   `json:"message_id,omitempty"`
	UsageIdentifiers []string `json:"usage_identifiers,omitempty"`
}

// ExternalPublication describes the locally imported external code release.
type ExternalPublication struct {
	File        string   `json:"file"`
	Format      string   `json:"format"`
	Publication string   `json:"publication,omitempty"`
	SHA256      string   `json:"sha256"`
	Sets        int      `json:"sets"`
	Codes       int      `json:"codes"`
	Warnings    []string `json:"warnings,omitempty"`
}

// Coverage states what local material was actually found. Expected and missing
// Usage Guidelines are identifiers only, never Swift-owned rule content.
type Coverage struct {
	ExpectedUsageGuidelines          int      `json:"expected_usage_guidelines"`
	PresentUsageGuidelines           int      `json:"present_usage_guidelines"`
	MissingUsageGuidelines           []string `json:"missing_usage_guidelines,omitempty"`
	ExecutableUsageGuidelines        int      `json:"executable_usage_guidelines,omitempty"`
	MissingExecutableUsageGuidelines []string `json:"missing_executable_usage_guidelines,omitempty"`
	Messages                         int      `json:"messages"`
	Constraints                      int      `json:"constraints"`
	Schemas                          int      `json:"schemas"`
	JSONSchemas                      int      `json:"json_schemas"`
	Samples                          int      `json:"samples"`
	AskISOGeneratedSamples           int      `json:"askiso_generated_samples,omitempty"`
	GeneratedSamples                 int      `json:"generated_samples,omitempty"`
	UsageGuidelineXML                int      `json:"usage_guideline_xml"`
	Spreadsheets                     int      `json:"spreadsheets"`
	PDFs                             int      `json:"pdfs"`
}

// Manifest is the reproducible, disclosure-safe description of a workspace.
type Manifest struct {
	Format          string               `json:"format"`
	Release         string               `json:"release"`
	Fingerprint     string               `json:"fingerprint"`
	Pack            string               `json:"pack,omitempty"`
	PackFingerprint string               `json:"pack_fingerprint,omitempty"`
	Sources         []SourceFile         `json:"sources"`
	ExternalCodes   *ExternalPublication `json:"external_codes,omitempty"`
	// ExternalCodeHistory records every local publication considered. Only the
	// selected ExternalCodes publication is compiled into the runtime index.
	ExternalCodeHistory []ExternalPublication `json:"external_code_history,omitempty"`
	Coverage            Coverage              `json:"coverage"`
	SuiteCases          int                   `json:"suite_cases"`
	SuiteFingerprint    string                `json:"suite_fingerprint"`
	Warnings            []string              `json:"warnings,omitempty"`
	// LocalOnly states that AskISO used no network service and copied no source
	// artefact. It does not describe OS-managed filesystem synchronisation.
	LocalOnly               bool `json:"local_only,omitempty"`
	EntitlementAcknowledged bool `json:"entitlement_acknowledged,omitempty"`
	dataRoot                string
}

// Suite is a versioned list of user-held samples and their expected schema
// result. Files containing .invalid., -invalid or _invalid expect rejection;
// all other samples expect acceptance.
type Suite struct {
	Format      string      `json:"format"`
	Release     string      `json:"release"`
	Fingerprint string      `json:"fingerprint"`
	Cases       []SuiteCase `json:"cases"`
}

// SuiteCase references source files by relative path and pins their hashes.
type SuiteCase struct {
	ID              string `json:"id"`
	MessageID       string `json:"message_id"`
	BusinessService string `json:"business_service,omitempty"`
	Sample          string `json:"sample"`
	SampleSHA256    string `json:"sample_sha256"`
	Schema          string `json:"schema"`
	SchemaSHA256    string `json:"schema_sha256"`
	Expected        string `json:"expected"`
	// Origin is user-provided, generated, askiso-generated, or askiso-anonymised. Empty retains
	// compatibility with v1 workspaces created before provenance was recorded.
	Origin   string `json:"origin,omitempty"`
	Mutation string `json:"mutation,omitempty"`
	Scenario string `json:"scenario,omitempty"`
}

// CaseResult is one content-free verification result.
type CaseResult struct {
	ID             string `json:"id"`
	Expected       string `json:"expected"`
	Actual         string `json:"actual"`
	Passed         bool   `json:"passed"`
	Errors         int    `json:"errors"`
	EnvelopeErrors int    `json:"envelope_errors,omitempty"`
}

// Verification is the suite summary returned to automation.
type Verification struct {
	Release string       `json:"release"`
	Cases   int          `json:"cases"`
	Passed  int          `json:"passed"`
	Failed  int          `json:"failed"`
	Results []CaseResult `json:"results"`

	// workspaceFingerprint pins this result to the immutable generation that
	// was actually verified without changing the public JSON contract.
	workspaceFingerprint string
}

func ensureWorkspaceSnapshot(fingerprint string, manifest *Manifest) error {
	// Empty is reserved for dependency-injected test doubles. Every result
	// returned by Verify carries a real generation fingerprint.
	if fingerprint == "" {
		return nil
	}
	if manifest == nil || manifest.Fingerprint != fingerprint {
		return errors.New("workspace generation changed during operation; retry")
	}
	return nil
}

// Runtime is the verified executable state loaded from a private workspace.
// It contains only the content-minimised pack and derived external-code index;
// source document bodies and their absolute location are never required.
type Runtime struct {
	Manifest      *Manifest
	Pack          *rules.CBPRPack
	ExternalCodes *codes.ExternalSets
}

type discovered struct {
	SourceFile
	abs string
}

// Import creates or refreshes a private workspace without copying any source
// artefact. Generated files contain hashes, identifiers and executable rules.
func Import(opt Options) (*Manifest, error) {
	opt.Release = strings.ToUpper(strings.TrimSpace(opt.Release))
	if opt.Release == "" {
		opt.Release = "SR2025"
	}
	if opt.Release != "SR2025" {
		return nil, fmt.Errorf("unsupported CBPR+ release %q (available: SR2025)", opt.Release)
	}
	source, workspace, err := validateRoots(opt.Source, opt.Workspace)
	if err != nil {
		return nil, err
	}
	files, warnings, err := discoverSource(source)
	if err != nil {
		return nil, err
	}
	workspaceStateMu.Lock()
	defer workspaceStateMu.Unlock()
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return nil, fmt.Errorf("creating private CBPR+ workspace: %w", err)
	}
	if err := protectWorkspace(workspace, 0o700); err != nil {
		return nil, fmt.Errorf("protecting private CBPR+ workspace: %w", err)
	}
	stage, err := beginGeneration(workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(stage) }()

	manifest := &Manifest{
		Format: ManifestFormat, Release: opt.Release, Warnings: warnings,
		LocalOnly: true, EntitlementAcknowledged: opt.EntitlementAcknowledged,
	}
	for _, file := range files {
		manifest.Sources = append(manifest.Sources, file.SourceFile)
		switch file.Kind {
		case "usage-guideline-pdf":
			manifest.Coverage.PDFs++
		case "schema":
			manifest.Coverage.Schemas++
		case "usage-guideline-json-schema":
			manifest.Coverage.JSONSchemas++
		case "sample":
			if strings.Contains(strings.ToLower(filepath.Base(file.Path)), "askiso-generated") {
				manifest.Coverage.AskISOGeneratedSamples++
			} else {
				manifest.Coverage.Samples++
			}
		case "usage-guideline-xml":
			manifest.Coverage.UsageGuidelineXML++
		case "spreadsheet":
			manifest.Coverage.Spreadsheets++
		}
	}

	pack, packErr := compilePackSource(source, files)
	if packErr != nil {
		return nil, packErr
	}
	if strings.TrimSpace(opt.RuleOverlay) != "" {
		overlay, err := rules.CompileCBPROverlay(opt.RuleOverlay)
		if err != nil {
			return nil, fmt.Errorf("compiling CBPR+ rule overlay: %w", err)
		}
		pack, err = rules.MergeCBPRPacks(pack, overlay)
		if err != nil {
			return nil, err
		}
	}
	if pack != nil {
		packName := strings.ToLower(opt.Release) + ".cbpr-pack.json"
		if err := writeCompiledPack(filepath.Join(stage, packName), pack); err != nil {
			return nil, err
		}
		manifest.Pack = packName
		manifest.PackFingerprint = pack.Fingerprint
		manifest.Coverage.Constraints = len(pack.Constraints)
		manifest.Warnings = append(manifest.Warnings, pack.Warnings...)
	}
	applyGuidelineCoverage(manifest, pack, files)
	if manifest.Coverage.JSONSchemas > 0 {
		manifest.Warnings = append(manifest.Warnings,
			"MyStandards JSON Schemas are indexed for provenance and Usage Guideline completeness; they describe JSON payloads and are not used as XML Schemas")
	}

	externalPath, history, selectedSets, err := selectExternalCodePublication(opt.ExternalCodes, opt.ExternalCodesAsOf, files)
	if err != nil {
		return nil, err
	}
	manifest.ExternalCodeHistory = history
	var externalSets *codes.ExternalSets
	if externalPath != "" {
		sets := selectedSets
		if sets == nil {
			sets, err = importExternalCodes(externalPath)
			if err != nil {
				return nil, fmt.Errorf("importing external code sets: %w", err)
			}
		}
		if _, err := saveExternalCodes(stage, sets); err != nil {
			return nil, err
		}
		manifest.ExternalCodes = &ExternalPublication{
			File: filepath.Base(externalPath), Format: sets.Format,
			Publication: sets.Publication, SHA256: sets.SHA256,
			Sets: len(sets.SetNames()), Codes: sets.Total(),
			Warnings: append([]string{}, sets.Warnings...),
		}
		manifest.Warnings = append(manifest.Warnings, sets.Warnings...)
		externalSets = sets
	}

	suite, suiteWarnings := buildSuite(opt.Release, files)
	manifest.Warnings = append(manifest.Warnings, suiteWarnings...)
	if opt.GenerateSamples {
		generated, generatedWarnings := generateSuiteCases(stage, files, externalSets)
		suite.Cases = append(suite.Cases, generated...)
		manifest.Coverage.GeneratedSamples = len(generated)
		manifest.Warnings = append(manifest.Warnings, generatedWarnings...)
		if len(generated) > 0 {
			manifest.Warnings = without(manifest.Warnings,
				"no local XML sample could be paired with a matching XSD; the conformance suite is empty")
		}
		if manifest.Coverage.Schemas == 0 {
			manifest.Warnings = append(manifest.Warnings,
				"sample generation requested but no entitled XML/XSD Usage Guideline export was found")
		}
	}
	sort.Slice(suite.Cases, func(i, j int) bool { return suite.Cases[i].ID < suite.Cases[j].ID })
	manifest.SuiteCases = len(suite.Cases)
	suite.Fingerprint = suiteFingerprint(suite)
	manifest.SuiteFingerprint = suite.Fingerprint
	if err := writeWorkspaceJSON(filepath.Join(stage, SuiteFile), suite); err != nil {
		return nil, err
	}
	sort.Strings(manifest.Warnings)
	manifest.Warnings = unique(manifest.Warnings)
	manifest.Fingerprint = manifestFingerprint(manifest)
	if err := writeWorkspaceJSON(filepath.Join(stage, ManifestFile), manifest); err != nil {
		return nil, err
	}
	if err := publishGeneration(workspace, stage, manifest, suite); err != nil {
		return nil, err
	}
	return manifest, nil
}

func selectExternalCodePublication(explicit, asOf string, files []discovered) (string, []ExternalPublication, *codes.ExternalSets, error) {
	path, err := selectExternalCodes(explicit, files)
	if err == nil && path == "" {
		return "", nil, nil, nil
	}
	if err != nil && strings.TrimSpace(explicit) == "" {
		return "", nil, nil, err
	}
	if strings.TrimSpace(explicit) == "" {
		return path, nil, nil, nil
	}
	info, statErr := os.Stat(filepath.Clean(explicit))
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return filepath.Clean(explicit), nil, nil, nil
		}
		return "", nil, nil, statErr
	}
	if !info.IsDir() {
		if strings.TrimSpace(asOf) != "" {
			if _, err := time.Parse("2006-01-02", asOf); err != nil {
				return "", nil, nil, fmt.Errorf("external codes as-of must be YYYY-MM-DD: %w", err)
			}
		}
		return filepath.Clean(explicit), nil, nil, nil
	}
	var paths []string
	err = filepath.WalkDir(filepath.Clean(explicit), func(candidate string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil
		}
		lower := strings.ToLower(entry.Name())
		if strings.Contains(lower, "externalcode") && (strings.HasSuffix(lower, ".json") || strings.HasSuffix(lower, ".xlsx")) {
			paths = append(paths, candidate)
		}
		return nil
	})
	if err != nil {
		return "", nil, nil, err
	}
	if len(paths) == 0 {
		return "", nil, nil, errors.New("external-code directory contains no supported JSON or XLSX publications")
	}
	var cutoff time.Time
	if strings.TrimSpace(asOf) != "" {
		cutoff, err = time.Parse("2006-01-02", asOf)
		if err != nil {
			return "", nil, nil, fmt.Errorf("external codes as-of must be YYYY-MM-DD: %w", err)
		}
	}
	type candidate struct {
		path    string
		sets    *codes.ExternalSets
		year    int
		quarter int
	}
	var candidates []candidate
	var history []ExternalPublication
	for _, candidatePath := range paths {
		sets, importErr := importExternalCodes(candidatePath)
		if importErr != nil {
			return "", nil, nil, fmt.Errorf("importing external code sets %s: %w", filepath.Base(candidatePath), importErr)
		}
		publication := ExternalPublication{File: filepath.Base(candidatePath), Format: sets.Format, Publication: sets.Publication,
			SHA256: sets.SHA256, Sets: len(sets.SetNames()), Codes: sets.Total(), Warnings: append([]string{}, sets.Warnings...)}
		history = append(history, publication)
		match := publicationQuarterRE.FindStringSubmatch(sets.Publication)
		if match == nil {
			if len(paths) == 1 && cutoff.IsZero() {
				return candidatePath, history, sets, nil
			}
			continue
		}
		quarter, _ := strconv.Atoi(match[1])
		year, _ := strconv.Atoi(match[2])
		publicationStart := time.Date(year, time.Month((quarter-1)*3+1), 1, 0, 0, 0, 0, time.UTC)
		if cutoff.IsZero() || !publicationStart.After(cutoff) {
			candidates = append(candidates, candidate{path: candidatePath, sets: sets, year: year, quarter: quarter})
		}
	}
	sort.Slice(history, func(i, j int) bool { return history[i].File < history[j].File })
	if len(candidates) == 0 {
		return "", history, nil, errors.New("no recognised external-code publication is effective by the requested as-of date")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].year != candidates[j].year {
			return candidates[i].year < candidates[j].year
		}
		return candidates[i].quarter < candidates[j].quarter
	})
	selected := candidates[len(candidates)-1]
	return selected.path, history, selected.sets, nil
}

func validateRoots(source, workspace string) (string, string, error) {
	if strings.TrimSpace(source) == "" || strings.TrimSpace(workspace) == "" {
		return "", "", errors.New("source and workspace directories are required")
	}
	source, err := filepath.Abs(filepath.Clean(source))
	if err != nil {
		return "", "", fmt.Errorf("resolving CBPR+ source: %w", err)
	}
	workspace, err = filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return "", "", fmt.Errorf("resolving CBPR+ workspace: %w", err)
	}
	info, err := os.Lstat(source)
	if err != nil {
		return "", "", fmt.Errorf("reading CBPR+ source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", "", fmt.Errorf("CBPR+ source must be a real directory: %s", source)
	}
	if within(source, workspace) {
		return "", "", errors.New("workspace must be outside the source directory so generated files cannot be mistaken for entitled inputs")
	}
	return source, workspace, nil
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func discover(root string) ([]discovered, []string, error) {
	var files []discovered
	var warnings []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			// WalkDir never follows a symlink, including one that targets a
			// directory, so skipping the entry is sufficient.
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		kind := sourceKind(path)
		if kind == "" {
			return nil
		}
		if len(files) >= maxFiles {
			return fmt.Errorf("source contains more than %d supported files", maxFiles)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSourceSize {
			return fmt.Errorf("%s is %d bytes; per-file safety limit is %d", entry.Name(), info.Size(), maxSourceSize)
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		messageID := ""
		var usageIdentifiers []string
		switch kind {
		case "schema":
			schema, parseErr := xsd.ParseFile(path)
			if parseErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s could not be indexed as XSD: %v", filepath.ToSlash(rel), parseErr))
			} else {
				messageID = messageFromNamespace(schema.TargetNamespace)
				usageIdentifiers = usageIdentifiersFromSchema(path, rel, messageID)
				if messageID != "" && len(usageIdentifiers) == 0 && isExpectedCBPRMessage(messageID) {
					warnings = append(warnings, fmt.Sprintf("%s is executable XML Schema but has no unambiguous SR2025 Business Service marker; it is not counted as exact Usage Guideline coverage", filepath.ToSlash(rel)))
				}
			}
		case "sample":
			// Some MyStandards machine-readable exports use .xml for a
			// constrained schema or a Usage Guideline model. Recognise those
			// without pretending they are executable sample messages.
			if schema, schemaErr := xsd.ParseFile(path); schemaErr == nil {
				kind = "schema"
				messageID = messageFromNamespace(schema.TargetNamespace)
				usageIdentifiers = usageIdentifiersFromSchema(path, rel, messageID)
				if messageID != "" && len(usageIdentifiers) == 0 && isExpectedCBPRMessage(messageID) {
					warnings = append(warnings, fmt.Sprintf("%s is executable XML Schema but has no unambiguous SR2025 Business Service marker; it is not counted as exact Usage Guideline coverage", filepath.ToSlash(rel)))
				}
			} else if messageID, usageIdentifiers, err = messageMetadataFromXML(path); err != nil {
				kind = "usage-guideline-xml"
				messageID = ""
				usageIdentifiers = nil
			} else if len(usageIdentifiers) == 0 {
				usageIdentifiers = sampleUsageIdentifiers(messageID, rel)
			}
		case "json":
			metadata, recognised, inspectErr := inspectMyStandardsJSON(path)
			if inspectErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s could not be indexed as MyStandards JSON Schema: %v", filepath.ToSlash(rel), inspectErr))
				return nil
			}
			if !recognised {
				return nil
			}
			kind = "usage-guideline-json-schema"
			messageID = metadata.MessageID
			usageIdentifiers = metadata.UsageIdentifiers
		}
		files = append(files, discovered{SourceFile: SourceFile{
			Path: filepath.ToSlash(rel), Kind: kind, SHA256: digest,
			Size: info.Size(), MessageID: messageID, UsageIdentifiers: usageIdentifiers,
		}, abs: path})
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scanning CBPR+ source: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, warnings, nil
}

func sourceKind(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".cbpr-pack.json"):
		return "compiled-pack"
	case strings.HasSuffix(lower, ".pdf"):
		return "usage-guideline-pdf"
	case strings.HasSuffix(lower, ".xsd"):
		return "schema"
	case strings.HasSuffix(lower, ".xml"):
		return "sample"
	case strings.HasSuffix(lower, ".xlsx") || strings.HasSuffix(lower, ".xls"):
		return "spreadsheet"
	case strings.HasSuffix(lower, ".json") && strings.Contains(filepath.Base(lower), "externalcode"):
		return "external-code-publication"
	case strings.HasSuffix(lower, ".json"):
		return "json"
	default:
		return ""
	}
}

// usageIdentifiersFromSchema records exact executable message/service
// coverage only when the service is explicit in the export (content or path),
// or the SR2025 message has exactly one possible service. This avoids calling
// a base ISO XSD a CBPR+ variant merely because its namespace matches.
func usageIdentifiersFromSchema(path, relative, messageID string) []string {
	if messageID == "" {
		return nil
	}
	var allowed []string
	for _, guideline := range rules.CBPRSR2025UsageGuidelines() {
		if guideline.MessageID == messageID {
			allowed = append(allowed, guideline.UsageIdentifier)
		}
	}
	data, err := readBounded(path)
	if err != nil {
		return nil
	}
	haystack := strings.ToLower(relative + "\n" + string(data))
	allowedSet := map[string]bool{}
	for _, service := range allowed {
		allowedSet[service] = true
	}
	found := map[string]bool{}
	explicit := usageIdentifierRE.FindAllString(haystack, -1)
	for _, match := range explicit {
		service := strings.ToLower(match)
		if allowedSet[service] {
			found[service] = true
		}
	}
	// MyStandards export folders often label specialised variants with their
	// human abbreviation rather than the complete Business Service.
	variantFound := false
	for _, marker := range variantMarkers(relative) {
		variantFound = true
		for _, service := range allowed {
			if strings.Contains(service, "."+marker+".") {
				found[service] = true
			}
		}
	}
	// A MyStandards file labelled CBPRPlus but carrying no variant marker is
	// the core Usage Guideline. Requiring that label keeps an ordinary base ISO
	// XSD from being inferred solely from its message namespace.
	if len(found) == 0 && len(explicit) == 0 && !variantFound && strings.Contains(haystack, "cbprplus") {
		for _, service := range allowed {
			if len(strings.Split(service, ".")) == 3 {
				found[service] = true
				break
			}
		}
	}
	var out []string
	for service := range found {
		out = append(out, service)
	}
	sort.Strings(out)
	return out
}

func isExpectedCBPRMessage(messageID string) bool {
	for _, guideline := range rules.CBPRSR2025UsageGuidelines() {
		if guideline.MessageID == messageID {
			return true
		}
	}
	return false
}

func compilePack(source string, files []discovered) (*rules.CBPRPack, error) {
	hasPDF := false
	var compiled []string
	for _, file := range files {
		switch file.Kind {
		case "usage-guideline-pdf":
			hasPDF = true
		case "compiled-pack":
			compiled = append(compiled, file.abs)
		}
	}
	if hasPDF {
		return compilePDFPack(source)
	}
	if len(compiled) > 1 {
		return nil, errors.New("source contains multiple compiled CBPR+ packs; keep one or import the PDF source")
	}
	if len(compiled) == 1 {
		return rules.LoadCBPRPack(compiled[0])
	}
	return nil, nil
}

func applyGuidelineCoverage(manifest *Manifest, pack *rules.CBPRPack, files []discovered) {
	expected := rules.CBPRSR2025UsageGuidelines()
	manifest.Coverage.ExpectedUsageGuidelines = len(expected)
	present := map[string]bool{}
	executable := map[string]bool{}
	messages := map[string]bool{}
	if pack != nil {
		for _, source := range pack.Sources {
			if source.Constraints == 0 || source.MessageID == "" {
				continue
			}
			messages[source.MessageID] = true
			for _, service := range source.UsageIdentifiers {
				present[source.MessageID+"|"+service] = true
			}
		}
	}
	for _, file := range files {
		if file.MessageID == "" {
			continue
		}
		if file.Kind == "usage-guideline-json-schema" {
			messages[file.MessageID] = true
			for _, service := range file.UsageIdentifiers {
				present[file.MessageID+"|"+service] = true
			}
		}
		if file.Kind == "schema" {
			messages[file.MessageID] = true
			for _, service := range file.UsageIdentifiers {
				key := file.MessageID + "|" + service
				present[key] = true
				executable[key] = true
			}
		}
	}
	for _, guideline := range expected {
		key := guideline.MessageID + "|" + guideline.UsageIdentifier
		if !present[key] {
			manifest.Coverage.MissingUsageGuidelines = append(manifest.Coverage.MissingUsageGuidelines, key)
		}
		if !executable[key] {
			manifest.Coverage.MissingExecutableUsageGuidelines = append(manifest.Coverage.MissingExecutableUsageGuidelines, key)
		}
	}
	manifest.Coverage.PresentUsageGuidelines = len(expected) - len(manifest.Coverage.MissingUsageGuidelines)
	manifest.Coverage.ExecutableUsageGuidelines = len(expected) - len(manifest.Coverage.MissingExecutableUsageGuidelines)
	manifest.Coverage.Messages = len(messages)
}

func selectExternalCodes(explicit string, files []discovered) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return filepath.Clean(explicit), nil
	}
	var candidates []string
	for _, file := range files {
		if file.Kind == "external-code-publication" ||
			(file.Kind == "spreadsheet" && strings.Contains(strings.ToLower(filepath.Base(file.Path)), "externalcode")) {
			candidates = append(candidates, file.abs)
		}
	}
	if len(candidates) > 1 {
		return "", errors.New("multiple external code publications found; select one with --external-codes")
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	return "", nil
}

func buildSuite(release string, files []discovered) (*Suite, []string) {
	suite := &Suite{Format: SuiteFormat, Release: release}
	var warnings []string
	schemas := map[string][]discovered{}
	for _, file := range files {
		if file.Kind == "schema" && file.MessageID != "" {
			schemas[file.MessageID] = append(schemas[file.MessageID], file)
		}
	}
	for _, file := range files {
		if file.Kind != "sample" || file.MessageID == "" {
			continue
		}
		candidates := matchingSchemas(schemas[file.MessageID], file.UsageIdentifiers)
		if len(candidates) == 0 {
			warnings = append(warnings, fmt.Sprintf("sample %s has no matching local schema variant", file.Path))
			continue
		}
		if len(candidates) > 1 {
			warnings = append(warnings, fmt.Sprintf("sample %s matches %d schema variants; add a Business Service or variant marker to select one", file.Path, len(candidates)))
			continue
		}
		schema := candidates[0]
		businessService := ""
		if len(file.UsageIdentifiers) == 1 {
			businessService = file.UsageIdentifiers[0]
		}
		expected := "valid"
		lower := strings.ToLower(filepath.Base(file.Path))
		if strings.Contains(lower, ".invalid.") || strings.Contains(lower, "-invalid") || strings.Contains(lower, "_invalid") {
			expected = "invalid"
		}
		scenario := scenarioFromName(file.Path, expected)
		origin := "user-provided"
		if strings.Contains(lower, "askiso-generated") {
			origin = "askiso-generated"
		} else if strings.Contains(lower, "askiso-anonymised") {
			origin = "askiso-anonymised"
		}
		suite.Cases = append(suite.Cases, SuiteCase{
			ID:        file.MessageID + "/" + strings.TrimSuffix(file.Path, filepath.Ext(file.Path)),
			MessageID: file.MessageID, BusinessService: businessService,
			Sample: file.Path, SampleSHA256: file.SHA256,
			Schema: schema.Path, SchemaSHA256: schema.SHA256, Expected: expected,
			Origin: origin, Scenario: scenario,
		})
	}
	sort.Slice(suite.Cases, func(i, j int) bool { return suite.Cases[i].ID < suite.Cases[j].ID })
	if len(suite.Cases) == 0 {
		warnings = append(warnings, "no local XML sample could be paired with a matching XSD; the conformance suite is empty")
	} else {
		warnings = append(warnings, "sample expectations use the local .invalid./-invalid/_invalid filename convention and are not Swift Readiness Portal verdicts")
	}
	return suite, warnings
}

func matchingSchemas(candidates []discovered, services []string) []discovered {
	if len(services) == 0 || len(candidates) < 2 {
		return candidates
	}
	wanted := map[string]bool{}
	for _, service := range services {
		wanted[service] = true
	}
	var matched []discovered
	for _, schema := range candidates {
		for _, service := range schema.UsageIdentifiers {
			if wanted[service] {
				matched = append(matched, schema)
				break
			}
		}
	}
	return matched
}

func sampleUsageIdentifiers(messageID, relative string) []string {
	for _, match := range usageIdentifierRE.FindAllString(strings.ToLower(relative), -1) {
		for _, guideline := range rules.CBPRSR2025UsageGuidelines() {
			if guideline.MessageID == messageID && guideline.UsageIdentifier == match {
				return []string{match}
			}
		}
	}
	for _, marker := range variantMarkers(relative) {
		return rules.CBPRUsageIdentifiers(messageID, marker)
	}
	return nil
}

// variantMarkers recognises both Business Service abbreviations and the names
// used by MyStandards XML Schema Package folders.
func variantMarkers(relative string) []string {
	lower := strings.ToLower(relative)
	var markers []string
	for _, variant := range []struct {
		marker  string
		aliases []string
	}{
		{"stp", nil}, {"cov", nil}, {"adv", nil},
		{"mlp", []string{"multiplecharges", "multiple-charges", "multiple_charges"}},
		{"col", []string{"margin collection", "margin-collection", "margin_collection"}},
	} {
		found := regexp.MustCompile(`(^|[^a-z0-9])` + variant.marker + `([^a-z0-9]|$)`).MatchString(lower)
		for _, alias := range variant.aliases {
			found = found || strings.Contains(lower, alias)
		}
		if found {
			markers = append(markers, variant.marker)
		}
	}
	return markers
}

func scenarioFromName(path, expected string) string {
	if expected == "valid" {
		return "valid"
	}
	lower := strings.ToLower(filepath.Base(path))
	for _, candidate := range []struct {
		marker   string
		scenario string
	}{
		{"external", "external-code"}, {"bizsvc", "business-service"},
		{"business-service", "business-service"}, {"bah", "bah-payload"},
		{"header", "bah-payload"}, {"mandatory", "missing-mandatory"},
		{"missing", "missing-mandatory"}, {"forbidden", "forbidden-element"},
		{"removed", "forbidden-element"}, {"cardinality", "cardinality"},
		{"repeat", "cardinality"}, {"pattern", "lexical"},
		{"length", "lexical"}, {"restricted-code", "restricted-code"},
		{"variant", "variant"},
	} {
		if strings.Contains(lower, candidate.marker) {
			return candidate.scenario
		}
	}
	return "invalid-unspecified"
}

// generateSuiteCases creates deterministic validator evidence from the user's
// XSDs. It never copies a schema and it does not present these derived messages
// as Swift-authored examples.
func generateSuiteCases(workspace string, files []discovered, external *codes.ExternalSets) ([]SuiteCase, []string) {
	var cases []SuiteCase
	var warnings []string
	for _, file := range files {
		if file.Kind != "schema" || file.MessageID == "" || strings.HasPrefix(file.MessageID, "head.") {
			continue
		}
		schema, err := xsd.ParseFile(file.abs)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not generate samples for %s: %v", file.Path, err))
			continue
		}
		generated, err := generateFromSchema(schema, schemagen.Options{
			Repeats: 1, MaxDepth: 30, ExternalCodes: external,
		})
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not generate samples for %s: %v", file.Path, err))
			continue
		}
		if verdict := validator.ValidateWithExternalSets([]byte(generated.XML), schema, external); !verdict.Valid {
			detail := "unknown validation error"
			if len(verdict.Errors) > 0 {
				detail = verdict.Errors[0].String()
			}
			warnings = append(warnings, fmt.Sprintf("generated positive sample for %s did not validate (%d error(s)); it was not added: %s", file.Path, len(verdict.Errors), detail))
			continue
		}

		stem := generatedSampleStem(file)
		validRel := filepath.ToSlash(filepath.Join(GeneratedDir, stem+".valid.xml"))
		invalidRel := filepath.ToSlash(filepath.Join(GeneratedDir, stem+".invalid.xml"))
		invalidXML := fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<%s xmlns=\"urn:askiso:invalid\"/>\n", generated.Root)
		if validator.ValidateWithExternalSets([]byte(invalidXML), schema, external).Valid {
			warnings = append(warnings, fmt.Sprintf("generated negative sample for %s was unexpectedly accepted; it was not added", file.Path))
			continue
		}
		validHash, err := writeGeneratedSample(workspace, validRel, []byte(generated.XML+"\n"))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not save generated positive sample for %s: %v", file.Path, err))
			continue
		}
		invalidHash, err := writeGeneratedSample(workspace, invalidRel, []byte(invalidXML))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("could not save generated negative sample for %s: %v", file.Path, err))
			continue
		}
		base := file.MessageID + "/generated/" + stem
		businessService := ""
		if len(file.UsageIdentifiers) == 1 {
			businessService = file.UsageIdentifiers[0]
		}
		cases = append(cases,
			SuiteCase{ID: base + "/valid", MessageID: file.MessageID, BusinessService: businessService, Sample: validRel, SampleSHA256: validHash,
				Schema: file.Path, SchemaSHA256: file.SHA256, Expected: "valid", Origin: "generated", Scenario: "schema-minimal-valid"},
			SuiteCase{ID: base + "/wrong-namespace", MessageID: file.MessageID, BusinessService: businessService, Sample: invalidRel, SampleSHA256: invalidHash,
				Schema: file.Path, SchemaSHA256: file.SHA256, Expected: "invalid", Origin: "generated", Mutation: "wrong-namespace", Scenario: "wrong-namespace"},
		)
	}
	if len(cases) > 0 {
		warnings = append(warnings, "generated samples are AskISO validator self-tests derived from local XSDs; they are not Swift-authored examples or Readiness Portal certification")
	}
	return cases, warnings
}

func generatedSampleStem(file discovered) string {
	digest := sha256.Sum256([]byte(file.Path))
	return strings.ReplaceAll(file.MessageID, ".", "-") + "-" + hex.EncodeToString(digest[:8])
}

func writeGeneratedSample(workspace, relative string, data []byte) (string, error) {
	dir := filepath.Join(workspace, GeneratedDir)
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("generated-samples path is not a real directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	} else if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(workspace, filepath.FromSlash(relative))
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("refusing to overwrite a symlinked generated sample")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// LoadManifest loads and verifies the shape and fingerprint of a workspace.
func LoadManifest(workspace string) (*Manifest, error) {
	workspaceStateMu.RLock()
	defer workspaceStateMu.RUnlock()
	return loadManifest(workspace)
}

func loadManifest(workspace string) (*Manifest, error) {
	root, err := activeWorkspaceRoot(workspace)
	if err != nil {
		return nil, err
	}
	return loadManifestAt(root)
}

func loadManifestAt(root string) (*Manifest, error) {
	manifestPath, err := safeWorkspaceFile(root, ManifestFile)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := readJSON(manifestPath, &manifest); err != nil {
		return nil, err
	}
	if manifest.Format != ManifestFormat {
		return nil, fmt.Errorf("unsupported CBPR+ workspace format %q", manifest.Format)
	}
	want := manifestFingerprint(&manifest)
	if manifest.Fingerprint != want {
		return nil, fmt.Errorf("CBPR+ workspace fingerprint mismatch: got %s, calculated %s", manifest.Fingerprint, want)
	}
	manifest.dataRoot = root
	return &manifest, nil
}

func manifestDataRoot(manifest *Manifest, workspace string) string {
	if manifest != nil && manifest.dataRoot != "" {
		return manifest.dataRoot
	}
	// Dependency-injection tests and legacy callers may construct a Manifest
	// directly. Those values have no pinned generation and retain v1 behavior.
	return filepath.Clean(workspace)
}

// LoadRuntime verifies and loads the executable files pinned by a workspace
// manifest. It rejects missing, substituted, or internally inconsistent pack
// and external-code data before a lint or batch run can use them.
func LoadRuntime(workspace string) (*Runtime, error) {
	workspaceStateMu.RLock()
	defer workspaceStateMu.RUnlock()
	root, err := activeWorkspaceRoot(workspace)
	if err != nil {
		return nil, err
	}
	manifest, err := loadManifestAt(root)
	if err != nil {
		return nil, err
	}
	return loadRuntimeAt(root, manifest)
}

func loadRuntimeAt(root string, manifest *Manifest) (*Runtime, error) {
	runtime := &Runtime{Manifest: manifest}
	if manifest.Pack != "" {
		packPath, err := safeWorkspaceFile(root, manifest.Pack)
		if err != nil {
			return nil, err
		}
		pack, err := rules.LoadCBPRPack(packPath)
		if err != nil {
			return nil, fmt.Errorf("loading workspace pack: %w", err)
		}
		if pack.Fingerprint != manifest.PackFingerprint || len(pack.Constraints) != manifest.Coverage.Constraints {
			return nil, errors.New("workspace pack does not match its manifest")
		}
		runtime.Pack = pack
	} else if manifest.PackFingerprint != "" || manifest.Coverage.Constraints != 0 {
		return nil, errors.New("workspace manifest records constraints without a pack")
	}
	if manifest.ExternalCodes != nil {
		externalPath := codes.ExternalCodesPath(root)
		storeInfo, err := os.Lstat(filepath.Dir(externalPath))
		if err != nil || storeInfo.Mode()&os.ModeSymlink != 0 || !storeInfo.IsDir() {
			return nil, errors.New("workspace external-code directory is missing or unsafe")
		}
		externalInfo, err := os.Lstat(externalPath)
		if err != nil || externalInfo.Mode()&os.ModeSymlink != 0 || !externalInfo.Mode().IsRegular() {
			return nil, errors.New("workspace external-code index is missing or unsafe")
		}
		external, err := codes.LoadExternalSets(root)
		if err != nil {
			return nil, err
		}
		if external == nil {
			return nil, errors.New("workspace external-code index is missing or empty")
		}
		if external.SHA256 != manifest.ExternalCodes.SHA256 || external.Total() != manifest.ExternalCodes.Codes ||
			len(external.SetNames()) != manifest.ExternalCodes.Sets {
			return nil, errors.New("workspace external-code index does not match its manifest")
		}
		runtime.ExternalCodes = external
	}
	return runtime, nil
}

func realWorkspaceRoot(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("private CBPR+ workspace is required")
	}
	root, err := filepath.Abs(filepath.Clean(workspace))
	if err != nil {
		return "", fmt.Errorf("resolving private CBPR+ workspace: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("reading private CBPR+ workspace: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("private CBPR+ workspace must be a real directory: %s", root)
	}
	return root, nil
}

func safeWorkspaceFile(root, relative string) (string, error) {
	if relative == "" || filepath.Base(relative) != relative {
		return "", fmt.Errorf("unsafe workspace filename %q", relative)
	}
	path := filepath.Join(root, relative)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("reading workspace file %s: %w", filepath.Base(path), err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("workspace file must be a regular non-symlink: %s", filepath.Base(path))
	}
	return path, nil
}

// Verify checks every pinned local sample without uploading it or persisting
// message content in the report.
func Verify(source, workspace string) (*Verification, error) {
	workspaceStateMu.RLock()
	defer workspaceStateMu.RUnlock()
	workspaceRoot, err := activeWorkspaceRoot(workspace)
	if err != nil {
		return nil, err
	}
	manifest, err := loadManifestAt(workspaceRoot)
	if err != nil {
		return nil, err
	}
	var suite Suite
	if err := readJSON(filepath.Join(workspaceRoot, SuiteFile), &suite); err != nil {
		return nil, err
	}
	if suite.Format != SuiteFormat || suite.Release != manifest.Release {
		return nil, errors.New("conformance suite does not match the workspace release")
	}
	if suite.Fingerprint != suiteFingerprint(&suite) || suite.Fingerprint != manifest.SuiteFingerprint {
		return nil, errors.New("conformance suite fingerprint does not match the workspace manifest")
	}
	root, _, err := validateRoots(source, workspace)
	if err != nil {
		return nil, err
	}
	external, err := codes.LoadExternalSets(workspaceRoot)
	if err != nil {
		return nil, err
	}
	report := &Verification{
		Release: suite.Release, Cases: len(suite.Cases),
		workspaceFingerprint: manifest.Fingerprint,
	}
	for _, testCase := range suite.Cases {
		var samplePath string
		switch testCase.Origin {
		case "", "user-provided", "askiso-generated", "askiso-anonymised":
			samplePath, err = safeSourcePath(root, testCase.Sample)
		case "generated":
			samplePath, err = safeGeneratedPath(workspaceRoot, testCase.Sample)
		default:
			return nil, fmt.Errorf("unsupported suite case origin %q", testCase.Origin)
		}
		if err != nil {
			return nil, err
		}
		schemaPath, err := safeSourcePath(root, testCase.Schema)
		if err != nil {
			return nil, err
		}
		if err := verifyHash(samplePath, testCase.SampleSHA256); err != nil {
			return nil, err
		}
		if err := verifyHash(schemaPath, testCase.SchemaSHA256); err != nil {
			return nil, err
		}
		data, err := readBounded(samplePath)
		if err != nil {
			return nil, err
		}
		schema, err := xsd.ParseFile(schemaPath)
		if err != nil {
			return nil, err
		}
		payload, envelopeErrors := validationPayload(data, testCase.MessageID, testCase.BusinessService)
		result := validator.ValidateWithExternalSets(payload, schema, external)
		actual := "invalid"
		if result.Valid && envelopeErrors == 0 {
			actual = "valid"
		}
		caseResult := CaseResult{
			ID: testCase.ID, Expected: testCase.Expected, Actual: actual,
			Passed: actual == testCase.Expected, Errors: len(result.Errors) + envelopeErrors,
			EnvelopeErrors: envelopeErrors,
		}
		if caseResult.Passed {
			report.Passed++
		} else {
			report.Failed++
		}
		report.Results = append(report.Results, caseResult)
	}
	return report, nil
}

func safeGeneratedPath(workspace, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(relative))
	if filepath.IsAbs(clean) || clean == GeneratedDir ||
		!strings.HasPrefix(clean, GeneratedDir+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe generated sample path %q", relative)
	}
	path := filepath.Join(workspace, clean)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("generated sample must be a regular non-symlink: %s", filepath.Base(path))
	}
	return path, nil
}

func safeSourcePath(root, relative string) (string, error) {
	// A leading separator is rejected as well as an absolute path: on Windows
	// "/x" is not absolute, but it is rooted at the drive rather than at the
	// suite, and a suite entry has no business naming either.
	if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(relative, "/") || strings.HasPrefix(relative, `\`) {
		return "", fmt.Errorf("unsafe suite source path %q", relative)
	}
	path := filepath.Join(root, filepath.FromSlash(relative))
	if !within(root, path) {
		return "", fmt.Errorf("unsafe suite source path %q", relative)
	}
	return path, nil
}

func messageFromNamespace(namespace string) string {
	match := messageNamespaceRE.FindStringSubmatch(namespace)
	if match == nil {
		return ""
	}
	return match[1]
}

func messageFromXML(path string) (string, error) {
	messageID, _, err := messageMetadataFromXML(path)
	return messageID, err
}

func messageMetadataFromXML(path string) (string, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	dec := xml.NewDecoder(io.LimitReader(f, maxSourceSize+1))
	messageID := ""
	businessService := ""
	inBusinessService := false
	for {
		token, err := dec.Token()
		if err == io.EOF {
			if messageID == "" {
				return "", nil, errors.New("no ISO 20022 Document namespace")
			}
			var services []string
			for _, allowed := range rules.CBPRSR2025UsageGuidelines() {
				if allowed.MessageID == messageID && allowed.UsageIdentifier == strings.ToLower(strings.TrimSpace(businessService)) {
					services = []string{allowed.UsageIdentifier}
					break
				}
			}
			return messageID, services, nil
		}
		if err != nil {
			return "", nil, err
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "Document" {
				if message := messageFromNamespace(value.Name.Space); message != "" {
					messageID = message
				}
			}
			inBusinessService = value.Name.Local == "BizSvc"
		case xml.CharData:
			if inBusinessService {
				businessService += string(value)
			}
		case xml.EndElement:
			if value.Name.Local == "BizSvc" {
				inBusinessService = false
			}
		}
	}
}

func readBounded(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSourceSize {
		return nil, fmt.Errorf("%s exceeds the %d-byte suite limit", filepath.Base(path), maxSourceSize)
	}
	return os.ReadFile(path)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("fingerprinting %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxSourceSize+1)); err != nil {
		return "", fmt.Errorf("fingerprinting %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func verifyHash(path, expected string) error {
	actual, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("source fingerprint changed for %s: got %s, expected %s", filepath.Base(path), actual, expected)
	}
	return nil
}

func manifestFingerprint(manifest *Manifest) string {
	clone := *manifest
	clone.Fingerprint = ""
	data, _ := json.Marshal(clone)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:12])
}

func suiteFingerprint(suite *Suite) string {
	clone := *suite
	clone.Fingerprint = ""
	data, _ := json.Marshal(clone)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:12])
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to overwrite symlinked workspace file: %s", filepath.Base(path))
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicfile.Write(path, data, 0o600)
}

func readJSON(path string, value any) error {
	data, err := readBounded(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	return decodeJSON(filepath.Base(path), data, value)
}

func decodeJSON(name string, data []byte, value any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return fmt.Errorf("decoding %s: %w", name, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decoding %s: trailing JSON", name)
	}
	return nil
}

func unique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func without(values []string, unwanted string) []string {
	out := values[:0]
	for _, value := range values {
		if value != unwanted {
			out = append(out, value)
		}
	}
	return out
}
