// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/sebastienrousseau/askiso/internal/codes"
)

func TestLoadRuntime(t *testing.T) {
	source, workspace, external := workspaceFixture(t)
	manifest, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRuntime(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Manifest.Fingerprint != manifest.Fingerprint || loaded.Pack == nil ||
		len(loaded.Pack.Constraints) != 1 || loaded.ExternalCodes == nil || loaded.ExternalCodes.Total() != 2 {
		t.Fatalf("runtime = %+v", loaded)
	}
}

func TestLoadRuntimeRejectsInconsistentFiles(t *testing.T) {
	t.Run("missing workspace", func(t *testing.T) {
		if _, err := LoadRuntime(""); err == nil {
			t.Fatal("empty workspace was loaded")
		}
		if _, err := LoadRuntime(filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Fatal("missing workspace was loaded")
		}
	})

	t.Run("manifest lookup", func(t *testing.T) {
		if _, err := LoadManifest(""); err == nil || !strings.Contains(err.Error(), "required") {
			t.Fatalf("empty manifest workspace error = %v", err)
		}
	})

	t.Run("pack count", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		var manifest Manifest
		if err := readJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.Coverage.Constraints++
		manifest.Fingerprint = manifestFingerprint(&manifest)
		if err := writeJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), "pack does not match") {
			t.Fatalf("pack mismatch error = %v", err)
		}
	})

	t.Run("unsafe pack", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		var manifest Manifest
		if err := readJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
			t.Fatal(err)
		}
		manifest.Pack = "../outside.cbpr-pack.json"
		manifest.Fingerprint = manifestFingerprint(&manifest)
		if err := writeJSON(filepath.Join(workspace, ManifestFile), &manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), "unsafe workspace") {
			t.Fatalf("unsafe pack error = %v", err)
		}
	})

	t.Run("corrupt pack", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		manifest, err := Import(Options{Source: source, Workspace: workspace})
		if err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		writeWorkspaceFile(t, filepath.Join(workspace, manifest.Pack), `{`)
		if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), "loading workspace pack") {
			t.Fatalf("corrupt pack error = %v", err)
		}
	})

	t.Run("constraints without pack", func(t *testing.T) {
		source, workspace, _ := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
			t.Fatal(err)
		}
		mutateRuntimeManifest(t, workspace, func(manifest *Manifest) { manifest.Pack = "" })
		if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), "without a pack") {
			t.Fatalf("missing pack declaration error = %v", err)
		}
	})

	t.Run("missing external index", func(t *testing.T) {
		source, workspace, external := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
			t.Fatal(err)
		}
		useLegacyWorkspace(t, workspace)
		if err := os.Remove(codes.ExternalCodesPath(workspace)); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), "missing or unsafe") {
			t.Fatalf("missing external index error = %v", err)
		}
	})

	t.Run("external fingerprint", func(t *testing.T) {
		source, workspace, external := workspaceFixture(t)
		if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
			t.Fatal(err)
		}
		mutateRuntimeManifest(t, workspace, func(manifest *Manifest) {
			manifest.ExternalCodes.SHA256 = strings.Repeat("0", 64)
		})
		if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), "does not match") {
			t.Fatalf("external fingerprint error = %v", err)
		}
	})

	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{name: "empty external index", body: "# no codes\n", want: "missing or empty"},
		{name: "malformed external index", body: strings.Repeat("x", (1<<20)+1), want: "token too long"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source, workspace, external := workspaceFixture(t)
			if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
				t.Fatal(err)
			}
			useLegacyWorkspace(t, workspace)
			writeWorkspaceFile(t, codes.ExternalCodesPath(workspace), tc.body)
			if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("external index error = %v", err)
			}
		})
	}

	if runtime.GOOS != "windows" {
		t.Run("symlink workspace", func(t *testing.T) {
			source, workspace, _ := workspaceFixture(t)
			if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(t.TempDir(), "workspace-link")
			if err := os.Symlink(workspace, link); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadRuntime(link); err == nil || !strings.Contains(err.Error(), "real directory") {
				t.Fatalf("symlink workspace error = %v", err)
			}
		})

		t.Run("symlink manifest", func(t *testing.T) {
			source, workspace, _ := workspaceFixture(t)
			if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
				t.Fatal(err)
			}
			useLegacyWorkspace(t, workspace)
			manifest := filepath.Join(workspace, ManifestFile)
			target := filepath.Join(workspace, "manifest-target.json")
			if err := os.Rename(manifest, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, manifest); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadManifest(workspace); err == nil || !strings.Contains(err.Error(), "non-symlink") {
				t.Fatalf("symlink manifest error = %v", err)
			}
		})

		for _, tc := range []struct {
			name string
			dir  bool
		}{
			{name: "symlink external directory", dir: true},
			{name: "symlink external index"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				source, workspace, external := workspaceFixture(t)
				if _, err := Import(Options{Source: source, Workspace: workspace, ExternalCodes: external}); err != nil {
					t.Fatal(err)
				}
				useLegacyWorkspace(t, workspace)
				index := codes.ExternalCodesPath(workspace)
				if tc.dir {
					store := filepath.Dir(index)
					if err := os.RemoveAll(store); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(t.TempDir(), store); err != nil {
						t.Fatal(err)
					}
				} else {
					target := filepath.Join(t.TempDir(), "external.tsv")
					writeWorkspaceFile(t, target, "# empty\n")
					if err := os.Remove(index); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, index); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := LoadRuntime(workspace); err == nil || !strings.Contains(err.Error(), "missing or unsafe") {
					t.Fatalf("unsafe external store error = %v", err)
				}
			})
		}
	}
}

func mutateRuntimeManifest(t *testing.T, workspace string, mutate func(*Manifest)) {
	t.Helper()
	useLegacyWorkspace(t, workspace)
	var manifest Manifest
	path := filepath.Join(workspace, ManifestFile)
	if err := readJSON(path, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	manifest.Fingerprint = manifestFingerprint(&manifest)
	if err := writeJSON(path, &manifest); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceFileHelpersRejectInvalidValues(t *testing.T) {
	root := t.TempDir()
	if _, err := safeWorkspaceFile(root, ""); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("empty workspace filename error = %v", err)
	}
	if err := writeJSON(filepath.Join(root, "unsupported.json"), make(chan int)); err == nil {
		t.Fatal("writeJSON accepted a value encoding/json cannot marshal")
	}
}

func TestWorkspaceHelpersPropagateFilesystemFailures(t *testing.T) {
	if _, _, err := discover(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("discover accepted a missing root")
	}
	if _, err := fileSHA256(t.TempDir()); err == nil {
		t.Fatal("fingerprinting a directory should fail")
	}

	source, workspace, _ := workspaceFixture(t)
	if _, err := Import(Options{Source: source, Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	useLegacyWorkspace(t, workspace)
	writeWorkspaceFile(t, filepath.Join(workspace, ManifestFile), `{`)
	if _, err := LoadRuntime(workspace); err == nil {
		t.Fatal("runtime accepted a malformed manifest")
	}
}

func TestWorkspacePathResolutionFailures(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses to remove a directory that is a process's working directory")
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	doomed := filepath.Join(base, "removed-cwd")
	if err := os.Mkdir(doomed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(doomed); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Remove(doomed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateRoots("relative-source", "relative-workspace"); err == nil {
		t.Fatal("relative source unexpectedly resolved from a removed working directory")
	}
}
