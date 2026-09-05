// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const ReleaseDiffFormat = "askiso-cbpr-release-diff/v1"

// ReleaseArtifact is disclosure-safe metadata for one machine-readable file.
type ReleaseArtifact struct {
	Path             string   `json:"path"`
	Kind             string   `json:"kind"`
	MessageID        string   `json:"message_id,omitempty"`
	BusinessServices []string `json:"business_services,omitempty"`
	SHA256           string   `json:"sha256"`
}

// ReleaseChange identifies a file added, removed, or changed between releases.
type ReleaseChange struct {
	Path             string   `json:"path"`
	MessageID        string   `json:"message_id,omitempty"`
	BusinessServices []string `json:"business_services,omitempty"`
	OldSHA256        string   `json:"old_sha256,omitempty"`
	NewSHA256        string   `json:"new_sha256,omitempty"`
}

// ReleaseDiff compares two user-entitled local exports without copying content.
type ReleaseDiff struct {
	Format          string          `json:"format"`
	FromRelease     string          `json:"from_release"`
	ToRelease       string          `json:"to_release"`
	FromFingerprint string          `json:"from_fingerprint"`
	ToFingerprint   string          `json:"to_fingerprint"`
	Added           []ReleaseChange `json:"added,omitempty"`
	Removed         []ReleaseChange `json:"removed,omitempty"`
	Changed         []ReleaseChange `json:"changed,omitempty"`
	Unchanged       int             `json:"unchanged"`
	Actions         []string        `json:"actions"`
}

// CompareReleaseSources performs a metadata-only delta between two local
// machine-readable exports. It accepts any release labels so it remains useful
// when the operator receives future entitled packages.
func CompareReleaseSources(fromSource, toSource, fromRelease, toRelease string) (*ReleaseDiff, error) {
	from, fromFingerprint, err := releaseArtifacts(fromSource)
	if err != nil {
		return nil, fmt.Errorf("indexing %s: %w", fromRelease, err)
	}
	to, toFingerprint, err := releaseArtifacts(toSource)
	if err != nil {
		return nil, fmt.Errorf("indexing %s: %w", toRelease, err)
	}
	if strings.TrimSpace(fromRelease) == "" || strings.TrimSpace(toRelease) == "" {
		return nil, errors.New("from and to release labels are required")
	}
	report := &ReleaseDiff{
		Format: ReleaseDiffFormat, FromRelease: strings.ToUpper(strings.TrimSpace(fromRelease)), ToRelease: strings.ToUpper(strings.TrimSpace(toRelease)),
		FromFingerprint: fromFingerprint, ToFingerprint: toFingerprint,
		Actions: []string{
			"review every added, removed, and changed artefact in the entitled release notes",
			"regenerate positive and negative synthetic suites from the new schemas",
			"obtain independent sample review and external test evidence for the target release",
			"pin the target external-code publication by effective date",
		},
	}
	fromByPath, toByPath := map[string]ReleaseArtifact{}, map[string]ReleaseArtifact{}
	for _, artifact := range from {
		fromByPath[artifact.Path] = artifact
	}
	for _, artifact := range to {
		toByPath[artifact.Path] = artifact
	}
	for path, oldArtifact := range fromByPath {
		newArtifact, ok := toByPath[path]
		if !ok {
			report.Removed = append(report.Removed, releaseChange(oldArtifact, oldArtifact.SHA256, ""))
		} else if oldArtifact.SHA256 != newArtifact.SHA256 {
			report.Changed = append(report.Changed, releaseChange(newArtifact, oldArtifact.SHA256, newArtifact.SHA256))
		} else {
			report.Unchanged++
		}
	}
	for path, artifact := range toByPath {
		if _, ok := fromByPath[path]; !ok {
			report.Added = append(report.Added, releaseChange(artifact, "", artifact.SHA256))
		}
	}
	sortChanges(report.Added)
	sortChanges(report.Removed)
	sortChanges(report.Changed)
	return report, nil
}

// WriteReleaseDiff writes disclosure-safe delta metadata with owner-only permissions.
func WriteReleaseDiff(output string, report *ReleaseDiff) error {
	if report == nil || report.Format != ReleaseDiffFormat {
		return errors.New("valid release diff is required")
	}
	return writeEvidenceJSON(output, report)
}

func releaseArtifacts(source string) ([]ReleaseArtifact, string, error) {
	root, _, err := validateRoots(source, source+".askiso-nonexistent-workspace")
	if err != nil {
		return nil, "", err
	}
	files, _, err := discoverSource(root)
	if err != nil {
		return nil, "", err
	}
	var artifacts []ReleaseArtifact
	for _, file := range files {
		if file.Kind != "schema" && file.Kind != "usage-guideline-json-schema" && file.Kind != "usage-guideline-xml" && file.Kind != "external-code-publication" {
			continue
		}
		services := append([]string{}, file.UsageIdentifiers...)
		if data, readErr := readBounded(file.abs); readErr == nil {
			services = append(services, usageIdentifierRE.FindAllString(strings.ToLower(string(data)), -1)...)
		}
		sort.Strings(services)
		services = unique(services)
		artifacts = append(artifacts, ReleaseArtifact{Path: file.Path, Kind: file.Kind, MessageID: file.MessageID, BusinessServices: services, SHA256: file.SHA256})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Path < artifacts[j].Path })
	encoded, _ := json.Marshal(artifacts)
	digest := sha256.Sum256(encoded)
	return artifacts, hex.EncodeToString(digest[:12]), nil
}

func releaseChange(artifact ReleaseArtifact, oldHash, newHash string) ReleaseChange {
	return ReleaseChange{Path: artifact.Path, MessageID: artifact.MessageID, BusinessServices: artifact.BusinessServices, OldSHA256: oldHash, NewSHA256: newHash}
}

func sortChanges(changes []ReleaseChange) {
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
}
