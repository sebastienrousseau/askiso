// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package rules

// This file implements the deliberately local CBPR+ pack path. Swift-owned
// source documents are never embedded in AskISO: a user points the compiler at
// PDFs they are authorised to use, and only a small, deterministic constraint
// model is kept in memory (or written to a user-selected JSON file).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/sebastienrousseau/askiso/internal/atomicfile"
	"github.com/sebastienrousseau/askiso/internal/converter"
)

const (
	cbprPackFormat             = "askiso-cbpr-pack/v1"
	maxCBPRPDFs                = 128
	maxCBPRPDFSize       int64 = 100 << 20
	maxCompiledPackSize        = 128 << 20
	maxExtractedPDFBytes       = 128 << 20
)

// CBPRPackInfo describes a local pack without exposing its source path or
// document contents. It is included in JSON output so automation can tell a
// PDF-derived check from the embedded profile.
type CBPRPackInfo struct {
	Format          string   `json:"format"`
	Fingerprint     string   `json:"fingerprint"`
	Sources         int      `json:"sources"`
	UsageGuidelines int      `json:"usage_guidelines"`
	Constraints     int      `json:"constraints"`
	Messages        []string `json:"messages"`
	Coverage        string   `json:"coverage"`
	Warnings        []string `json:"warnings,omitempty"`
}

// CBPRPack is the portable, content-minimised form produced by the compiler.
// It contains paths and restrictions, never PDF prose or absolute paths.
type CBPRPack struct {
	Format      string               `json:"format"`
	Fingerprint string               `json:"fingerprint"`
	Sources     []CBPRPackSource     `json:"sources"`
	Constraints []CBPRPackConstraint `json:"constraints"`
	Warnings    []string             `json:"warnings,omitempty"`
}

// CBPRPackSource records provenance without recording a local directory.
type CBPRPackSource struct {
	Name             string   `json:"name"`
	SHA256           string   `json:"sha256"`
	MessageID        string   `json:"message_id,omitempty"`
	UsageIdentifiers []string `json:"usage_identifiers,omitempty"`
	Constraints      int      `json:"constraints"`
}

// CBPRPackConstraint is the stable rule interchange format. Min/Max constrain
// occurrences under each matching parent. Max=-1 means unbounded.
type CBPRPackConstraint struct {
	ID               string   `json:"id"`
	Source           string   `json:"source"`
	MessageID        string   `json:"message_id"`
	UsageIdentifiers []string `json:"usage_identifiers,omitempty"`
	Path             []string `json:"path"`
	Min              int      `json:"min"`
	Max              int      `json:"max"`
	DataType         string   `json:"data_type,omitempty"`
	MinLength        int      `json:"min_length,omitempty"`
	MaxLength        int      `json:"max_length,omitempty"`
	Pattern          string   `json:"pattern,omitempty"`
	Values           []string `json:"values,omitempty"`
	Choice           string   `json:"choice,omitempty"`
	// WhenPath and WhenValues make a narrative condition executable. The
	// constraint applies when the path exists and, when values are supplied,
	// at least one matching leaf has an allowed value. WhenAbsent reverses the
	// existence test. These fields are populated only by explicit local overlay
	// files, never guessed from prose.
	WhenPath   []string `json:"when_path,omitempty"`
	WhenValues []string `json:"when_values,omitempty"`
	WhenAbsent bool     `json:"when_absent,omitempty"`
}

var (
	// Do not require a trailing word boundary: MyStandards filenames commonly
	// put an underscore immediately after the final version digit.
	packMessageIDRE         = regexp.MustCompile(`(?i)(?:admi|camt|pacs|pain)\.\d{3}\.\d{3}\.\d{2}`)
	packFilenameMessageIDRE = regexp.MustCompile(`(?i)(?:admi|camt|pacs|pain)[._-]\d{3}[._-]\d{3}[._-]\d{2}`)
	packServiceRE           = regexp.MustCompile(`(?i)swift\.cbprplus(?:\.[a-z]+)?\.\d{2}`)
	packRowStartRE          = regexp.MustCompile(`^\s*(\d+(?:\.\d+)+)\s+`)
	packXMLTagRE            = regexp.MustCompile(`<([A-Za-z_][A-Za-z0-9_.-]*)>`)
	packOccRE               = regexp.MustCompile(`\[(\d+)\s*\.\.\s*(\d+|\*)\]`)
	packDataTypeRE          = regexp.MustCompile(`\]\s+([A-Za-z][A-Za-z0-9+_.-]*)\b`)
	packMaxTextRE           = regexp.MustCompile(`(?i)^Max(\d+)(Numeric)?Text$`)
	packExactTextRE         = regexp.MustCompile(`(?i)^Exact(\d+)(Numeric)?Text$`)
	findPDFTextTool         = exec.LookPath
	runPDFTextTool          = extractPDF
)

// CompileCBPRPack imports either one PDF or every PDF below a directory. Text
// extraction is delegated to pdftotext with argv (never a shell), so filenames
// cannot become commands and source content never leaves the machine.
func CompileCBPRPack(source string) (*CBPRPack, error) {
	path := filepath.Clean(source)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading CBPR+ pack source: %w", err)
	}
	if !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
		return LoadCBPRPack(path)
	}

	paths, err := cbprPDFPaths(path, info)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("CBPR+ pack source contains no PDF files: %s", path)
	}
	if len(paths) > maxCBPRPDFs {
		return nil, fmt.Errorf("CBPR+ pack contains %d PDFs; safety limit is %d", len(paths), maxCBPRPDFs)
	}
	tool, err := findPDFTextTool("pdftotext")
	if err != nil {
		return nil, errors.New("PDF import needs pdftotext (install Poppler), or pass a compiled .cbpr-pack.json file")
	}

	pack := &CBPRPack{Format: cbprPackFormat, Warnings: []string{}}
	seen := map[string]bool{}
	seenDigests := map[string]string{}
	for _, pdf := range paths {
		stat, err := os.Stat(pdf)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filepath.Base(pdf), err)
		}
		if stat.Size() > maxCBPRPDFSize {
			return nil, fmt.Errorf("%s is %d bytes; per-PDF safety limit is %d", filepath.Base(pdf), stat.Size(), maxCBPRPDFSize)
		}
		digest, err := fileSHA256(pdf)
		if err != nil {
			return nil, err
		}
		if original, duplicate := seenDigests[digest]; duplicate {
			pack.Warnings = append(pack.Warnings, fmt.Sprintf(
				"%s duplicates %s (same SHA-256); skipped", filepath.Base(pdf), original))
			continue
		}
		seenDigests[digest] = filepath.Base(pdf)
		// Page boundaries let the structure-table parser compensate for the
		// alternating margins used by MyStandards PDF exports.
		text, err := runPDFTextTool(tool, pdf, true)
		if err != nil {
			return nil, err
		}
		sourceInfo, constraints, warnings := parseCBPRPDFText(filepath.Base(pdf), digest, text)
		pack.Sources = append(pack.Sources, sourceInfo)
		pack.Warnings = append(pack.Warnings, warnings...)
		for _, c := range constraints {
			key := constraintKey(c)
			if seen[key] {
				continue
			}
			seen[key] = true
			pack.Constraints = append(pack.Constraints, c)
		}
	}
	if len(pack.Constraints) == 0 {
		return nil, errors.New("the PDFs yielded no enforceable XML-tag/cardinality rows; use text-based MyStandards exports or a compiled pack")
	}
	pack.Warnings = append(pack.Warnings,
		"PDF-derived coverage includes explicit element cardinalities and supported lexical types; narrative, conditional, external-code-set and diagram-only rules require independent confirmation.")
	pack.normalise()
	pack.Fingerprint = packFingerprint(pack)
	return pack, nil
}

func cbprPDFPaths(path string, info os.FileInfo) ([]string, error) {
	if !info.IsDir() {
		if !strings.EqualFold(filepath.Ext(path), ".pdf") {
			return nil, fmt.Errorf("CBPR+ pack source must be a PDF, PDF directory, or .cbpr-pack.json file: %s", path)
		}
		return []string{path}, nil
	}
	var paths []string
	err := filepath.WalkDir(path, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			// WalkDir does not follow file or directory symlinks.
			return nil
		}
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".pdf") {
			paths = append(paths, p)
		}
		return nil
	})
	sort.Strings(paths)
	return paths, err
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type cappedBuffer struct {
	buf bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buf.Len()+len(p) > b.max {
		return 0, fmt.Errorf("extracted PDF text exceeds %d bytes", b.max)
	}
	return b.buf.Write(p)
}

func extractPDF(tool, path string, preservePages bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	stdout := &cappedBuffer{max: maxExtractedPDFBytes}
	stderr := &cappedBuffer{max: 1 << 20}
	args := []string{"-layout"}
	if !preservePages {
		args = append(args, "-nopgbrk")
	}
	args = append(args, path, "-")
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("extracting %s exceeded two minutes", filepath.Base(path))
		}
		detail := strings.TrimSpace(stderr.buf.String())
		if detail != "" {
			return "", fmt.Errorf("extracting %s: %w: %s", filepath.Base(path), err, detail)
		}
		return "", fmt.Errorf("extracting %s: %w", filepath.Base(path), err)
	}
	text := stdout.buf.String()
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("%s has no extractable text (it may be scanned or encrypted)", filepath.Base(path))
	}
	return text, nil
}

// CBPRPackHit is an extractive result from a private local PDF. The search is
// intentionally local and non-generative: no document text is sent to an LLM
// or network service.
type CBPRPackHit struct {
	Source  string `json:"source"`
	Kind    string `json:"kind,omitempty"`
	Page    int    `json:"page,omitempty"`
	Score   int    `json:"score"`
	Snippet string `json:"snippet"`
}

// SearchCBPRPack searches authorised local PDFs without loading the AskISO AI
// provider path. Compiled packs cannot be searched because they intentionally
// contain no source prose.
func SearchCBPRPack(source, query string, limit int) ([]CBPRPackHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("local CBPR+ search needs a question")
	}
	if limit < 1 || limit > 20 {
		return nil, errors.New("local CBPR+ search limit must be between 1 and 20")
	}
	path := filepath.Clean(source)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading CBPR+ pack source: %w", err)
	}
	if !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".json") {
		return nil, errors.New("compiled CBPR+ packs contain no document prose; search the private PDF directory")
	}
	paths, err := cbprPDFPaths(path, info)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, errors.New("CBPR+ search source contains no PDF files")
	}
	if len(paths) > maxCBPRPDFs {
		return nil, fmt.Errorf("CBPR+ search contains %d PDFs; safety limit is %d", len(paths), maxCBPRPDFs)
	}
	tool, err := findPDFTextTool("pdftotext")
	if err != nil {
		return nil, errors.New("local CBPR+ PDF search needs pdftotext (install Poppler)")
	}

	var hits []CBPRPackHit
	for _, pdf := range paths {
		stat, statErr := os.Stat(pdf)
		if statErr != nil {
			return nil, statErr
		}
		if stat.Size() > maxCBPRPDFSize {
			return nil, fmt.Errorf("%s is %d bytes; per-PDF safety limit is %d", filepath.Base(pdf), stat.Size(), maxCBPRPDFSize)
		}
		text, extractErr := runPDFTextTool(tool, pdf, true)
		if extractErr != nil {
			return nil, extractErr
		}
		hits = append(hits, rankCBPRPages(filepath.Base(pdf), text, query)...)
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Source != hits[j].Source {
			return hits[i].Source < hits[j].Source
		}
		return hits[i].Page < hits[j].Page
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// RankCBPRText applies the same deterministic local evidence ranking used for
// PDF pages to text extracted from another user-held source format.
func RankCBPRText(source, text, query string) []CBPRPackHit {
	return rankCBPRPages(source, text, query)
}

var packWordRE = regexp.MustCompile(`[A-Za-z0-9]+(?:[._-][A-Za-z0-9]+)*`)

func rankCBPRPages(source, text, query string) []CBPRPackHit {
	terms := searchTerms(query)
	if len(terms) == 0 {
		return nil
	}
	pages := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\f")
	var hits []CBPRPackHit
	for pageNumber, page := range pages {
		lower := strings.ToLower(page)
		score := 0
		matchedTerms := 0
		exact := strings.ToLower(strings.TrimSpace(query))
		if len(exact) >= 4 {
			score += minInt(strings.Count(lower, exact), 4) * 30
		}
		for _, term := range terms {
			count := strings.Count(lower, term)
			if count == 0 {
				continue
			}
			matchedTerms++
			weight := 2
			if packMessageIDRE.MatchString(term) || strings.Contains(term, "cbpr") {
				weight = 8
			}
			score += minInt(count, 12) * weight
		}
		// A single generic word from a multi-term question is weak evidence and is
		// especially noisy in large code-set publications. Require two terms to
		// occur within the snippet window, so every returned extract visibly
		// supports its own ranking rather than relying on distant document text.
		focus, nearbyTerms := bestSearchFocus(lower, terms)
		if len(terms) > 1 && nearbyTerms < 2 {
			continue
		}
		// Matching more of the user's distinct terms is stronger evidence than
		// repeating one generic word many times on a long reference page.
		score += matchedTerms * matchedTerms * 4
		if score == 0 {
			continue
		}
		hits = append(hits, CBPRPackHit{
			Source: source, Kind: "pdf", Page: pageNumber + 1, Score: score,
			Snippet: localSnippet(page, focus),
		})
	}
	return hits
}

func bestSearchFocus(text string, terms []string) (int, int) {
	const radius = 360
	bestPosition, bestMatches := -1, 0
	for _, anchor := range terms {
		remaining := text
		offset := 0
		for occurrences := 0; occurrences < 64; occurrences++ {
			index := strings.Index(remaining, anchor)
			if index < 0 {
				break
			}
			position := offset + index
			start, end := position-radius, position+radius
			if start < 0 {
				start = 0
			}
			if end > len(text) {
				end = len(text)
			}
			window := text[start:end]
			matches := 0
			for _, term := range terms {
				if strings.Contains(window, term) {
					matches++
				}
			}
			if matches > bestMatches || (matches == bestMatches && (bestPosition < 0 || position < bestPosition)) {
				bestPosition, bestMatches = position, matches
			}
			next := index + len(anchor)
			offset += next
			remaining = remaining[next:]
		}
	}
	return bestPosition, bestMatches
}

func searchTerms(query string) []string {
	stop := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "for": true,
		"from": true, "how": true, "in": true, "is": true, "of": true,
		"it": true, "on": true, "or": true, "the": true, "to": true, "what": true,
		"when": true, "where": true, "which": true, "with": true,
	}
	seen := map[string]bool{}
	var terms []string
	for _, match := range packWordRE.FindAllString(strings.ToLower(query), -1) {
		if stop[match] || len(match) < 2 || seen[match] {
			continue
		}
		seen[match] = true
		terms = append(terms, match)
	}
	return terms
}

func localSnippet(page string, focus int) string {
	const radius = 360
	page = strings.ReplaceAll(page, "\x00", "")
	if focus < 0 {
		focus = 0
	}
	start := focus - radius
	if start < 0 {
		start = 0
	}
	end := focus + radius
	if end > len(page) {
		end = len(page)
	}
	// Keep byte slicing on UTF-8 boundaries.
	for start > 0 && !utf8.RuneStart(page[start]) {
		start--
	}
	for end < len(page) && !utf8.RuneStart(page[end]) {
		end++
	}
	snippet := strings.Join(strings.Fields(page[start:end]), " ")
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(page) {
		snippet += "…"
	}
	return snippet
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type parsedPackRow struct {
	index    string
	tags     []string
	path     []string
	min      int
	max      int
	dataType string
	choice   bool
}

func parseCBPRPDFText(name, digest, text string) (CBPRPackSource, []CBPRPackConstraint, []string) {
	messageID, allMessageIDs := primaryMessageID(name, text)
	services := usageIdentifiers(name, text, messageID)
	rows := parsePackRows(text)
	source := CBPRPackSource{Name: name, SHA256: digest, MessageID: messageID, UsageIdentifiers: services}
	var warnings []string
	if messageID == "" {
		warnings = append(warnings, name+": no unambiguous CBPR+ message identifier; treated as supporting documentation")
		return source, nil, warnings
	}
	if len(allMessageIDs) > 1 && !packFilenameMessageIDRE.MatchString(name) {
		warnings = append(warnings, name+": contains several message identifiers; selected "+messageID+" by frequency")
	}
	if len(rows) == 0 {
		warnings = append(warnings, name+": no XML-tag/cardinality table rows found")
		return source, nil, warnings
	}
	paths := map[string][]string{}
	rootIndex := ""
	var constraints []CBPRPackConstraint
	for _, row := range rows {
		var parent, path []string
		if len(row.path) > 0 {
			path = append([]string{}, row.path...)
			if len(path) > 1 {
				parent = path[:len(path)-1]
			}
		} else {
			parent = parentRowPath(row.index, paths, rootIndex)
			path = append(append([]string{}, parent...), row.tags...)
		}
		if len(path) == 0 {
			continue
		}
		if row.index != "" {
			paths[row.index] = path
			if strings.HasSuffix(row.index, ".0") && rootIndex == "" {
				rootIndex = row.index
			}
		}
		// The root is selected by the message namespace and has no useful
		// parent cardinality to check here.
		if len(parent) == 0 {
			continue
		}
		minLength, maxLength, pattern := lexicalRestriction(row.dataType)
		min := row.min
		if row.choice {
			// A choice alternative may be absent even when its own schema particle
			// says [1..1]. Enforcing the lower bound independently is incorrect.
			min = 0
		}
		c := CBPRPackConstraint{
			Source: name, MessageID: messageID, UsageIdentifiers: services,
			Path: path, Min: min, Max: row.max, DataType: row.dataType,
			MinLength: minLength, MaxLength: maxLength, Pattern: pattern,
		}
		c.ID = constraintID(c)
		constraints = append(constraints, c)
	}
	source.Constraints = len(constraints)
	return source, constraints, warnings
}

func primaryMessageID(name, text string) (string, []string) {
	if id := normaliseMessageID(packFilenameMessageIDRE.FindString(name)); id != "" {
		return id, uniqueLower(packMessageIDRE.FindAllString(text, -1))
	}
	matches := packMessageIDRE.FindAllString(text, -1)
	counts := map[string]int{}
	for _, match := range matches {
		counts[strings.ToLower(match)]++
	}
	var ids []string
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if counts[ids[i]] != counts[ids[j]] {
			return counts[ids[i]] > counts[ids[j]]
		}
		return ids[i] < ids[j]
	})
	if len(ids) == 0 {
		return "", nil
	}
	return ids[0], ids
}

func normaliseMessageID(value string) string {
	value = strings.ToLower(value)
	return strings.NewReplacer("_", ".", "-", ".").Replace(value)
}

func uniqueLower(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		value = strings.ToLower(value)
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func usageIdentifiers(name, text, messageID string) []string {
	services := cbprSR2025[messageID]
	if len(services) == 1 {
		return append([]string{}, services...)
	}
	filename := strings.ToLower(strings.NewReplacer("_", " ", "-", " ").Replace(name))
	filenameVariants := []struct{ token, marker string }{
		{" stp ", ".stp."}, {" cov ", ".cov."}, {" adv ", ".adv."},
		{"margin collection", ".col."}, {"multiple", ".mlp."}, {" mlp ", ".mlp."},
	}
	for _, variant := range filenameVariants {
		if !strings.Contains(" "+filename+" ", variant.token) {
			continue
		}
		for _, service := range services {
			if strings.Contains(service, variant.marker) {
				return []string{service}
			}
		}
	}
	if explicit := uniqueLower(packServiceRE.FindAllString(text, -1)); len(explicit) > 0 {
		var allowed []string
		for _, service := range explicit {
			if contains(services, service) {
				allowed = append(allowed, service)
			}
		}
		if len(allowed) == 1 {
			return allowed
		}
	}
	// Body prose is only a fallback: supporting discussion may mention another
	// variant, so it must never override an explicit filename marker or a single
	// allowed BizSvc found in the document.
	lowerText := strings.ToLower(firstN(text, 16000))
	textVariants := []struct{ token, marker string }{
		{"stp", ".stp."}, {"cover", ".cov."}, {"advice", ".adv."},
		{"margin collection", ".col."}, {"multiple", ".mlp."},
	}
	for _, variant := range textVariants {
		if !strings.Contains(lowerText, variant.token) {
			continue
		}
		for _, service := range services {
			if strings.Contains(service, variant.marker) {
				return []string{service}
			}
		}
	}
	// The unqualified service is the core Usage Guideline.
	for _, service := range services {
		if len(strings.Split(service, ".")) == 3 {
			return []string{service}
		}
	}
	return nil
}

// CBPRUsageIdentifiers resolves the SR2025 Business Service associated with a
// Usage Guideline label. It is intended for local machine-readable exports
// whose metadata contains the base message and guideline name but not BizSvc.
// An empty result means the message is outside the supported release.
func CBPRUsageIdentifiers(messageID, guideline string) []string {
	return usageIdentifiers(guideline, "", strings.ToLower(strings.TrimSpace(messageID)))
}

func firstN(value string, n int) string {
	if len(value) <= n {
		return value
	}
	return value[:n]
}

func parsePackRows(text string) []parsedPackRow {
	if rows := parseStructureRows(text); len(rows) > 0 {
		return rows
	}
	return parseIndexedPackRows(text)
}

// parseStructureRows reads the flattened "2 Structure" table emitted by
// MyStandards. Its indentation expresses the actual XML hierarchy. This is
// preferable to the later component catalogue, whose numbered sections list
// reusable datatype members rather than instance paths.
func parseStructureRows(text string) []parsedPackRow {
	lines := strings.Split(strings.ReplaceAll(text, "\u00a0", " "), "\n")
	start, end := -1, len(lines)
	for i, line := range lines {
		fields := strings.Fields(strings.TrimLeft(line, "\f"))
		if start < 0 && len(fields) == 2 && fields[0] == "2" && fields[1] == "Structure" {
			start = i + 1
			continue
		}
		if start >= 0 && len(fields) == 4 && fields[0] == "3" && fields[1] == "ISO" && fields[2] == "20022" && fields[3] == "Rules" {
			end = i
			break
		}
	}
	if start < 0 {
		return nil
	}

	type structureRow struct {
		indent int
		tag    string
		min    int
		max    int
		choice bool
		root   bool
	}
	var pages [][]structureRow
	page := []structureRow{}
	flushPage := func() {
		if len(page) > 0 {
			pages = append(pages, page)
			page = nil
		}
	}
	for _, raw := range lines[start:end] {
		if strings.Contains(raw, "\f") {
			flushPage()
			raw = strings.TrimLeft(raw, "\f")
		}
		tags := packXMLTagRE.FindAllStringSubmatch(raw, -1)
		occurrences := packOccRE.FindAllStringSubmatch(raw, -1)
		if len(tags) != 1 || len(occurrences) == 0 {
			continue
		}
		occ := occurrences[len(occurrences)-1] // R[x..y] overrides base Mult.
		min, err := strconv.Atoi(occ[1])
		if err != nil {
			continue
		}
		max := -1
		if occ[2] != "*" {
			max, err = strconv.Atoi(occ[2])
			if err != nil {
				continue
			}
		}
		trimmed := strings.TrimSpace(raw)
		page = append(page, structureRow{
			indent: structureItemIndent(raw), tag: tags[0][1],
			min: min, max: max,
			choice: strings.Contains(raw, "{Or") || strings.Contains(raw, "Or}"),
			// Top-level rows in the exported structure have no page reference.
			root: tags[0][1] == "AppHdr" || strings.HasSuffix(trimmed, "]"),
		})
	}
	flushPage()
	if len(pages) == 0 {
		return nil
	}

	rootIndent := -1
	previousIndent := -1
	stack := map[int][]string{}
	var rows []parsedPackRow
	for _, pageRows := range pages {
		offset := 0
		for _, row := range pageRows {
			if row.root {
				if rootIndent < 0 {
					rootIndent = row.indent
				}
				offset = rootIndent - row.indent
				break
			}
		}
		if rootIndent < 0 {
			continue
		}
		if previousIndent >= 0 {
			hasRoot := false
			for _, row := range pageRows {
				if row.root {
					hasRoot = true
					break
				}
			}
			if !hasRoot {
				offset = previousIndent - pageRows[0].indent
			}
		}
		for _, row := range pageRows {
			indent := row.indent + offset
			if !row.root && indent < rootIndent {
				// Some continued tables shift the payload root one column farther
				// left than AppHdr. Rebase the rest of that page when this signals
				// the start of the second top-level tree.
				offset += rootIndent - indent
				indent = rootIndent
			}
			var path []string
			if row.root || indent <= rootIndent {
				path = []string{row.tag}
				stack = map[int][]string{rootIndent: path}
				indent = rootIndent
			} else {
				parentIndent := -1
				for candidate := range stack {
					if candidate < indent && candidate > parentIndent {
						parentIndent = candidate
					}
				}
				if parentIndent < 0 {
					continue
				}
				path = append(append([]string{}, stack[parentIndent]...), row.tag)
				for candidate := range stack {
					if candidate >= indent {
						delete(stack, candidate)
					}
				}
				stack[indent] = path
			}
			previousIndent = indent
			rows = append(rows, parsedPackRow{
				tags: []string{row.tag}, path: path, min: row.min, max: row.max,
				choice: row.choice,
			})
		}
	}
	return rows
}

func structureItemIndent(line string) int {
	if match := packRowStartRE.FindStringIndex(line); match != nil {
		return match[1]
	}
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

func parseIndexedPackRows(text string) []parsedPackRow {
	lines := strings.Split(strings.ReplaceAll(text, "\u00a0", " "), "\n")
	var logical []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			logical = append(logical, current.String())
			current.Reset()
		}
	}
	for _, line := range lines {
		if packRowStartRE.MatchString(line) {
			flush()
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(strings.TrimSpace(line))
	}
	flush()

	var rows []parsedPackRow
	for _, line := range logical {
		start := packRowStartRE.FindStringSubmatch(line)
		occ := packOccRE.FindStringSubmatch(line)
		tagMatches := packXMLTagRE.FindAllStringSubmatch(line, -1)
		if len(start) == 0 || len(occ) == 0 || len(tagMatches) == 0 {
			continue
		}
		min, err := strconv.Atoi(occ[1])
		if err != nil {
			continue
		}
		max := -1
		if occ[2] != "*" {
			max, err = strconv.Atoi(occ[2])
			if err != nil {
				continue
			}
		}
		var tags []string
		for _, match := range tagMatches {
			if match[1] != "XML" {
				tags = append(tags, match[1])
			}
		}
		if len(tags) == 0 {
			continue
		}
		dataType := ""
		if match := packDataTypeRE.FindStringSubmatch(line); len(match) > 0 {
			candidate := match[1]
			if candidate != "Only" && candidate != "Definition" && candidate != "Comments" {
				dataType = candidate
			}
		}
		rows = append(rows, parsedPackRow{
			index: start[1], tags: tags, min: min, max: max, dataType: dataType,
			choice: strings.Contains(line, "(Or") || strings.Contains(line, "Or)"),
		})
	}
	return rows
}

func parentRowPath(index string, paths map[string][]string, rootIndex string) []string {
	parent := index
	for {
		pos := strings.LastIndexByte(parent, '.')
		if pos < 0 {
			break
		}
		parent = parent[:pos]
		if path, ok := paths[parent]; ok {
			return path
		}
	}
	if rootIndex != "" {
		if path, ok := paths[rootIndex]; ok {
			return path
		}
	}
	return nil
}

func lexicalRestriction(dataType string) (minLength, maxLength int, pattern string) {
	if match := packMaxTextRE.FindStringSubmatch(dataType); len(match) > 0 {
		maxLength, _ = strconv.Atoi(match[1])
		if match[2] != "" {
			pattern = `^[0-9]+$`
		}
		return 0, maxLength, pattern
	}
	if match := packExactTextRE.FindStringSubmatch(dataType); len(match) > 0 {
		length, _ := strconv.Atoi(match[1])
		pattern = ""
		if match[2] != "" {
			pattern = `^[0-9]+$`
		}
		return length, length, pattern
	}
	return 0, 0, ""
}

func constraintKey(c CBPRPackConstraint) string {
	return strings.Join([]string{c.MessageID, strings.Join(c.UsageIdentifiers, ","), strings.Join(c.Path, "/"), strconv.Itoa(c.Min), strconv.Itoa(c.Max), c.DataType,
		strings.Join(c.WhenPath, "/"), strings.Join(c.WhenValues, ","), strconv.FormatBool(c.WhenAbsent),
		strconv.Itoa(c.MinLength), strconv.Itoa(c.MaxLength), c.Pattern, strings.Join(c.Values, ","), c.Choice}, "|")
}

func constraintID(c CBPRPackConstraint) string {
	h := sha256.Sum256([]byte(constraintKey(c)))
	return "CBPR-PACK-" + strings.ToUpper(hex.EncodeToString(h[:6]))
}

func (p *CBPRPack) normalise() {
	if p.Format == "" {
		p.Format = cbprPackFormat
	}
	for i := range p.Sources {
		sort.Strings(p.Sources[i].UsageIdentifiers)
	}
	for i := range p.Constraints {
		sort.Strings(p.Constraints[i].UsageIdentifiers)
		sort.Strings(p.Constraints[i].Values)
		sort.Strings(p.Constraints[i].WhenValues)
		if p.Constraints[i].ID == "" {
			p.Constraints[i].ID = constraintID(p.Constraints[i])
		}
	}
	sort.Slice(p.Sources, func(i, j int) bool { return p.Sources[i].Name < p.Sources[j].Name })
	sort.Slice(p.Constraints, func(i, j int) bool {
		left, right := constraintKey(p.Constraints[i]), constraintKey(p.Constraints[j])
		if left != right {
			return left < right
		}
		if p.Constraints[i].Source != p.Constraints[j].Source {
			return p.Constraints[i].Source < p.Constraints[j].Source
		}
		return p.Constraints[i].ID < p.Constraints[j].ID
	})
	sort.Strings(p.Warnings)
}

// cloneCBPRPack copies every mutable level of a pack. Normalisation sorts
// nested slices, so a shallow copy would still mutate the caller and concurrent
// exports of one pack would race with each other.
func cloneCBPRPack(pack *CBPRPack) *CBPRPack {
	if pack == nil {
		return nil
	}
	clone := *pack
	clone.Sources = append([]CBPRPackSource(nil), pack.Sources...)
	for i := range clone.Sources {
		clone.Sources[i].UsageIdentifiers = append([]string(nil), pack.Sources[i].UsageIdentifiers...)
	}
	clone.Constraints = append([]CBPRPackConstraint(nil), pack.Constraints...)
	for i := range clone.Constraints {
		original := pack.Constraints[i]
		clone.Constraints[i].UsageIdentifiers = append([]string(nil), original.UsageIdentifiers...)
		clone.Constraints[i].Path = append([]string(nil), original.Path...)
		clone.Constraints[i].Values = append([]string(nil), original.Values...)
		clone.Constraints[i].WhenPath = append([]string(nil), original.WhenPath...)
		clone.Constraints[i].WhenValues = append([]string(nil), original.WhenValues...)
	}
	clone.Warnings = append([]string(nil), pack.Warnings...)
	return &clone
}

// MergeCBPRPacks combines independently compiled local packs, for example a
// PDF-derived pack and an operator-authored conditional overlay.
func MergeCBPRPacks(packs ...*CBPRPack) (*CBPRPack, error) {
	merged := &CBPRPack{Format: cbprPackFormat}
	for _, pack := range packs {
		if pack == nil {
			continue
		}
		if pack.Format != cbprPackFormat {
			return nil, fmt.Errorf("unsupported CBPR+ pack format %q", pack.Format)
		}
		clone := cloneCBPRPack(pack)
		merged.Sources = append(merged.Sources, clone.Sources...)
		merged.Constraints = append(merged.Constraints, clone.Constraints...)
		merged.Warnings = append(merged.Warnings, clone.Warnings...)
	}
	if len(merged.Sources) == 0 && len(merged.Constraints) == 0 {
		return nil, errors.New("at least one CBPR+ pack is required")
	}
	merged.normalise()
	if len(merged.Warnings) > 1 {
		warnings := merged.Warnings[:1]
		for _, warning := range merged.Warnings[1:] {
			if warning != warnings[len(warnings)-1] {
				warnings = append(warnings, warning)
			}
		}
		merged.Warnings = warnings
	}
	merged.Fingerprint = packFingerprint(merged)
	return merged, nil
}

func packFingerprint(pack *CBPRPack) string {
	clone := *pack
	clone.Fingerprint = ""
	data, _ := json.Marshal(clone)
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:12])
}

// LoadCBPRPack loads a previously compiled local pack. It rejects unknown
// formats and invalid constraints instead of silently weakening validation.
func LoadCBPRPack(path string) (*CBPRPack, error) {
	cleanPath := filepath.Clean(path)
	info, err := os.Stat(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("reading compiled CBPR+ pack: %w", err)
	}
	if info.Size() > maxCompiledPackSize {
		return nil, fmt.Errorf("compiled CBPR+ pack is %d bytes; safety limit is %d", info.Size(), maxCompiledPackSize)
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("reading compiled CBPR+ pack: %w", err)
	}
	var pack CBPRPack
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pack); err != nil {
		return nil, fmt.Errorf("decoding compiled CBPR+ pack: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("decoding compiled CBPR+ pack: trailing JSON value")
		}
		return nil, fmt.Errorf("decoding compiled CBPR+ pack: %w", err)
	}
	if pack.Format != cbprPackFormat {
		return nil, fmt.Errorf("unsupported CBPR+ pack format %q (expected %q)", pack.Format, cbprPackFormat)
	}
	if len(pack.Constraints) == 0 {
		return nil, errors.New("compiled CBPR+ pack contains no constraints")
	}
	pack.normalise()
	for i, constraint := range pack.Constraints {
		if err := validatePackConstraint(constraint); err != nil {
			return nil, fmt.Errorf("constraint %d (%s): %w", i+1, constraint.ID, err)
		}
	}
	want := packFingerprint(&pack)
	if pack.Fingerprint != "" && pack.Fingerprint != want {
		return nil, fmt.Errorf("compiled CBPR+ pack fingerprint mismatch: got %s, calculated %s", pack.Fingerprint, want)
	}
	pack.Fingerprint = want
	return &pack, nil
}

func validatePackConstraint(c CBPRPackConstraint) error {
	if !isCBPRMessage(c.MessageID) {
		return fmt.Errorf("message %q is not in the live SR2025 collection", c.MessageID)
	}
	if len(c.Path) < 2 {
		return errors.New("path must contain a root and an element")
	}
	for _, part := range c.Path {
		if part == "" || strings.ContainsAny(part, "/[]@") {
			return fmt.Errorf("invalid path component %q", part)
		}
	}
	for _, part := range c.WhenPath {
		if part == "" || strings.ContainsAny(part, "/[]@") {
			return fmt.Errorf("invalid condition path component %q", part)
		}
	}
	if c.WhenAbsent && len(c.WhenPath) == 0 {
		return errors.New("when_absent requires when_path")
	}
	if c.Min < 0 || (c.Max >= 0 && c.Min > c.Max) {
		return fmt.Errorf("invalid cardinality [%d..%d]", c.Min, c.Max)
	}
	for _, service := range c.UsageIdentifiers {
		if !contains(cbprSR2025[c.MessageID], service) {
			return fmt.Errorf("usage identifier %q is not valid for %s", service, c.MessageID)
		}
	}
	if c.Pattern != "" {
		if _, err := regexp.Compile(c.Pattern); err != nil {
			return fmt.Errorf("invalid RE2 pattern: %w", err)
		}
	}
	return nil
}

// WriteCBPRPack serialises a reproducible compiled pack. Mode 0600 avoids
// accidentally making locally derived standards material world-readable.
func WriteCBPRPack(path string, pack *CBPRPack) error {
	if pack == nil {
		return errors.New("cannot write a nil CBPR+ pack")
	}
	normalised := cloneCBPRPack(pack)
	normalised.normalise()
	normalised.Fingerprint = packFingerprint(normalised)
	data, err := json.MarshalIndent(normalised, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	cleanPath := filepath.Clean(path)
	if info, err := os.Lstat(cleanPath); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to overwrite symlinked compiled pack: %s", cleanPath)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking compiled CBPR+ pack destination: %w", err)
	}
	if err := atomicfile.Write(cleanPath, data, 0o600); err != nil {
		return fmt.Errorf("writing compiled CBPR+ pack: %w", err)
	}
	return nil
}

// Info returns the disclosure-safe summary attached to validation results.
func (p *CBPRPack) Info() *CBPRPackInfo {
	if p == nil {
		return nil
	}
	messages := map[string]bool{}
	guidelines := map[string]bool{}
	for _, source := range p.Sources {
		if source.MessageID != "" && source.Constraints > 0 {
			messages[source.MessageID] = true
			guidelines[source.MessageID+"|"+strings.Join(source.UsageIdentifiers, ",")] = true
		}
	}
	messageList := make([]string, 0, len(messages))
	for message := range messages {
		messageList = append(messageList, message)
	}
	sort.Strings(messageList)
	return &CBPRPackInfo{
		Format: p.Format, Fingerprint: p.Fingerprint, Sources: len(p.Sources),
		UsageGuidelines: len(guidelines), Constraints: len(p.Constraints),
		Messages: messageList, Coverage: "pdf-derived structural and supported lexical constraints",
		Warnings: append([]string{}, p.Warnings...),
	}
}

// Augment appends locally compiled checks to the embedded CBPR+ profile.
func (p *CBPRPack) Augment(base Profile) (Profile, error) {
	if p == nil {
		return Profile{}, errors.New("CBPR+ pack is nil")
	}
	if base.Name != "cbpr-plus" {
		return Profile{}, fmt.Errorf("a CBPR+ pack can only augment the cbpr-plus profile, not %q", base.Name)
	}
	groups := map[string][]CBPRPackConstraint{}
	for _, constraint := range p.Constraints {
		if err := validatePackConstraint(constraint); err != nil {
			return Profile{}, fmt.Errorf("constraint %s: %w", constraint.ID, err)
		}
		key := constraint.MessageID + "|" + strings.Join(constraint.UsageIdentifiers, ",") + "|" + constraint.Source
		groups[key] = append(groups[key], constraint)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	profile := base
	profile.Rules = append([]Rule{}, base.Rules...)
	profile.Pack = p.Info()
	profile.Description = base.Description + " Local PDF-derived pack " + p.Fingerprint + " is active."
	for _, key := range keys {
		constraints := append([]CBPRPackConstraint{}, groups[key]...)
		messageID := constraints[0].MessageID
		services := append([]string{}, constraints[0].UsageIdentifiers...)
		sourceName := constraints[0].Source
		ruleID := "CBPR-PACK-SOURCE-" + strings.ToUpper(shortHash(key))
		profile.Rules = append(profile.Rules, Rule{
			ID: ruleID, Name: "Local CBPR+ Usage Guideline: " + sourceName,
			Severity:    SeverityError,
			Description: "Locally compiled PDF-derived CBPR+ cardinality and lexical constraints.",
			Remediation: "Populate the message according to the locally supplied Usage Guideline.",
			Reference:   cbprReference,
			Exempt:      func(id string) bool { return id != messageID },
			Check: func(ctx *Context) []Finding {
				if len(services) > 0 {
					header, ok := firstLocated(ctx.Root, "AppHdr")
					if !ok || !contains(services, ChildText(header.Node, "BizSvc")) {
						return nil
					}
				}
				return checkPackConstraints(ctx.Root, constraints)
			},
		})
	}
	return profile, nil
}

func shortHash(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:6])
}

func checkPackConstraints(root *converter.Node, constraints []CBPRPackConstraint) []Finding {
	var findings []Finding
	for _, constraint := range constraints {
		if !constraintApplies(root, constraint) {
			continue
		}
		parents := findPathMatches(root, constraint.Path[:len(constraint.Path)-1])
		if len(parents) == 0 {
			// The ancestor's own rule reports why the branch is absent. Reporting
			// every mandatory descendant would produce a cascade of noise.
			continue
		}
		target := constraint.Path[len(constraint.Path)-1]
		for _, parent := range parents {
			children := Children(parent.Node, target)
			count := len(children)
			if count < constraint.Min || (constraint.Max >= 0 && count > constraint.Max) {
				expected := fmt.Sprintf("%d..* occurrence(s)", constraint.Min)
				if constraint.Max >= 0 {
					expected = fmt.Sprintf("%d..%d occurrence(s)", constraint.Min, constraint.Max)
				}
				findings = append(findings, Finding{
					RuleID: constraint.ID, Rule: "Local CBPR+ cardinality",
					Path:    parent.Path + "/" + target,
					Message: fmt.Sprintf("%s occurs %d time(s); the local Usage Guideline requires %s", target, count, expected),
					Found:   strconv.Itoa(count), Expected: expected,
				})
			}
			for i, child := range children {
				path := parent.Path + "/" + target
				if len(children) > 1 {
					path += fmt.Sprintf("[%d]", i+1)
				}
				if finding := checkPackValue(constraint, child, path); finding != nil {
					findings = append(findings, *finding)
				}
			}
		}
	}
	return findings
}

func constraintApplies(root *converter.Node, constraint CBPRPackConstraint) bool {
	if len(constraint.WhenPath) == 0 {
		return true
	}
	matches := findPathMatches(root, constraint.WhenPath)
	if constraint.WhenAbsent {
		return len(matches) == 0
	}
	if len(matches) == 0 {
		return false
	}
	if len(constraint.WhenValues) == 0 {
		return true
	}
	for _, match := range matches {
		if contains(constraint.WhenValues, strings.TrimSpace(match.Node.Text)) {
			return true
		}
	}
	return false
}

func checkPackValue(c CBPRPackConstraint, node *converter.Node, path string) *Finding {
	if len(node.Children) > 0 {
		return nil
	}
	value := strings.TrimSpace(node.Text)
	length := utf8.RuneCountInString(value)
	reason, expected := "", ""
	switch {
	case c.MinLength > 0 && length < c.MinLength:
		reason, expected = fmt.Sprintf("has %d characters", length), fmt.Sprintf("at least %d characters", c.MinLength)
	case c.MaxLength > 0 && length > c.MaxLength:
		reason, expected = fmt.Sprintf("has %d characters", length), fmt.Sprintf("at most %d characters", c.MaxLength)
	case c.Pattern != "" && !regexp.MustCompile(c.Pattern).MatchString(value):
		reason, expected = "does not match the local Usage Guideline pattern", c.Pattern
	case len(c.Values) > 0 && !contains(c.Values, value):
		reason, expected = fmt.Sprintf("contains unsupported value %q", value), strings.Join(c.Values, ", ")
	}
	if reason == "" {
		return nil
	}
	return &Finding{
		RuleID: c.ID, Rule: "Local CBPR+ lexical restriction", Path: path,
		Message: node.Name + " " + reason, Found: value, Expected: expected,
	}
}

func findPathMatches(root *converter.Node, path []string) []Located {
	if root == nil || len(path) == 0 {
		return nil
	}
	var out []Located
	for _, candidate := range Walk(root) {
		if candidate.Node.Name != path[0] {
			continue
		}
		current := []Located{candidate}
		for _, part := range path[1:] {
			var next []Located
			for _, parent := range current {
				matches := Children(parent.Node, part)
				for i, match := range matches {
					childPath := parent.Path + "/" + part
					if len(matches) > 1 {
						childPath += fmt.Sprintf("[%d]", i+1)
					}
					next = append(next, Located{Node: match, Path: childPath})
				}
			}
			current = next
			if len(current) == 0 {
				break
			}
		}
		out = append(out, current...)
	}
	return out
}
