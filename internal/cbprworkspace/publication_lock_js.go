// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

//go:build js

package cbprworkspace

import "os"

func lockPublicationFile(*os.File) error   { return nil }
func unlockPublicationFile(*os.File) error { return nil }
