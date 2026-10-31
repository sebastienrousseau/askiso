// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Package fsperm holds the one place askiso narrows a directory to its owner.
package fsperm

import "os"

// PrivateDirMode is owner read, write and search: nobody else can list or
// enter the directory.
const PrivateDirMode os.FileMode = 0o700

// PrivateDir sets path to PrivateDirMode, whatever the umask or an earlier
// mode left it at.
func PrivateDir(path string) error {
	// #nosec G302 -- gosec applies its 0600 file ceiling to every chmod, but a
	// directory needs the search bit to be entered; 0700 is still owner-only.
	return os.Chmod(path, PrivateDirMode)
}
