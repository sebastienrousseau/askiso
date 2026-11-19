// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package mcp

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestServerSetLogger(t *testing.T) {
	var logBuf bytes.Buffer
	var errOut bytes.Buffer
	s := New(strings.NewReader(""), &bytes.Buffer{}, &errOut)
	s.SetLogger(slog.New(slog.NewTextHandler(&logBuf, nil)))
	s.logf("mcp diagnostic %s", "ok")

	if !strings.Contains(logBuf.String(), "mcp diagnostic ok") {
		t.Errorf("structured logger missing message: %q", logBuf.String())
	}
	if !strings.Contains(errOut.String(), "askiso-mcp: mcp diagnostic ok") {
		t.Errorf("errOut missing message: %q", errOut.String())
	}
}
