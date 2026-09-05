// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/sebastienrousseau/askiso/internal/rules"
)

func TestStrictConformanceGateAndExternalEvidence(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	manifest, err := Import(Options{
		Source: source, Workspace: workspace, ExternalCodes: external,
		EntitlementAcknowledged: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	useLegacyWorkspace(t, workspace)
	var suite Suite
	if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	var valid, invalid SuiteCase
	for _, testCase := range suite.Cases {
		if testCase.Expected == "valid" {
			valid = testCase
		} else {
			invalid = testCase
		}
	}
	suite.Cases = nil
	guidelines := rules.CBPRSR2025UsageGuidelines()
	for i, guideline := range guidelines {
		positive := valid
		positive.ID = guideline.MessageID + "/positive"
		positive.MessageID = guideline.MessageID
		positive.BusinessService = guideline.UsageIdentifier
		positive.Scenario = "valid"
		negative := invalid
		negative.ID = guideline.MessageID + "/negative/" + guideline.UsageIdentifier
		negative.MessageID = guideline.MessageID
		negative.BusinessService = guideline.UsageIdentifier
		negative.Scenario = representativeScenarios[i%len(representativeScenarios)]
		suite.Cases = append(suite.Cases, positive, negative)
	}
	suite.Fingerprint = suiteFingerprint(&suite)
	if err := writeJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	manifest.Coverage.ExecutableUsageGuidelines = len(guidelines)
	manifest.Coverage.MissingExecutableUsageGuidelines = nil
	manifest.SuiteCases = len(suite.Cases)
	manifest.SuiteFingerprint = suite.Fingerprint
	manifest.Fingerprint = manifestFingerprint(manifest)
	if err := writeJSON(filepath.Join(workspace, ManifestFile), manifest); err != nil {
		t.Fatal(err)
	}

	evidencePath := filepath.Join(t.TempDir(), "evidence.json")
	evidence := ExternalEvidence{
		Format: EvidenceFormat, Provider: "MyStandards Readiness Portal",
		WorkspaceFingerprint: manifest.Fingerprint, SuiteFingerprint: suite.Fingerprint,
		TestedAt: "2026-09-05T08:00:00Z", Cases: len(suite.Cases), Passed: true,
	}
	data, _ := json.Marshal(evidence)
	writeWorkspaceFile(t, evidencePath, string(data))
	report, err := CheckConformance(ConformanceOptions{
		Source: source, Workspace: workspace, AsOf: "2026-09-05", Evidence: evidencePath,
		RequireUserSamples: true, RequireExternalEvidence: true,
	})
	if err != nil || !report.Ready || len(report.MissingPositiveSamples) != 0 ||
		len(report.MissingNegativeSamples) != 0 || len(report.MissingScenarios) != 0 {
		t.Fatalf("strict report = %+v, %v", report, err)
	}
	if len(report.Checks) != 11 {
		t.Fatalf("strict checks = %+v", report.Checks)
	}

	report, err = CheckConformance(ConformanceOptions{
		Source: source, Workspace: workspace, AsOf: "2025-11-22",
	})
	if err != nil || report.Ready || checkPassed(report, "external-code-as-of") {
		t.Fatalf("future code publication was accepted: %+v, %v", report, err)
	}
}

func TestConformanceHelpersAndFailures(t *testing.T) {
	if _, err := conformanceDate("5 September 2026"); err == nil {
		t.Fatal("invalid conformance date was accepted")
	}
	if today, err := conformanceDate(""); err != nil || today.IsZero() {
		t.Fatalf("default conformance date = %v, %v", today, err)
	}
	if ok, detail := externalPublicationAsOf(nil, time.Now()); ok || detail != "absent" {
		t.Fatalf("absent external publication = %t, %q", ok, detail)
	}
	if ok, detail := externalPublicationAsOf(&ExternalPublication{Publication: "latest"}, time.Now()); ok ||
		!strings.Contains(detail, "unrecognised") {
		t.Fatalf("unrecognised publication = %t, %q", ok, detail)
	}
	if scenarioFromName("valid.xml", "valid") != "valid" ||
		scenarioFromName("case.invalid.xml", "invalid") != "invalid-unspecified" {
		t.Fatal("base scenario classification failed")
	}
	for filename, want := range map[string]string{
		"x-external.invalid.xml": "external-code", "x-bizsvc.invalid.xml": "business-service",
		"x-header.invalid.xml": "bah-payload", "x-missing.invalid.xml": "missing-mandatory",
		"x-removed.invalid.xml": "forbidden-element", "x-repeat.invalid.xml": "cardinality",
		"x-length.invalid.xml": "lexical", "x-restricted-code.invalid.xml": "restricted-code",
		"x-variant.invalid.xml": "variant",
	} {
		if got := scenarioFromName(filename, "invalid"); got != want {
			t.Fatalf("scenarioFromName(%s) = %s, want %s", filename, got, want)
		}
	}

	positive, negative, scenarios := userSampleCoverage([]SuiteCase{
		{Origin: "generated", BusinessService: "swift.cbprplus.03", Expected: "valid"},
		{Origin: "user-provided", MessageID: "pacs.008.001.08", Expected: "valid"},
		{Origin: "", MessageID: "pacs.008.001.08", BusinessService: "swift.cbprplus.03", Expected: "valid"},
		{Origin: "user-provided", MessageID: "pacs.008.001.08", BusinessService: "swift.cbprplus.03", Expected: "invalid", Scenario: "lexical"},
	})
	key := "pacs.008.001.08|swift.cbprplus.03"
	if !positive[key] || !negative[key] || !scenarios["lexical"] {
		t.Fatalf("user coverage = %v %v %v", positive, negative, scenarios)
	}

	root := t.TempDir()
	if ok, _ := privateWorkspace(filepath.Join(root, "missing")); ok {
		t.Fatal("missing workspace was private")
	}
	writeWorkspaceFile(t, filepath.Join(root, ManifestFile), "x")
	writeWorkspaceFile(t, filepath.Join(root, SuiteFile), "x")
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, detail := privateWorkspace(root); runtime.GOOS != "windows" && (ok || !strings.Contains(detail, "mode")) {
		t.Fatalf("public workspace = %t, %s", ok, detail)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	writeWorkspaceFile(t, filepath.Join(root, CurrentFile), `{}`)
	if err := os.Chmod(filepath.Join(root, CurrentFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, detail := privateWorkspace(root); runtime.GOOS != "windows" && (ok || !strings.Contains(detail, "mode")) {
		t.Fatalf("public pointer = %t, %s", ok, detail)
	}
	if err := os.Remove(filepath.Join(root, CurrentFile)); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		target := filepath.Join(t.TempDir(), "pointer")
		writeWorkspaceFile(t, target, `{}`)
		if err := os.Symlink(target, filepath.Join(root, CurrentFile)); err != nil {
			t.Fatal(err)
		}
		if ok, detail := privateWorkspace(root); ok || !strings.Contains(detail, "symlink") {
			t.Fatalf("symlink pointer = %t, %s", ok, detail)
		}
	}

	if evidence, err := loadExternalEvidence(""); err != nil || evidence != nil {
		t.Fatalf("empty evidence = %+v, %v", evidence, err)
	}
	missing := filepath.Join(root, "missing-evidence.json")
	if _, err := loadExternalEvidence(missing); err == nil || !strings.Contains(err.Error(), "reading external evidence") {
		t.Fatalf("missing evidence error = %v", err)
	}
	for name, body := range map[string]string{
		"malformed":    `{`,
		"wrong-format": `{"format":"wrong","provider":"portal"}`,
		"bad-date":     `{"format":"askiso-cbpr-external-evidence/v1","provider":"portal","tested_at":"today"}`,
		"trailing":     `{"format":"askiso-cbpr-external-evidence/v1","provider":"portal","tested_at":"2026-09-05T08:00:00Z"}{}`,
	} {
		path := filepath.Join(t.TempDir(), name+".json")
		writeWorkspaceFile(t, path, body)
		if _, err := loadExternalEvidence(path); err == nil {
			t.Fatalf("%s evidence was accepted", name)
		}
	}
}

func TestStrictConformanceReportsIncompleteLocalEvidence(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	manifest, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external})
	if err != nil {
		t.Fatal(err)
	}
	report, err := CheckConformance(ConformanceOptions{
		Source: source, Workspace: workspace, AsOf: "2026-09-05",
		RequireUserSamples: true, RequireExternalEvidence: true,
	})
	if err != nil || report.Ready {
		t.Fatalf("incomplete report = %+v, %v", report, err)
	}
	if checkPassed(report, "entitlement") || checkPassed(report, "executable-usage-guidelines") ||
		checkPassed(report, "external-evidence") {
		t.Fatalf("incomplete gates unexpectedly passed: %+v", report.Checks)
	}
	if len(report.MissingPositiveSamples) != 31 || len(report.MissingNegativeSamples) != 31 ||
		len(report.MissingScenarios) != len(representativeScenarios) {
		t.Fatalf("incomplete sample inventory = %d/%d/%d", len(report.MissingPositiveSamples),
			len(report.MissingNegativeSamples), len(report.MissingScenarios))
	}

	evidencePath := filepath.Join(t.TempDir(), "failed-evidence.json")
	evidence := ExternalEvidence{
		Format: EvidenceFormat, Provider: "independent portal",
		WorkspaceFingerprint: manifest.Fingerprint, SuiteFingerprint: manifest.SuiteFingerprint,
		TestedAt: "2026-09-05T08:00:00Z", Cases: 2, Passed: false,
	}
	data, _ := json.Marshal(evidence)
	writeWorkspaceFile(t, evidencePath, string(data))
	report, err = CheckConformance(ConformanceOptions{
		Source: source, Workspace: workspace, AsOf: "2026-09-05", Evidence: evidencePath,
	})
	if err != nil || report.Ready || checkPassed(report, "external-evidence") {
		t.Fatalf("failed external verdict = %+v, %v", report, err)
	}
}

func TestStrictConformanceRejectsUnpinnedInputs(t *testing.T) {
	t.Run("missing workspace", func(t *testing.T) {
		if _, err := CheckConformance(ConformanceOptions{Workspace: filepath.Join(t.TempDir(), "missing")}); err == nil {
			t.Fatal("missing workspace was accepted")
		}
	})
	t.Run("suite fingerprint", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		var suite Suite
		if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		suite.Cases = nil
		if err := writeJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckConformance(ConformanceOptions{Source: source, Workspace: workspace}); err == nil ||
			!strings.Contains(err.Error(), "suite fingerprint") {
			t.Fatalf("suite mismatch error = %v", err)
		}
	})
	t.Run("generation drift", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		old := conformanceVerify
		conformanceVerify = func(string, string) (*Verification, error) {
			return &Verification{workspaceFingerprint: strings.Repeat("a", 24)}, nil
		}
		t.Cleanup(func() { conformanceVerify = old })
		if _, err := CheckConformance(ConformanceOptions{Source: source, Workspace: workspace}); err == nil ||
			!strings.Contains(err.Error(), "generation changed") {
			t.Fatalf("generation drift error = %v", err)
		}
	})
	t.Run("source fingerprint", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		writeWorkspaceFile(t, filepath.Join(source, "samples", "payment.xml"), `<Document/>`)
		if _, err := CheckConformance(ConformanceOptions{Source: source, Workspace: workspace}); err == nil ||
			!strings.Contains(err.Error(), "fingerprint changed") {
			t.Fatalf("source mismatch error = %v", err)
		}
	})
	t.Run("date and evidence", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckConformance(ConformanceOptions{
			Source: source, Workspace: workspace, AsOf: "tomorrow",
		}); err == nil || !strings.Contains(err.Error(), "invalid --as-of") {
			t.Fatalf("invalid date error = %v", err)
		}
		badEvidence := filepath.Join(t.TempDir(), "bad.json")
		writeWorkspaceFile(t, badEvidence, `{`)
		if _, err := CheckConformance(ConformanceOptions{
			Source: source, Workspace: workspace, Evidence: badEvidence,
		}); err == nil || !strings.Contains(err.Error(), "decoding external evidence") {
			t.Fatalf("invalid evidence error = %v", err)
		}
	})
}

func checkPassed(report *ConformanceReport, id string) bool {
	for _, check := range report.Checks {
		if check.ID == id {
			return check.Passed
		}
	}
	return false
}
