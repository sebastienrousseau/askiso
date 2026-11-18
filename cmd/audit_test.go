// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditCommand(t *testing.T) {
	dir := t.TempDir()
	structuredXML := `<Document><FIToFICstmrCdtTrf><CdtTrfTxInf><Dbtr><PstlAdr><StrtNm>Main</StrtNm><TwnNm>London</TwnNm><Ctry>GB</Ctry></PstlAdr></Dbtr></CdtTrfTxInf></FIToFICstmrCdtTrf></Document>`
	unstructuredXML := `<Document><FIToFICstmrCdtTrf><CdtTrfTxInf><Cdtr><PstlAdr><AdrLine>10 Downing St</AdrLine></PstlAdr></Cdtr></CdtTrfTxInf></FIToFICstmrCdtTrf></Document>`
	if err := os.WriteFile(filepath.Join(dir, "a.xml"), []byte(structuredXML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.xml"), []byte(unstructuredXML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, format := range []string{"text", "json", "html"} {
		var out bytes.Buffer
		RootCmd.SetOut(&out)
		RootCmd.SetArgs([]string{"audit", dir, "--format", format})
		if err := RootCmd.Execute(); err != nil {
			t.Fatalf("audit --format %s failed: %v", format, err)
		}
		if format == "json" {
			var card AuditScorecard
			if err := json.Unmarshal(out.Bytes(), &card); err != nil {
				t.Fatalf("unmarshal json: %v", err)
			}
			if card.FilesScanned != 2 || card.TotalAddresses != 2 || card.Structured != 1 || card.Unstructured != 1 || card.RiskLevel != "medium" {
				t.Fatalf("unexpected scorecard: %+v", card)
			}
		} else if !strings.Contains(out.String(), "SR2027 Readiness Scorecard") {
			t.Fatalf("missing heading in %s output: %q", format, out.String())
		}
	}

	RootCmd.SetArgs([]string{"audit", dir, "--format", "invalid"})
	if err := RootCmd.Execute(); err == nil {
		t.Fatal("expected error for invalid format")
	}
	RootCmd.SetOut(nil)
}
