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
)

func TestReviewAuditAndEvidenceWorkflow(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	nameEvidenceSamples(t, source)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external, EntitlementAcknowledged: true}); err != nil {
		t.Fatal(err)
	}
	checklistPath := filepath.Join(t.TempDir(), "review.json")
	checklist, err := WriteReviewChecklist(workspace, checklistPath, "2026-09-05T10:00:00+01:00")
	if err != nil || len(checklist.Items) != 62 || checklist.CreatedAt != "2026-09-05T09:00:00Z" {
		t.Fatalf("checklist = %+v, %v", checklist, err)
	}
	if info, err := os.Stat(checklistPath); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("checklist mode = %v, %v", info, err)
	}
	audit, err := AuditSamples(source, workspace)
	if err != nil || !audit.ReadyForAttestation || audit.Eligible != 2 || audit.Synthetic != 0 {
		t.Fatalf("audit = %+v, %v", audit, err)
	}
	attestationPath := filepath.Join(t.TempDir(), "attestation.json")
	if _, err := WriteSampleAttestation(source, workspace, attestationPath, "", "provider", "", true); err == nil {
		t.Fatal("empty reviewer accepted")
	}
	if _, err := WriteSampleAttestation(source, workspace, attestationPath, "reviewer", "provider", "", false); err == nil {
		t.Fatal("missing acknowledgement accepted")
	}
	attestation, err := WriteSampleAttestation(source, workspace, attestationPath, " reviewer ", " provider ", "2026-09-05T09:00:00Z", true)
	if err != nil || len(attestation.Cases) != 2 || attestation.Reviewer != "reviewer" {
		t.Fatalf("attestation = %+v, %v", attestation, err)
	}
	evidencePath := filepath.Join(t.TempDir(), "external.json")
	if _, err := WriteExternalEvidence(workspace, evidencePath, "swift-test", "bad", 2, true, true); err == nil {
		t.Fatal("bad time accepted")
	}
	if _, err := WriteExternalEvidence(workspace, evidencePath, "swift-test", "", 2, true, false); err == nil {
		t.Fatal("missing verdict acknowledgement accepted")
	}
	evidence, err := WriteExternalEvidence(workspace, evidencePath, " swift-test ", "2026-09-05T09:00:00Z", 2, true, true)
	if err != nil || evidence.Provider != "swift-test" || !evidence.Passed {
		t.Fatalf("evidence = %+v, %v", evidence, err)
	}
}

func TestSampleAuditFindsSensitiveAndDuplicateData(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	nameEvidenceSamples(t, source)
	live := `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><Purpose>GB82WEST12345698765432</Purpose></Document>`
	writeWorkspaceFile(t, filepath.Join(source, "samples", "live - swift.cbprplus.03 - valid.xml"), live)
	writeWorkspaceFile(t, filepath.Join(source, "samples", "live-copy - swift.cbprplus.03 - valid.xml"), live)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	audit, err := AuditSamples(source, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if audit.ReadyForAttestation || len(audit.Duplicates) == 0 || len(audit.SensitiveDataWarnings) == 0 {
		t.Fatalf("unsafe audit = %+v", audit)
	}
	if _, err := WriteSampleAttestation(source, workspace, filepath.Join(t.TempDir(), "no.json"), "r", "p", "", true); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Fatalf("unsafe attestation error = %v", err)
	}
}

func nameEvidenceSamples(t *testing.T, source string) {
	t.Helper()
	for oldName, newName := range map[string]string{
		"payment.xml":         "payment - swift.cbprplus.03 - valid.xml",
		"payment.invalid.xml": "payment - swift.cbprplus.03 - x.invalid.xml",
	} {
		if err := os.Rename(filepath.Join(source, "samples", oldName), filepath.Join(source, "samples", newName)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEvidenceInputErrors(t *testing.T) {
	if _, err := evidenceTime("not-a-time"); err == nil {
		t.Fatal("invalid time accepted")
	}
	if err := writeEvidenceJSON("", map[string]bool{"ok": true}); err == nil {
		t.Fatal("empty output accepted")
	}
	if _, err := WriteExternalEvidence("missing", "out", "", "", 0, false, true); err == nil {
		t.Fatal("empty provider/cases accepted")
	}
	if _, err := WriteExternalEvidence("missing", "out", "provider", "", 1, false, true); err == nil {
		t.Fatal("missing workspace accepted")
	}
}

func TestAnonymiseSamplesPreservesProvenance(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	nameEvidenceSamples(t, source)
	writeWorkspaceFile(t, filepath.Join(source, "samples", "account - swift.cbprplus.03 - x.invalid.xml"),
		`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><Purpose>GB82WEST12345698765432</Purpose></Document>`)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(source, "anonymised")
	report, err := AnonymiseSamples(source, workspace, output)
	if err != nil || report.Processed != 3 || report.Changed != 1 {
		t.Fatalf("anonymisation = %+v, %v", report, err)
	}
	data, err := os.ReadFile(filepath.Join(output, "account - swift.cbprplus.03 - x.invalid - askiso-anonymised.xml"))
	if err != nil || strings.Contains(string(data), "GB82WEST") || !strings.Contains(string(data), "ZZ00") {
		t.Fatalf("scrubbed = %s, %v", data, err)
	}
	workspace2 := filepath.Join(t.TempDir(), "workspace")
	if _, err := Import(Options{Source: source, Workspace: workspace2, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	var suite Suite
	if err := readJSON(filepath.Join(workspace2, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, testCase := range suite.Cases {
		if strings.Contains(testCase.Sample, "askiso-anonymised") {
			found = true
			if testCase.Origin != "askiso-anonymised" {
				t.Fatalf("origin = %q", testCase.Origin)
			}
		}
	}
	if !found {
		t.Fatal("anonymised case was not indexed")
	}
	if got := string(anonymiseSample([]byte("123456789012"))); got != "000000000000" {
		t.Fatalf("number scrub = %q", got)
	}
}

func TestEvidenceAndAnonymisationAdditionalBranches(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReviewChecklist(workspace, filepath.Join(t.TempDir(), "review.json"), "bad"); err == nil {
		t.Fatal("bad checklist time accepted")
	}
	if _, err := WriteReviewChecklist(workspace, filepath.Join(t.TempDir(), "missing", "review.json"), ""); err == nil {
		t.Fatal("missing output parent accepted")
	}
	if _, err := AnonymiseSamples(source, workspace, filepath.Join(t.TempDir(), "outside")); err == nil {
		t.Fatal("outside anonymisation output accepted")
	}

	// Reclassify every fixture as synthetic to reach the explicit no-user path.
	for _, name := range []string{"payment.xml", "payment.invalid.xml"} {
		oldPath := filepath.Join(source, "samples", name)
		newPath := filepath.Join(source, "samples", strings.TrimSuffix(name, ".xml")+"-askiso-generated.xml")
		if err := os.Rename(oldPath, newPath); err != nil {
			t.Fatal(err)
		}
	}
	workspace2 := filepath.Join(t.TempDir(), "workspace")
	if _, err := Import(Options{Source: source, Workspace: workspace2, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	if _, err := AnonymiseSamples(source, workspace2, filepath.Join(source, "anonymous")); err == nil || !strings.Contains(err.Error(), "no user-provided") {
		t.Fatalf("no-user error = %v", err)
	}
}

func TestEvidenceDependencyErrors(t *testing.T) {
	sentinel := errors.New("sentinel")
	origVerify, origLoad, origRead := evidenceVerify, evidenceLoadManifest, evidenceReadJSON
	origRoots, origSafe, origBounded := evidenceValidateRoots, evidenceSafeSourcePath, evidenceReadBounded
	origAudit, origWrite := evidenceAuditSamples, evidenceWriteJSON
	t.Cleanup(func() {
		evidenceVerify, evidenceLoadManifest, evidenceReadJSON = origVerify, origLoad, origRead
		evidenceValidateRoots, evidenceSafeSourcePath, evidenceReadBounded = origRoots, origSafe, origBounded
		evidenceAuditSamples, evidenceWriteJSON = origAudit, origWrite
	})
	evidenceVerify = func(string, string) (*Verification, error) { return &Verification{}, nil }
	evidenceLoadManifest = func(string) (*Manifest, error) { return nil, sentinel }
	if _, err := AuditSamples("s", "w"); !errors.Is(err, sentinel) {
		t.Fatalf("manifest error = %v", err)
	}
	evidenceVerify = func(string, string) (*Verification, error) {
		return &Verification{workspaceFingerprint: strings.Repeat("a", 24)}, nil
	}
	evidenceLoadManifest = func(string) (*Manifest, error) {
		return &Manifest{Fingerprint: strings.Repeat("b", 24)}, nil
	}
	if _, err := AuditSamples("s", "w"); err == nil || !strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("audit generation drift error = %v", err)
	}
	evidenceVerify = func(string, string) (*Verification, error) { return &Verification{}, nil }
	evidenceLoadManifest = func(string) (*Manifest, error) { return &Manifest{}, nil }
	evidenceReadJSON = func(string, any) error { return sentinel }
	if _, err := AuditSamples("s", "w"); !errors.Is(err, sentinel) {
		t.Fatalf("suite error = %v", err)
	}
	evidenceReadJSON = func(_ string, value any) error {
		value.(*Suite).Cases = []SuiteCase{{ID: "case", BusinessService: "service", Sample: "sample", SampleSHA256: "hash"}}
		return nil
	}
	evidenceReadJSON = func(_ string, value any) error { value.(*Suite).Cases = []SuiteCase{{ID: "unpaired"}}; return nil }
	evidenceValidateRoots = func(string, string) (string, string, error) { return "root", "workspace", nil }
	if report, err := AuditSamples("s", "w"); err != nil || len(report.Unpaired) != 1 {
		t.Fatalf("unpaired audit = %+v, %v", report, err)
	}
	evidenceReadJSON = func(_ string, value any) error {
		value.(*Suite).Cases = []SuiteCase{{ID: "case", BusinessService: "service", Sample: "sample", SampleSHA256: "hash"}}
		return nil
	}
	evidenceValidateRoots = func(string, string) (string, string, error) { return "", "", sentinel }
	if _, err := AuditSamples("s", "w"); !errors.Is(err, sentinel) {
		t.Fatalf("roots error = %v", err)
	}
	evidenceValidateRoots = func(string, string) (string, string, error) { return "root", "workspace", nil }
	evidenceSafeSourcePath = func(string, string) (string, error) { return "", sentinel }
	if _, err := AuditSamples("s", "w"); !errors.Is(err, sentinel) {
		t.Fatalf("safe path error = %v", err)
	}
	evidenceSafeSourcePath = func(string, string) (string, error) { return "sample", nil }
	evidenceReadBounded = func(string) ([]byte, error) { return nil, sentinel }
	if _, err := AuditSamples("s", "w"); !errors.Is(err, sentinel) {
		t.Fatalf("read error = %v", err)
	}

	evidenceAuditSamples = func(string, string) (*SampleAudit, error) { return nil, sentinel }
	if _, err := WriteSampleAttestation("s", "w", "o", "r", "p", "", true); !errors.Is(err, sentinel) {
		t.Fatalf("audit error = %v", err)
	}
	evidenceAuditSamples = func(string, string) (*SampleAudit, error) { return &SampleAudit{ReadyForAttestation: true}, nil }
	if _, err := WriteSampleAttestation("s", "w", "o", "r", "p", "bad", true); err == nil {
		t.Fatal("bad attestation time accepted")
	}
	evidenceAuditSamples = func(string, string) (*SampleAudit, error) {
		return &SampleAudit{ReadyForAttestation: true, workspaceFingerprint: strings.Repeat("a", 24)}, nil
	}
	evidenceLoadManifest = func(string) (*Manifest, error) {
		return &Manifest{Fingerprint: strings.Repeat("b", 24)}, nil
	}
	if _, err := WriteSampleAttestation("s", "w", "o", "r", "p", "", true); err == nil ||
		!strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("attestation generation drift error = %v", err)
	}
	evidenceAuditSamples = func(string, string) (*SampleAudit, error) { return &SampleAudit{ReadyForAttestation: true}, nil }
	evidenceLoadManifest = func(string) (*Manifest, error) { return nil, sentinel }
	if _, err := WriteSampleAttestation("s", "w", "o", "r", "p", "", true); !errors.Is(err, sentinel) {
		t.Fatalf("attest manifest error = %v", err)
	}
	evidenceLoadManifest = func(string) (*Manifest, error) { return &Manifest{}, nil }
	evidenceReadJSON = func(string, any) error { return sentinel }
	if _, err := WriteSampleAttestation("s", "w", "o", "r", "p", "", true); !errors.Is(err, sentinel) {
		t.Fatalf("attest suite error = %v", err)
	}
	evidenceReadJSON = func(string, any) error { return nil }
	evidenceWriteJSON = func(string, any) error { return sentinel }
	if _, err := WriteSampleAttestation("s", "w", "o", "r", "p", "", true); !errors.Is(err, sentinel) {
		t.Fatalf("attest write error = %v", err)
	}
	if _, err := WriteReviewChecklist("w", "o", ""); !errors.Is(err, sentinel) {
		t.Fatalf("checklist write error = %v", err)
	}
	if _, err := WriteExternalEvidence("w", "o", "p", "", 1, true, true); !errors.Is(err, sentinel) {
		t.Fatalf("external write error = %v", err)
	}
}

func TestAnonymiseDependencyErrors(t *testing.T) {
	sentinel := errors.New("sentinel")
	origLoad, origJSON, origRoots := anonymiseLoadManifest, anonymiseReadJSON, anonymiseValidateRoots
	origSafe, origRead, origWrite := anonymiseSafeSource, anonymiseReadBounded, anonymiseWriteSample
	t.Cleanup(func() {
		anonymiseLoadManifest, anonymiseReadJSON, anonymiseValidateRoots = origLoad, origJSON, origRoots
		anonymiseSafeSource, anonymiseReadBounded, anonymiseWriteSample = origSafe, origRead, origWrite
	})
	anonymiseLoadManifest = func(string) (*Manifest, error) { return nil, sentinel }
	if _, err := AnonymiseSamples("s", "w", "o"); !errors.Is(err, sentinel) {
		t.Fatalf("manifest error = %v", err)
	}
	anonymiseLoadManifest = func(string) (*Manifest, error) { return &Manifest{}, nil }
	anonymiseReadJSON = func(string, any) error { return sentinel }
	if _, err := AnonymiseSamples("s", "w", "o"); !errors.Is(err, sentinel) {
		t.Fatalf("suite error = %v", err)
	}
	anonymiseReadJSON = func(_ string, value any) error {
		value.(*Suite).Cases = []SuiteCase{{Sample: "sample.xml"}}
		return nil
	}
	anonymiseValidateRoots = func(string, string) (string, string, error) { return "", "", sentinel }
	if _, err := AnonymiseSamples("s", "w", "o"); !errors.Is(err, sentinel) {
		t.Fatalf("roots error = %v", err)
	}
	root := t.TempDir()
	source, workspace := filepath.Join(root, "source"), filepath.Join(root, "workspace")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	anonymiseValidateRoots = func(string, string) (string, string, error) { return source, workspace, nil }
	anonymiseSafeSource = func(string, string) (string, error) { return "", sentinel }
	if _, err := AnonymiseSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("safe error = %v", err)
	}
	anonymiseSafeSource = func(string, string) (string, error) { return "sample", nil }
	anonymiseReadBounded = func(string) ([]byte, error) { return nil, sentinel }
	if _, err := AnonymiseSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("read error = %v", err)
	}
	anonymiseReadBounded = func(string) ([]byte, error) { return []byte(`<Document/>`), nil }
	anonymiseWriteSample = func(string, []byte) error { return sentinel }
	if _, err := AnonymiseSamples("s", "w", filepath.Join(source, "out")); !errors.Is(err, sentinel) {
		t.Fatalf("write error = %v", err)
	}
}
