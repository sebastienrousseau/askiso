// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package importer

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The Registration Authority nests zips inside zips, so the importer opens
// archives it finds inside archives. That is exactly the shape a zip bomb
// takes, and the input is a file the user downloaded from a website — so the
// limits are a security control, not a tidiness measure, and they deserve a
// test that actually trips them.

func TestNestedArchiveOverTheSizeLimitIsRefused(t *testing.T) {
	dir := t.TempDir()

	inner := buildZip(t, entry{"Payments/Version 1.0/Schemas/a.xsd", []byte("<xs:schema/>")})
	outer := writeZip(t, dir, "outer.zip", entry{"nested.zip", inner})

	limits := DefaultLimits()
	limits.MaxFileSize = 10 // smaller than the nested archive

	_, err := ImportArchive(outer, Options{Root: filepath.Join(dir, "out"), Limits: limits})
	if err == nil {
		t.Fatal("an oversized nested archive was accepted")
	}
	if !strings.Contains(err.Error(), "nested.zip") {
		t.Errorf("the error does not name the offending entry: %v", err)
	}
}

func TestNestedArchiveWithinTheLimitIsRead(t *testing.T) {
	dir := t.TempDir()

	inner := buildZip(t, entry{"Payments/Version 1.0/Schemas/a.xsd", []byte("<xs:schema/>")})
	outer := writeZip(t, dir, "outer.zip", entry{"nested.zip", inner})

	res, err := ImportArchive(outer, Options{Root: filepath.Join(dir, "out")})
	if err != nil {
		t.Fatalf("a nested archive within the limits was refused: %v", err)
	}
	if res.Schemas == 0 {
		t.Errorf("the schema inside the nested archive was not imported: %+v", res)
	}
}

// An entry over the limit inside a plain archive takes a different path from
// the nested case, and must be refused just as clearly.
func TestOversizedEntryIsRefused(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 500)
	z := writeZip(t, dir, "big.zip",
		entry{"Payments/Version 1.0/Schemas/big.xsd", []byte(big)})

	limits := DefaultLimits()
	limits.MaxFileSize = 100

	if _, err := ImportArchive(z, Options{Root: filepath.Join(dir, "out"), Limits: limits}); err == nil {
		t.Error("an oversized entry was accepted")
	}
}

// A negative limit never reaches checkEntry, but if one did it must refuse
// every entry instead of wrapping round to a near-unbounded uint64.
func TestLimitAsUint64(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want uint64
	}{
		{-1, 0},
		{0, 0},
		{100, 100},
	} {
		if got := limitAsUint64(tc.in); got != tc.want {
			t.Errorf("limitAsUint64(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

// A catalogue is the user's own download: what the importer writes is not
// readable by other accounts, and its directories are not writable by the
// group or open to others.
func TestImportedCatalogueIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not map POSIX permission bits")
	}
	dest := t.TempDir()
	archive := writeZip(t, t.TempDir(), "PaymentsClearingAndSettlement_v11.zip",
		entry{"pacs.008.001.10.xsd", []byte("<xs:schema/>")})
	if _, err := ImportArchive(archive, Options{Root: dest}); err != nil {
		t.Fatalf("ImportArchive: %v", err)
	}

	dir := filepath.Join(dest, "Payments Clearing and Settlement", "Version 11.0", "Schemas")
	file := filepath.Join(dir, "pacs.008.001.10.xsd")
	for path, forbidden := range map[string]os.FileMode{file: 0o077, dir: 0o027} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if extra := info.Mode().Perm() & forbidden; extra != 0 {
			t.Errorf("%s has mode %v; bits %v should be clear", path, info.Mode().Perm(), extra)
		}
	}
}
