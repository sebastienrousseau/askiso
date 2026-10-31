// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const mt103Sample = `{1:F01BANKGB2LAXXX0000000000}{2:I103BANKDEFFXXXXN}{3:{121:f3a1b2c4-5d6e-4f70-8a91-2b3c4d5e6f70}}{4:
:20:REF20260824001
:23B:CRED
:32A:260824EUR25000,00
:50K:/GB29NWBK60161331926819
ACME TRADING LIMITED
14 GRESHAM STREET
LONDON EC2V 7NN
:52A:BANKGB2LXXX
:57A:BANKDEFFXXX
:59:/DE89370400440532013000
MUELLER GMBH
HAUPTSTRASSE 12
60311 FRANKFURT AM MAIN
:70:INVOICE 2026-0815 CONSULTING SERVICES
:71A:SHA
-}{5:{CHK:123456789ABC}}`

// writeMT drops a fixture into a temporary directory and returns its path.
func writeMT(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTranslateMatrix(t *testing.T) {
	out, err := run(t, "translate", "--matrix")
	if err != nil {
		t.Fatalf("translate --matrix: %v", err)
	}
	wantContains(t, out, "MT103", "pacs.008", "MT940")
}

func TestTranslateLookup(t *testing.T) {
	out, err := run(t, "translate", "MT103")
	if err != nil {
		t.Fatalf("translate MT103: %v", err)
	}
	wantContains(t, out, "pacs.008")
}

func TestTranslateNoArgsShowsMatrix(t *testing.T) {
	out, err := run(t, "translate")
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	wantContains(t, out, "TRANSLATION MATRIX")
}

func TestTranslateUnknown(t *testing.T) {
	_, err := run(t, "translate", "MT999")
	if err == nil {
		t.Fatal("expected an error for an unknown code")
	}
	if !strings.Contains(err.Error(), "no such file") {
		t.Errorf("error = %q; it should mention that no file matched either", err)
	}
}

func TestTranslateFileToStdout(t *testing.T) {
	path := writeMT(t, "payment.mt103", mt103Sample)

	out, err := run(t, "translate", path)
	if err != nil {
		t.Fatalf("translate %s: %v", path, err)
	}
	wantContains(t, out,
		"urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10",
		"<MsgId>REF20260824001</MsgId>",
		"<IBAN>GB29NWBK60161331926819</IBAN>")
	// Without --report the message alone goes to stdout, so it can be piped.
	if strings.Contains(out, "TRANSLATE") {
		t.Errorf("the report was printed without --report:\n%s", out)
	}
}

func TestTranslateFileReport(t *testing.T) {
	path := writeMT(t, "payment.mt103", mt103Sample)

	out, err := run(t, "translate", path, "--report")
	if err != nil {
		t.Fatalf("translate --report: %v", err)
	}
	wantContains(t, out,
		"MT103 → pacs.008.001.10",
		"unmapped",
		// The note wraps, so match a fragment that survives the line break.
		"CBPR+ rejects those",
		"lossy",
		"--profile cbpr-2026")
	// With --report the message itself is not printed.
	if strings.Contains(out, "<Document") {
		t.Errorf("--report also printed the message:\n%s", out)
	}
}

func TestTranslateFileOut(t *testing.T) {
	path := writeMT(t, "payment.mt103", mt103Sample)
	dest := filepath.Join(t.TempDir(), "pacs008.xml")

	out, err := run(t, "translate", path, "--out", dest)
	if err != nil {
		t.Fatalf("translate --out: %v", err)
	}
	// Writing to a file implies the report, otherwise the command says nothing.
	wantContains(t, out, "written", dest)

	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(written), "<Document") {
		t.Errorf("the written file is not a message:\n%s", written)
	}
	// A translated payment carries account and party data; other accounts on
	// the machine must not be able to read it.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(dest)
		if err != nil {
			t.Fatal(err)
		}
		if extra := info.Mode().Perm() & 0o077; extra != 0 {
			t.Errorf("%s has mode %v; group and other bits should be clear", dest, info.Mode().Perm())
		}
	}
}

func TestTranslateFileJSON(t *testing.T) {
	path := writeMT(t, "cover.mt202", `{1:F01BANKGB2LAXXX0000000000}{2:I202BANKDEFFXXXXN}{4:
:20:COVER1
:21:REF1
:32A:260824EUR25000,00
:52A:BANKGB2LXXX
:57A:BANKDEFFXXX
-}`)

	out, err := run(t, "translate", path, "--format", "json")
	if err != nil {
		t.Fatalf("translate --format json: %v", err)
	}

	var conv struct {
		SourceType string `json:"source_type"`
		TargetType string `json:"target_type"`
		XML        string `json:"xml"`
		Report     []struct {
			Tag      string `json:"tag"`
			Fidelity string `json:"fidelity"`
		} `json:"report"`
	}
	if err := json.Unmarshal([]byte(out), &conv); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if conv.SourceType != "202" || conv.TargetType != "pacs.009.001.10" {
		t.Errorf("got %s -> %s", conv.SourceType, conv.TargetType)
	}
	if len(conv.Report) == 0 {
		t.Error("the report is empty")
	}
}

func TestTranslateLosslessMessage(t *testing.T) {
	path := writeMT(t, "cover.mt202", `{1:F01BANKGB2LAXXX0000000000}{2:I202BANKDEFFXXXXN}{3:{121:f3a1b2c4-5d6e-4f70-8a91-2b3c4d5e6f70}}{4:
:20:COVER1
:21:REF1
:32A:260824EUR25000,00
:52A:BANKGB2LXXX
:53A:CHASGB2LXXX
:57A:BANKDEFFXXX
:58A:DEUTDEFFXXX
-}`)

	out, err := run(t, "translate", path, "--report")
	if err != nil {
		t.Fatalf("translate --report: %v", err)
	}
	wantContains(t, out, "every field was carried across intact")
}

func TestTranslateUnsupportedType(t *testing.T) {
	path := writeMT(t, "lc.mt700", "{1:F01BANKGB2LAXXX0000000000}{2:I700BANKDEFFXXXXN}{4:\n:20:REF1\n-}")

	_, err := run(t, "translate", path)
	if err == nil {
		t.Fatal("expected an error for an unsupported message type")
	}
	if !strings.Contains(err.Error(), "MT700") {
		t.Errorf("error = %q", err)
	}
}

func TestTranslateUnreadableFile(t *testing.T) {
	// A directory passes the "not a file" test and falls through to the lookup.
	_, err := run(t, "translate", t.TempDir())
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestWrapNote(t *testing.T) {
	if got := wrapNote("", 10); got != nil {
		t.Errorf("wrapNote(\"\") = %v, want nil", got)
	}
	got := wrapNote("one two three four five six seven", 10)
	if len(got) < 3 {
		t.Errorf("wrapNote produced %d lines: %v", len(got), got)
	}
	for _, line := range got {
		if len(line) > 12 {
			t.Errorf("line %q exceeds the width", line)
		}
	}
	// A single word longer than the width still gets its own line.
	if got := wrapNote("supercalifragilistic", 5); len(got) != 1 {
		t.Errorf("wrapNote = %v, want one line", got)
	}
}

// ---------------------------------------------------------------------------
// The reverse direction
// ---------------------------------------------------------------------------

const pacs008Sample = `<?xml version="1.0" encoding="UTF-8"?>
<Document xmlns="urn:iso:std:iso:20022:tech:xsd:pacs.008.001.10">
  <FIToFICstmrCdtTrf>
    <GrpHdr><MsgId>MSG-0001</MsgId><CreDtTm>2026-08-24T09:00:00Z</CreDtTm><NbOfTxs>1</NbOfTxs></GrpHdr>
    <CdtTrfTxInf>
      <PmtId><EndToEndId>E2E-1</EndToEndId>
        <UETR>f3a1b2c4-5d6e-4f70-8a91-2b3c4d5e6f70</UETR></PmtId>
      <IntrBkSttlmAmt Ccy="EUR">25000.00</IntrBkSttlmAmt>
      <IntrBkSttlmDt>2026-08-24</IntrBkSttlmDt>
      <ChrgBr>SHAR</ChrgBr>
      <Dbtr><Nm>ACME TRADING LIMITED</Nm>
        <PstlAdr><TwnNm>LONDON</TwnNm><Ctry>GB</Ctry></PstlAdr></Dbtr>
      <DbtrAcct><Id><IBAN>GB29NWBK60161331926819</IBAN></Id></DbtrAcct>
      <DbtrAgt><FinInstnId><BICFI>BANKGB2LXXX</BICFI></FinInstnId></DbtrAgt>
      <CdtrAgt><FinInstnId><BICFI>BANKDEFFXXX</BICFI></FinInstnId></CdtrAgt>
      <Cdtr><Nm>MUELLER GMBH</Nm></Cdtr>
      <CdtrAcct><Id><IBAN>DE89370400440532013000</IBAN></Id></CdtrAcct>
      <Purp><Cd>SUPP</Cd></Purp>
      <RmtInf><Ustrd>INVOICE 2026-0815</Ustrd></RmtInf>
    </CdtTrfTxInf>
  </FIToFICstmrCdtTrf>
</Document>`

func TestTranslateDetectsTheDirection(t *testing.T) {
	// The same command, the same flag: which way the conversion goes is decided
	// by what the file holds.
	path := writeMT(t, "payment.xml", pacs008Sample)

	out, err := run(t, "translate", path)
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	wantContains(t, out, "{1:F01BANKGB2LAXXX", ":20:MSG-0001", ":32A:260824EUR25000,00", ":71A:SHA")
	if strings.Contains(out, "<Document") {
		t.Errorf("an ISO 20022 message was produced from an ISO 20022 message:\n%s", out)
	}
}

func TestTranslateReverseReportsWhatIsLost(t *testing.T) {
	path := writeMT(t, "payment.xml", pacs008Sample)

	out, err := run(t, "translate", path, "--report")
	if err != nil {
		t.Fatalf("translate --report: %v", err)
	}
	wantContains(t, out,
		"pacs.008.001.10 → MT103",
		"/Document/FIToFICstmrCdtTrf/CdtTrfTxInf/Purp",
		"unmapped",
		"lossy")

	// The element path reads as a path, not as an MT field tag.
	if strings.Contains(out, ":/Document/") {
		t.Errorf("an element path was rendered as an MT tag:\n%s", out)
	}
}

func TestTranslateReverseJSON(t *testing.T) {
	path := writeMT(t, "payment.xml", pacs008Sample)

	out, err := run(t, "translate", path, "--format", "json")
	if err != nil {
		t.Fatalf("translate --format json: %v", err)
	}

	var conv struct {
		SourceType string `json:"source_type"`
		TargetType string `json:"target_type"`
		XML        string `json:"xml"`
	}
	if err := json.Unmarshal([]byte(out), &conv); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if conv.SourceType != "pacs.008.001.10" || conv.TargetType != "MT103" {
		t.Errorf("got %s -> %s", conv.SourceType, conv.TargetType)
	}
	if !strings.Contains(conv.XML, "{1:F01") {
		t.Errorf("the output is not an MT message:\n%s", conv.XML)
	}
}

func TestTranslateReverseUnsupportedMessage(t *testing.T) {
	path := writeMT(t, "unsupported.xml",
		`<?xml version="1.0"?><Document xmlns="urn:iso:std:iso:20022:tech:xsd:seev.031.001.09"/>`)

	_, err := run(t, "translate", path)
	if err == nil {
		t.Fatal("an unsupported message was converted")
	}
	if !strings.Contains(err.Error(), "pacs.008") {
		t.Errorf("the error does not list what is supported: %v", err)
	}
}

func TestTranslateRoundTripThroughBothDirections(t *testing.T) {
	dir := t.TempDir()
	mxPath := filepath.Join(dir, "payment.xml")
	mtPath := filepath.Join(dir, "payment.mt103")
	backPath := filepath.Join(dir, "back.xml")

	if err := os.WriteFile(mxPath, []byte(pacs008Sample), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "translate", mxPath, "--out", mtPath); err != nil {
		t.Fatalf("MX to MT: %v", err)
	}
	if _, err := run(t, "translate", mtPath, "--out", backPath); err != nil {
		t.Fatalf("MT to MX: %v", err)
	}

	data, err := os.ReadFile(backPath)
	if err != nil {
		t.Fatal(err)
	}
	round := string(data)

	for _, want := range []string{
		`<IntrBkSttlmAmt Ccy="EUR">25000.00</IntrBkSttlmAmt>`,
		`<IBAN>GB29NWBK60161331926819</IBAN>`,
		`<Nm>MUELLER GMBH</Nm>`,
		`<UETR>f3a1b2c4-5d6e-4f70-8a91-2b3c4d5e6f70</UETR>`,
	} {
		if !strings.Contains(round, want) {
			t.Errorf("the round trip lost %s:\n%s", want, round)
		}
	}

	// A structured address cannot survive a trip through MT, which is the
	// point the CBPR+ address rules exist to make.
	if strings.Contains(round, "<TwnNm>") {
		t.Error("a structured address survived a trip through MT")
	}
	if !strings.Contains(round, "<AdrLine>") {
		t.Errorf("the address was lost entirely:\n%s", round)
	}

	// And what came back is still a valid pacs.008 the linter accepts.
	if _, err := run(t, "lint", backPath); err != nil {
		t.Errorf("the round-tripped message does not lint clean: %v", err)
	}
}
