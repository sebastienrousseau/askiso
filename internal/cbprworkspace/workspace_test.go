// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/codes"
	"github.com/sebastienrousseau/askiso/internal/rules"
	"github.com/sebastienrousseau/askiso/internal/schemagen"
	"github.com/sebastienrousseau/askiso/internal/xsd"
)

const workspaceSchema = `<?xml version="1.0"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
 targetNamespace="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"
 xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08" elementFormDefault="qualified">
 <xs:annotation><xs:documentation>Business Service swift.cbprplus.03</xs:documentation></xs:annotation>
 <xs:simpleType name="ExternalPurpose1Code"><xs:restriction base="xs:string"><xs:minLength value="1"/><xs:maxLength value="4"/></xs:restriction></xs:simpleType>
 <xs:complexType name="DocumentType"><xs:sequence><xs:element name="Purpose" type="ExternalPurpose1Code"/></xs:sequence></xs:complexType>
 <xs:element name="Document" type="DocumentType"/>
</xs:schema>`

const workspaceHeaderSchema = `<?xml version="1.0"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
 targetNamespace="urn:iso:std:iso:20022:tech:xsd:head.001.001.02"
 xmlns="urn:iso:std:iso:20022:tech:xsd:head.001.001.02" elementFormDefault="qualified">
 <xs:simpleType name="Text"><xs:restriction base="xs:string"><xs:minLength value="1"/></xs:restriction></xs:simpleType>
 <xs:complexType name="Party"><xs:sequence><xs:element name="FIId" type="Text"/></xs:sequence></xs:complexType>
 <xs:complexType name="Header"><xs:sequence>
  <xs:element name="Fr" type="Party"/><xs:element name="To" type="Party"/>
  <xs:element name="BizMsgIdr" type="Text"/><xs:element name="MsgDefIdr" type="Text"/>
  <xs:element name="BizSvc" type="Text"/><xs:element name="CreDt" type="xs:dateTime"/>
 </xs:sequence></xs:complexType>
 <xs:element name="AppHdr" type="Header"/>
</xs:schema>`

func writeWorkspaceFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func workspaceFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "input")
	workspace := filepath.Join(root, "private-output")
	writeWorkspaceFile(t, filepath.Join(source, "schemas", "pacs.008.001.08.xsd"), workspaceSchema)
	writeWorkspaceFile(t, filepath.Join(source, "samples", "payment.xml"),
		`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><Purpose>SALA</Purpose></Document>`)
	writeWorkspaceFile(t, filepath.Join(source, "samples", "payment.invalid.xml"),
		`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><Purpose>NOPE</Purpose></Document>`)
	writeWorkspaceFile(t, filepath.Join(source, "exports", "guide.xml"), `<usageGuideline release="SR2025"/>`)
	writeWorkspaceFile(t, filepath.Join(source, "exports", "guide.xlsx"), "inventory only")
	external := filepath.Join(source, "2Q2026_externalcodesets_v3.json")
	writeWorkspaceFile(t, external,
		`{"definitions":{"ExternalPurpose1Code":{"type":"string","enum":["SALA","SUPP"]}}}`)

	pack := &rules.CBPRPack{
		Format: "askiso-cbpr-pack/v1",
		Sources: []rules.CBPRPackSource{{
			Name: "pacs008.pdf", SHA256: strings.Repeat("a", 64),
			MessageID: "pacs.008.001.08", UsageIdentifiers: []string{"swift.cbprplus.03"}, Constraints: 1,
		}},
		Constraints: []rules.CBPRPackConstraint{{
			Source: "pacs008.pdf", MessageID: "pacs.008.001.08",
			UsageIdentifiers: []string{"swift.cbprplus.03"},
			Path:             []string{"Document", "Purpose"}, Min: 1, Max: 1,
		}},
	}
	if err := rules.WriteCBPRPack(filepath.Join(source, "source.cbpr-pack.json"), pack); err != nil {
		t.Fatal(err)
	}
	return source, workspace, external
}

// useLegacyWorkspace deliberately disables the generation pointer so tests
// that mutate workspace artifacts exercise v1 compatibility paths. Published
// generations are immutable; generation-specific tamper tests live alongside
// the publisher tests.
func useLegacyWorkspace(t *testing.T, workspace string) {
	t.Helper()
	if err := os.Remove(filepath.Join(workspace, CurrentFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestImportAndVerifyPrivateWorkspace(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	manifest, err := Import(Options{Source: source, Workspace: workspace, Release: "sr2025", ExternalCodes: external})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Format != ManifestFormat || manifest.Release != "SR2025" || len(manifest.Fingerprint) != 24 {
		t.Fatalf("manifest identity = %+v", manifest)
	}
	if manifest.ExternalCodes == nil || manifest.ExternalCodes.Format != "json-schema" ||
		manifest.ExternalCodes.Sets != 1 || manifest.ExternalCodes.Codes != 2 {
		t.Fatalf("external publication = %+v", manifest.ExternalCodes)
	}
	if manifest.Coverage.ExpectedUsageGuidelines != 31 || manifest.Coverage.PresentUsageGuidelines != 1 ||
		len(manifest.Coverage.MissingUsageGuidelines) != 30 || manifest.Coverage.Schemas != 1 ||
		manifest.Coverage.Samples != 2 || manifest.Coverage.UsageGuidelineXML != 1 ||
		manifest.Coverage.Spreadsheets != 1 || manifest.SuiteCases != 2 {
		t.Fatalf("coverage = %+v, suite cases %d", manifest.Coverage, manifest.SuiteCases)
	}
	if manifest.Pack == "" || manifest.PackFingerprint == "" {
		t.Fatalf("compiled pack provenance is absent: %+v", manifest)
	}
	if strings.Contains(string(mustRead(t, filepath.Join(workspace, ManifestFile))), source) {
		t.Fatal("manifest disclosed the absolute source directory")
	}
	if info, err := os.Stat(filepath.Join(workspace, ManifestFile)); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
		t.Fatalf("manifest permissions = %v, %v", info, err)
	}

	loaded, err := LoadManifest(workspace)
	if err != nil || loaded.Fingerprint != manifest.Fingerprint {
		t.Fatalf("loading manifest: %v, %+v", err, loaded)
	}
	report, err := Verify(source, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if report.Cases != 2 || report.Passed != 2 || report.Failed != 0 {
		t.Fatalf("verification = %+v", report)
	}
}

func TestImportGeneratesPrivatePositiveAndNegativeSamples(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	manifest, err := Import(Options{
		Source: source, Workspace: workspace, ExternalCodes: external, GenerateSamples: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Coverage.ExecutableUsageGuidelines != 1 ||
		len(manifest.Coverage.MissingExecutableUsageGuidelines) != 30 ||
		manifest.Coverage.GeneratedSamples != 2 || manifest.SuiteCases != 4 {
		t.Fatalf("generated coverage = %+v, cases %d", manifest.Coverage, manifest.SuiteCases)
	}
	var suite Suite
	if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	generated := 0
	for _, testCase := range suite.Cases {
		if testCase.Origin != "generated" {
			continue
		}
		generated++
		path, err := safeGeneratedPath(workspace, testCase.Sample)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0) {
			t.Fatalf("generated sample permissions = %v, %v", info, err)
		}
		body := string(mustRead(t, path))
		if testCase.Expected == "valid" && !strings.Contains(body, "<Purpose>SALA</Purpose>") {
			t.Fatalf("positive sample did not use imported external code: %s", body)
		}
		if testCase.Expected == "invalid" && testCase.Mutation != "wrong-namespace" {
			t.Fatalf("negative provenance = %+v", testCase)
		}
	}
	if generated != 2 {
		t.Fatalf("generated cases = %d: %+v", generated, suite.Cases)
	}
	report, err := Verify(source, workspace)
	if err != nil || report.Passed != 4 || report.Failed != 0 {
		t.Fatalf("generated verification = %+v, %v", report, err)
	}

	useLegacyWorkspace(t, workspace)
	for _, testCase := range suite.Cases {
		if testCase.Origin == "generated" {
			writeWorkspaceFile(t, filepath.Join(workspace, filepath.FromSlash(testCase.Sample)), "<tampered/>")
			break
		}
	}
	if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "fingerprint changed") {
		t.Fatalf("tampered generated sample error = %v", err)
	}
}

func TestExportValidSamplesAndPreserveSyntheticProvenance(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	writeWorkspaceFile(t, filepath.Join(source, "schemas", "head.001.001.02.xsd"), workspaceHeaderSchema)
	if _, err := Import(Options{
		Source: source, Workspace: workspace, ExternalCodes: external, GenerateSamples: true,
	}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(source, "04 Conformance Samples", "Valid")
	report, err := ExportValidSamples(source, workspace, output)
	if err != nil {
		t.Fatal(err)
	}
	if report.Release != "SR2025" || report.Generated != 1 || report.Output != output || len(report.Files) != 1 {
		t.Fatalf("export report = %+v", report)
	}
	path := filepath.Join(output, report.Files[0])
	body := string(mustRead(t, path))
	for _, want := range []string{"<Envelope>", "<AppHdr", "<BizSvc>swift.cbprplus.03</BizSvc>", "<Document"} {
		if !strings.Contains(body, want) {
			t.Fatalf("exported sample lacks %q: %s", want, body)
		}
	}
	if info, err := os.Stat(path); err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("exported sample permissions = %v, %v", info, err)
	}

	reimported := filepath.Join(t.TempDir(), "reimported")
	manifest, err := Import(Options{Source: source, Workspace: reimported, ExternalCodes: external})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Coverage.Samples != 2 || manifest.Coverage.AskISOGeneratedSamples != 1 {
		t.Fatalf("reimported sample count = %+v", manifest.Coverage)
	}
	var suite Suite
	if err := readJSON(filepath.Join(reimported, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, testCase := range suite.Cases {
		if testCase.Origin == "askiso-generated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("synthetic provenance absent: %+v", suite.Cases)
	}
	positive, _, _ := userSampleCoverage(suite.Cases)
	if len(positive) != 0 {
		t.Fatalf("AskISO fixture changed independent user evidence: %v", positive)
	}
	if verified, err := Verify(source, reimported); err != nil || verified.Failed != 0 {
		t.Fatalf("reimported fixture verification = %+v, %v", verified, err)
	}
}

func TestExportValidSamplesRejectsUnsafeOrIncompleteInputs(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportValidSamples(source, workspace, filepath.Join(source, "valid")); err == nil ||
		!strings.Contains(err.Error(), "no generated positive") {
		t.Fatalf("missing generated cases error = %v", err)
	}

	generatedWorkspace := filepath.Join(t.TempDir(), "generated")
	if _, err := Import(Options{Source: source, Workspace: generatedWorkspace, ExternalCodes: external, GenerateSamples: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := ExportValidSamples(source, generatedWorkspace, filepath.Join(t.TempDir(), "outside")); err == nil ||
		!strings.Contains(err.Error(), "inside the private source") {
		t.Fatalf("outside output error = %v", err)
	}
	if _, err := ExportValidSamples(source, generatedWorkspace, filepath.Join(source, "valid")); err == nil ||
		!strings.Contains(err.Error(), "paired BAH") {
		t.Fatalf("missing header error = %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(source, "valid")); err != nil || len(entries) != 0 {
		t.Fatalf("failed export left partial samples: %v, %v", entries, err)
	}
}

func TestExportValidSamplesRejectsFailedAndDuplicateGeneratedCases(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*Suite)
		want string
	}{
		{name: "failed verification", want: "failed case", edit: func(suite *Suite) {
			for index := range suite.Cases {
				if suite.Cases[index].Origin == "generated" && suite.Cases[index].Expected == "valid" {
					suite.Cases[index].Expected = "invalid"
					return
				}
			}
		}},
		{name: "duplicate positive", want: "more than one positive", edit: func(suite *Suite) {
			for _, testCase := range suite.Cases {
				if testCase.Origin == "generated" && testCase.Expected == "valid" {
					duplicate := testCase
					duplicate.ID += "/duplicate"
					suite.Cases = append(suite.Cases, duplicate)
					return
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, workspace, external := workspaceFixture(t)
			writeWorkspaceFile(t, filepath.Join(source, "schemas", "head.001.001.02.xsd"), workspaceHeaderSchema)
			if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external, GenerateSamples: true}); err != nil {
				t.Fatal(err)
			}
			rewriteWorkspaceSuite(t, workspace, test.edit)
			_, err := ExportValidSamples(source, workspace, filepath.Join(source, "valid"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("export error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestExportValidSamplesRejectsUnusableHeaderSchemas(t *testing.T) {
	for _, test := range []struct {
		name, header, want string
	}{
		{name: "malformed", header: `<broken>`, want: "unexpected EOF"},
		{name: "no root", header: `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:iso:std:iso:20022:tech:xsd:head.001.001.02"/>`, want: "generating BAH"},
		{name: "wrong service", header: strings.Replace(workspaceHeaderSchema,
			`<xs:restriction base="xs:string"><xs:minLength value="1"/></xs:restriction>`,
			`<xs:restriction base="xs:string"><xs:enumeration value="wrong.service"/></xs:restriction>`, 1), want: "did not validate"},
		{name: "wrong namespace", header: strings.ReplaceAll(workspaceHeaderSchema, "head.001.001.02", "head.001.001.03"), want: "BAH error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, workspace, external := workspaceFixture(t)
			headerPath := filepath.Join(source, "schemas", "head.001.001.02.xsd")
			header := test.header
			if test.name == "malformed" {
				header = workspaceHeaderSchema
			}
			writeWorkspaceFile(t, headerPath, header)
			if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external, GenerateSamples: true}); err != nil {
				t.Fatal(err)
			}
			if test.name == "malformed" {
				writeWorkspaceFile(t, headerPath, test.header)
			}
			_, err := ExportValidSamples(source, workspace, filepath.Join(source, "valid"))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("export error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSampleExportPathAndFileSafety(t *testing.T) {
	source, workspace := t.TempDir(), t.TempDir()
	if _, err := prepareSampleOutput(source, workspace, ""); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("blank output error = %v", err)
	}
	if _, err := prepareSampleOutput(source, workspace, filepath.Join(workspace, "samples")); err == nil || !strings.Contains(err.Error(), "private source") {
		t.Fatalf("workspace output error = %v", err)
	}
	if _, err := prepareSampleOutput(source, source, filepath.Join(source, "workspace-samples")); err == nil || !strings.Contains(err.Error(), "generated workspace") {
		t.Fatalf("nested workspace output error = %v", err)
	}
	file := filepath.Join(source, "file")
	writeWorkspaceFile(t, file, "not a directory")
	if _, err := prepareSampleOutput(source, workspace, file); err == nil || !strings.Contains(err.Error(), "real directory") {
		t.Fatalf("file output error = %v", err)
	}
	if _, err := prepareSampleOutput(source, workspace, filepath.Join(file, "child")); err == nil {
		t.Fatal("output below a regular file succeeded")
	}
	directory := filepath.Join(source, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeExportedSample(directory, []byte("sample")); err == nil || !strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("directory sample error = %v", err)
	}
	if runtime.GOOS != "windows" {
		locked := filepath.Join(source, "locked")
		if err := os.Mkdir(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		if _, err := prepareSampleOutput(source, workspace, filepath.Join(locked, "output")); err == nil {
			t.Fatal("creating output below an unwritable directory succeeded")
		}
		if err := writeExportedSample(filepath.Join(locked, "sample.xml"), []byte("sample")); err == nil {
			t.Fatal("writing sample below an unwritable directory succeeded")
		}
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(source, "link")
		if err := os.Symlink(directory, link); err != nil {
			t.Fatal(err)
		}
		if _, err := prepareSampleOutput(source, workspace, link); err == nil || !strings.Contains(err.Error(), "real directory") {
			t.Fatalf("symlink output error = %v", err)
		}
		if err := writeExportedSample(link, []byte("sample")); err == nil || !strings.Contains(err.Error(), "non-regular") {
			t.Fatalf("symlink sample error = %v", err)
		}
	}
}

func TestExportValidSamplesPropagatesDependencyFailures(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	writeWorkspaceFile(t, filepath.Join(source, "schemas", "head.001.001.02.xsd"), workspaceHeaderSchema)
	if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external, GenerateSamples: true}); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(source, "valid")
	sentinel := errors.New("export dependency failed")
	assertFailure := func(t *testing.T) {
		t.Helper()
		if _, err := ExportValidSamples(source, workspace, output); !errors.Is(err, sentinel) {
			t.Fatalf("dependency error = %v", err)
		}
	}

	t.Run("manifest", func(t *testing.T) {
		old := exportLoadManifest
		exportLoadManifest = func(string) (*Manifest, error) { return nil, sentinel }
		t.Cleanup(func() { exportLoadManifest = old })
		assertFailure(t)
	})
	t.Run("generation drift", func(t *testing.T) {
		oldVerify, oldManifest := exportVerify, exportLoadManifest
		exportVerify = func(string, string) (*Verification, error) {
			return &Verification{workspaceFingerprint: strings.Repeat("a", 24)}, nil
		}
		exportLoadManifest = func(string) (*Manifest, error) {
			return &Manifest{Fingerprint: strings.Repeat("b", 24)}, nil
		}
		t.Cleanup(func() { exportVerify, exportLoadManifest = oldVerify, oldManifest })
		if _, err := ExportValidSamples(source, workspace, output); err == nil ||
			!strings.Contains(err.Error(), "generation changed") {
			t.Fatalf("generation drift error = %v", err)
		}
	})
	t.Run("suite", func(t *testing.T) {
		old := exportReadJSON
		exportReadJSON = func(string, any) error { return sentinel }
		t.Cleanup(func() { exportReadJSON = old })
		assertFailure(t)
	})
	t.Run("roots", func(t *testing.T) {
		old := exportValidateRoots
		exportValidateRoots = func(string, string) (string, string, error) { return "", "", sentinel }
		t.Cleanup(func() { exportValidateRoots = old })
		assertFailure(t)
	})
	t.Run("external codes", func(t *testing.T) {
		old := exportLoadExternalSets
		exportLoadExternalSets = func(string) (*codes.ExternalSets, error) { return nil, sentinel }
		t.Cleanup(func() { exportLoadExternalSets = old })
		assertFailure(t)
	})
	t.Run("generated path", func(t *testing.T) {
		old := exportSafeGenerated
		exportSafeGenerated = func(string, string) (string, error) { return "", sentinel }
		t.Cleanup(func() { exportSafeGenerated = old })
		assertFailure(t)
	})
	t.Run("generated read", func(t *testing.T) {
		old := exportReadBounded
		exportReadBounded = func(string) ([]byte, error) { return nil, sentinel }
		t.Cleanup(func() { exportReadBounded = old })
		assertFailure(t)
	})
	t.Run("source path", func(t *testing.T) {
		old := exportSafeSource
		exportSafeSource = func(string, string) (string, error) { return "", sentinel }
		t.Cleanup(func() { exportSafeSource = old })
		assertFailure(t)
	})
	t.Run("schema parse", func(t *testing.T) {
		old := exportParseSchema
		exportParseSchema = func(string) (*xsd.Schema, error) { return nil, sentinel }
		t.Cleanup(func() { exportParseSchema = old })
		assertFailure(t)
	})
	t.Run("sample write", func(t *testing.T) {
		old := exportWriteSample
		exportWriteSample = func(string, []byte) error { return sentinel }
		t.Cleanup(func() { exportWriteSample = old })
		assertFailure(t)
	})
}

func TestSampleExportFilesystemFailures(t *testing.T) {
	source, workspace := t.TempDir(), t.TempDir()
	sentinel := errors.New("filesystem failed")
	t.Run("absolute path", func(t *testing.T) {
		old := sampleAbs
		sampleAbs = func(string) (string, error) { return "", sentinel }
		t.Cleanup(func() { sampleAbs = old })
		if _, err := prepareSampleOutput(source, workspace, "output"); !errors.Is(err, sentinel) {
			t.Fatalf("absolute-path error = %v", err)
		}
	})
	t.Run("output stat", func(t *testing.T) {
		old := sampleLstat
		sampleLstat = func(string) (os.FileInfo, error) { return nil, sentinel }
		t.Cleanup(func() { sampleLstat = old })
		if _, err := prepareSampleOutput(source, workspace, filepath.Join(source, "output")); !errors.Is(err, sentinel) {
			t.Fatalf("output-stat error = %v", err)
		}
	})
	t.Run("output mkdir", func(t *testing.T) {
		old := sampleMkdirAll
		sampleMkdirAll = func(string, os.FileMode) error { return sentinel }
		t.Cleanup(func() { sampleMkdirAll = old })
		if _, err := prepareSampleOutput(source, workspace, filepath.Join(source, "mkdir")); !errors.Is(err, sentinel) {
			t.Fatalf("output-mkdir error = %v", err)
		}
	})
	t.Run("output chmod", func(t *testing.T) {
		old := sampleChmod
		sampleChmod = func(string, os.FileMode) error { return sentinel }
		t.Cleanup(func() { sampleChmod = old })
		if _, err := prepareSampleOutput(source, workspace, filepath.Join(source, "chmod")); !errors.Is(err, sentinel) {
			t.Fatalf("output-chmod error = %v", err)
		}
	})
	t.Run("sample stat", func(t *testing.T) {
		old := sampleLstat
		sampleLstat = func(string) (os.FileInfo, error) { return nil, sentinel }
		t.Cleanup(func() { sampleLstat = old })
		if err := writeExportedSample(filepath.Join(source, "sample.xml"), nil); !errors.Is(err, sentinel) {
			t.Fatalf("sample-stat error = %v", err)
		}
	})
	t.Run("sample write", func(t *testing.T) {
		old := sampleWriteFile
		sampleWriteFile = func(string, []byte, os.FileMode) error { return sentinel }
		t.Cleanup(func() { sampleWriteFile = old })
		if err := writeExportedSample(filepath.Join(source, "sample.xml"), nil); !errors.Is(err, sentinel) {
			t.Fatalf("sample-write error = %v", err)
		}
	})
	t.Run("sample chmod", func(t *testing.T) {
		old := sampleChmod
		sampleChmod = func(string, os.FileMode) error { return sentinel }
		t.Cleanup(func() { sampleChmod = old })
		if err := writeExportedSample(filepath.Join(source, "sample.xml"), nil); !errors.Is(err, sentinel) {
			t.Fatalf("sample-chmod error = %v", err)
		}
	})
}

func rewriteWorkspaceSuite(t *testing.T, workspace string, edit func(*Suite)) {
	t.Helper()
	useLegacyWorkspace(t, workspace)
	manifest, err := LoadManifest(workspace)
	if err != nil {
		t.Fatal(err)
	}
	var suite Suite
	if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	edit(&suite)
	suite.Fingerprint = suiteFingerprint(&suite)
	manifest.SuiteCases = len(suite.Cases)
	manifest.SuiteFingerprint = suite.Fingerprint
	manifest.Fingerprint = manifestFingerprint(manifest)
	if err := writeJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(workspace, ManifestFile), manifest); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaUsageIdentifierEvidence(t *testing.T) {
	root := t.TempDir()
	if got := usageIdentifiersFromSchema("missing", "x.xsd", ""); got != nil {
		t.Fatalf("empty message evidence = %v", got)
	}
	if got := usageIdentifiersFromSchema(filepath.Join(root, "missing.xsd"), "x.xsd", "pacs.009.001.08"); got != nil {
		t.Fatalf("unreadable schema evidence = %v", got)
	}
	multi := filepath.Join(root, "pacs.009.001.08.xsd")
	multiBody := strings.ReplaceAll(workspaceSchema, "pacs.008.001.08", "pacs.009.001.08")
	multiBody = strings.ReplaceAll(multiBody, "swift.cbprplus.03", "business-service-not-exported")
	writeWorkspaceFile(t, multi, multiBody)
	if got := usageIdentifiersFromSchema(multi, "plain/pacs.009.001.08.xsd", "pacs.009.001.08"); len(got) != 0 {
		t.Fatalf("ambiguous multi-variant XSD = %v", got)
	}
	if got := usageIdentifiersFromSchema(multi, "CBPRPlus-pacs.009_COV/pacs.009.001.08.xsd", "pacs.009.001.08"); len(got) != 1 || got[0] != "swift.cbprplus.cov.03" {
		t.Fatalf("COV folder inference = %v", got)
	}
	if got := usageIdentifiersFromSchema(multi, "CBPRPlus-pacs.009_MultipleCharges/pacs.009.001.08.xsd", "pacs.009.001.08"); len(got) != 0 {
		t.Fatalf("unsupported MLP service inferred for pacs.009 = %v", got)
	}
	single := filepath.Join(root, "camt.108.001.01.xsd")
	singleBody := strings.ReplaceAll(workspaceSchema, "pacs.008.001.08", "camt.108.001.01")
	singleBody = strings.ReplaceAll(singleBody, "swift.cbprplus.03", "CBPRPlus SR2025")
	writeWorkspaceFile(t, single, singleBody)
	if got := usageIdentifiersFromSchema(single, "camt.108/schema.xsd", "camt.108.001.01"); len(got) != 1 || got[0] != "swift.cbprplus.02" {
		t.Fatalf("unambiguous service = %v", got)
	}
	if got := usageIdentifiersFromSchema(single, "x.xsd", "not.cbpr"); got != nil || isExpectedCBPRMessage("not.cbpr") ||
		!isExpectedCBPRMessage("pacs.008.001.08") {
		t.Fatalf("unknown message evidence = %v", got)
	}
}

func TestMyStandardsVariantFolderAliases(t *testing.T) {
	for path, want := range map[string]string{
		"CBPRPlus-camt_105_MultipleCharges/schema.xsd":                           "mlp",
		"CBPRPlus-camt_106_multiple_charges/schema.xsd":                          "mlp",
		"CBPRPlus-pacs_010_Interbank_Direct_Debit-_Margin_Collection/schema.xsd": "col",
		"CBPRPlus-pacs_008_STP/schema.xsd":                                       "stp",
	} {
		markers := variantMarkers(path)
		if len(markers) != 1 || markers[0] != want {
			t.Fatalf("variantMarkers(%q) = %v, want %q", path, markers, want)
		}
	}
	if got := sampleUsageIdentifiers("camt.105.001.02", "case-MultipleCharges.xml"); len(got) != 1 || got[0] != "swift.cbprplus.mlp.02" {
		t.Fatalf("sample MLP alias = %v", got)
	}
}

func TestDiscoveryWarnsForAmbiguousExecutableVariants(t *testing.T) {
	root := t.TempDir()
	body := strings.ReplaceAll(workspaceSchema, "pacs.008.001.08", "pacs.009.001.08")
	body = strings.ReplaceAll(body, "swift.cbprplus.03", "business-service-not-exported")
	writeWorkspaceFile(t, filepath.Join(root, "plain.xsd"), body)
	writeWorkspaceFile(t, filepath.Join(root, "plain.xml"), body)
	_, warnings, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 2 || !strings.Contains(strings.Join(warnings, "\n"), "not counted as exact") {
		t.Fatalf("ambiguous schema warnings = %v", warnings)
	}
}

func TestGeneratedSuiteDiagnosticsAndPathSafety(t *testing.T) {
	t.Run("no schema", func(t *testing.T) {
		source := filepath.Join(t.TempDir(), "source")
		writeWorkspaceFile(t, filepath.Join(source, "notes.txt"), "ignored")
		manifest, err := Import(Options{
			Source: source, Workspace: filepath.Join(t.TempDir(), "workspace"), GenerateSamples: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(manifest.Warnings, "\n"), "no entitled XML/XSD") {
			t.Fatalf("missing-XSD warning absent: %v", manifest.Warnings)
		}
	})

	t.Run("generation failures", func(t *testing.T) {
		root := t.TempDir()
		malformed := filepath.Join(root, "malformed.xsd")
		writeWorkspaceFile(t, malformed, `<broken>`)
		noRoot := filepath.Join(root, "no-root.xsd")
		writeWorkspaceFile(t, noRoot, `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"/>`)
		recursive := filepath.Join(root, "recursive.xsd")
		writeWorkspaceFile(t, recursive, `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:test" xmlns="urn:test" elementFormDefault="qualified">
 <xs:complexType name="Loop"><xs:sequence><xs:element name="Child" type="Loop"/></xs:sequence></xs:complexType>
 <xs:element name="Document" type="Loop"/>
</xs:schema>`)
		acceptedNegative := filepath.Join(root, "accepted-negative.xsd")
		writeWorkspaceFile(t, acceptedNegative, `
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:askiso:invalid" xmlns="urn:askiso:invalid" elementFormDefault="qualified">
 <xs:complexType name="Empty"><xs:sequence/></xs:complexType><xs:element name="Document" type="Empty"/>
</xs:schema>`)
		files := []discovered{
			{SourceFile: SourceFile{Path: "malformed.xsd", Kind: "schema", MessageID: "test.001.001.01"}, abs: malformed},
			{SourceFile: SourceFile{Path: "no-root.xsd", Kind: "schema", MessageID: "test.001.001.01"}, abs: noRoot},
			{SourceFile: SourceFile{Path: "recursive.xsd", Kind: "schema", MessageID: "test.001.001.01"}, abs: recursive},
			{SourceFile: SourceFile{Path: "accepted-negative.xsd", Kind: "schema", MessageID: "test.001.001.01"}, abs: acceptedNegative},
		}
		cases, warnings := generateSuiteCases(root, files, nil)
		if len(cases) != 0 || len(warnings) != 4 {
			t.Fatalf("failure diagnostics = %d cases, %v", len(cases), warnings)
		}
		joined := strings.Join(warnings, "\n")
		for _, want := range []string{"could not generate", "did not validate", "unexpectedly accepted"} {
			if !strings.Contains(joined, want) {
				t.Fatalf("%q absent from %s", want, joined)
			}
		}
	})

	t.Run("generator error", func(t *testing.T) {
		root := t.TempDir()
		schemaPath := filepath.Join(root, "schema.xsd")
		writeWorkspaceFile(t, schemaPath, workspaceSchema)
		original := generateFromSchema
		generateFromSchema = func(*xsd.Schema, schemagen.Options) (*schemagen.Result, error) {
			return nil, errors.New("generation failed")
		}
		t.Cleanup(func() { generateFromSchema = original })
		cases, warnings := generateSuiteCases(root, []discovered{{
			SourceFile: SourceFile{Path: "schema.xsd", Kind: "schema", MessageID: "pacs.008.001.08"}, abs: schemaPath,
		}}, nil)
		if len(cases) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "generation failed") {
			t.Fatalf("generator error = %v, %v", cases, warnings)
		}
	})

	t.Run("writer refuses unsafe targets", func(t *testing.T) {
		root := t.TempDir()
		writeWorkspaceFile(t, filepath.Join(root, GeneratedDir), "not a directory")
		if _, err := writeGeneratedSample(root, GeneratedDir+"/x.xml", []byte("x")); err == nil {
			t.Fatal("non-directory generated path was accepted")
		}
		missingParent := filepath.Join(root, "missing", "workspace")
		if _, err := writeGeneratedSample(missingParent, GeneratedDir+"/x.xml", []byte("x")); err == nil {
			t.Fatal("missing workspace parent was accepted")
		}

		root = t.TempDir()
		if _, err := writeGeneratedSample(root, GeneratedDir+"/x.xml", []byte("x")); err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" {
			link := filepath.Join(root, GeneratedDir, "link.xml")
			if err := os.Symlink(filepath.Join(root, GeneratedDir, "x.xml"), link); err != nil {
				t.Fatal(err)
			}
			if _, err := writeGeneratedSample(root, GeneratedDir+"/link.xml", []byte("x")); err == nil {
				t.Fatal("symlinked generated sample was overwritten")
			}
		}
		if _, err := writeGeneratedSample(root, GeneratedDir, []byte("x")); err == nil {
			t.Fatal("generated directory was overwritten")
		}
	})

	if runtime.GOOS != "windows" {
		t.Run("generation reports sample write failures", func(t *testing.T) {
			sourceRoot := t.TempDir()
			schemaPath := filepath.Join(sourceRoot, "schema.xsd")
			writeWorkspaceFile(t, schemaPath, workspaceSchema)
			digest, err := fileSHA256(schemaPath)
			if err != nil {
				t.Fatal(err)
			}
			file := discovered{SourceFile: SourceFile{
				Path: "schema.xsd", Kind: "schema", MessageID: "pacs.008.001.08", SHA256: digest,
			}, abs: schemaPath}
			stem := generatedSampleStem(file)
			for _, suffix := range []string{".valid.xml", ".invalid.xml"} {
				workspace := t.TempDir()
				dir := filepath.Join(workspace, GeneratedDir)
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(workspace, "target"), filepath.Join(dir, stem+suffix)); err != nil {
					t.Fatal(err)
				}
				cases, warnings := generateSuiteCases(workspace, []discovered{file}, nil)
				if len(cases) != 0 || len(warnings) != 1 || !strings.Contains(warnings[0], "could not save generated") {
					t.Fatalf("%s write failure = %v, %v", suffix, cases, warnings)
				}
			}
		})
	}

	t.Run("reader refuses unsafe targets", func(t *testing.T) {
		root := t.TempDir()
		for _, path := range []string{"outside.xml", GeneratedDir, GeneratedDir + "/../outside.xml"} {
			if _, err := safeGeneratedPath(root, path); err == nil {
				t.Fatalf("unsafe generated path accepted: %s", path)
			}
		}
		if _, err := safeGeneratedPath(root, GeneratedDir+"/missing.xml"); err == nil {
			t.Fatal("missing generated sample was accepted")
		}
		if runtime.GOOS != "windows" {
			target := filepath.Join(root, "target.xml")
			writeWorkspaceFile(t, target, "x")
			if err := os.Mkdir(filepath.Join(root, GeneratedDir), 0o700); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(root, GeneratedDir, "link.xml")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := safeGeneratedPath(root, GeneratedDir+"/link.xml"); err == nil {
				t.Fatal("symlinked generated sample was accepted")
			}
		}
	})
}

func TestVerifyDetectsChangedAndUnsafeSources(t *testing.T) {
	source, workspace, _ := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	sample := filepath.Join(source, "samples", "payment.xml")
	writeWorkspaceFile(t, sample, `<changed/>`)
	if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "fingerprint changed") {
		t.Fatalf("changed source error = %v", err)
	}

	// Restore the source, then demonstrate that a tampered suite cannot escape
	// the directory selected by the user.
	writeWorkspaceFile(t, sample,
		`<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.08"><Purpose>SALA</Purpose></Document>`)
	useLegacyWorkspace(t, workspace)
	var suite Suite
	if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	suite.Cases[0].Sample = "../outside.xml"
	suite.Fingerprint = suiteFingerprint(&suite)
	if err := writeJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := readJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.SuiteFingerprint = suite.Fingerprint
	manifest.Fingerprint = manifestFingerprint(&manifest)
	if err := writeJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "unsafe suite source path") {
		t.Fatalf("unsafe suite error = %v", err)
	}
}

func TestWorkspaceValidationAndEmptyInventory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	workspace := filepath.Join(root, "workspace")
	writeWorkspaceFile(t, filepath.Join(source, "notes", "ignored.txt"), "nothing")
	manifest, err := Import(Options{Source: source, Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Pack != "" || manifest.SuiteCases != 0 || len(manifest.Coverage.MissingUsageGuidelines) != 31 {
		t.Fatalf("empty inventory = %+v", manifest)
	}

	if _, err := Import(Options{Source: "", Workspace: workspace}); err == nil {
		t.Fatal("empty source was accepted")
	}
	if _, err := Import(Options{Source: source, Workspace: filepath.Join(source, "generated")}); err == nil {
		t.Fatal("workspace inside source was accepted")
	}
	if _, err := Import(Options{Source: source, Workspace: filepath.Join(root, "other"), Release: "SR2026"}); err == nil {
		t.Fatal("unknown release was accepted")
	}
	fileSource := filepath.Join(root, "file")
	writeWorkspaceFile(t, fileSource, "not a directory")
	if _, err := Import(Options{Source: fileSource, Workspace: filepath.Join(root, "other")}); err == nil {
		t.Fatal("file source was accepted")
	}
	if runtime.GOOS != "windows" {
		link := filepath.Join(root, "source-link")
		if err := os.Symlink(source, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Import(Options{Source: link, Workspace: filepath.Join(root, "other")}); err == nil {
			t.Fatal("symlink source was accepted")
		}
	}
}

func TestManifestAndExternalSelectionFailures(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	workspace := filepath.Join(root, "workspace")
	writeWorkspaceFile(t, filepath.Join(source, "1Q2026_externalcodes.json"), `{"One":[{"code":"A"}]}`)
	writeWorkspaceFile(t, filepath.Join(source, "2Q2026_externalcodes.json"), `{"Two":[{"code":"B"}]}`)
	if _, err := Import(Options{Source: source, Workspace: workspace}); err == nil || !strings.Contains(err.Error(), "multiple external") {
		t.Fatalf("multiple publication error = %v", err)
	}

	writeWorkspaceFile(t, filepath.Join(workspace, ManifestFile), `{}`)
	if _, err := LoadManifest(workspace); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("bad format error = %v", err)
	}
	writeWorkspaceFile(t, filepath.Join(workspace, ManifestFile), `{"format":"askiso-cbpr-workspace/v1"} {}`)
	if _, err := LoadManifest(workspace); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing manifest error = %v", err)
	}
}

func TestMultipleCompiledPacksAreRejected(t *testing.T) {
	source, workspace, _ := workspaceFixture(t)
	data := mustRead(t, filepath.Join(source, "source.cbpr-pack.json"))
	if err := os.WriteFile(filepath.Join(source, "second.cbpr-pack.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(Options{Source: source, Workspace: workspace}); err == nil || !strings.Contains(err.Error(), "multiple compiled") {
		t.Fatalf("multiple pack error = %v", err)
	}
}

func TestManifestFingerprintDetectsTampering(t *testing.T) {
	source, workspace, _ := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	useLegacyWorkspace(t, workspace)
	path := filepath.Join(workspace, ManifestFile)
	var manifest Manifest
	if err := json.Unmarshal(mustRead(t, path), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.Release = "SR2024"
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(workspace); err == nil || !strings.Contains(err.Error(), "fingerprint mismatch") {
		t.Fatalf("tampered manifest error = %v", err)
	}
}

func TestImportPropagatesSecurityAndStorageFailures(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	writeWorkspaceFile(t, filepath.Join(source, "ignored.txt"), "input")
	sentinel := errors.New("sentinel")

	originalDiscover := discoverSource
	originalCompile := compilePackSource
	originalCompilePDF := compilePDFPack
	originalWritePack := writeCompiledPack
	originalImportCodes := importExternalCodes
	originalSaveCodes := saveExternalCodes
	originalWriteJSON := writeWorkspaceJSON
	originalProtect := protectWorkspace
	t.Cleanup(func() {
		discoverSource = originalDiscover
		compilePackSource = originalCompile
		compilePDFPack = originalCompilePDF
		writeCompiledPack = originalWritePack
		importExternalCodes = originalImportCodes
		saveExternalCodes = originalSaveCodes
		writeWorkspaceJSON = originalWriteJSON
		protectWorkspace = originalProtect
	})

	t.Run("discover", func(t *testing.T) {
		discoverSource = func(string) ([]discovered, []string, error) { return nil, nil, sentinel }
		defer func() { discoverSource = originalDiscover }()
		if _, err := Import(Options{Source: source, Workspace: filepath.Join(root, "discover")}); !errors.Is(err, sentinel) {
			t.Fatalf("discover error = %v", err)
		}
	})
	t.Run("mkdir", func(t *testing.T) {
		blocked := filepath.Join(root, "blocked")
		writeWorkspaceFile(t, blocked, "file")
		if _, err := Import(Options{Source: source, Workspace: filepath.Join(blocked, "child")}); err == nil || !strings.Contains(err.Error(), "creating private") {
			t.Fatalf("mkdir error = %v", err)
		}
	})
	t.Run("protect", func(t *testing.T) {
		protectWorkspace = func(string, os.FileMode) error { return sentinel }
		defer func() { protectWorkspace = originalProtect }()
		if _, err := Import(Options{Source: source, Workspace: filepath.Join(root, "protect")}); !errors.Is(err, sentinel) {
			t.Fatalf("protection error = %v", err)
		}
	})
	t.Run("compile", func(t *testing.T) {
		compilePackSource = func(string, []discovered) (*rules.CBPRPack, error) { return nil, sentinel }
		defer func() { compilePackSource = originalCompile }()
		if _, err := Import(Options{Source: source, Workspace: filepath.Join(root, "compile")}); !errors.Is(err, sentinel) {
			t.Fatalf("compile error = %v", err)
		}
	})
	t.Run("write pack", func(t *testing.T) {
		compilePackSource = func(string, []discovered) (*rules.CBPRPack, error) {
			return &rules.CBPRPack{Fingerprint: "pack"}, nil
		}
		writeCompiledPack = func(string, *rules.CBPRPack) error { return sentinel }
		defer func() { compilePackSource, writeCompiledPack = originalCompile, originalWritePack }()
		if _, err := Import(Options{Source: source, Workspace: filepath.Join(root, "pack")}); !errors.Is(err, sentinel) {
			t.Fatalf("pack write error = %v", err)
		}
	})
	t.Run("external import", func(t *testing.T) {
		importExternalCodes = func(string) (*codes.ExternalSets, error) { return nil, sentinel }
		defer func() { importExternalCodes = originalImportCodes }()
		_, err := Import(Options{Source: source, Workspace: filepath.Join(root, "external-import"), ExternalCodes: "codes.json"})
		if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "importing external") {
			t.Fatalf("external import error = %v", err)
		}
	})
	t.Run("external save", func(t *testing.T) {
		importExternalCodes = func(string) (*codes.ExternalSets, error) {
			return &codes.ExternalSets{Format: "json", Codes: []codes.ExternalCode{{Set: "S", Code: "A"}}}, nil
		}
		saveExternalCodes = func(string, *codes.ExternalSets) (string, error) { return "", sentinel }
		defer func() { importExternalCodes, saveExternalCodes = originalImportCodes, originalSaveCodes }()
		_, err := Import(Options{Source: source, Workspace: filepath.Join(root, "external-save"), ExternalCodes: "codes.json"})
		if !errors.Is(err, sentinel) {
			t.Fatalf("external save error = %v", err)
		}
	})
	t.Run("suite and manifest writes", func(t *testing.T) {
		calls := 0
		writeWorkspaceJSON = func(string, any) error {
			calls++
			if calls == 1 {
				return sentinel
			}
			return nil
		}
		_, err := Import(Options{Source: source, Workspace: filepath.Join(root, "suite-write")})
		if !errors.Is(err, sentinel) {
			t.Fatalf("suite write error = %v", err)
		}
		calls = 0
		writeWorkspaceJSON = func(string, any) error {
			calls++
			if calls == 2 {
				return sentinel
			}
			return nil
		}
		_, err = Import(Options{Source: source, Workspace: filepath.Join(root, "manifest-write")})
		if !errors.Is(err, sentinel) {
			t.Fatalf("manifest write error = %v", err)
		}
		writeWorkspaceJSON = originalWriteJSON
	})
}

func TestDiscoveryLimitsKindsAndWarnings(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, filepath.Join(root, "broken.xsd"), `<broken>`)
	writeWorkspaceFile(t, filepath.Join(root, "guide.xml"), `<usage-guideline/>`)
	writeWorkspaceFile(t, filepath.Join(root, "schema.xml"), workspaceSchema)
	writeWorkspaceFile(t, filepath.Join(root, "legacy.xls"), "sheet")
	writeWorkspaceFile(t, filepath.Join(root, "ignored.json"), `{}`)
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(root, "guide.xml"), filepath.Join(root, "guide-link.xml")); err != nil {
			t.Fatal(err)
		}
	}
	files, warnings, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 || len(warnings) != 1 || !strings.Contains(warnings[0], "could not be indexed as XSD") {
		t.Fatalf("discovery = %d files, warnings %v", len(files), warnings)
	}
	if sourceKind("PACK.CBPR-PACK.JSON") != "compiled-pack" || sourceKind("guide.PDF") != "usage-guideline-pdf" ||
		sourceKind("codes_externalcode.JSON") != "external-code-publication" || sourceKind("guide.json") != "json" ||
		sourceKind("archive.zip") != "" || sourceKind("notes.txt") != "" {
		t.Fatal("source kind classification is incomplete")
	}

	large := filepath.Join(root, "large.pdf")
	if err := os.WriteFile(large, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(large, maxSourceSize+1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := discover(root); err == nil || !strings.Contains(err.Error(), "per-file safety limit") {
		t.Fatalf("large source error = %v", err)
	}
}

func TestPDFInventoryAndCompilerSelection(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	workspace := filepath.Join(root, "workspace")
	pdf := filepath.Join(source, "pacs008.pdf")
	writeWorkspaceFile(t, pdf, "%PDF fixture")

	originalCompile := compilePackSource
	compilePackSource = func(string, []discovered) (*rules.CBPRPack, error) { return nil, nil }
	t.Cleanup(func() { compilePackSource = originalCompile })
	manifest, err := Import(Options{Source: source, Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Coverage.PDFs != 1 {
		t.Fatalf("PDF count = %d", manifest.Coverage.PDFs)
	}

	originalPDF := compilePDFPack
	want := &rules.CBPRPack{Format: "test"}
	compilePDFPack = func(got string) (*rules.CBPRPack, error) {
		if got != source {
			t.Fatalf("compiler source = %q", got)
		}
		return want, nil
	}
	t.Cleanup(func() { compilePDFPack = originalPDF })
	got, err := compilePack(source, []discovered{{SourceFile: SourceFile{Kind: "usage-guideline-pdf"}, abs: pdf}})
	if err != nil || got != want {
		t.Fatalf("PDF compiler result = %+v, %v", got, err)
	}
}

func TestCoverageAndSuiteEdgeCases(t *testing.T) {
	manifest := &Manifest{}
	applyGuidelineCoverage(manifest, &rules.CBPRPack{Sources: []rules.CBPRPackSource{
		{Name: "empty", MessageID: "pacs.008.001.08", Constraints: 0},
		{Name: "unknown", Constraints: 1},
	}}, nil)
	if manifest.Coverage.PresentUsageGuidelines != 0 || manifest.Coverage.Messages != 0 {
		t.Fatalf("empty sources counted as guidelines: %+v", manifest.Coverage)
	}

	suite, warnings := buildSuite("SR2025", []discovered{{SourceFile: SourceFile{
		Path: "sample.xml", Kind: "sample", MessageID: "pacs.008.001.08",
	}}})
	if len(suite.Cases) != 0 || len(warnings) != 2 || !strings.Contains(warnings[0], "no matching") {
		t.Fatalf("unmatched sample = %+v, %v", suite, warnings)
	}
}

func TestSuitePairsSharedNamespacesByBusinessService(t *testing.T) {
	files := []discovered{
		{SourceFile: SourceFile{Path: "core.xsd", Kind: "schema", MessageID: "pacs.009.001.08", UsageIdentifiers: []string{"swift.cbprplus.03"}}},
		{SourceFile: SourceFile{Path: "cov.xsd", Kind: "schema", MessageID: "pacs.009.001.08", UsageIdentifiers: []string{"swift.cbprplus.cov.03"}}},
		{SourceFile: SourceFile{Path: "payment-cov.xml", Kind: "sample", MessageID: "pacs.009.001.08", UsageIdentifiers: []string{"swift.cbprplus.cov.03"}}},
	}
	suite, warnings := buildSuite("SR2025", files)
	if len(suite.Cases) != 1 || suite.Cases[0].Schema != "cov.xsd" ||
		suite.Cases[0].BusinessService != "swift.cbprplus.cov.03" {
		t.Fatalf("variant pairing = %+v, %v", suite.Cases, warnings)
	}

	files[2].UsageIdentifiers = nil
	suite, warnings = buildSuite("SR2025", files)
	if len(suite.Cases) != 0 || !strings.Contains(strings.Join(warnings, "\n"), "matches 2 schema variants") {
		t.Fatalf("ambiguous pairing = %+v, %v", suite.Cases, warnings)
	}
	files[2].UsageIdentifiers = []string{"swift.cbprplus.adv.03"}
	suite, warnings = buildSuite("SR2025", files)
	if len(suite.Cases) != 0 || !strings.Contains(strings.Join(warnings, "\n"), "no matching local schema variant") {
		t.Fatalf("missing variant pairing = %+v, %v", suite.Cases, warnings)
	}

	if got := sampleUsageIdentifiers("pacs.009.001.08", "samples/payment_COV.xml"); len(got) != 1 || got[0] != "swift.cbprplus.cov.03" {
		t.Fatalf("sample filename service = %v", got)
	}
	if got := sampleUsageIdentifiers("pacs.009.001.08", "samples/payment.xml"); got != nil {
		t.Fatalf("unmarked sample service = %v", got)
	}
}

func TestMessageMetadataReadsBusinessService(t *testing.T) {
	path := filepath.Join(t.TempDir(), "envelope.xml")
	writeWorkspaceFile(t, path, `<Envelope><AppHdr><BizSvc>swift.cbprplus.cov.03</BizSvc></AppHdr><Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.009.001.08"/></Envelope>`)
	messageID, services, err := messageMetadataFromXML(path)
	if err != nil || messageID != "pacs.009.001.08" || len(services) != 1 || services[0] != "swift.cbprplus.cov.03" {
		t.Fatalf("message metadata = %q, %v, %v", messageID, services, err)
	}
	path = filepath.Join(t.TempDir(), "unknown-service.xml")
	writeWorkspaceFile(t, path, `<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.009.001.08"><BizSvc>not.cbpr</BizSvc></Document>`)
	_, services, err = messageMetadataFromXML(path)
	if err != nil || services != nil {
		t.Fatalf("unknown service = %v, %v", services, err)
	}
}

func TestVerificationFailureAndMalformedSchema(t *testing.T) {
	source, workspace, _ := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	useLegacyWorkspace(t, workspace)
	var suite Suite
	if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	for i := range suite.Cases {
		if suite.Cases[i].Expected == "valid" {
			suite.Cases[i].Expected = "invalid"
		}
	}
	resignSuite(t, workspace, &suite)
	report, err := Verify(source, workspace)
	if err != nil || report.Failed != 1 || report.Passed != 1 {
		t.Fatalf("expected mismatch report = %+v, %v", report, err)
	}

	// Re-import, then replace and re-pin the schema so parsing, rather than the
	// source fingerprint check, is the reported failure.
	if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
		t.Fatal(err)
	}
	schemaPath := filepath.Join(source, filepath.FromSlash(suite.Cases[0].Schema))
	writeWorkspaceFile(t, schemaPath, `<broken>`)
	newHash, err := fileSHA256(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	for i := range suite.Cases {
		suite.Cases[i].SchemaSHA256 = newHash
	}
	resignSuite(t, workspace, &suite)
	if _, err := Verify(source, workspace); err == nil {
		t.Fatal("malformed re-pinned schema was accepted")
	}
}

func TestHelperErrorsAndSuiteIntegrity(t *testing.T) {
	root := t.TempDir()
	if _, _, err := validateRoots(filepath.Join(root, "missing"), filepath.Join(root, "workspace")); err == nil {
		t.Fatal("missing source directory was accepted")
	}
	absolute := filepath.Join(filepath.VolumeName(root)+string(filepath.Separator), "absolute.xml")
	if _, err := safeSourcePath(root, absolute); err == nil {
		t.Fatal("absolute suite path was accepted")
	}
	if _, err := safeSourcePath(root, "/rooted.xml"); err == nil {
		t.Fatal("rooted suite path was accepted")
	}
	if messageFromNamespace("urn:not-iso") != "" {
		t.Fatal("foreign namespace produced a message identifier")
	}
	malformed := filepath.Join(root, "malformed.xml")
	writeWorkspaceFile(t, malformed, `<Document>`)
	if _, err := messageFromXML(malformed); err == nil {
		t.Fatal("malformed XML produced a message identifier")
	}
	if _, err := messageFromXML(filepath.Join(root, "missing.xml")); err == nil {
		t.Fatal("missing XML produced a message identifier")
	}
	if _, err := readBounded(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing bounded file was read")
	}
	large := filepath.Join(root, "large.xml")
	writeWorkspaceFile(t, large, "")
	if err := os.Truncate(large, maxSourceSize+1); err != nil {
		t.Fatal(err)
	}
	if _, err := readBounded(large); err == nil || !strings.Contains(err.Error(), "suite limit") {
		t.Fatalf("large bounded file error = %v", err)
	}
	if _, err := fileSHA256(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing file was fingerprinted")
	}
	if err := verifyHash(filepath.Join(root, "missing"), "hash"); err == nil {
		t.Fatal("missing source hash was verified")
	}
	if err := writeJSON(filepath.Join(root, "channel.json"), make(chan int)); err == nil {
		t.Fatal("unencodable JSON was written")
	}
	if runtime.GOOS != "windows" {
		target := filepath.Join(root, "target")
		writeWorkspaceFile(t, target, "data")
		link := filepath.Join(root, "link.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(link, map[string]string{"x": "y"}); err == nil || !strings.Contains(err.Error(), "symlinked") {
			t.Fatalf("symlink write error = %v", err)
		}
	}
	if err := writeJSON(root, map[string]string{"x": "y"}); err == nil {
		t.Fatal("JSON writer overwrote a directory")
	}
	unknown := filepath.Join(root, "unknown.json")
	writeWorkspaceFile(t, unknown, `{"unexpected":true}`)
	var manifest Manifest
	if err := readJSON(unknown, &manifest); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}
	if err := readJSON(filepath.Join(root, "missing.json"), &manifest); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Fatalf("missing JSON error = %v", err)
	}
	broken := filepath.Join(root, "broken.json")
	writeWorkspaceFile(t, broken, `{`)
	if err := readJSON(broken, &manifest); err == nil || !strings.Contains(err.Error(), "decoding") {
		t.Fatalf("malformed JSON error = %v", err)
	}
	if got := unique(nil); got != nil {
		t.Fatalf("unique(nil) = %v", got)
	}
}

func TestVerifyRejectsWorkspaceAndSuiteCorruption(t *testing.T) {
	t.Run("missing manifest", func(t *testing.T) {
		if _, err := Verify(t.TempDir(), t.TempDir()); err == nil {
			t.Fatal("workspace without manifest was verified")
		}
	})

	t.Run("missing suite", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		if err := os.Remove(filepath.Join(workspace, SuiteFile)); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(source, workspace); err == nil {
			t.Fatal("workspace without suite was verified")
		}
	})

	t.Run("release mismatch", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		var suite Suite
		if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		suite.Release = "SR2024"
		if err := writeJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "workspace release") {
			t.Fatalf("release mismatch error = %v", err)
		}
	})

	t.Run("suite fingerprint mismatch", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		var suite Suite
		if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		suite.Fingerprint = "tampered"
		if err := writeJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "suite fingerprint") {
			t.Fatalf("suite fingerprint error = %v", err)
		}
	})

	t.Run("unknown sample origin", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		var suite Suite
		if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		suite.Cases[0].Origin = "remote"
		resignSuite(t, workspace, &suite)
		if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "unsupported suite case origin") {
			t.Fatalf("unknown origin error = %v", err)
		}
	})

	t.Run("workspace moved inside source", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(source, "private-output")
		if err := os.Rename(workspace, nested); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(source, nested); err == nil || !strings.Contains(err.Error(), "outside the source") {
			t.Fatalf("nested workspace error = %v", err)
		}
	})

	t.Run("external store unreadable", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		storeDir := filepath.Dir(codes.ExternalCodesPath(workspace))
		if err := os.Remove(codes.ExternalCodesPath(workspace)); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(storeDir); err != nil {
			t.Fatal(err)
		}
		writeWorkspaceFile(t, storeDir, "not a directory")
		if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "external-codes.tsv") {
			t.Fatalf("external store error = %v", err)
		}
	})

	t.Run("unsafe schema path", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		var suite Suite
		if err := readJSON(filepath.Join(workspace, SuiteFile), &suite); err != nil {
			t.Fatal(err)
		}
		suite.Cases[0].Schema = "../outside.xsd"
		resignSuite(t, workspace, &suite)
		if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "unsafe suite source") {
			t.Fatalf("unsafe schema error = %v", err)
		}
	})

	t.Run("schema fingerprint changed", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		writeWorkspaceFile(t, filepath.Join(source, "schemas", "pacs.008.001.08.xsd"), workspaceSchema+"\n")
		if _, err := Verify(source, workspace); err == nil || !strings.Contains(err.Error(), "fingerprint changed") {
			t.Fatalf("changed schema error = %v", err)
		}
	})
}

func resignSuite(t *testing.T, workspace string, suite *Suite) {
	t.Helper()
	useLegacyWorkspace(t, workspace)
	suite.Fingerprint = suiteFingerprint(suite)
	if err := writeJSON(filepath.Join(workspace, SuiteFile), suite); err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := readJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.SuiteFingerprint = suite.Fingerprint
	manifest.Fingerprint = manifestFingerprint(&manifest)
	if err := writeJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
		t.Fatal(err)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
