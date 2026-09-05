// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build windows

package cbprworkspace

import (
	"os"

	"golang.org/x/sys/windows"
)

func publishGenerationDirectory(stage, target string) error {
	from, err := windows.UTF16PtrFromString(stage)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}

// MoveFileEx with MOVEFILE_WRITE_THROUGH supplies the strongest directory
// publication guarantee exposed by the Windows API for this operation, so there
// is nothing further to flush. A directory that does not exist is still an
// error, as it is on every other platform.
func syncGenerationDirectory(path string) error {
	_, err := os.Stat(path)
	return err
}
