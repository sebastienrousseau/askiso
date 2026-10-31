// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package fsperm

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrivateDirNarrowsAnOpenDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not map POSIX permission bits")
	}
	dir := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != PrivateDirMode {
		t.Fatalf("mode = %v, want %v", got, PrivateDirMode)
	}
}

func TestPrivateDirReportsAMissingPath(t *testing.T) {
	if err := PrivateDir(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("PrivateDir on a missing path returned nil")
	}
}
