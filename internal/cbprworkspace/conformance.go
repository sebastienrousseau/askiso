// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/sebastienrousseau/askiso/internal/rules"
)

const EvidenceFormat = "askiso-cbpr-external-evidence/v1"

var publicationQuarterRE = regexp.MustCompile(`(?i)([1-4])Q(20[0-9]{2})`)

// ConformanceOptions defines the strict, local-only release gate.
type ConformanceOptions struct {
	Source                  string
	Workspace               string
	AsOf                    string
	Evidence                string
	RequireUserSamples      bool
	RequireExternalEvidence bool
}

// ConformanceCheck is one content-free gate result.
type ConformanceCheck struct {
	ID       string `json:"id"`
	Passed   bool   `json:"passed"`
	Actual   string `json:"actual"`
	Required string `json:"required"`
	Detail   string `json:"detail,omitempty"`
}

// ExternalEvidence pins an independently obtained verdict without retaining
// the submitted message or any response body.
type ExternalEvidence struct {
	Format               string `json:"format"`
	Provider             string `json:"provider"`
	WorkspaceFingerprint string `json:"workspace_fingerprint"`
	SuiteFingerprint     string `json:"suite_fingerprint"`
	TestedAt             string `json:"tested_at"`
	Cases                int    `json:"cases"`
	Passed               bool   `json:"passed"`
}

// ConformanceReport is suitable for saving as content-free audit evidence.
type ConformanceReport struct {
	Release                string             `json:"release"`
	WorkspaceFingerprint   string             `json:"workspace_fingerprint"`
	SuiteFingerprint       string             `json:"suite_fingerprint"`
	AsOf                   string             `json:"as_of"`
	Ready                  bool               `json:"ready"`
	Checks                 []ConformanceCheck `json:"checks"`
	MissingPositiveSamples []string           `json:"missing_positive_samples,omitempty"`
	MissingNegativeSamples []string           `json:"missing_negative_samples,omitempty"`
	MissingScenarios       []string           `json:"missing_scenarios,omitempty"`
}

var representativeScenarios = []string{
	"missing-mandatory", "forbidden-element", "cardinality", "lexical",
	"restricted-code", "external-code", "business-service", "bah-payload",
}

var conformanceVerify = Verify

// CheckConformance runs strict checks using only local files. It never invokes
// a model provider or network service.
func CheckConformance(opt ConformanceOptions) (*ConformanceReport, error) {
	manifest, err := LoadManifest(opt.Workspace)
	if err != nil {
		return nil, err
	}
	workspace, err := realWorkspaceRoot(opt.Workspace)
	if err != nil {
		return nil, err
	}
	dataRoot := manifestDataRoot(manifest, workspace)
	var suite Suite
	if err := readJSON(filepath.Join(dataRoot, SuiteFile), &suite); err != nil {
		return nil, err
	}
	if suite.Fingerprint != manifest.SuiteFingerprint || suite.Fingerprint != suiteFingerprint(&suite) {
		return nil, errors.New("conformance suite fingerprint does not match the workspace")
	}
	verification, err := conformanceVerify(opt.Source, workspace)
	if err != nil {
		return nil, err
	}
	if err := ensureWorkspaceSnapshot(verification.workspaceFingerprint, manifest); err != nil {
		return nil, err
	}
	asOf, err := conformanceDate(opt.AsOf)
	if err != nil {
		return nil, err
	}
	report := &ConformanceReport{
		Release: manifest.Release, WorkspaceFingerprint: manifest.Fingerprint,
		SuiteFingerprint: suite.Fingerprint, AsOf: asOf.Format("2006-01-02"), Ready: true,
	}
	add := func(id string, passed bool, actual, required, detail string) {
		report.Checks = append(report.Checks, ConformanceCheck{
			ID: id, Passed: passed, Actual: actual, Required: required, Detail: detail,
		})
		if !passed {
			report.Ready = false
		}
	}

	add("local-only", manifest.LocalOnly, strconv.FormatBool(manifest.LocalOnly), "true",
		"re-import with the current AskISO workspace format")
	add("entitlement", manifest.EntitlementAcknowledged, strconv.FormatBool(manifest.EntitlementAcknowledged), "true",
		"re-import with --acknowledge-entitlement after confirming your rights")
	private, detail := privateWorkspace(workspace, dataRoot)
	add("private-permissions", private, detail, "workspace and control files mode 0700/0600", "")
	add("executable-usage-guidelines",
		manifest.Coverage.ExecutableUsageGuidelines == manifest.Coverage.ExpectedUsageGuidelines,
		fmt.Sprintf("%d/%d", manifest.Coverage.ExecutableUsageGuidelines, manifest.Coverage.ExpectedUsageGuidelines),
		fmt.Sprintf("%d/%d", manifest.Coverage.ExpectedUsageGuidelines, manifest.Coverage.ExpectedUsageGuidelines), "")
	add("local-suite", verification.Failed == 0 && verification.Cases > 0,
		fmt.Sprintf("%d passed, %d failed", verification.Passed, verification.Failed), "at least one case and zero failures", "")

	externalPresent := manifest.ExternalCodes != nil
	actualExternal := "absent"
	if externalPresent {
		actualExternal = manifest.ExternalCodes.Publication
	}
	add("external-code-publication", externalPresent, actualExternal, "pinned local publication", "")
	quarterOK, quarterDetail := externalPublicationAsOf(manifest.ExternalCodes, asOf)
	add("external-code-as-of", quarterOK, quarterDetail, "publication quarter not later than --as-of", "")

	if opt.RequireUserSamples {
		positive, negative, scenarios := userSampleCoverage(suite.Cases)
		guidelines := rules.CBPRSR2025UsageGuidelines()
		for _, guideline := range guidelines {
			key := guideline.MessageID + "|" + guideline.UsageIdentifier
			if !positive[key] {
				report.MissingPositiveSamples = append(report.MissingPositiveSamples, key)
			}
			if !negative[key] {
				report.MissingNegativeSamples = append(report.MissingNegativeSamples, key)
			}
		}
		for _, scenario := range representativeScenarios {
			if !scenarios[scenario] {
				report.MissingScenarios = append(report.MissingScenarios, scenario)
			}
		}
		expected := len(guidelines)
		add("user-positive-samples", len(report.MissingPositiveSamples) == 0,
			fmt.Sprintf("%d/%d", expected-len(report.MissingPositiveSamples), expected),
			fmt.Sprintf("%d/%d", expected, expected), "generated cases do not count")
		add("user-negative-samples", len(report.MissingNegativeSamples) == 0,
			fmt.Sprintf("%d/%d", expected-len(report.MissingNegativeSamples), expected),
			fmt.Sprintf("%d/%d", expected, expected), "generated cases do not count")
		add("representative-scenarios", len(report.MissingScenarios) == 0,
			fmt.Sprintf("%d/%d", len(representativeScenarios)-len(report.MissingScenarios), len(representativeScenarios)),
			fmt.Sprintf("%d/%d", len(representativeScenarios), len(representativeScenarios)), "scenario is inferred from the sample filename")
	}

	evidence, evidenceErr := loadExternalEvidence(opt.Evidence)
	if evidenceErr != nil {
		return nil, evidenceErr
	}
	evidenceOK := evidence != nil && evidence.Passed && evidence.Cases > 0 &&
		evidence.WorkspaceFingerprint == manifest.Fingerprint && evidence.SuiteFingerprint == suite.Fingerprint
	if evidence != nil || opt.RequireExternalEvidence {
		actual := "absent"
		if evidence != nil {
			actual = fmt.Sprintf("%s: %d case(s), passed=%t", evidence.Provider, evidence.Cases, evidence.Passed)
		}
		add("external-evidence", evidenceOK, actual, "matching passed content-free evidence", "")
	}
	return report, nil
}

func conformanceDate(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Now().UTC(), nil
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --as-of date %q; use YYYY-MM-DD", value)
	}
	return date, nil
}

func privateWorkspace(workspace string, dataRoots ...string) (bool, string) {
	dataRoot := workspace
	if len(dataRoots) > 0 {
		dataRoot = dataRoots[0]
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{
		{workspace, 0o700}, {dataRoot, 0o700},
		{filepath.Join(dataRoot, ManifestFile), 0o600},
		{filepath.Join(dataRoot, SuiteFile), 0o600},
	} {
		info, err := os.Stat(item.path)
		if err != nil {
			return false, err.Error()
		}
		if !modeIsPrivate(info) {
			return false, fmt.Sprintf("%s mode is %04o", filepath.Base(item.path), info.Mode().Perm())
		}
	}
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(workspace, CurrentFile), 0o600},
		{filepath.Join(workspace, publicationLockFile), 0o600},
		{filepath.Join(workspace, GenerationsDir), 0o700},
	} {
		info, err := os.Lstat(item.path)
		if errors.Is(err, os.ErrNotExist) {
			continue // v1 workspace compatibility
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			if err != nil {
				return false, err.Error()
			}
			return false, fmt.Sprintf("%s is a symlink", filepath.Base(item.path))
		}
		if !modeIsPrivate(info) {
			return false, fmt.Sprintf("%s mode is %04o", filepath.Base(item.path), info.Mode().Perm())
		}
	}
	if runtime.GOOS == "windows" {
		return true, "POSIX modes are not enforced on Windows; the workspace inherits the user profile ACL"
	}
	return true, "workspace 0700; control files 0600"
}

// modeIsPrivate reports whether nobody but the owner can reach the entry.
// Windows carries no POSIX permission bits: Go reports 0777 for every
// directory and 0666 for every writable file there, so the check would fail
// on every workspace. Privacy on Windows comes from the profile directory's
// ACL, which this check cannot inspect and so does not claim to.
func modeIsPrivate(info os.FileInfo) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o077 == 0
}

func externalPublicationAsOf(publication *ExternalPublication, asOf time.Time) (bool, string) {
	if publication == nil {
		return false, "absent"
	}
	match := publicationQuarterRE.FindStringSubmatch(publication.Publication)
	if match == nil {
		return false, fmt.Sprintf("unrecognised publication %q", publication.Publication)
	}
	quarter, _ := strconv.Atoi(match[1])
	year, _ := strconv.Atoi(match[2])
	asOfQuarter := (int(asOf.Month())-1)/3 + 1
	ok := year < asOf.Year() || (year == asOf.Year() && quarter <= asOfQuarter)
	return ok, fmt.Sprintf("%dQ%d against %s", quarter, year, asOf.Format("2006-01-02"))
}

func userSampleCoverage(cases []SuiteCase) (map[string]bool, map[string]bool, map[string]bool) {
	positive, negative, scenarios := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, testCase := range cases {
		if testCase.Origin != "user-provided" && testCase.Origin != "" {
			continue
		}
		if testCase.BusinessService == "" {
			continue
		}
		key := testCase.MessageID + "|" + testCase.BusinessService
		switch testCase.Expected {
		case "valid":
			positive[key] = true
		case "invalid":
			negative[key] = true
			scenarios[testCase.Scenario] = true
		}
	}
	return positive, negative, scenarios
}

func loadExternalEvidence(path string) (*ExternalEvidence, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := readBounded(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("reading external evidence: %w", err)
	}
	var evidence ExternalEvidence
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&evidence); err != nil {
		return nil, fmt.Errorf("decoding external evidence: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("decoding external evidence: trailing JSON content")
	}
	if evidence.Format != EvidenceFormat || strings.TrimSpace(evidence.Provider) == "" {
		return nil, errors.New("external evidence has an unsupported format or empty provider")
	}
	if _, err := time.Parse(time.RFC3339, evidence.TestedAt); err != nil {
		return nil, fmt.Errorf("external evidence tested_at must be RFC3339: %w", err)
	}
	return &evidence, nil
}
