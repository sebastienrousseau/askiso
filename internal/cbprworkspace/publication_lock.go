// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cbprworkspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var (
	publicationLstat = os.Lstat
	publicationOpen  = os.OpenFile
	publicationChmod = func(file *os.File) error { return file.Chmod(0o600) }
	publicationStat  = func(file *os.File) (os.FileInfo, error) { return file.Stat() }
	publicationLock  = lockPublicationFile
)

func withPublicationLock(workspace string, publish func() error) error {
	// A workspace path that exists but is not a directory is rejected here
	// rather than left to the open below. Unix reports ENOTDIR from the lstat,
	// which is not ErrNotExist; Windows reports ERROR_PATH_NOT_FOUND, which
	// is, so without this the two platforms would fail with different errors.
	if info, err := os.Stat(workspace); err == nil && !info.IsDir() {
		return fmt.Errorf("checking workspace publication lock: %s is not a directory", workspace)
	}
	path := filepath.Join(workspace, publicationLockFile)
	if info, err := publicationLstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("workspace publication lock must be a regular non-symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking workspace publication lock: %w", err)
	}
	file, err := publicationOpen(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening workspace publication lock: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := publicationChmod(file); err != nil {
		return fmt.Errorf("protecting workspace publication lock: %w", err)
	}
	pathInfo, err := publicationLstat(path)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() {
		return errors.New("workspace publication lock changed during open")
	}
	fileInfo, err := publicationStat(file)
	if err != nil || !os.SameFile(pathInfo, fileInfo) {
		return errors.New("workspace publication lock changed during open")
	}
	if err := publicationLock(file); err != nil {
		return fmt.Errorf("locking workspace publication: %w", err)
	}
	defer func() { _ = unlockPublicationFile(file) }()
	return publish()
}
