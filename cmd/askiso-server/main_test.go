// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunFlags(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"-version"}, &out, &errOut); code != 0 {
		t.Fatalf("-version exit = %d", code)
	}
	if !strings.Contains(out.String(), "askiso-server") {
		t.Fatalf("unexpected version output: %q", out.String())
	}

	out.Reset()
	errOut.Reset()
	if code := run([]string{"-h"}, &out, &errOut); code != 0 {
		t.Fatalf("-h exit = %d", code)
	}

	if code := run([]string{"-unknown-flag"}, &out, &errOut); code != 2 {
		t.Fatalf("unknown flag exit = %d, want 2", code)
	}

	if code := run([]string{"-addr", "127.0.0.1:-1"}, &out, &errOut); code != 1 {
		t.Fatalf("invalid addr exit = %d, want 1", code)
	}
}
