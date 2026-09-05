// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build js

package cbprworkspace

import "os"

func publishGenerationDirectory(stage, target string) error { return os.Rename(stage, target) }

// The browser filesystem shim has no directory sync. Report a missing
// directory as the error it is, so callers see the same contract everywhere.
func syncGenerationDirectory(path string) error {
	_, err := os.Stat(path)
	return err
}
