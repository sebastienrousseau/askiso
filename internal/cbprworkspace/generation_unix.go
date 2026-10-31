// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build !windows && !js

package cbprworkspace

import (
	"os"
	"path/filepath"
)

func publishGenerationDirectory(stage, target string) error {
	return os.Rename(stage, target)
}

func syncGenerationDirectory(path string) error {
	dir, err := os.Open(filepath.Clean(path))
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}
