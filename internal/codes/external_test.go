// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package codes_test

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sebastienrousseau/askiso/internal/codes"
)

// The Registration Authority publishes the external code sets as a spreadsheet,
// and more recently as JSON. AskISO imports whichever the user got, because
// they should not have to care.

// writeSpreadsheet builds an .xlsx in the shape the publication takes: a shared
// string table, and a sheet whose cells index into it.
func writeSpreadsheet(t *testing.T, path string, header []string, rows [][]string) {
	t.Helper()

	// Every distinct string goes into the shared table once, as the real
	// producer does.
	var strs []string
	index := map[string]int{}
	intern := func(s string) int {
		if i, ok := index[s]; ok {
			return i
		}
		index[s] = len(strs)
		strs = append(strs, s)
		return index[s]
	}

	all := append([][]string{header}, rows...)
	var sheet strings.Builder
	for r, row := range all {
		fmt.Fprintf(&sheet, `<row r="%d">`, r+1)
		for c, cell := range row {
			if cell == "" {
				continue // a sparse sheet omits empty cells
			}
			fmt.Fprintf(&sheet, `<c r="%c%d" t="s"><v>%d</v></c>`, 'A'+c, r+1, intern(cell))
		}
		sheet.WriteString("</row>")
	}

	var shared strings.Builder
	shared.WriteString(`<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range strs {
		fmt.Fprintf(&shared, "<si><t>%s</t></si>", escapeXML(s))
	}
	shared.WriteString("</sst>")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)
	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("[Content_Types].xml", "<Types/>")
	write("xl/sharedStrings.xml", shared.String())
	write("xl/worksheets/sheet1.xml",
		`<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`+
			sheet.String()+`</sheetData></worksheet>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

var publicationRows = [][]string{
	{"ExternalPurposeCode", "SALA", "Salary Payment", "Transaction is the payment of salaries."},
	{"ExternalPurposeCode", "SUPP", "Supplier Payment", "Transaction is a payment to a supplier."},
	{"ExternalStatusReason1Code", "AC04", "Closed Account Number", "Account number specified has been closed."},
}

func TestImportSpreadsheet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ExternalCodeSets.xlsx")
	writeSpreadsheet(t, path,
		[]string{"Code Set", "Code Value", "Code Name", "Code Definition"}, publicationRows)

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	if sets.Total() != 3 {
		t.Errorf("Total() = %d, want 3", sets.Total())
	}
	if names := sets.SetNames(); len(names) != 2 || names[0] != "ExternalPurposeCode" {
		t.Errorf("SetNames() = %v", names)
	}

	members := sets.Set("ExternalPurposeCode")
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	// Sorted, so two imports of the same file read the same way.
	if members[0].Code != "SALA" {
		t.Errorf("the set is unsorted: %+v", members)
	}
	if members[0].Definition != "Transaction is the payment of salaries." {
		t.Errorf("the definition was lost: %+v", members[0])
	}

	// The set name is matched case-insensitively, because nobody types
	// ExternalStatusReason1Code from memory.
	if got := sets.Set("externalstatusreason1code"); len(got) != 1 {
		t.Errorf("a case-insensitive set lookup found %d", len(got))
	}
	if got := sets.Set("NoSuchSet"); got != nil {
		t.Errorf("an unknown set returned %v", got)
	}
}

func TestSpreadsheetText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guidelines.xlsx")
	writeSpreadsheet(t, path,
		[]string{"Rule", "Definition"},
		[][]string{{"CBPR-UETR", "Unique transaction reference is mandatory."}})
	text, err := codes.SpreadsheetText(path)
	if err != nil || !strings.Contains(text, "Unique transaction reference") {
		t.Fatalf("spreadsheet text = %q, %v", text, err)
	}
	if _, err := codes.SpreadsheetText(filepath.Join(t.TempDir(), "legacy.xls")); err == nil {
		t.Fatal("legacy XLS was accepted")
	}
}

func TestImportSpreadsheetColumnOrderAndNames(t *testing.T) {
	// The Registration Authority has renamed and reordered these columns
	// between publications, so the import finds them by heading.
	path := filepath.Join(t.TempDir(), "codes.xlsx")
	writeSpreadsheet(t, path,
		[]string{"Description", "Code", "Set", "Name"},
		[][]string{
			{"Account number specified has been closed.", "AC04", "ExternalStatusReason1Code", "Closed Account Number"},
		})

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	got := sets.Lookup("AC04")
	if len(got) != 1 {
		t.Fatalf("Lookup found %d", len(got))
	}
	if got[0].Set != "ExternalStatusReason1Code" || got[0].Name != "Closed Account Number" {
		t.Errorf("the columns were read in the wrong order: %+v", got[0])
	}
}

func TestImportSpreadsheetRejects(t *testing.T) {
	dir := t.TempDir()

	// A sheet with no code set and code columns cannot be read, and the error
	// has to say which headings it did find.
	wrong := filepath.Join(dir, "wrong.xlsx")
	writeSpreadsheet(t, wrong, []string{"Something", "Else"}, [][]string{{"a", "b"}})
	_, err := codes.ImportExternalSets(wrong)
	if err == nil {
		t.Fatal("a spreadsheet with no recognisable columns was accepted")
	}
	if !strings.Contains(err.Error(), "Something") {
		t.Errorf("the error does not name the headings it found: %v", err)
	}

	// A header and nothing else.
	empty := filepath.Join(dir, "empty.xlsx")
	writeSpreadsheet(t, empty, []string{"Code Set", "Code Value"}, nil)
	if _, err := codes.ImportExternalSets(empty); err == nil {
		t.Error("a spreadsheet with no data rows was accepted")
	}

	// Rows missing a set or a code are skipped rather than imported blank.
	partial := filepath.Join(dir, "partial.xlsx")
	writeSpreadsheet(t, partial, []string{"Code Set", "Code Value"},
		[][]string{{"", "SALA"}, {"ExternalPurposeCode", ""}, {"ExternalPurposeCode", "SUPP"}})
	sets, err := codes.ImportExternalSets(partial)
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	if sets.Total() != 1 {
		t.Errorf("Total() = %d, want the one complete row", sets.Total())
	}

	// A zip that is not a spreadsheet at all.
	notSheet := filepath.Join(dir, "not-a-sheet.xlsx")
	f, err := os.Create(notSheet)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("readme.txt")
	_, _ = w.Write([]byte("nothing here"))
	_ = zw.Close()
	_ = f.Close()
	if _, err := codes.ImportExternalSets(notSheet); err == nil {
		t.Error("a zip with no worksheet was accepted")
	}

	// And something that is not a zip.
	broken := filepath.Join(dir, "broken.xlsx")
	if err := os.WriteFile(broken, []byte("not a zip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := codes.ImportExternalSets(broken); err == nil {
		t.Error("a file that is not a zip was accepted")
	}
}

func TestImportJSONArray(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codes.json")
	body, err := json.Marshal([]map[string]string{
		{"codeSet": "ExternalPurposeCode", "codeValue": "SALA",
			"codeName": "Salary Payment", "definition": "Transaction is the payment of salaries."},
		{"set": "ExternalStatusReason1Code", "code": "AC04",
			"name": "Closed Account Number", "definition": "Account number specified has been closed."},
		{"codeSet": "", "codeValue": "IGNORED"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	// Both field-naming conventions the publication has used are read, and the
	// row with no set is skipped.
	if sets.Total() != 2 {
		t.Errorf("Total() = %d, want 2: %+v", sets.Total(), sets.Codes)
	}
	if got := sets.Lookup("SALA"); len(got) != 1 || got[0].Name != "Salary Payment" {
		t.Errorf("Lookup(SALA) = %+v", got)
	}
}

func TestImportJSONSchemaPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2Q2026_externalcodesets_v3.json")
	body := `{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "definitions":{
    "ExternalPurpose1Code":{"type":"string","description":"Purpose","enum":["SALA","SUPP"]},
    "ExternalOpenType":{"type":"string","minLength":1,"maxLength":4}
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing JSON Schema publication: %v", err)
	}
	if sets.Format != "json-schema" || sets.Publication != "2Q2026/v3" {
		t.Fatalf("provenance = format %q, publication %q", sets.Format, sets.Publication)
	}
	if len(sets.SHA256) != 64 || sets.Total() != 2 || len(sets.SetNames()) != 1 {
		t.Fatalf("schema import summary = sha %q, %d codes, sets %v", sets.SHA256, sets.Total(), sets.SetNames())
	}
	if got := sets.Lookup("SALA"); len(got) != 1 || got[0].Set != "ExternalPurpose1Code" || got[0].Definition != "" {
		t.Fatalf("schema enum lookup = %+v", got)
	}
	if len(sets.Warnings) != 1 || !strings.Contains(sets.Warnings[0], "no per-code") {
		t.Fatalf("schema information-loss warning = %v", sets.Warnings)
	}
}

func TestImportOfficialExternalCodesFromEnvironment(t *testing.T) {
	path := os.Getenv("ASKISO_TEST_EXTERNAL_CODES")
	if path == "" {
		t.Skip("set ASKISO_TEST_EXTERNAL_CODES to exercise a locally downloaded publication")
	}
	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing local official publication: %v", err)
	}
	if sets.Format == "" || sets.Total() == 0 || len(sets.SetNames()) == 0 || len(sets.SHA256) != 64 {
		t.Fatalf("incomplete official publication summary: format=%q codes=%d sets=%d sha=%q",
			sets.Format, sets.Total(), len(sets.SetNames()), sets.SHA256)
	}
}

func TestImportJSONGrouped(t *testing.T) {
	// The other shape the publication has taken: an object keyed by set name.
	path := filepath.Join(t.TempDir(), "codes.json")
	body := `{
  "ExternalPurposeCode": [
    {"code": "SALA", "name": "Salary Payment", "definition": "Transaction is the payment of salaries."},
    {"code": "", "name": "skipped"}
  ],
  "ExternalStatusReason1Code": [
    {"code": "AC04", "name": "Closed Account Number"}
  ]
}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	if sets.Total() != 2 {
		t.Errorf("Total() = %d, want 2", sets.Total())
	}
	if len(sets.SetNames()) != 2 {
		t.Errorf("SetNames() = %v", sets.SetNames())
	}
}

func TestImportRejectsUnknownShapes(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "codes.json")
	if err := os.WriteFile(bad, []byte(`"just a string"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := codes.ImportExternalSets(bad); err == nil {
		t.Error("JSON of an unrecognised shape was accepted")
	}

	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := codes.ImportExternalSets(empty); err == nil {
		t.Error("an empty publication was accepted")
	}

	// A format AskISO does not read has to say where to get one it does.
	other := filepath.Join(dir, "codes.csv")
	if err := os.WriteFile(other, []byte("a,b"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := codes.ImportExternalSets(other)
	if err == nil {
		t.Fatal("a .csv was accepted")
	}
	if !strings.Contains(err.Error(), "iso20022.org") {
		t.Errorf("the error does not say where to download the publication: %v", err)
	}

	if _, err := codes.ImportExternalSets(filepath.Join(dir, "absent.xlsx")); err == nil {
		t.Error("a missing file was accepted")
	}
}

func TestSearchAndLookup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codes.xlsx")
	writeSpreadsheet(t, path,
		[]string{"Code Set", "Code Value", "Code Name", "Code Definition"}, publicationRows)

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatal(err)
	}

	// An exact code leads, whatever else matches.
	got := sets.Search("SALA")
	if len(got) == 0 || got[0].Code != "SALA" {
		t.Errorf("Search(SALA) = %+v", got)
	}
	// Descriptive text matches too, which is how someone finds a code they
	// cannot name.
	if got := sets.Search("supplier"); len(got) != 1 {
		t.Errorf("Search(supplier) = %+v", got)
	}
	// A set name matches every member.
	if got := sets.Search("ExternalPurposeCode"); len(got) != 2 {
		t.Errorf("Search by set name = %+v", got)
	}
	if got := sets.Search("  "); got != nil {
		t.Errorf("Search of nothing = %+v", got)
	}
	if got := sets.Lookup("nope"); got != nil {
		t.Errorf("Lookup of an unknown code = %+v", got)
	}
}

func TestNilPublicationIsSafe(t *testing.T) {
	// Every caller treats a catalogue with no import as "not imported" rather
	// than as a failure, so the zero value has to behave.
	var none *codes.ExternalSets
	if none.Total() != 0 || none.SetNames() != nil || none.Set("x") != nil ||
		none.Lookup("x") != nil || none.Search("x") != nil {
		t.Error("the nil publication does not behave as empty")
	}
}

func TestSaveAndLoad(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "codes.xlsx")
	writeSpreadsheet(t, path,
		[]string{"Code Set", "Code Value", "Code Name", "Code Definition"},
		append(publicationRows, []string{
			"ExternalPurposeCode", "TABS",
			"Tabbed\tName", "A definition\nwith a newline",
		}))

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := codes.SaveExternalSets(root, sets)
	if err != nil {
		t.Fatalf("saving: %v", err)
	}
	if stored != codes.ExternalCodesPath(root) {
		t.Errorf("stored at %q", stored)
	}

	loaded, err := codes.LoadExternalSets(root)
	if err != nil {
		t.Fatalf("loading: %v", err)
	}
	if loaded.Total() != sets.Total() {
		t.Errorf("loaded %d of %d codes", loaded.Total(), sets.Total())
	}
	if loaded.Format != sets.Format || loaded.Publication != sets.Publication || loaded.SHA256 != sets.SHA256 {
		t.Errorf("stored provenance changed: got %s/%s/%s, want %s/%s/%s",
			loaded.Format, loaded.Publication, loaded.SHA256, sets.Format, sets.Publication, sets.SHA256)
	}

	// A tab or a newline in a definition would otherwise split the row.
	tabs := loaded.Lookup("TABS")
	if len(tabs) != 1 {
		t.Fatalf("the row with a tab in it did not survive: %+v", loaded.Codes)
	}
	if strings.Contains(tabs[0].Name, "\t") || strings.Contains(tabs[0].Definition, "\n") {
		t.Errorf("the separators were not removed: %+v", tabs[0])
	}
	if !strings.Contains(tabs[0].Definition, "with a newline") {
		t.Errorf("the definition was lost: %+v", tabs[0])
	}

	// A catalogue with no import is not an error.
	if got, err := codes.LoadExternalSets(t.TempDir()); err != nil || got != nil {
		t.Errorf("an empty catalogue returned %v, %v", got, err)
	}
	if got, err := codes.LoadExternalSets(""); err != nil || got != nil {
		t.Errorf("an empty root returned %v, %v", got, err)
	}
	if _, err := codes.SaveExternalSets(root, nil); err == nil {
		t.Error("saving nothing was accepted")
	}
}

func TestLoadIgnoresCommentsAndShortRows(t *testing.T) {
	root := t.TempDir()
	path := codes.ExternalCodesPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "# a comment\n# format=tsv ignored-field publication=manual sha256=test\n\nExternalPurposeCode\tSALA\tSalary Payment\tPays salaries\n" +
		"tooshort\n" +
		"ExternalPurposeCode\tSUPP\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	loaded, err := codes.LoadExternalSets(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Total() != 2 {
		t.Errorf("Total() = %d, want 2: %+v", loaded.Total(), loaded.Codes)
	}
	if loaded.Format != "tsv" || loaded.Publication != "manual" || loaded.SHA256 != "test" {
		t.Fatalf("stored header metadata = %s/%s/%s", loaded.Format, loaded.Publication, loaded.SHA256)
	}
	// A row with only a set and a code is still a code.
	if got := loaded.Lookup("SUPP"); len(got) != 1 || got[0].Name != "" {
		t.Errorf("Lookup(SUPP) = %+v", got)
	}

	// A file holding only comments is the same as none.
	other := t.TempDir()
	commentsOnly := codes.ExternalCodesPath(other)
	if err := os.MkdirAll(filepath.Dir(commentsOnly), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(commentsOnly, []byte("# nothing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := codes.LoadExternalSets(other); err != nil || got != nil {
		t.Errorf("a file of comments returned %v, %v", got, err)
	}
}

func TestExternalSetsForCaches(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "codes.xlsx")
	writeSpreadsheet(t, path,
		[]string{"Code Set", "Code Value"}, [][]string{{"ExternalPurposeCode", "SALA"}})

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codes.SaveExternalSets(root, sets); err != nil {
		t.Fatal(err)
	}

	first := codes.ExternalSetsFor(root)
	if first.Total() != 1 {
		t.Fatalf("Total() = %d", first.Total())
	}
	// Cached state is returned as an independent snapshot: callers cannot
	// mutate the publication shared by later lookups.
	second := codes.ExternalSetsFor(root)
	if second == first || second.Total() != first.Total() {
		t.Error("cached publication was not returned as an independent snapshot")
	}
	first.Codes[0].Code = "CALLER-MUTATION"
	if fresh := codes.ExternalSetsFor(root); fresh.Codes[0].Code == "CALLER-MUTATION" {
		t.Error("caller mutation contaminated the cached publication")
	}

	// A fresh import has to be visible without restarting.
	writeSpreadsheet(t, path, []string{"Code Set", "Code Value"},
		[][]string{{"ExternalPurposeCode", "SALA"}, {"ExternalPurposeCode", "SUPP"}})
	updated, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codes.SaveExternalSets(root, updated); err != nil {
		t.Fatal(err)
	}
	codes.ForgetExternalSets(root)

	if got := codes.ExternalSetsFor(root); got.Total() != 2 {
		t.Errorf("after re-importing, Total() = %d, want 2", got.Total())
	}
	if codes.ExternalSetsFor("") != nil {
		t.Error("an empty root returned a publication")
	}
}

func TestImportRefusesAnOversizedFile(t *testing.T) {
	// The real publication is a few megabytes. A limit stops an unrelated
	// archive being read into memory whole.
	path := filepath.Join(t.TempDir(), "huge.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(65 << 20); err != nil {
		_ = f.Close()
		t.Skipf("cannot create a sparse file here: %v", err)
	}
	_ = f.Close()

	_, err = codes.ImportExternalSets(path)
	if err == nil {
		t.Fatal("an oversized file was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("the error does not mention the limit: %v", err)
	}
}

func TestSpreadsheetWithInlineAndNumericCells(t *testing.T) {
	// Not every producer uses the shared string table: a cell may carry its
	// text inline, or a number with no type at all.
	dir := t.TempDir()
	path := filepath.Join(dir, "inline.xlsx")

	sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="A1" t="inlineStr"><is><t>Code Set</t></is></c>` +
		`<c r="B1" t="inlineStr"><is><t>Code Value</t></is></c>` +
		`<c r="C1" t="inlineStr"><is><t>Code Name</t></is></c></row>` +
		`<row r="2"><c r="A2" t="inlineStr"><is><t>ExternalPurposeCode</t></is></c>` +
		`<c r="B2" t="inlineStr"><is><t>SALA</t></is></c>` +
		`<c r="C2"><v>42</v></c></row>` +
		`</sheetData></worksheet>`

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	// No shared string table at all.
	w, err := zw.Create("xl/worksheets/sheet3.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(sheet)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	got := sets.Lookup("SALA")
	if len(got) != 1 {
		t.Fatalf("Lookup found %d: %+v", len(got), sets.Codes)
	}
	// A numeric cell keeps its digits rather than being read as a string index.
	if got[0].Name != "42" {
		t.Errorf("the numeric cell became %q", got[0].Name)
	}
}

func TestSpreadsheetWithABadStringIndex(t *testing.T) {
	// A cell pointing past the end of the shared table is a corrupt file, and
	// the row is skipped rather than crashing the import.
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.xlsx")

	shared := `<?xml version="1.0"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
		`<si><t>Code Set</t></si><si><t>Code Value</t></si>` +
		`<si><t>ExternalPurposeCode</t></si><si><t>SALA</t></si></sst>`
	sheet := `<?xml version="1.0"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
		`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="B1" t="s"><v>1</v></c></row>` +
		`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" t="s"><v>3</v></c></row>` +
		`<row r="3"><c r="A3" t="s"><v>999</v></c><c r="B3" t="s"><v>not-a-number</v></c></row>` +
		`</sheetData></worksheet>`

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"xl/sharedStrings.xml":     shared,
		"xl/worksheets/sheet1.xml": sheet,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatalf("importing: %v", err)
	}
	if sets.Total() != 1 {
		t.Errorf("Total() = %d, want the one intact row: %+v", sets.Total(), sets.Codes)
	}
}

func TestSaveIntoAnUnwritablePath(t *testing.T) {
	// A catalogue root that is a file cannot hold a publication.
	root := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(root, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "codes.xlsx")
	writeSpreadsheet(t, path,
		[]string{"Code Set", "Code Value"}, [][]string{{"ExternalPurposeCode", "SALA"}})
	sets, err := codes.ImportExternalSets(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := codes.SaveExternalSets(root, sets); err == nil {
		t.Error("saving into a file was accepted")
	}
}

func TestLoadReportsAnUnreadableFile(t *testing.T) {
	root := t.TempDir()
	path := codes.ExternalCodesPath(root)
	// A directory where the file should be: readable as a path, not as a file.
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := codes.LoadExternalSets(root); err == nil {
		t.Error("a directory was read as a publication")
	}
	// And the cached accessor treats that as "not imported" rather than
	// failing a lookup that never needed it.
	if got := codes.ExternalSetsFor(root); got != nil {
		t.Errorf("ExternalSetsFor returned %v", got)
	}
}

func TestExternalSetsIsStale(t *testing.T) {
	now := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	maxAge := 180 * 24 * time.Hour

	var nilSets *codes.ExternalSets
	if stale, age := nilSets.IsStale(maxAge, now); stale || age != 0 {
		t.Errorf("nil IsStale = (%v, %v), want (false, 0)", stale, age)
	}

	sets := &codes.ExternalSets{Publication: "1Q2026"}
	if stale, age := sets.IsStale(0, now); stale || age != 0 {
		t.Errorf("zero maxAge IsStale = (%v, %v), want (false, 0)", stale, age)
	}

	unparsed := &codes.ExternalSets{Publication: "unknown"}
	if stale, age := unparsed.IsStale(maxAge, now); stale || age != 0 {
		t.Errorf("unparsed IsStale = (%v, %v), want (false, 0)", stale, age)
	}

	fresh := &codes.ExternalSets{Publication: "4Q2026"}
	if stale, _ := fresh.IsStale(maxAge, now); stale {
		t.Errorf("4Q2026 should not be stale at %v", now)
	}

	staleSet := &codes.ExternalSets{Publication: "1Q2025/v2"}
	if stale, age := staleSet.IsStale(maxAge, now); !stale || age <= maxAge {
		t.Errorf("1Q2025/v2 IsStale = (%v, %v), want (true, >%v)", stale, age, maxAge)
	}
}
