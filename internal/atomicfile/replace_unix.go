// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build !windows

package atomicfile

import "os"

func replace(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }

// Transient reports whether an error from opening or replacing a published
// file is a passing sharing conflict worth retrying. POSIX rename is atomic
// against readers, so nothing here is transient.
func Transient(error) bool { return false }

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
