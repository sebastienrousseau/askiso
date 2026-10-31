// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/atomicfile"
	"github.com/sebastienrousseau/askiso/internal/fsperm"
)

const (
	CurrentFile         = "current.json"
	GenerationsDir      = ".generations"
	publicationLockFile = ".publish.lock"
	pointerFormat       = "askiso-cbpr-workspace-pointer/v1"
	stagingPrefix       = ".staging-"
)

var generationNameRE = regexp.MustCompile(`^[a-f0-9]{24}$`)

type workspacePointer struct {
	Format              string `json:"format"`
	Generation          string `json:"generation"`
	ManifestFingerprint string `json:"manifest_fingerprint"`
}

// Generation describes one locally retained immutable workspace snapshot.
type Generation struct {
	Fingerprint string `json:"fingerprint"`
	Release     string `json:"release,omitempty"`
	Active      bool   `json:"active"`
	Valid       bool   `json:"valid"`
	SizeBytes   int64  `json:"size_bytes"`
	Error       string `json:"error,omitempty"`
}

func generationSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("generation contains unsafe symlink: %s", filepath.Base(path))
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func beginGeneration(workspace string) (string, error) {
	dir := filepath.Join(workspace, GenerationsDir)
	if err := ensurePrivateDirectory(dir); err != nil {
		return "", fmt.Errorf("creating workspace generations: %w", err)
	}
	stage, err := os.MkdirTemp(dir, stagingPrefix)
	if err != nil {
		return "", fmt.Errorf("creating workspace generation: %w", err)
	}
	if err := fsperm.PrivateDir(stage); err != nil {
		_ = os.RemoveAll(stage)
		return "", fmt.Errorf("protecting workspace generation: %w", err)
	}
	return stage, nil
}

func publishGeneration(workspace, stage string, manifest *Manifest, suite *Suite) error {
	if manifest == nil || suite == nil || !generationNameRE.MatchString(manifest.Fingerprint) {
		return errors.New("cannot publish a workspace generation without a valid manifest fingerprint")
	}
	generations := filepath.Join(workspace, GenerationsDir)
	target := filepath.Join(generations, manifest.Fingerprint)
	if err := publishGenerationDirectory(stage, target); err != nil {
		// An identical import may already have published this immutable
		// generation. Accept it only after verifying its manifest.
		existing, loadErr := loadManifestAt(target)
		if loadErr != nil || existing.Fingerprint != manifest.Fingerprint {
			return fmt.Errorf("publishing workspace generation: %w", err)
		}
		if removeErr := os.RemoveAll(stage); removeErr != nil {
			return fmt.Errorf("removing duplicate workspace generation: %w", removeErr)
		}
	}
	if _, err := validatePublishedGeneration(target, manifest); err != nil {
		return fmt.Errorf("validating workspace generation: %w", err)
	}
	if err := syncGenerationDirectory(generations); err != nil {
		return fmt.Errorf("syncing workspace generations: %w", err)
	}
	return activatePublishedGeneration(workspace, target, manifest, suite)
}

func activatePublishedGeneration(workspace, target string, manifest *Manifest, suite *Suite) error {
	return withPublicationLock(workspace, func() error {
		if err := mirrorGeneration(workspace, target, manifest, suite); err != nil {
			return err
		}
		pointer := workspacePointer{
			Format: pointerFormat, Generation: manifest.Fingerprint,
			ManifestFingerprint: manifest.Fingerprint,
		}
		if err := writeWorkspacePointer(filepath.Join(workspace, CurrentFile), &pointer); err != nil {
			return fmt.Errorf("activating workspace generation: %w", err)
		}
		return nil
	})
}

func validatePublishedGeneration(root string, expected *Manifest) (*Suite, error) {
	manifest, err := loadManifestAt(root)
	if err != nil {
		return nil, err
	}
	if manifest.Fingerprint != expected.Fingerprint {
		return nil, errors.New("published generation manifest is not the requested snapshot")
	}
	if _, err := loadRuntimeAt(root, manifest); err != nil {
		return nil, err
	}
	var suite Suite
	if err := readJSON(filepath.Join(root, SuiteFile), &suite); err != nil {
		return nil, err
	}
	if suite.Format != SuiteFormat || suite.Release != manifest.Release ||
		suite.Fingerprint != suiteFingerprint(&suite) || suite.Fingerprint != manifest.SuiteFingerprint ||
		len(suite.Cases) != manifest.SuiteCases {
		return nil, errors.New("published conformance suite does not match its manifest")
	}
	for _, testCase := range suite.Cases {
		if testCase.Origin != "generated" {
			continue
		}
		path, err := safeGeneratedPath(root, testCase.Sample)
		if err != nil {
			return nil, err
		}
		if err := verifyHash(path, testCase.SampleSHA256); err != nil {
			return nil, err
		}
	}
	return &suite, nil
}

var writeWorkspacePointer = func(path string, pointer *workspacePointer) error {
	return writeJSON(path, pointer)
}

// ListGenerations inventories locally retained snapshots without exposing any
// proprietary source content.
func ListGenerations(workspace string) ([]Generation, error) {
	workspaceStateMu.RLock()
	defer workspaceStateMu.RUnlock()
	root, err := realWorkspaceRoot(workspace)
	if err != nil {
		return nil, err
	}
	active := ""
	if info, statErr := os.Lstat(filepath.Join(root, CurrentFile)); statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		var pointer workspacePointer
		if readErr := readJSON(filepath.Join(root, CurrentFile), &pointer); readErr == nil && pointer.Format == pointerFormat &&
			pointer.Generation == pointer.ManifestFingerprint && generationNameRE.MatchString(pointer.Generation) {
			active = pointer.Generation
		}
	}
	dir := filepath.Join(root, GenerationsDir)
	dirInfo, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || dirInfo.Mode()&os.ModeSymlink != 0 || !dirInfo.IsDir() {
		return nil, errors.New("workspace generations path is missing or unsafe")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	result := make([]Generation, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), stagingPrefix) || !generationNameRE.MatchString(entry.Name()) {
			continue
		}
		item := Generation{Fingerprint: entry.Name(), Active: entry.Name() == active}
		generationRoot := filepath.Join(dir, entry.Name())
		entryInfo, loadErr := entry.Info()
		if loadErr == nil && (entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.IsDir()) {
			loadErr = errors.New("generation path is not a real directory")
		}
		var manifest *Manifest
		if loadErr == nil {
			manifest, loadErr = loadManifestAt(generationRoot)
		}
		if loadErr == nil {
			if manifest.Fingerprint != entry.Name() {
				loadErr = errors.New("generation manifest fingerprint does not match its directory")
			} else {
				_, loadErr = validatePublishedGeneration(generationRoot, manifest)
			}
		}
		if loadErr != nil {
			item.Error = loadErr.Error()
		} else {
			item.Valid = true
			item.Release = manifest.Release
			item.SizeBytes, loadErr = generationSize(generationRoot)
			if loadErr != nil {
				item.Valid = false
				item.Error = loadErr.Error()
			}
		}
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Fingerprint < result[j].Fingerprint })
	return result, nil
}

// ActivateGeneration validates and atomically selects a retained snapshot.
// This is a local rollback primitive; it never reads the entitled source tree.
func ActivateGeneration(workspace, fingerprint string) (*Manifest, error) {
	workspaceStateMu.Lock()
	defer workspaceStateMu.Unlock()
	root, err := realWorkspaceRoot(workspace)
	if err != nil {
		return nil, err
	}
	fingerprint = strings.TrimSpace(fingerprint)
	if !generationNameRE.MatchString(fingerprint) {
		return nil, errors.New("workspace generation fingerprint must be 24 lowercase hexadecimal characters")
	}
	generations := filepath.Join(root, GenerationsDir)
	generationsInfo, err := os.Lstat(generations)
	if err != nil || generationsInfo.Mode()&os.ModeSymlink != 0 || !generationsInfo.IsDir() {
		return nil, errors.New("workspace generations path is missing or unsafe")
	}
	target := filepath.Join(generations, fingerprint)
	info, err := os.Lstat(target)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("requested workspace generation is missing or unsafe")
	}
	manifest, err := loadManifestAt(target)
	if err != nil {
		return nil, err
	}
	if manifest.Fingerprint != fingerprint {
		return nil, errors.New("requested workspace generation fingerprint does not match its directory")
	}
	suite, err := validatePublishedGeneration(target, manifest)
	if err != nil {
		return nil, err
	}
	if err := activatePublishedGeneration(root, target, manifest, suite); err != nil {
		return nil, err
	}
	return manifest, nil
}

func mirrorGeneration(workspace, generation string, manifest *Manifest, suite *Suite) error {
	relatives := []string{ManifestFile, SuiteFile}
	if manifest.Pack != "" {
		relatives = append(relatives, manifest.Pack)
	}
	if manifest.ExternalCodes != nil {
		relatives = append(relatives, filepath.ToSlash(filepath.Join(".askiso", "external-codes.tsv")))
	}
	seen := map[string]bool{}
	for _, testCase := range suite.Cases {
		if testCase.Origin == "generated" {
			relatives = append(relatives, testCase.Sample)
		}
	}
	for _, relative := range relatives {
		clean := filepath.Clean(filepath.FromSlash(relative))
		if seen[clean] {
			continue
		}
		seen[clean] = true
		source := filepath.Join(generation, clean)
		info, err := os.Lstat(source)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("generation artifact is missing or unsafe: %s", relative)
		}
		data, err := readBounded(source)
		if err != nil {
			return err
		}
		destination := filepath.Join(workspace, clean)
		if err := ensurePrivateDirectory(filepath.Dir(destination)); err != nil {
			return err
		}
		if err := atomicfile.Write(destination, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func activeWorkspaceRoot(workspace string) (string, error) {
	root, err := realWorkspaceRoot(workspace)
	if err != nil {
		return "", err
	}
	pointerPath := filepath.Join(root, CurrentFile)
	info, err := os.Lstat(pointerPath)
	if errors.Is(err, os.ErrNotExist) {
		return root, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading workspace generation pointer: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("workspace generation pointer must be a regular non-symlink")
	}
	var pointer workspacePointer
	if err := readJSON(pointerPath, &pointer); err != nil {
		return "", err
	}
	if pointer.Format != pointerFormat || !generationNameRE.MatchString(pointer.Generation) ||
		pointer.ManifestFingerprint != pointer.Generation {
		return "", errors.New("workspace generation pointer is invalid")
	}
	generation := filepath.Join(root, GenerationsDir, pointer.Generation)
	generationsInfo, err := os.Lstat(filepath.Join(root, GenerationsDir))
	if err != nil || generationsInfo.Mode()&os.ModeSymlink != 0 || !generationsInfo.IsDir() {
		return "", errors.New("workspace generations path is missing or unsafe")
	}
	generationInfo, err := os.Lstat(generation)
	if err != nil || generationInfo.Mode()&os.ModeSymlink != 0 || !generationInfo.IsDir() {
		return "", errors.New("active workspace generation is missing or unsafe")
	}
	manifest, err := loadManifestAt(generation)
	if err != nil {
		return "", err
	}
	if manifest.Fingerprint != pointer.ManifestFingerprint {
		return "", errors.New("active workspace generation does not match its pointer")
	}
	return generation, nil
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("workspace artifact directory is unsafe: %s", filepath.Base(path))
	}
	return fsperm.PrivateDir(path)
}
