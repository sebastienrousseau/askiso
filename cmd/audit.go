// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sebastienrousseau/askiso/pkg/iso20022"
	"github.com/spf13/cobra"
)

var auditFormat string

// AuditScorecard summarises SR2027 structured-address readiness across a
// corpus of ISO 20022 XML messages.
type AuditScorecard struct {
	FilesScanned     int     `json:"files_scanned"`
	TotalAddresses   int     `json:"total_addresses"`
	Structured       int     `json:"structured"`
	Hybrid           int     `json:"hybrid"`
	Unstructured     int     `json:"unstructured"`
	ReadinessPercent float64 `json:"readiness_percent"`
	RiskLevel        string  `json:"risk_level"`
}

var auditCmd = &cobra.Command{
	Use:   "audit <xml-or-dir>...",
	Short: "Score SR2027 structured-address readiness across XML messages",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := normalizeChoice("format", auditFormat, "text", "json", "html")
		if err != nil {
			return err
		}
		card, err := runAudit(args)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		switch format {
		case "json":
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(card)
		case "html":
			return auditHTMLTmpl.Execute(out, card)
		default:
			_, err := fmt.Fprintf(out,
				"SR2027 Readiness Scorecard\n  Files scanned: %d\n  Addresses: %d (structured=%d, hybrid=%d, unstructured=%d)\n  Readiness: %.1f%% (%s risk)\n",
				card.FilesScanned, card.TotalAddresses, card.Structured, card.Hybrid, card.Unstructured, card.ReadinessPercent, card.RiskLevel)
			return err
		}
	},
}

func init() {
	auditCmd.Flags().StringVar(&auditFormat, "format", "text", "Output format (text, json, html)")
	RootCmd.AddCommand(auditCmd)
}

func runAudit(paths []string) (AuditScorecard, error) {
	var card AuditScorecard
	for _, root := range paths {
		err := filepath.WalkDir(filepath.Clean(root), func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(p), ".xml") {
				return nil
			}
			f, err := os.Open(filepath.Clean(p))
			if err != nil {
				return err
			}
			data, readErr := io.ReadAll(io.LimitReader(f, 16<<20))
			_ = f.Close()
			if readErr != nil {
				return readErr
			}
			card.FilesScanned++
			classes, err := iso20022.ClassifyAddresses(data)
			if err != nil {
				return nil
			}
			for _, cls := range classes {
				switch cls {
				case "structured":
					card.Structured++
					card.TotalAddresses++
				case "hybrid":
					card.Hybrid++
					card.TotalAddresses++
				case "unstructured":
					card.Unstructured++
					card.TotalAddresses++
				}
			}
			return nil
		})
		if err != nil {
			return card, err
		}
	}
	if card.TotalAddresses > 0 {
		card.ReadinessPercent = float64(card.Structured+card.Hybrid) * 100 / float64(card.TotalAddresses)
	} else {
		card.ReadinessPercent = 100
	}
	switch {
	case card.ReadinessPercent >= 90:
		card.RiskLevel = "low"
	case card.ReadinessPercent >= 50:
		card.RiskLevel = "medium"
	default:
		card.RiskLevel = "high"
	}
	return card, nil
}

var auditHTMLTmpl = template.Must(template.New("audit").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"><title>AskISO SR2027 Readiness Scorecard</title></head>
<body>
<h1>SR2027 Readiness Scorecard</h1>
<ul>
  <li>Files scanned: {{.FilesScanned}}</li>
  <li>Total addresses: {{.TotalAddresses}}</li>
  <li>Structured: {{.Structured}}</li>
  <li>Hybrid: {{.Hybrid}}</li>
  <li>Unstructured: {{.Unstructured}}</li>
  <li>Readiness: {{printf "%.1f" .ReadinessPercent}}% ({{.RiskLevel}} risk)</li>
</ul>
</body></html>
`))
