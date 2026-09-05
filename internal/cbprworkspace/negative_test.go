// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/codes"
	"github.com/sebastienrousseau/askiso/internal/xsd"
)

func TestExportNegativeSamples(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	writeWorkspaceFile(t, filepath.Join(source, "schemas", "head.001.001.02.xsd"), workspaceHeaderSchema)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external, GenerateSamples: true}); err != nil {
		t.Fatal(err)
	}
	validDir := filepath.Join(source, "04 Conformance Samples", "Valid")
	if _, err := ExportValidSamples(source, workspace, validDir); err != nil {
		t.Fatal(err)
	}
	workspace2 := filepath.Join(t.TempDir(), "workspace")
	if _, err := Import(Options{Source: source, Workspace: workspace2, ExternalCodes: external, GenerateSamples: true}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(source, "04 Conformance Samples", "Generated Invalid")
	report, err := ExportNegativeSamples(source, workspace2, output)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"missing-mandatory", "forbidden-element", "cardinality", "lexical", "business-service", "bah-payload"} {
		if report.Scenarios[scenario] == 0 {
			t.Fatalf("scenario %s absent: %+v", scenario, report)
		}
	}
	if report.Generated < 6 || len(report.Files) != report.Generated {
		t.Fatalf("negative report = %+v", report)
	}
	for _, name := range report.Files {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) || !strings.Contains(name, "askiso-generated.invalid.xml") {
			t.Fatalf("negative sample %q = %v, %v", name, info, err)
		}
	}
	workspace3 := filepath.Join(t.TempDir(), "workspace")
	if _, err := Import(Options{Source: source, Workspace: workspace3, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	verification, err := Verify(source, workspace3)
	if err != nil || verification.Failed != 0 {
		t.Fatalf("negative suite = %+v, %v", verification, err)
	}
}

func TestExportNegativeDependencyErrors(t *testing.T) {
	sentinel := errors.New("sentinel")
	origVerify := negativeVerify
	origLoad, origReadJSON, origRoots := exportLoadManifest, exportReadJSON, exportValidateRoots
	origExternal, origSafe, origRead := exportLoadExternalSets, exportSafeSource, exportReadBounded
	origParse, origWrite := exportParseSchema, exportWriteSample
	t.Cleanup(func() {
		negativeVerify = origVerify
		exportLoadManifest, exportReadJSON, exportValidateRoots = origLoad, origReadJSON, origRoots
		exportLoadExternalSets, exportSafeSource, exportReadBounded = origExternal, origSafe, origRead
		exportParseSchema, exportWriteSample = origParse, origWrite
	})
	negativeVerify = func(string, string) (*Verification, error) { return nil, sentinel }
	if _, err := ExportNegativeSamples("s", "w", "o"); !errors.Is(err, sentinel) {
		t.Fatalf("verify error = %v", err)
	}
	negativeVerify = func(string, string) (*Verification, error) { return &Verification{}, nil }
	exportLoadManifest = func(string) (*Manifest, error) { return nil, sentinel }
	if _, err := ExportNegativeSamples("s", "w", "o"); !errors.Is(err, sentinel) {
		t.Fatalf("manifest error = %v", err)
	}
	negativeVerify = func(string, string) (*Verification, error) {
		return &Verification{workspaceFingerprint: strings.Repeat("a", 24)}, nil
	}
	exportLoadManifest = func(string) (*Manifest, error) {
		return &Manifest{Fingerprint: strings.Repeat("b", 24)}, nil
	}
	if _, err := ExportNegativeSamples("s", "w", "o"); err == nil || !strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("generation drift error = %v", err)
	}
	negativeVerify = func(string, string) (*Verification, error) { return &Verification{}, nil }
	exportLoadManifest = func(string) (*Manifest, error) { return &Manifest{}, nil }
	exportReadJSON = func(string, any) error { return sentinel }
	if _, err := ExportNegativeSamples("s", "w", "o"); !errors.Is(err, sentinel) {
		t.Fatalf("suite error = %v", err)
	}
	exportReadJSON = func(_ string, value any) error {
		value.(*Suite).Cases = []SuiteCase{{Origin: "askiso-generated", Expected: "valid", Sample: "sample.xml", Schema: "schema.xsd", MessageID: "pacs.008.001.08", BusinessService: "swift.cbprplus.03"}}
		return nil
	}
	exportValidateRoots = func(string, string) (string, string, error) { return "", "", sentinel }
	if _, err := ExportNegativeSamples("s", "w", "o"); !errors.Is(err, sentinel) {
		t.Fatalf("roots error = %v", err)
	}
	root := t.TempDir()
	source, workspace := filepath.Join(root, "source"), filepath.Join(root, "workspace")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	exportValidateRoots = func(string, string) (string, string, error) { return source, workspace, nil }
	exportLoadExternalSets = func(string) (*codes.ExternalSets, error) { return nil, sentinel }
	if _, err := ExportNegativeSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("external error = %v", err)
	}
	exportLoadExternalSets = func(string) (*codes.ExternalSets, error) { return &codes.ExternalSets{}, nil }
	exportSafeSource = func(string, string) (string, error) { return "", sentinel }
	if _, err := ExportNegativeSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("safe sample error = %v", err)
	}
	exportSafeSource = func(string, string) (string, error) { return "path", nil }
	exportReadBounded = func(string) ([]byte, error) { return nil, sentinel }
	if _, err := ExportNegativeSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("sample read error = %v", err)
	}
	exportReadBounded = func(string) ([]byte, error) {
		return []byte(`<Envelope><Document><Value>x</Value></Document></Envelope>`), nil
	}
	calls := 0
	exportSafeSource = func(string, string) (string, error) {
		calls++
		if calls == 2 {
			return "", sentinel
		}
		return "path", nil
	}
	if _, err := ExportNegativeSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("safe schema error = %v", err)
	}
	exportSafeSource = func(string, string) (string, error) { return "path", nil }
	exportParseSchema = func(string) (*xsd.Schema, error) { return nil, sentinel }
	if _, err := ExportNegativeSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("schema parse error = %v", err)
	}
	exportParseSchema = func(string) (*xsd.Schema, error) { return &xsd.Schema{}, nil }
	exportWriteSample = func(string, []byte) error { return sentinel }
	if _, err := ExportNegativeSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("write error = %v", err)
	}
}

func TestNegativeMutationHelpers(t *testing.T) {
	if _, _, _, _, ok := documentBounds([]byte(`<Envelope/>`)); ok {
		t.Fatal("missing Document was accepted")
	}
	if _, _, _, _, ok := documentBounds([]byte(`<Document`)); ok {
		t.Fatal("unterminated Document opening was accepted")
	}
	if _, _, _, _, ok := documentBounds([]byte(`<Document>`)); ok {
		t.Fatal("unterminated Document was accepted")
	}
	original := []byte(`<Envelope><BizSvc>x</BizSvc></Envelope>`)
	if got := string(replaceElementText(original, "BizSvc", "y")); !strings.Contains(got, ">y<") {
		t.Fatalf("replacement = %s", got)
	}
	if got := string(replaceElementText(original, "Missing", "y")); got != string(original) {
		t.Fatalf("missing replacement changed XML: %s", got)
	}
	if got := string(replaceElementText([]byte(`<BizSvc>x`), "BizSvc", "y")); got != `<BizSvc>x` {
		t.Fatalf("unterminated replacement changed XML: %s", got)
	}
	if got := string(appendEnvelopeParts([]byte("a"), []byte("b"), []byte("c"))); got != "a\nb\nc" {
		t.Fatalf("parts = %q", got)
	}
	if got := negativeMutations([]byte(`<Envelope/>`), nil, nil, "", ""); len(got) != 0 {
		t.Fatalf("missing Document mutations = %v", got)
	}
}

func TestExportNegativeSamplesRequiresExportedPositive(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external, GenerateSamples: true}); err != nil {
		t.Fatal(err)
	}
	_, err := ExportNegativeSamples(source, workspace, filepath.Join(source, "invalid"))
	if err == nil || !strings.Contains(err.Error(), "export-valid-samples") {
		t.Fatalf("missing positive error = %v", err)
	}
}

func TestExportNegativeSamplesRejectsFailedOrUnsafeSuite(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, filepath.Join(source, "samples", "payment.xml"),
		`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><Purpose>NOPE</Purpose></Document>`)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportNegativeSamples(source, workspace, filepath.Join(source, "invalid")); err == nil || !strings.Contains(err.Error(), "failed case") {
		t.Fatalf("failed suite error = %v", err)
	}

	source2, workspace2, external2 := workspaceFixture(t)
	if _, err := Import(Options{Source: source2, Workspace: workspace2, ExternalCodes: external2, GenerateSamples: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportNegativeSamples(source2, workspace2, filepath.Join(t.TempDir(), "outside")); err == nil {
		t.Fatal("outside output accepted")
	}
}
