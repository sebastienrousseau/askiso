// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package codes

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Most ISO 20022 code sets are enumerated in the schemas, and AskISO reads them
// from the user's own copy. The rest are "external": the Registration Authority
// maintains them separately on a quarterly cycle, the schemas reference them by
// name only, and there are several thousand of them.
//
// AskISO does not redistribute that publication any more than it redistributes
// the schemas. It imports the file the user downloaded, exactly as `catalog
// add` imports a message set, and stores a normalised copy alongside their
// catalogue.
//
// The Registration Authority publishes the sets as a spreadsheet, and more
// recently as JSON. Both are read here, because a user should not have to care
// which one they got.

// ExternalCode is one code from an external code set.
type ExternalCode struct {
	// Set is the code set name, for example "ExternalPurposeCode".
	Set string `json:"set"`
	// Code is the code value, for example "SALA".
	Code string `json:"code"`
	// Name is the code's short name.
	Name string `json:"name"`
	// Definition is what the Registration Authority says the code means.
	Definition string `json:"definition"`
}

// ExternalSets is an imported external code set publication.
type ExternalSets struct {
	// Source names the file the codes were imported from.
	Source string `json:"source"`
	// Format identifies the source representation that was recognised.
	Format string `json:"format,omitempty"`
	// Publication is the release marker inferred from the source filename.
	Publication string `json:"publication,omitempty"`
	// SHA256 identifies the exact local publication without copying it.
	SHA256 string `json:"sha256,omitempty"`
	// Warnings disclose information the selected source format cannot carry.
	Warnings []string `json:"warnings,omitempty"`
	// Codes are every code, sorted by set then code.
	Codes []ExternalCode `json:"codes"`

	bySet  map[string][]ExternalCode
	byCode map[string][]ExternalCode
}

// Total reports how many codes were imported.
func (e *ExternalSets) Total() int {
	if e == nil {
		return 0
	}
	return len(e.Codes)
}

// SetNames lists the code sets, sorted.
func (e *ExternalSets) SetNames() []string {
	if e == nil {
		return nil
	}
	out := make([]string, 0, len(e.bySet))
	for name := range e.bySet {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Set returns the members of one code set, matched case-insensitively.
func (e *ExternalSets) Set(name string) []ExternalCode {
	if e == nil {
		return nil
	}
	if members, ok := e.bySet[name]; ok {
		return append([]ExternalCode(nil), members...)
	}
	for known, members := range e.bySet {
		if strings.EqualFold(known, name) {
			return append([]ExternalCode(nil), members...)
		}
	}
	return nil
}

// Lookup finds every occurrence of a code across all sets.
func (e *ExternalSets) Lookup(code string) []ExternalCode {
	if e == nil {
		return nil
	}
	return append([]ExternalCode(nil), e.byCode[strings.ToUpper(strings.TrimSpace(code))]...)
}

// Search matches a code, a set name, or descriptive text.
func (e *ExternalSets) Search(query string) []ExternalCode {
	if e == nil {
		return nil
	}
	q := strings.ToUpper(strings.TrimSpace(query))
	if q == "" {
		return nil
	}

	var exact, partial []ExternalCode
	for _, c := range e.Codes {
		switch {
		case strings.ToUpper(c.Code) == q:
			exact = append(exact, c)
		case strings.Contains(strings.ToUpper(c.Code), q),
			strings.Contains(strings.ToUpper(c.Set), q),
			strings.Contains(strings.ToUpper(c.Name), q),
			strings.Contains(strings.ToUpper(c.Definition), q):
			partial = append(partial, c)
		}
	}
	return append(exact, partial...)
}

// index builds the lookup maps and sorts the codes.
func (e *ExternalSets) index() {
	sort.SliceStable(e.Codes, func(i, j int) bool {
		if e.Codes[i].Set != e.Codes[j].Set {
			return e.Codes[i].Set < e.Codes[j].Set
		}
		return e.Codes[i].Code < e.Codes[j].Code
	})

	e.bySet = map[string][]ExternalCode{}
	e.byCode = map[string][]ExternalCode{}
	for _, c := range e.Codes {
		e.bySet[c.Set] = append(e.bySet[c.Set], c)
		key := strings.ToUpper(c.Code)
		e.byCode[key] = append(e.byCode[key], c)
	}
}

func cloneExternalSets(sets *ExternalSets) *ExternalSets {
	if sets == nil {
		return nil
	}
	clone := *sets
	clone.Warnings = append([]string(nil), sets.Warnings...)
	clone.Codes = append([]ExternalCode(nil), sets.Codes...)
	clone.index()
	return &clone
}

// ---------------------------------------------------------------------------
// Importing
// ---------------------------------------------------------------------------

// maxImportSize bounds a publication. The real file is a few megabytes; this
// stops an unrelated archive being read into memory.
const maxImportSize = 64 << 20

// ImportExternalSets reads a Registration Authority external code set
// publication. Both the spreadsheet and the JSON form are accepted.
func ImportExternalSets(path string) (*ExternalSets, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if info.Size() > maxImportSize {
		return nil, fmt.Errorf("%s is %d bytes, above the %d-byte limit",
			filepath.Base(path), info.Size(), maxImportSize)
	}

	var sets *ExternalSets
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		sets, err = importExternalJSON(path)
	case ".xlsx":
		sets, err = importExternalSpreadsheet(path)
	default:
		return nil, fmt.Errorf("%s is neither a spreadsheet nor JSON\n\n"+
			"The Registration Authority publishes the external code sets at\n"+
			"https://www.iso20022.org/catalogue-messages/additional-content-messages/external-code-sets\n"+
			"as an .xlsx file, and more recently as .json; pass either",
			filepath.Base(path))
	}
	if err != nil {
		return nil, err
	}

	if len(sets.Codes) == 0 {
		return nil, fmt.Errorf("%s holds no external code sets AskISO recognises",
			filepath.Base(path))
	}
	sets.Source = path
	sets.Publication = publicationFromName(filepath.Base(path))
	sets.SHA256, err = externalFileSHA256(path)
	if err != nil {
		return nil, err
	}
	sets.index()
	return sets, nil
}

// importExternalJSON reads the JSON form.
//
// The publication has moved between shapes over the years, so this accepts the
// two that occur: an array of records, and an object whose keys are set names.
func importExternalJSON(path string) (*ExternalSets, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return parseExternalJSON(filepath.Base(path), data)
}

func parseExternalJSON(name string, data []byte) (*ExternalSets, error) {
	var flat []struct {
		Set        string `json:"set"`
		CodeSet    string `json:"codeSet"`
		Name       string `json:"name"`
		CodeName   string `json:"codeName"`
		Code       string `json:"code"`
		CodeValue  string `json:"codeValue"`
		Definition string `json:"definition"`
	}
	if err := json.Unmarshal(data, &flat); err == nil && len(flat) > 0 {
		out := &ExternalSets{Format: "json-records"}
		for _, r := range flat {
			code := firstOf(r.Code, r.CodeValue)
			set := firstOf(r.Set, r.CodeSet)
			if code == "" || set == "" {
				continue
			}
			out.Codes = append(out.Codes, ExternalCode{
				Set:        set,
				Code:       code,
				Name:       firstOf(r.Name, r.CodeName),
				Definition: r.Definition,
			})
		}
		return out, nil
	}

	// Since v3 the Registration Authority also publishes the code sets as a
	// JSON Schema: each definition is a named external code type and its enum
	// contains the permitted values. That representation intentionally carries
	// no per-code name or definition, so those fields remain empty rather than
	// assigning the type-level description to every member.
	var schema struct {
		Definitions map[string]struct {
			Type        string   `json:"type"`
			Description string   `json:"description"`
			Enum        []string `json:"enum"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(data, &schema); err == nil && len(schema.Definitions) > 0 {
		out := &ExternalSets{Format: "json-schema"}
		for set, definition := range schema.Definitions {
			for _, code := range definition.Enum {
				if strings.TrimSpace(code) == "" {
					continue
				}
				out.Codes = append(out.Codes, ExternalCode{Set: set, Code: code})
			}
		}
		if len(out.Codes) > 0 {
			out.Warnings = append(out.Warnings,
				"the JSON Schema publication carries permitted values but no per-code names or definitions")
		}
		return out, nil
	}

	var grouped map[string][]struct {
		Code       string `json:"code"`
		Name       string `json:"name"`
		Definition string `json:"definition"`
	}
	if err := json.Unmarshal(data, &grouped); err != nil {
		return nil, fmt.Errorf("%s is not a shape AskISO recognises: %w", name, err)
	}

	out := &ExternalSets{Format: "json-groups"}
	for set, members := range grouped {
		for _, m := range members {
			if m.Code == "" {
				continue
			}
			out.Codes = append(out.Codes, ExternalCode{
				Set: set, Code: m.Code, Name: m.Name, Definition: m.Definition,
			})
		}
	}
	return out, nil
}

var publicationNameRE = regexp.MustCompile(`(?i)([1-4]Q[0-9]{4})(?:[_-].*?v([0-9]+))?`)

func publicationFromName(name string) string {
	match := publicationNameRE.FindStringSubmatch(name)
	if match == nil {
		return ""
	}
	publication := strings.ToUpper(match[1])
	if match[2] != "" {
		publication += "/v" + match[2]
	}
	return publication
}

func externalFileSHA256(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("fingerprinting %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxImportSize+1)); err != nil {
		return "", fmt.Errorf("fingerprinting %s: %w", filepath.Base(path), err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func firstOf(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Spreadsheets
// ---------------------------------------------------------------------------

// importExternalSpreadsheet reads the .xlsx publication.
//
// An .xlsx file is a zip of XML: one part holds every distinct string, and each
// sheet references them by index. Reading it needs no dependency, only the
// patience to follow that indirection.
func importExternalSpreadsheet(path string) (*ExternalSets, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = zr.Close() }()

	shared, err := readSharedStrings(&zr.Reader)
	if err != nil {
		return nil, err
	}

	rows, err := readFirstSheet(&zr.Reader, shared)
	if err != nil {
		return nil, err
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s has no data rows", filepath.Base(path))
	}

	columns := headerColumns(rows[0])
	if columns.set < 0 || columns.code < 0 {
		return nil, fmt.Errorf("%s has no code set and code columns; "+
			"expected headers naming the code set and the code value, found %q",
			filepath.Base(path), strings.Join(rows[0], ", "))
	}

	out := &ExternalSets{Format: "xlsx"}
	for _, row := range rows[1:] {
		set := at(row, columns.set)
		code := at(row, columns.code)
		if set == "" || code == "" {
			continue
		}
		out.Codes = append(out.Codes, ExternalCode{
			Set:        set,
			Code:       code,
			Name:       at(row, columns.name),
			Definition: at(row, columns.definition),
		})
	}
	return out, nil
}

// SpreadsheetText extracts searchable cell text from every worksheet in a
// local XLSX file. It does not persist cells or evaluate formulas. Legacy .xls
// is a binary format and is deliberately rejected rather than guessed at.
func SpreadsheetText(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if !strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return "", fmt.Errorf("%s is not an XLSX workbook; save legacy .xls as .xlsx", filepath.Base(path))
	}
	if info.Size() > maxImportSize {
		return "", fmt.Errorf("%s is %d bytes, above the %d-byte limit", filepath.Base(path), info.Size(), maxImportSize)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", filepath.Base(path), err)
	}
	defer func() { _ = zr.Close() }()
	shared, err := readSharedStrings(&zr.Reader)
	if err != nil {
		return "", err
	}
	var sheets []*zip.File
	for _, entry := range zr.File {
		if strings.HasPrefix(entry.Name, "xl/worksheets/") && strings.HasSuffix(entry.Name, ".xml") {
			sheets = append(sheets, entry)
		}
	}
	if len(sheets) == 0 {
		return "", errors.New("the file holds no worksheet; is it really a spreadsheet?")
	}
	sort.Slice(sheets, func(i, j int) bool { return sheets[i].Name < sheets[j].Name })
	var text strings.Builder
	for _, sheet := range sheets {
		rows, err := readWorksheet(sheet, shared)
		if err != nil {
			return "", err
		}
		for _, row := range rows {
			line := strings.Join(row, "\t") + "\n"
			if text.Len()+len(line) > maxImportSize {
				return "", fmt.Errorf("searchable workbook text exceeds %d bytes", maxImportSize)
			}
			text.WriteString(line)
		}
	}
	return text.String(), nil
}

// sheetColumns is where each field sits in the spreadsheet.
type sheetColumns struct{ set, code, name, definition int }

// headerColumns finds the columns by their headings, because the Registration
// Authority has renamed them between publications.
func headerColumns(header []string) sheetColumns {
	cols := sheetColumns{set: -1, code: -1, name: -1, definition: -1}

	for i, cell := range header {
		h := strings.ToLower(strings.TrimSpace(cell))
		switch {
		case cols.set < 0 && (h == "code set" || h == "codeset" || h == "external code set" || h == "set"):
			cols.set = i
		case cols.code < 0 && (h == "code value" || h == "code" || h == "codevalue"):
			cols.code = i
		case cols.name < 0 && (h == "code name" || h == "name" || h == "codename"):
			cols.name = i
		case cols.definition < 0 && (h == "code definition" || h == "definition" || h == "description"):
			cols.definition = i
		}
	}
	return cols
}

func at(row []string, index int) string {
	if index < 0 || index >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[index])
}

// readSharedStrings reads the table every cell string is indexed into.
func readSharedStrings(zr *zip.Reader) ([]string, error) {
	f := zipEntry(zr, "xl/sharedStrings.xml")
	if f == nil {
		// A sheet whose cells are all inline needs no shared table.
		return nil, nil
	}

	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("opening the shared string table: %w", err)
	}
	defer func() { _ = rc.Close() }()

	dec := xml.NewDecoder(io.LimitReader(rc, maxImportSize))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	var out []string
	var current strings.Builder
	inString, inText := false, false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading the shared string table: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inString = true
				current.Reset()
			case "t":
				inText = inString
			}
		case xml.CharData:
			if inText {
				current.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "si":
				out = append(out, current.String())
				inString = false
			}
		}
	}
	return out, nil
}

// readFirstSheet reads the first worksheet into rows of cell values.
func readFirstSheet(zr *zip.Reader, shared []string) ([][]string, error) {
	f := zipEntry(zr, "xl/worksheets/sheet1.xml")
	if f == nil {
		// Some producers number differently; take the first worksheet there is.
		for _, entry := range zr.File {
			if strings.HasPrefix(entry.Name, "xl/worksheets/") && strings.HasSuffix(entry.Name, ".xml") {
				f = entry
				break
			}
		}
	}
	if f == nil {
		return nil, fmt.Errorf("the file holds no worksheet; is it really a spreadsheet?")
	}
	return readWorksheet(f, shared)
}

func readWorksheet(f *zip.File, shared []string) ([][]string, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()

	dec := xml.NewDecoder(io.LimitReader(rc, maxImportSize))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity

	var rows [][]string
	var row []string
	var cell strings.Builder
	cellType, cellRef := "", ""
	inValue := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f.Name, err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				row = nil
			case "c":
				cell.Reset()
				cellType = attrOf(t, "t")
				cellRef = attrOf(t, "r")
			case "v", "t":
				inValue = true
			}
		case xml.CharData:
			if inValue {
				cell.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v", "t":
				inValue = false
			case "c":
				// A sparse sheet omits empty cells, so the column a cell
				// belongs to comes from its reference rather than its position.
				if column := columnIndex(cellRef); column >= 0 {
					for len(row) <= column {
						row = append(row, "")
					}
					row[column] = resolveCell(cellType, cell.String(), shared)
				} else {
					row = append(row, resolveCell(cellType, cell.String(), shared))
				}
			case "row":
				rows = append(rows, row)
			}
		}
	}
	return rows, nil
}

// resolveCell turns a raw cell value into text, following the shared string
// table when the cell is indexed into it.
func resolveCell(cellType, raw string, shared []string) string {
	if cellType != "s" {
		return strings.TrimSpace(raw)
	}
	index, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || index < 0 || index >= len(shared) {
		return ""
	}
	return strings.TrimSpace(shared[index])
}

// columnIndex turns a cell reference such as "AB12" into a zero-based column.
func columnIndex(ref string) int {
	letters := 0
	for _, r := range ref {
		if r < 'A' || r > 'Z' {
			break
		}
		letters = letters*26 + int(r-'A') + 1
	}
	if letters == 0 {
		return -1
	}
	return letters - 1
}

func zipEntry(zr *zip.Reader, name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func attrOf(start xml.StartElement, name string) string {
	for _, a := range start.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
