// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/converter"
)

func stubPDFExtraction(t *testing.T, extract func(string, string, bool) (string, error)) {
	t.Helper()
	oldFind, oldRun := findPDFTextTool, runPDFTextTool
	findPDFTextTool = func(string) (string, error) { return "/local/pdftotext", nil }
	runPDFTextTool = extract
	t.Cleanup(func() {
		findPDFTextTool, runPDFTextTool = oldFind, oldRun
	})
}

const syntheticUGText = `
CBPRPlus-pacs.008.001.08_FIToFICustomerCreditTransfer
Business Service swift.cbprplus.03

ISO Index  Or  Level  Message Item  <XML Tag>  Occ  Data Type
1.0            Message root <Document> <FIToFICstmrCdtTrf> [1..1]
1.1            Group Header <GrpHdr> [1..1]
1.1.1          Message Identification <MsgId> [1..1] Max35Text
1.2            Credit Transfer Transaction Information <CdtTrfTxInf> [1..*]
1.2.1          Payment Identification <PmtId> [1..1]
1.2.1.1        Instruction Identification <InstrId> [0..1] Max16Text
1.3       (Or  Choice A <ChoiceA> [1..1] Max4Text
1.4       Or)  Choice B <ChoiceB> [1..1] Max4Text
`

func TestParseCBPRPDFTextCompilesHierarchyCardinalityAndTypes(t *testing.T) {
	source, constraints, warnings := parseCBPRPDFText(
		"CBPRPlus-pacs.008.001.08_FIToFICustomerCreditTransfer.pdf",
		strings.Repeat("a", 64), syntheticUGText)
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	if source.MessageID != "pacs.008.001.08" || len(source.UsageIdentifiers) != 1 || source.UsageIdentifiers[0] != "swift.cbprplus.03" {
		t.Fatalf("wrong source metadata: %+v", source)
	}
	if source.Constraints != 7 || len(constraints) != 7 {
		t.Fatalf("constraints = %d/%d, want 7", source.Constraints, len(constraints))
	}
	var msgID, choice *CBPRPackConstraint
	for i := range constraints {
		c := &constraints[i]
		switch c.Path[len(c.Path)-1] {
		case "MsgId":
			msgID = c
		case "ChoiceA":
			choice = c
		}
	}
	if msgID == nil || strings.Join(msgID.Path, "/") != "Document/FIToFICstmrCdtTrf/GrpHdr/MsgId" || msgID.Min != 1 || msgID.MaxLength != 35 {
		t.Fatalf("wrong MsgId constraint: %+v", msgID)
	}
	if choice == nil || choice.Min != 0 {
		t.Fatalf("choice alternative lower bound must not be enforced independently: %+v", choice)
	}
}

func TestPrimaryMessageIDAcceptsMyStandardsFilenameSeparators(t *testing.T) {
	for _, name := range []string{"CBPRPlus-admi_024_001_01_Guide.pdf", "CBPRPlus-admi-024-001-01-Guide.pdf"} {
		id, _ := primaryMessageID(name, "pacs.008.001.08 also appears in prose")
		if id != "admi.024.001.01" {
			t.Fatalf("primaryMessageID(%q) = %q", name, id)
		}
	}
}

func TestCBPRPackAugmentsEmbeddedProfile(t *testing.T) {
	_, constraints, _ := parseCBPRPDFText(
		"CBPRPlus-pacs.008.001.08_FIToFICustomerCreditTransfer.pdf",
		strings.Repeat("b", 64), syntheticUGText)
	pack := &CBPRPack{
		Format: cbprPackFormat,
		Sources: []CBPRPackSource{{
			Name: "pacs.008.pdf", SHA256: strings.Repeat("b", 64),
			MessageID: "pacs.008.001.08", UsageIdentifiers: []string{"swift.cbprplus.03"},
			Constraints: len(constraints),
		}},
		Constraints: constraints,
	}
	pack.normalise()
	pack.Fingerprint = packFingerprint(pack)
	base, err := Get("cbpr-plus")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := pack.Augment(base)
	if err != nil {
		t.Fatal(err)
	}
	xml := `<Envelope xmlns="urn:test"><AppHdr xmlns="urn:iso:std:iso:20022:tech:xsd:head.001.001.02"><BizSvc>swift.cbprplus.03</BizSvc></AppHdr><Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><FIToFICstmrCdtTrf><GrpHdr/></FIToFICstmrCdtTrf></Document></Envelope>`
	root, err := converter.Parse([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	result := Run(profile, root, "pacs.008.001.08", "payment.xml")
	found := false
	for _, finding := range result.Findings {
		if strings.HasPrefix(finding.RuleID, "CBPR-PACK-") && strings.HasSuffix(finding.Path, "/GrpHdr/MsgId") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing locally compiled cardinality finding: %+v", result.Findings)
	}
	if result.Pack == nil || result.Pack.Constraints != len(constraints) || result.Pack.Coverage == "" {
		t.Fatalf("pack provenance missing from result: %+v", result.Pack)
	}
}

func TestCBPRPackVariantDispatch(t *testing.T) {
	for name, want := range map[string]string{
		"CBPRPlus-pacs.009.001.08_COV_FinancialInstitutionCreditTransfer.pdf": "swift.cbprplus.cov.03",
		"CBPRPlus-pacs.009.001.08_ADV_FinancialInstitutionCreditTransfer.pdf": "swift.cbprplus.adv.03",
		"CBPRPlus-pacs.008.001.08_STP_FIToFICustomerCreditTransfer.pdf":       "swift.cbprplus.stp.03",
		"CBPRPlus-pacs.010.001.03_Margin_Collection.pdf":                      "swift.cbprplus.col.02",
		"CBPRPlus-camt.105.001.02_Multiple.pdf":                               "swift.cbprplus.mlp.02",
	} {
		messageID, _ := primaryMessageID(name, "")
		got := usageIdentifiers(name, "", messageID)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s: got %v, want %s", name, got, want)
		}
	}
	got := usageIdentifiers(
		"CBPRPlus-pacs.009.001.08_ADV_FinancialInstitutionCreditTransfer.pdf",
		"This prose discusses a cover payment before the selected variant.",
		"pacs.009.001.08")
	if len(got) != 1 || got[0] != "swift.cbprplus.adv.03" {
		t.Fatalf("filename ADV marker must beat body prose: %v", got)
	}
	for _, test := range []struct {
		name, text, want string
	}{
		{"generic.pdf", "Single value: swift.cbprplus.cov.03", "swift.cbprplus.cov.03"},
		{"generic.pdf", "This is the advice variant.", "swift.cbprplus.adv.03"},
		{"generic.pdf", "No variant marker is present.", "swift.cbprplus.03"},
	} {
		got = usageIdentifiers(test.name, test.text, "pacs.009.001.08")
		if len(got) != 1 || got[0] != test.want {
			t.Errorf("usageIdentifiers(%q, %q) = %v, want %s", test.name, test.text, got, test.want)
		}
	}
}

func TestCBPRSR2025InventoryIncludesCurrentCamt109Service(t *testing.T) {
	guidelines := CBPRSR2025UsageGuidelines()
	if len(guidelines) != 31 {
		t.Fatalf("SR2025 Usage Guidelines = %d, want 31", len(guidelines))
	}
	for _, guideline := range guidelines {
		if guideline.MessageID == "camt.109.001.01" {
			if guideline.UsageIdentifier != "swift.cbprplus.03" {
				t.Fatalf("camt.109 Business Service = %q", guideline.UsageIdentifier)
			}
			return
		}
	}
	t.Fatal("camt.109.001.01 is absent from SR2025")
}

func TestCBPRPackRoundTripAndTamperDetection(t *testing.T) {
	pack := &CBPRPack{
		Format:  cbprPackFormat,
		Sources: []CBPRPackSource{{Name: "local.pdf", SHA256: strings.Repeat("c", 64), MessageID: "pacs.008.001.08", Constraints: 1}},
		Constraints: []CBPRPackConstraint{{
			Source: "local.pdf", MessageID: "pacs.008.001.08",
			UsageIdentifiers: []string{"swift.cbprplus.03"},
			Path:             []string{"Document", "FIToFICstmrCdtTrf", "GrpHdr"}, Min: 1, Max: 1,
		}},
	}
	path := filepath.Join(t.TempDir(), "sr2025.cbpr-pack.json")
	if err := WriteCBPRPack(path, pack); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCBPRPack(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Fingerprint == "" || len(loaded.Constraints) != 1 {
		t.Fatalf("bad round trip: %+v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"min": 1`, `"min": 0`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCBPRPack(path); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("tampered pack should fail its fingerprint: %v", err)
	}
}

func TestWriteCBPRPackProtectsExistingDestination(t *testing.T) {
	pack := &CBPRPack{
		Format: cbprPackFormat,
		Constraints: []CBPRPackConstraint{{
			MessageID: "pacs.008.001.08", Path: []string{"Document", "GrpHdr"}, Min: 0, Max: 1,
		}},
	}
	path := filepath.Join(t.TempDir(), "private.cbpr-pack.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteCBPRPack(path, pack); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); runtime.GOOS != "windows" && got != 0o600 {
		t.Fatalf("compiled pack mode = %o, want 600", got)
	}
}

func TestWriteCBPRPackRejectsSymlinkDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not generally available to unprivileged Windows tests")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "private.cbpr-pack.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	pack := &CBPRPack{Format: cbprPackFormat, Constraints: []CBPRPackConstraint{{
		MessageID: "pacs.008.001.08", Path: []string{"Document", "GrpHdr"}, Max: 1,
	}}}
	if err := WriteCBPRPack(link, pack); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink destination should be rejected: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "do not replace" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
}

func TestLoadCBPRPackRejectsUnsafeConstraints(t *testing.T) {
	pack := &CBPRPack{
		Format: cbprPackFormat,
		Constraints: []CBPRPackConstraint{{
			MessageID: "pacs.008.001.08", Path: []string{"Document", "../Secret"}, Min: 0, Max: 1,
		}},
	}
	path := filepath.Join(t.TempDir(), "unsafe.cbpr-pack.json")
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCBPRPack(path); err == nil || !strings.Contains(err.Error(), "invalid path component") {
		t.Fatalf("unsafe path should be rejected: %v", err)
	}
}

func TestRankCBPRPagesReturnsFocusedLocalEvidence(t *testing.T) {
	text := "General introduction to payment messages.\f" +
		"The UETR is carried in pacs.008 payment identification. The UETR supports tracking.\f" +
		"Unrelated reporting material."
	hits := rankCBPRPages("local-guide.pdf", text, "Where is the UETR in pacs.008?")
	if len(hits) == 0 {
		t.Fatal("expected a local evidence hit")
	}
	if hits[0].Page != 2 || !strings.Contains(hits[0].Snippet, "UETR") {
		t.Fatalf("wrong top hit: %+v", hits[0])
	}
	if hits[0].Source != "local-guide.pdf" || strings.Contains(hits[0].Source, "/") {
		t.Fatalf("source should expose only the basename: %+v", hits[0])
	}
}

func TestSearchTermsDropsNoiseAndKeepsMessageIdentifiers(t *testing.T) {
	got := searchTerms("What is the UETR for pacs.008.001.08 and where is it?")
	want := []string{"uetr", "pacs.008.001.08"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("terms = %v, want %v", got, want)
	}
}

func TestRankCBPRPagesRequiresDistinctQueryCoverage(t *testing.T) {
	text := strings.Repeat("mandatory ", 12) + "\f" +
		"GroupHeader minimum occurrence changed to one and is mandatory."
	hits := rankCBPRPages("local.pdf", text, "GroupHeader minimum occurrence changed mandatory")
	if len(hits) != 1 || hits[0].Page != 2 {
		t.Fatalf("single generic match should be excluded from multi-term evidence: %+v", hits)
	}
}

func TestCompileCBPRPackFromPrivatePDFDirectory(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"CBPRPlus-pacs.008.001.08_FIToFICustomerCreditTransfer.pdf",
		"CBPRPlus-pacs.008.001.08_FIToFICustomerCreditTransfer_copy.pdf",
		"SR2025_CBPRplus_Release_Note.pdf",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("private fixture "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubPDFExtraction(t, func(_ string, path string, preservePages bool) (string, error) {
		if !preservePages {
			t.Fatal("compiler should retain page breaks for structure parsing")
		}
		if strings.Contains(path, "Release_Note") {
			return "pacs.008.001.08 pacs.009.001.08 supporting document", nil
		}
		return syntheticUGText, nil
	})
	pack, err := CompileCBPRPack(dir)
	if err != nil {
		t.Fatal(err)
	}
	info := pack.Info()
	if info.Sources != 3 || info.UsageGuidelines != 1 || info.Constraints != 7 || info.Fingerprint == "" {
		t.Fatalf("wrong compiled summary: %+v", info)
	}
	if len(info.Warnings) < 2 {
		t.Fatalf("coverage and supporting-document warnings are required: %+v", info.Warnings)
	}
	single, err := CompileCBPRPack(filepath.Join(dir, "CBPRPlus-pacs.008.001.08_FIToFICustomerCreditTransfer.pdf"))
	if err != nil || len(single.Constraints) != 7 {
		t.Fatalf("single PDF source should compile: %+v, %v", single, err)
	}
	loadedPath := filepath.Join(t.TempDir(), "compiled.cbpr-pack.json")
	if err := WriteCBPRPack(loadedPath, pack); err != nil {
		t.Fatal(err)
	}
	loaded, err := CompileCBPRPack(loadedPath)
	if err != nil || loaded.Fingerprint != pack.Fingerprint {
		t.Fatalf("compiled JSON source should load: %+v, %v", loaded, err)
	}
}

func TestCompileCBPRPackSkipsIdenticalPDFs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"CBPRPlus-pacs.008.001.08_A.pdf", "CBPRPlus-pacs.008.001.08_B.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("identical"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubPDFExtraction(t, func(_ string, _ string, _ bool) (string, error) {
		return syntheticUGText, nil
	})
	pack, err := CompileCBPRPack(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Sources) != 1 || len(pack.Warnings) != 2 || !strings.Contains(strings.Join(pack.Warnings, " "), "same SHA-256") {
		t.Fatalf("duplicate PDF should be skipped and disclosed: %+v", pack.Info())
	}
}

func TestParseStructureRowsUsesHierarchyAndUsageOverrides(t *testing.T) {
	text := `
2             Structure
                    BusinessApplicationHeaderV02           <AppHdr>                 [1..1]
                     From                                  <Fr>                      [1..1]  9
                      FinancialInstitutionIdentification   <FIId>              {Or  [1..1]  75
                     Related                               <Rltd>                    [0..*] R[0..1] 14
                      CharacterSet                         <CharSet>                 [0..1] 25
` + "\f" + `04 September 2026
                        CopyDuplicate                     <CpyDplct>                [0..1] 29
                     NotificationOfCorrespondenceV01      <NtfctnOfCrspdc>          [1..1] ! 16
2.4.1                 GroupHeader                         <GrpHdr>                  [0..1] R[1..1] 15
6.1.12.5.1             MessageIdentification              <MsgId>                   [1..1] 63
` + "\f" + `04 September 2026
6.1.12.5.2             CreationDateTime                   <CreDtTm>                 [1..1] 63
	3             ISO 20022 Rules
`
	rows := parsePackRows(text)
	if len(rows) != 10 {
		t.Fatalf("rows = %d, want 10: %+v", len(rows), rows)
	}
	want := map[string]struct{ min, max int }{
		"AppHdr/Fr":                     {1, 1},
		"AppHdr/Fr/FIId":                {1, 1},
		"AppHdr/Rltd":                   {0, 1},
		"AppHdr/Rltd/CharSet":           {0, 1},
		"AppHdr/Rltd/CpyDplct":          {0, 1},
		"NtfctnOfCrspdc/GrpHdr":         {1, 1},
		"NtfctnOfCrspdc/GrpHdr/MsgId":   {1, 1},
		"NtfctnOfCrspdc/GrpHdr/CreDtTm": {1, 1},
	}
	for _, row := range rows {
		key := strings.Join(row.path, "/")
		expected, ok := want[key]
		if !ok {
			continue // root rows
		}
		if row.min != expected.min || row.max != expected.max {
			t.Errorf("%s occurrence = %d..%d, want %d..%d", key, row.min, row.max, expected.min, expected.max)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing paths: %v", want)
	}
	source, constraints, warnings := parseCBPRPDFText(
		"CBPRPlus-admi.024.001.01_NotificationOfCorrespondence.pdf",
		strings.Repeat("c", 64), text)
	if len(warnings) != 0 || source.MessageID != "admi.024.001.01" || len(constraints) != 8 {
		t.Fatalf("structure compilation = %+v, %d constraints, warnings %v", source, len(constraints), warnings)
	}
	for _, constraint := range constraints {
		if strings.Join(constraint.Path, "/") == "AppHdr/Rltd" && constraint.Max != 1 {
			t.Fatalf("usage override was not compiled: %+v", constraint)
		}
	}
}

func TestParseStructureRowsNeedsTopLevelRoot(t *testing.T) {
	text := "2 Structure\n                     Child <Child> [0..1] 9\n3 ISO 20022 Rules\n"
	if rows := parseStructureRows(text); len(rows) != 0 {
		t.Fatalf("unrooted structure rows must not become constraints: %+v", rows)
	}
	if rows := parseStructureRows("2 Structure\n3 ISO 20022 Rules\n"); len(rows) != 0 {
		t.Fatalf("empty structure should have no rows: %+v", rows)
	}
	huge := strings.Repeat("9", 1000)
	malformed := "2 Structure\n" +
		"                    Root <AppHdr> [1..1]\n" +
		"                     BadMin <BadMin> [" + huge + "..1] 9\n" +
		"                     BadMax <BadMax> [0.." + huge + "] 9\n" +
		"3 ISO 20022 Rules\n"
	if rows := parseStructureRows(malformed); len(rows) != 1 || strings.Join(rows[0].path, "/") != "AppHdr" {
		t.Fatalf("overflowing occurrences should be ignored safely: %+v", rows)
	}
}

func TestStructureItemIndentUsesMessageItemAfterIndex(t *testing.T) {
	indexed := "6.1.12.5.1             MessageIdentification <MsgId> [1..1] 63"
	if got := structureItemIndent(indexed); got != strings.Index(indexed, "MessageIdentification") {
		t.Fatalf("indexed item indent = %d", got)
	}
	plain := "                    Payload <Payload> [1..1]"
	if got := structureItemIndent(plain); got != 20 {
		t.Fatalf("plain item indent = %d", got)
	}
}

func TestSearchCBPRPackStaysOnLocalPDFs(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "local-guide.pdf")
	if err := os.WriteFile(pdf, []byte("private fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	stubPDFExtraction(t, func(tool, path string, preservePages bool) (string, error) {
		if tool != "/local/pdftotext" || path != pdf || !preservePages {
			t.Fatalf("unexpected local extractor call: %q %q %v", tool, path, preservePages)
		}
		return "Introduction.\fUETR is mandatory for this payment.\fAnother UETR explanation.", nil
	})
	hits, err := SearchCBPRPack(dir, "Where is UETR mandatory?", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Page != 2 || hits[0].Source != "local-guide.pdf" {
		t.Fatalf("wrong local hits: %+v", hits)
	}
}

func TestCBPRPackInputErrorsAreExplicit(t *testing.T) {
	if _, err := CompileCBPRPack(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("missing source should fail")
	}
	txt := filepath.Join(t.TempDir(), "guide.txt")
	if err := os.WriteFile(txt, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileCBPRPack(txt); err == nil || !strings.Contains(err.Error(), "must be a PDF") {
		t.Fatalf("wrong extension should fail clearly: %v", err)
	}
	empty := t.TempDir()
	if _, err := CompileCBPRPack(empty); err == nil || !strings.Contains(err.Error(), "no PDF") {
		t.Fatalf("empty directory should fail clearly: %v", err)
	}

	tooMany := t.TempDir()
	for i := 0; i <= maxCBPRPDFs; i++ {
		name := filepath.Join(tooMany, fmt.Sprintf("%03d.pdf", i))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := CompileCBPRPack(tooMany); err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("too many PDFs should fail: %v", err)
	}
	if _, err := SearchCBPRPack(tooMany, "question", 5); err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("too many search PDFs should fail: %v", err)
	}
}

func TestCBPRPackToolAndExtractionErrorsAreExplicit(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "guide.pdf")
	if err := os.WriteFile(pdf, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldFind, oldRun := findPDFTextTool, runPDFTextTool
	t.Cleanup(func() { findPDFTextTool, runPDFTextTool = oldFind, oldRun })
	findPDFTextTool = func(string) (string, error) { return "", errors.New("absent") }
	if _, err := CompileCBPRPack(dir); err == nil || !strings.Contains(err.Error(), "Poppler") {
		t.Fatalf("missing tool should fail clearly: %v", err)
	}
	findPDFTextTool = func(string) (string, error) { return "tool", nil }
	runPDFTextTool = func(string, string, bool) (string, error) { return "", errors.New("encrypted") }
	if _, err := CompileCBPRPack(dir); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("extractor error should propagate: %v", err)
	}
	runPDFTextTool = func(string, string, bool) (string, error) { return "no tables here", nil }
	if _, err := CompileCBPRPack(dir); err == nil || !strings.Contains(err.Error(), "no enforceable") {
		t.Fatalf("zero constraints should fail explicitly: %v", err)
	}
}

func TestSearchCBPRPackInputErrors(t *testing.T) {
	if _, err := SearchCBPRPack("missing", "question", 5); err == nil {
		t.Error("missing search source should fail")
	}
	if _, err := SearchCBPRPack("missing", "", 5); err == nil {
		t.Error("empty question should fail before source access")
	}
	if _, err := SearchCBPRPack("missing", "question", 0); err == nil {
		t.Error("bad limit should fail before source access")
	}
	compiled := filepath.Join(t.TempDir(), "private.cbpr-pack.json")
	if err := os.WriteFile(compiled, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SearchCBPRPack(compiled, "question", 5); err == nil || !strings.Contains(err.Error(), "no document prose") {
		t.Fatalf("compiled pack search should explain its privacy design: %v", err)
	}
	empty := t.TempDir()
	if _, err := SearchCBPRPack(empty, "question", 5); err == nil || !strings.Contains(err.Error(), "no PDF") {
		t.Fatalf("empty search directory should fail: %v", err)
	}
}

func TestSearchCBPRPackToolExtractionAndSizeErrors(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "guide.pdf")
	if err := os.WriteFile(pdf, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldFind, oldRun := findPDFTextTool, runPDFTextTool
	t.Cleanup(func() { findPDFTextTool, runPDFTextTool = oldFind, oldRun })
	findPDFTextTool = func(string) (string, error) { return "", errors.New("absent") }
	if _, err := SearchCBPRPack(dir, "question", 5); err == nil || !strings.Contains(err.Error(), "Poppler") {
		t.Fatalf("missing search tool should fail: %v", err)
	}
	findPDFTextTool = func(string) (string, error) { return "tool", nil }
	runPDFTextTool = func(string, string, bool) (string, error) { return "", errors.New("extract failed") }
	if _, err := SearchCBPRPack(dir, "question", 5); err == nil || !strings.Contains(err.Error(), "extract failed") {
		t.Fatalf("search extraction error should propagate: %v", err)
	}
	if err := os.Truncate(pdf, maxCBPRPDFSize+1); err != nil {
		t.Fatal(err)
	}
	if _, err := SearchCBPRPack(dir, "question", 5); err == nil || !strings.Contains(err.Error(), "per-PDF safety limit") {
		t.Fatalf("oversized search PDF should fail: %v", err)
	}
	if _, err := CompileCBPRPack(dir); err == nil || !strings.Contains(err.Error(), "per-PDF safety limit") {
		t.Fatalf("oversized compiler PDF should fail: %v", err)
	}
}

func TestCappedBufferAndLexicalRestrictions(t *testing.T) {
	buffer := cappedBuffer{max: 3}
	if _, err := buffer.Write([]byte("abcd")); err == nil {
		t.Error("oversize extracted text should fail")
	}
	if _, err := buffer.Write([]byte("abc")); err != nil || buffer.buf.String() != "abc" {
		t.Fatalf("bounded write failed: %v, %q", err, buffer.buf.String())
	}
	if min, max, pattern := lexicalRestriction("Exact4NumericText"); min != 4 || max != 4 || pattern == "" {
		t.Fatalf("exact numeric restriction not compiled: %d %d %q", min, max, pattern)
	}
	if min, max, pattern := lexicalRestriction("OtherType"); min != 0 || max != 0 || pattern != "" {
		t.Fatalf("unknown type should remain unconstrained: %d %d %q", min, max, pattern)
	}
}

func TestExtractPDFSuccessAndFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable executable fixture is covered by integration tests on Unix")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "pdftotext")
	script := "#!/bin/sh\n" +
		"if [ \"$ASKISO_PDF_TEST\" = fail ]; then echo encrypted >&2; exit 2; fi\n" +
		"if [ \"$ASKISO_PDF_TEST\" = empty ]; then exit 0; fi\n" +
		"printf 'page one\\fpage two'\n"
	if err := os.WriteFile(tool, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	pdf := filepath.Join(dir, "private.pdf")
	if err := os.WriteFile(pdf, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ASKISO_PDF_TEST", "ok")
	text, err := extractPDF(tool, pdf, true)
	if err != nil || !strings.Contains(text, "page two") {
		t.Fatalf("extract success: %q, %v", text, err)
	}
	t.Setenv("ASKISO_PDF_TEST", "empty")
	if _, err := extractPDF(tool, pdf, false); err == nil || !strings.Contains(err.Error(), "no extractable text") {
		t.Fatalf("empty extraction should fail: %v", err)
	}
	t.Setenv("ASKISO_PDF_TEST", "fail")
	if _, err := extractPDF(tool, pdf, false); err == nil || !strings.Contains(err.Error(), "encrypted") {
		t.Fatalf("extractor stderr should be preserved: %v", err)
	}
}

func TestValidatePackConstraintRejectsInvalidData(t *testing.T) {
	valid := CBPRPackConstraint{
		MessageID: "pacs.008.001.08", UsageIdentifiers: []string{"swift.cbprplus.03"},
		Path: []string{"Document", "GrpHdr"}, Min: 0, Max: 1,
	}
	if err := validatePackConstraint(valid); err != nil {
		t.Fatalf("valid constraint rejected: %v", err)
	}
	cases := []CBPRPackConstraint{
		{MessageID: "pacs.008.001.99", Path: valid.Path, Max: 1},
		{MessageID: valid.MessageID, Path: []string{"Document"}, Max: 1},
		{MessageID: valid.MessageID, Path: []string{"Document", "Bad@Path"}, Max: 1},
		{MessageID: valid.MessageID, Path: valid.Path, Min: 2, Max: 1},
		{MessageID: valid.MessageID, Path: valid.Path, Max: 1, UsageIdentifiers: []string{"swift.cbprplus.cov.03"}},
		{MessageID: valid.MessageID, Path: valid.Path, Max: 1, Pattern: "["},
	}
	for i, constraint := range cases {
		if err := validatePackConstraint(constraint); err == nil {
			t.Errorf("case %d should fail: %+v", i, constraint)
		}
	}
}

func TestCheckPackValueRestrictions(t *testing.T) {
	node := &converter.Node{Name: "Value", Text: "AB"}
	tests := []struct {
		name string
		c    CBPRPackConstraint
		text string
		fail bool
	}{
		{"minimum", CBPRPackConstraint{MinLength: 3}, "AB", true},
		{"maximum", CBPRPackConstraint{MaxLength: 1}, "AB", true},
		{"pattern", CBPRPackConstraint{Pattern: `^[0-9]+$`}, "AB", true},
		{"values", CBPRPackConstraint{Values: []string{"CD", "EF"}}, "AB", true},
		{"pass", CBPRPackConstraint{MinLength: 1, MaxLength: 3, Pattern: `^[A-Z]+$`, Values: []string{"AB"}}, "AB", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			node.Text = test.text
			finding := checkPackValue(test.c, node, "/Value")
			if (finding != nil) != test.fail {
				t.Fatalf("finding = %+v, fail=%v", finding, test.fail)
			}
		})
	}
	node.Children = []*converter.Node{{Name: "Nested"}}
	if finding := checkPackValue(CBPRPackConstraint{MinLength: 9}, node, "/Value"); finding != nil {
		t.Fatalf("complex element should not receive a text restriction: %+v", finding)
	}
}

func TestPackNilAndProfileErrors(t *testing.T) {
	var pack *CBPRPack
	if pack.Info() != nil {
		t.Error("nil pack info should be nil")
	}
	if _, err := pack.Augment(Profile{Name: "cbpr-plus"}); err == nil {
		t.Error("nil pack should not augment a profile")
	}
	if err := WriteCBPRPack(filepath.Join(t.TempDir(), "nil.cbpr-pack.json"), nil); err == nil {
		t.Error("nil pack should not be written")
	}
	nonNil := &CBPRPack{Format: cbprPackFormat}
	if _, err := nonNil.Augment(Profile{Name: "base"}); err == nil {
		t.Error("CBPR+ pack should not augment another profile")
	}
}

func TestLoadCBPRPackMalformedDocuments(t *testing.T) {
	dir := t.TempDir()
	tests := map[string]string{
		"bad.cbpr-pack.json":      `{`,
		"unknown.cbpr-pack.json":  `{"format":"other","constraints":[{}]}`,
		"empty.cbpr-pack.json":    `{"format":"askiso-cbpr-pack/v1"}`,
		"field.cbpr-pack.json":    `{"format":"askiso-cbpr-pack/v1","unexpected":true}`,
		"trailing.cbpr-pack.json": `{"format":"askiso-cbpr-pack/v1","constraints":[{}]} {}`,
	}
	for name, body := range tests {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCBPRPack(path); err == nil {
			t.Errorf("%s should fail", name)
		}
	}
	if _, err := LoadCBPRPack(filepath.Join(dir, "missing.cbpr-pack.json")); err == nil {
		t.Error("missing compiled pack should fail")
	}
	if _, err := LoadCBPRPack(dir); err == nil || !strings.Contains(err.Error(), "reading compiled CBPR+ pack") {
		t.Fatalf("directory used as a compiled pack should fail: %v", err)
	}
	large := filepath.Join(dir, "large.cbpr-pack.json")
	if err := os.WriteFile(large, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, maxCompiledPackSize+1); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCBPRPack(large); err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("oversized compiled pack should fail: %v", err)
	}
}

func TestPackHelpersCoverCardinalityAndLocalSnippets(t *testing.T) {
	if minInt(1, 2) != 1 || minInt(2, 1) != 1 {
		t.Fatal("minInt should return the smaller operand")
	}
	if firstN("ab", 3) != "ab" || firstN("abcd", 3) != "abc" {
		t.Fatal("firstN should preserve short values and truncate long values")
	}
	root := &converter.Node{Name: "Document", Children: []*converter.Node{{
		Name: "Parent", Children: []*converter.Node{{Name: "Value", Text: "TOO-LONG"}, {Name: "Value", Text: "ALSO-LONG"}},
	}}}
	constraints := []CBPRPackConstraint{{
		ID: "MAX", Path: []string{"Document", "Parent", "Value"}, Min: 0, Max: 1, MaxLength: 3,
	}, {
		ID: "UNBOUNDED", Path: []string{"Document", "Parent", "Missing"}, Min: 1, Max: -1,
	}}
	findings := checkPackConstraints(root, constraints)
	if len(findings) < 4 {
		t.Fatalf("expected max, lexical and unbounded findings: %+v", findings)
	}
	if got := findPathMatches(nil, []string{"A"}); got != nil {
		t.Fatalf("nil root should not match: %+v", got)
	}
	if got := findPathMatches(root, nil); got != nil {
		t.Fatalf("empty path should not match: %+v", got)
	}
	long := strings.Repeat("prefix ", 100) + "UETR focus " + strings.Repeat("suffix ", 100)
	snippet := localSnippet(long, strings.Index(long, "UETR"))
	if !strings.HasPrefix(snippet, "…") || !strings.HasSuffix(snippet, "…") {
		t.Fatalf("focused long snippet should mark both truncations: %q", snippet)
	}
	if hits := rankCBPRPages("local.pdf", "nothing relevant", "the and or"); len(hits) != 0 {
		t.Fatalf("stop-word-only query should have no hits: %+v", hits)
	}
}

func TestCBPRPDFPathsSkipsSymlinksAndRejectsOtherFiles(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "guideline.PDF")
	if err := os.WriteFile(pdf, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(pdf, filepath.Join(dir, "linked.pdf")); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := cbprPDFPaths(dir, info)
	if err != nil || len(paths) != 1 || paths[0] != pdf {
		t.Fatalf("PDF discovery = %v, %v", paths, err)
	}
	textFile := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(textFile, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	textInfo, err := os.Stat(textFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cbprPDFPaths(textFile, textInfo); err == nil {
		t.Fatal("a non-PDF source should be rejected")
	}
}

func TestPackFileAndWriteErrors(t *testing.T) {
	if _, err := fileSHA256(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("hashing missing input should fail")
	}
	pack := &CBPRPack{Format: cbprPackFormat, Constraints: []CBPRPackConstraint{{
		MessageID: "pacs.008.001.08", Path: []string{"Document", "GrpHdr"}, Max: 1,
	}}}
	if err := WriteCBPRPack(filepath.Join(t.TempDir(), "missing", "pack.cbpr-pack.json"), pack); err == nil {
		t.Error("writing below a missing directory should fail")
	}
	path := filepath.Join(t.TempDir(), "no-fingerprint.cbpr-pack.json")
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadCBPRPack(path)
	if err != nil || loaded.Fingerprint == "" {
		t.Fatalf("a valid pack without a stored fingerprint should calculate one: %+v, %v", loaded, err)
	}
}

func TestExtractPDFFailureWithoutDiagnostic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("executable fixture uses a Unix shell")
	}
	dir := t.TempDir()
	tool := filepath.Join(dir, "pdftotext")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 2\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := extractPDF(tool, filepath.Join(dir, "private.pdf"), true); err == nil || strings.Contains(err.Error(), ": \n") {
		t.Fatalf("silent extractor failure should still be reported: %v", err)
	}
}

func TestPackParserOverflowAndNumericText(t *testing.T) {
	huge := strings.Repeat("9", 1000)
	text := "1.0 Root <Document> [1..1]\n" +
		"1.1 TooLargeMin <BadMin> [" + huge + "..1]\n" +
		"1.2 TooLargeMax <BadMax> [0.." + huge + "]\n" +
		"1.3 XML Placeholder <XML> [0..1]\n"
	rows := parseIndexedPackRows(text)
	if len(rows) != 1 || rows[0].tags[0] != "Document" {
		t.Fatalf("overflow and placeholder rows should be ignored safely: %+v", rows)
	}
	if min, max, pattern := lexicalRestriction("Max12NumericText"); min != 0 || max != 12 || pattern != `^[0-9]+$` {
		t.Fatalf("maximum numeric text restriction = %d, %d, %q", min, max, pattern)
	}
}

func TestPackNormalisationAndRejectedAugmentConstraint(t *testing.T) {
	pack := &CBPRPack{Constraints: []CBPRPackConstraint{{
		MessageID: "not.a.message", Path: []string{"Document", "Value"}, Max: 1,
	}}}
	pack.normalise()
	if pack.Format != cbprPackFormat || pack.Constraints[0].ID == "" {
		t.Fatalf("normalisation did not fill stable metadata: %+v", pack)
	}
	if _, err := pack.Augment(Profile{Name: "cbpr-plus"}); err == nil || !strings.Contains(err.Error(), "not in the live") {
		t.Fatalf("invalid local constraint should not augment a profile: %v", err)
	}
}

func TestPackDispatchRequiresMatchingHeaderService(t *testing.T) {
	base, err := Get("cbpr-plus")
	if err != nil {
		t.Fatal(err)
	}
	pack := &CBPRPack{Format: cbprPackFormat, Constraints: []CBPRPackConstraint{{
		ID: "LOCAL", Source: "guide.pdf", MessageID: "pacs.008.001.08",
		UsageIdentifiers: []string{"swift.cbprplus.03"}, Path: []string{"Document", "Required"}, Min: 1, Max: 1,
	}}}
	profile, err := pack.Augment(base)
	if err != nil {
		t.Fatal(err)
	}
	local := profile.Rules[len(profile.Rules)-1]
	for name, root := range map[string]*converter.Node{
		"no header":     {Name: "Document"},
		"wrong service": {Name: "Envelope", Children: []*converter.Node{{Name: "AppHdr", Children: []*converter.Node{{Name: "BizSvc", Text: "swift.cbprplus.stp.03"}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			if findings := local.Check(&Context{Root: root, MsgID: "pacs.008.001.08"}); len(findings) != 0 {
				t.Fatalf("non-matching Usage Guideline should be skipped: %+v", findings)
			}
		})
	}
}

func TestPackSearchTieBreaksAndUTF8SnippetBoundaries(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.pdf", "a.pdf"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubPDFExtraction(t, func(_ string, path string, _ bool) (string, error) {
		return "match\fmatch", nil
	})
	hits, err := SearchCBPRPack(dir, "match", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 4 || hits[0].Source != "a.pdf" || hits[0].Page != 1 || hits[1].Page != 2 {
		t.Fatalf("stable source/page tie break = %+v", hits)
	}
	page := strings.Repeat("é", 500)
	snippet := localSnippet(page, 721)
	if snippet == "" || !strings.HasPrefix(snippet, "…") {
		t.Fatalf("UTF-8 snippet boundary was not preserved: %q", snippet)
	}
	if got := localSnippet("\x00 short text", -1); got != "short text" {
		t.Fatalf("negative focus/NUL cleanup = %q", got)
	}
}

func TestPackSearchRejectsSingleGenericMatchForMultiTermQuestion(t *testing.T) {
	farApart := "UETR " + strings.Repeat("unrelated ", 100) + "mandatory"
	if hits := RankCBPRText("codes.json", farApart, "Where is UETR mandatory?"); len(hits) != 0 {
		t.Fatalf("single generic term should not be evidence for a multi-term question: %+v", hits)
	}
	if hits := RankCBPRText("codes.json", "The UETR is mandatory.", "Where is UETR mandatory?"); len(hits) != 1 {
		t.Fatalf("two distinct terms should remain searchable: %+v", hits)
	}
	position, matches := bestSearchFocus("mandatory filler uetr mandatory", []string{"uetr", "mandatory"})
	if position < 0 || matches != 2 {
		t.Fatalf("best local evidence window = %d, %d", position, matches)
	}
}

func TestCompileReportsPDFDisappearingAfterDiscovery(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "guide.pdf")
	if err := os.WriteFile(pdf, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldFind, oldRun := findPDFTextTool, runPDFTextTool
	t.Cleanup(func() { findPDFTextTool, runPDFTextTool = oldFind, oldRun })
	findPDFTextTool = func(string) (string, error) {
		if err := os.Remove(pdf); err != nil {
			t.Fatal(err)
		}
		return "pdftotext", nil
	}
	if _, err := CompileCBPRPack(dir); err == nil || !strings.Contains(err.Error(), "reading guide.pdf") {
		t.Fatalf("disappearing PDF should be reported: %v", err)
	}
}

func TestSearchReportsPDFDisappearingBetweenFiles(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "a.pdf")
	second := filepath.Join(dir, "b.pdf")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stubPDFExtraction(t, func(_ string, path string, _ bool) (string, error) {
		if path == first {
			if err := os.Remove(second); err != nil {
				t.Fatal(err)
			}
		}
		return "pacs.008 local rule", nil
	})
	if _, err := SearchCBPRPack(dir, "pacs.008", 5); err == nil {
		t.Fatal("a PDF disappearing during search should fail")
	}
}

func TestPackDefensiveOrderingAndIOErrors(t *testing.T) {
	if _, err := fileSHA256(t.TempDir()); err == nil || !strings.Contains(err.Error(), "hashing") {
		t.Fatalf("hashing a directory should expose the read failure: %v", err)
	}

	id, ids := primaryMessageID("supporting.pdf",
		"pacs.009.001.08 pacs.008.001.08 pacs.009.001.08")
	if id != "pacs.009.001.08" || len(ids) != 2 {
		t.Fatalf("frequency ordering failed: %q %v", id, ids)
	}

	hits := rankCBPRPages("guide.pdf", "pacs.008.001.08 CBPR rule", "pacs.008.001.08 CBPR")
	if len(hits) != 1 || hits[0].Score < 20 {
		t.Fatalf("message and CBPR terms should receive specialist weighting: %+v", hits)
	}

	page := strings.Repeat("a", 359) + "é" + strings.Repeat("b", 400)
	if got := localSnippet(page, 0); !strings.Contains(got, "é") {
		t.Fatalf("UTF-8 boundary expansion lost the rune: %q", got)
	}
}

func TestStructureParserHandlesRootOnEveryPage(t *testing.T) {
	text := "2 Structure\n" +
		"                    Header <AppHdr> [1..1]\n" +
		"                     From <Fr> [1..1]\n\f" +
		"                    Payload <Document> [1..1]\n" +
		"                     Body <Body> [1..1]\n" +
		"3 ISO 20022 Rules\n"
	rows := parseStructureRows(text)
	if len(rows) != 4 {
		t.Fatalf("rows across rooted pages = %d, want 4: %+v", len(rows), rows)
	}
}

func TestLoadCBPRPackRejectsMalformedTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trailing.cbpr-pack.json")
	body := `{"format":"askiso-cbpr-pack/v1","constraints":[{"message_id":"pacs.008.001.08","path":["Document","GrpHdr"],"max":1}]} {`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCBPRPack(path); err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Fatalf("malformed trailing JSON should fail: %v", err)
	}
}

func TestFindPathMatchesIndexesRepeatedParents(t *testing.T) {
	root := &converter.Node{Name: "Document", Children: []*converter.Node{
		{Name: "Parent", Children: []*converter.Node{{Name: "Value"}}},
		{Name: "Parent", Children: []*converter.Node{{Name: "Value"}}},
	}}
	matches := findPathMatches(root, []string{"Document", "Parent", "Value"})
	if len(matches) != 2 || !strings.Contains(matches[0].Path, "Parent[1]") || !strings.Contains(matches[1].Path, "Parent[2]") {
		t.Fatalf("repeated parent paths = %+v", matches)
	}
}
