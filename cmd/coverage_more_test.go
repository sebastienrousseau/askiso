// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/linter"
	"github.com/sebastienrousseau/askiso/internal/rules"
	"github.com/sebastienrousseau/askiso/internal/swift"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
)

func useCatalogueRoot(t *testing.T, root string) {
	t.Helper()
	t.Setenv("ASKISO_CATALOG", root)
	previous := catalogPath
	catalogPath = ""
	t.Cleanup(func() { catalogPath = previous })
}

func TestInfoInstalledSearchJSONAndMissing(t *testing.T) {
	withCatalogue(t)
	out, err := run(t, "info", "pacs.008")
	if err != nil {
		t.Fatalf("approximate installed lookup: %v", err)
	}
	wantContains(t, out, "MESSAGE INFO", "Message Definition Reports")

	out, err = run(t, "info", "pacs.008.001.10", "--json")
	if err != nil || !strings.Contains(out, `"ID": "pacs.008.001.10"`) {
		t.Fatalf("installed JSON info: %v\n%s", err, out)
	}
	if _, err := run(t, "info", "zzzz.999"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown installed message should fail: %v", err)
	}
}

func TestSampleOutputModesAndRelatedVersionFallback(t *testing.T) {
	withCatalogue(t)
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	if out, err := run(t, "sample", "pacs.008.001.10", "--raw"); err != nil || !strings.Contains(out, "<Document") {
		t.Fatalf("raw sample: %v\n%s", err, out)
	}
	if out, err := run(t, "sample", "pacs.008.001.10"); err != nil || !strings.Contains(out, "Document") {
		t.Fatalf("highlighted sample: %v\n%s", err, out)
	}
	// The first pacs.008 version in the fixture has no published sample. A base
	// query should move to the related version that does, rather than fail.
	if out, err := run(t, "sample", "pacs.008"); err != nil || !strings.Contains(out, "MSG-0001") {
		t.Fatalf("related-version sample fallback: %v\n%s", err, out)
	}

	isolate(t)
	if _, err := run(t, "sample", "admi.999.001.01"); err == nil {
		t.Fatal("unsupported sample without a catalogue should fail")
	}
}

func TestSchemaOutputModesAndRelatedVersionFallback(t *testing.T) {
	root := fixtureCatalogue(t)
	base := filepath.Join(root, "Payments Clearing and Settlement", "Version 11.0")
	// Leave .09 discoverable through a sample but remove its schema, forcing the
	// base-code lookup to continue to the installed .10 schema.
	if err := os.WriteFile(filepath.Join(base, "Sample Messages", "pacs.008.001.09.xml"),
		[]byte(fixtureInstance("pacs.008.001.09", "EUR")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(base, "Schemas", "pacs.008.001.09.xsd")); err != nil {
		t.Fatal(err)
	}
	useCatalogueRoot(t, root)
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")

	if out, err := run(t, "schema", "pacs.008", "--raw"); err != nil || !strings.Contains(out, "targetNamespace") {
		t.Fatalf("raw related schema: %v\n%s", err, out)
	}
	if out, err := run(t, "schema", "pacs.008.001.10"); err != nil || !strings.Contains(out, "xs:schema") {
		t.Fatalf("highlighted schema: %v\n%s", err, out)
	}

}

func TestGenerateSchemaFailureAndOutputBranches(t *testing.T) {
	root := withCatalogue(t)
	if _, err := run(t, "generate", "pacs.008", "-o", t.TempDir()); err == nil {
		t.Error("template output to a directory should fail")
	}
	if _, err := run(t, "generate", "pacs.009.001.10", "--amount", "9.50",
		"--currency", "GBP", "--debtor", "Alice", "--debtor-iban", "GB82WEST12345698765432", "--copy"); err != nil {
		t.Fatalf("schema overrides/copy: %v", err)
	}
	if _, err := run(t, "generate", "pacs.009.001.10", "-o", t.TempDir()); err == nil {
		t.Error("schema output to a directory should fail")
	}

	badSchema := filepath.Join(root, "Payments Clearing and Settlement", "Version 11.0", "Schemas", "pacs.009.001.10.xsd")
	if err := os.WriteFile(badSchema, []byte("<not-closed>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "generate", "pacs.009.001.10", "--from-schema"); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Fatalf("malformed schema should be reported: %v", err)
	}
}

func TestLintRenderersCoverEvidenceAndProfileStates(t *testing.T) {
	out := captureStdout(t, func() {
		printIssueDetail(linter.Issue{
			Field: "IBAN", Value: "bad", Path: "/Document/IBAN",
			Expected: "a valid checksum", Actual: "remainder 2",
			Remediation: strings.Repeat("replace the incorrect value carefully ", 4),
		})
		printIssueDetail(linter.Issue{Field: "BIC", Expected: "8 or 11 characters"})
		printProfileFindings(&rules.Result{
			Profile: "cbpr-plus", Pack: &rules.CBPRPackInfo{
				Fingerprint: "abc", Constraints: 3, UsageGuidelines: 1,
				Coverage: "structural", Warnings: []string{"confirm narrative rules"},
			},
			Findings: []rules.Finding{
				{RuleID: "W", Severity: rules.SeverityWarning, Message: "warning", Path: "/W", Expected: "x", Remediation: "fix w"},
				{RuleID: "I", Severity: rules.SeverityInfo, Message: "info", Path: "/I"},
				{RuleID: "E", Severity: rules.SeverityError, Message: "error", Path: "/E"},
			}, Skipped: 2,
		})
		printProfileFindings(&rules.Result{Profile: "cbpr-plus", Checked: 0, Skipped: 4})
		printProfileFindings(&rules.Result{Profile: "cbpr-plus", Checked: 3, Skipped: 1})
	})
	for _, want := range []string{"remainder 2", "Expected 8 or 11", "local pack abc", "warning:", "exempt", "passed"} {
		if !strings.Contains(out, want) {
			t.Errorf("renderer output missing %q:\n%s", want, out)
		}
	}
	if got := wrapAt("   ", 10); got != nil {
		t.Fatalf("blank remediation should produce no lines: %v", got)
	}
}

func TestBatchDirectProfileAndSchemaFailures(t *testing.T) {
	path := writeTemp(t, "message.xml", validMessage)
	previous := batchProfile
	batchProfile = "cbpr-2026"
	t.Cleanup(func() { batchProfile = previous })
	if rep := checkOne(path, nil); rep.Profile == nil {
		t.Fatalf("checkOne did not apply its selected profile: %+v", rep)
	}
	if rep := checkOneWithProfile(writeTemp(t, "malformed.xml", "<Document>"), nil, nil); rep.Err == "" {
		t.Fatal("malformed input should be retained as a per-file error")
	}

	cat, err := iso20022.OpenCatalogue(fixtureCatalogue(t))
	if err != nil {
		t.Fatal(err)
	}
	unknown := strings.Replace(validMessage, "pacs.008.001.10", "admi.999.001.01", 1)
	if rep := checkOneWithProfile(writeTemp(t, "unknown.xml", unknown), cat, nil); rep.Err == "" {
		t.Fatalf("missing schema should be reported in the file result: %+v", rep)
	}
}

func TestMockRejectsBadFlagsAndReportsBindFailure(t *testing.T) {
	if _, err := run(t, "mock", "--port", "70000"); err == nil {
		t.Error("out-of-range port should fail")
	}
	if _, err := run(t, "mock", "--scenario", "not-real"); err == nil {
		t.Error("unknown scenario should fail")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	port := ln.Addr().(*net.TCPAddr).Port
	out, err := run(t, "mock", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--scenario", "reject-ac04")
	if err == nil {
		t.Fatal("occupied port should make the mock command return")
	}
	if !strings.Contains(out, "Scenario") {
		t.Fatalf("selected scenario was not disclosed before start: %s", out)
	}
}

func TestFormattingFlowAndTranslationFailurePaths(t *testing.T) {
	dir := t.TempDir()
	brokenXML := filepath.Join(dir, "broken.xml")
	if err := os.WriteFile(brokenXML, []byte(`<Document>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "format", brokenXML, "--minify"); err == nil || !strings.Contains(err.Error(), "minify") {
		t.Fatalf("malformed minify error = %v", err)
	}
	goodXML := filepath.Join(dir, "good.xml")
	if err := os.WriteFile(goodXML, []byte(`<Document/>`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "format", goodXML, "--output", t.TempDir()); err == nil {
		t.Fatal("formatting to a directory succeeded")
	}

	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "flow", "--output-dir", filepath.Join(blocked, "child")); err == nil || !strings.Contains(err.Error(), "create output") {
		t.Fatalf("flow directory error = %v", err)
	}
	flowDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(flowDir, "01_pain.001_Initiation.xml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "flow", "--output-dir", flowDir); err == nil || !strings.Contains(err.Error(), "failed to write") {
		t.Fatalf("flow file error = %v", err)
	}

	if err := translateFile(filepath.Join(dir, "missing.mt")); err == nil {
		t.Fatal("translateFile accepted a missing file")
	}
	mt := writeMT(t, "payment.mt103", mt103Sample)
	if _, err := run(t, "translate", mt, "--out", t.TempDir()); err == nil {
		t.Fatal("translation output to a directory succeeded")
	}
	out := captureStdout(t, func() {
		printConversionReport("input.mt", &iso20022.Conversion{
			SourceType: "103", TargetType: "pacs.008.001.10",
			Report: []swift.FieldReport{{Tag: "121", Fidelity: swift.FidelityDerived, Note: "generated"}},
		})
	})
	if !strings.Contains(out, "had to be derived") {
		t.Fatalf("derived-only conversion summary missing:\n%s", out)
	}
}

func TestCommandsReportMissingAndEvictedLocalMaterial(t *testing.T) {
	firstRoot := fixtureCatalogue(t)
	unsupportedSchema := filepath.Join(firstRoot, "Payments Clearing and Settlement", "Version 11.0", "Schemas", "admi.024.001.01.xsd")
	if err := os.WriteFile(unsupportedSchema, []byte(fixtureSchema("admi.024.001.01")), 0o600); err != nil {
		t.Fatal(err)
	}
	useCatalogueRoot(t, firstRoot)
	if _, err := run(t, "sample", "admi.024.001.01"); err == nil {
		t.Fatal("message with neither published nor generated sample succeeded")
	}
	if _, err := run(t, "validate", writeTemp(t, "instance.xml", `<Document/>`), filepath.Join(t.TempDir(), "missing.xsd")); err == nil || !strings.Contains(err.Error(), "schema not found") {
		t.Fatalf("missing explicit schema error = %v", err)
	}

	root := fixtureCatalogue(t)
	base := filepath.Join(root, "Payments Clearing and Settlement", "Version 11.0")
	schemaPath := filepath.Join(base, "Schemas", "pacs.008.001.10.xsd")
	if err := os.WriteFile(schemaPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(schemaPath), "."+filepath.Base(schemaPath)+".icloud"), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	useCatalogueRoot(t, root)
	if _, err := run(t, "schema", "pacs.008.001.10"); err == nil || !strings.Contains(err.Error(), "evicted") {
		t.Fatalf("evicted schema error = %v", err)
	}
	instance := filepath.Join(base, "Sample Messages", "pacs.008.001.10.xml")
	if _, err := run(t, "validate", instance, schemaPath); err == nil || !strings.Contains(err.Error(), "evicted") {
		t.Fatalf("validate evicted schema error = %v", err)
	}
}

func TestCatalogAddDirectoryAndContentChecks(t *testing.T) {
	source := t.TempDir()
	archive := filepath.Join(source, "set.zip")
	writeTestZip(t, archive, map[string]string{
		"Payments Clearing and Settlement/Version 11.0/Schemas/pacs.008.001.10.xsd": fixtureSchema("pacs.008.001.10"),
	})
	if out, err := run(t, "catalog", "add", source, "--to", t.TempDir()); err != nil || !strings.Contains(out, "1 schema") {
		t.Fatalf("directory import: %v\n%s", err, out)
	}

	docsOnly := filepath.Join(t.TempDir(), "docs.zip")
	writeTestZip(t, docsOnly, map[string]string{"Documentation/readme.txt": "notes"})
	if _, err := run(t, "catalog", "add", docsOnly, "--to", t.TempDir()); err == nil || !strings.Contains(err.Error(), "no schemas") {
		t.Fatalf("non-schema archive error = %v", err)
	}
}

func TestAdditionalCommandFailurePaths(t *testing.T) {
	if _, err := run(t, "flow", "--amount", "not-an-amount"); err == nil {
		t.Fatal("flow accepted a non-numeric amount")
	}

	missing := filepath.Join(t.TempDir(), "missing.pdf")
	if _, err := run(t, "cbpr-pack", "compile", missing); err == nil {
		t.Fatal("CBPR+ compiler accepted a missing source")
	}
	if _, err := run(t, "cbpr-pack", "status", t.TempDir()); err == nil {
		t.Fatal("CBPR+ status accepted a workspace without a manifest")
	}
	if _, err := run(t, "cbpr-pack", "verify", t.TempDir(), "--workspace", t.TempDir()); err == nil {
		t.Fatal("CBPR+ verify accepted a workspace without a manifest")
	}
	pack := fixtureCompiledCBPRPack(t)
	blockedPack := filepath.Join(t.TempDir(), "blocked.cbpr-pack.json")
	if err := os.Mkdir(blockedPack, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "cbpr-pack", "compile", pack, "--output", blockedPack); err == nil {
		t.Fatal("CBPR+ compiler wrote over an output directory")
	}
	if _, err := resolveRuleProfile("", missing); err == nil || !strings.Contains(err.Error(), "compiling --cbpr-pack") {
		t.Fatalf("missing profile pack error = %v", err)
	}

	profile, err := rules.Get("cbpr-plus")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runResolvedProfile([]byte(`<Document>`), "broken.xml", profile); err == nil || !strings.Contains(err.Error(), "parsing broken.xml") {
		t.Fatalf("malformed profiled message error = %v", err)
	}

	xmlPath := writeTemp(t, "instance.xml", `<Document/>`)
	xsdPath := writeTemp(t, "broken.xsd", `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`)
	if _, err := run(t, "validate", xmlPath, xsdPath); err == nil {
		t.Fatal("validation accepted a malformed schema")
	}
	if _, err := run(t, "validate", xmlPath, xsdPath, "--stream"); err == nil {
		t.Fatal("streaming validation accepted a malformed schema")
	}
	if _, err := run(t, "validate", xmlPath, xsdPath, "--external-codes", filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("validation accepted a missing external-code publication")
	}

	withCatalogue(t)
	unknown := []byte(`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:admi.999.001.01"/>`)
	if _, err := resolveSchemaForInstance(unknown); err == nil {
		t.Fatal("schema resolution accepted an uninstalled message")
	}
	t.Setenv("PATH", t.TempDir())
	if err := validateWithXmllint(xsdPath, xmlPath); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("missing xmllint error = %v", err)
	}

	corruptDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(corruptDir, "broken.zip"), []byte("not a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "catalog", "add", corruptDir, "--to", t.TempDir()); err == nil {
		t.Fatal("catalog directory import accepted a corrupt archive")
	}
}
