// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/translator"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
	"github.com/spf13/cobra"
)

var (
	showMatrix      bool
	translateOut    string
	translateReport bool
	translateFormat string
)

var translateCmd = &cobra.Command{
	Use:   "translate [mt-code|mx-code|file]",
	Short: "Translate SWIFT MT messages to ISO 20022, or look up the mapping",
	Long: `Translate does two things.

Given a message identifier it prints the cross-reference: which MX message
replaces which MT, and how the fields line up.

Given a file it performs the conversion, in whichever direction the file calls
for -- an MT message becomes ISO 20022, an ISO 20022 message becomes MT:

  MT101  request for transfer           -> pain.001  customer credit transfer initiation
  MT103  customer credit transfer       -> pacs.008  FI to FI customer credit transfer
  MT104  request for debit transfer     -> pain.008  customer direct debit initiation
  MT107  general direct debit           -> pain.008  customer direct debit initiation
  MT202  general financial institution  -> pacs.009  financial institution credit transfer
  MT204  financial markets direct debit -> pacs.010  financial institution direct debit
  MT940  customer statement             -> camt.053  bank to customer statement
  MTn92  request for cancellation       -> camt.056  payment cancellation request
  MTn95  queries                        -> camt.110  investigation request
  MTn96  answers                        -> camt.111  investigation response

The exception messages are numbered by category -- MT192 cancels a customer
payment, MT292 an institution one -- and every category converts.

  pain.001 -> MT101    pacs.008 -> MT103    pain.008 -> MT104
  pacs.009 -> MT202    pacs.010 -> MT204    camt.053 -> MT940

Each conversion comes with a fidelity report saying what was carried across,
what was shortened to fit, what was inferred, and what had nowhere to go.
Nothing is dropped silently: every field in the source appears in the report.

The two directions lose different things. MT to MX produces unstructured
addresses, which CBPR+ stops accepting once the deferred structured address
requirement takes effect. MX to MT loses what
was added since: purpose codes, legal entity identifiers, structured remittance,
and the structured addresses themselves -- and cuts a 35-character reference to
the 16 an MT field allows.

MT addresses are unstructured, so a converted message will not satisfy the CBPR+
structured-address requirement once it takes effect. The report
says so, and 'askiso lint --profile cbpr-2026' shows exactly which elements need
enriching.`,
	Example: `  askiso translate MT103
  askiso translate --matrix
  askiso translate payment.mt103
  askiso translate request.mt101 --out pain001.xml --report
  askiso translate payment.xml --report          # ISO 20022 back to MT
	  askiso translate statement.mt940 --format json`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := normalizeChoice("format", translateFormat, "text", "json")
		if err != nil {
			return err
		}
		translateFormat = format
		if showMatrix || len(args) == 0 {
			printMatrix()
			return nil
		}

		query := args[0]

		// A path that exists is a message to convert; anything else is a lookup.
		if info, err := os.Stat(query); err == nil && !info.IsDir() {
			return translateFile(query)
		}

		m, ok := translator.Lookup(query)
		if !ok {
			return fmt.Errorf("no translation mapping found for %q, and no such file "+
				"(try: MT103, MT202, MT940, pacs.008, camt.053)", query)
		}

		fmt.Println()
		fmt.Println(translator.FormatMapping(m))
		fmt.Println()
		return nil
	},
}

func printMatrix() {
	mappings := translator.GetAllMappings()
	fmt.Printf("\n%s SWIFT MT ⇄ ISO 20022 MX Migration Cross-Reference Matrix\n\n", headStyle.Render(" TRANSLATION MATRIX "))
	fmt.Printf("  %-12s %-24s %-32s\n", "SWIFT MT", "ISO 20022 MX", "Business Purpose")
	fmt.Printf("  %-12s %-24s %-32s\n", "────────", "────────────", "────────────────")
	for _, m := range mappings {
		fmt.Printf("  %-12s %-24s %-32s\n", titleStyle.Render(m.MTCode), subtleStyle.Render(m.MXCode), m.MTTitle)
	}
	fmt.Println("\nConvert a real message:  askiso translate payment.mt103 --report")
	fmt.Println("Field-level mapping:     askiso translate MT103")
	fmt.Println()
}

func translateFile(path string) error {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return err
	}

	// Which direction this is depends on what the file holds: an MT message
	// starts with its block structure, an MX one is XML.
	conv, err := translateEitherWay(data)
	if err != nil {
		return err
	}

	if translateFormat == "json" {
		out, err := json.MarshalIndent(conv, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		return nil
	}

	if translateOut != "" {
		if err := os.WriteFile(translateOut, []byte(conv.XML+"\n"), 0o600); err != nil {
			return err
		}
	} else if !translateReport {
		fmt.Println(conv.XML)
	}

	if translateOut != "" || translateReport {
		printConversionReport(path, conv)
	}
	return nil
}

// translateEitherWay picks the conversion by what the file actually contains.
func translateEitherWay(data []byte) (*iso20022.Conversion, error) {
	if looksLikeXML(data) {
		return iso20022.TranslateMX(data)
	}
	return iso20022.TranslateMT(data)
}

// looksLikeXML reports whether a file is an ISO 20022 document rather than an
// MT message. An MT message begins with "{1:"; XML begins with a declaration or
// an element.
func looksLikeXML(data []byte) bool {
	trimmed := strings.TrimLeft(string(data), " \t\r\n\uFEFF")
	return strings.HasPrefix(trimmed, "<")
}

func printConversionReport(path string, conv *iso20022.Conversion) {
	counts := conv.Counts()

	fmt.Printf("\n%s  %s → %s\n\n", headStyle.Render(" TRANSLATE "),
		conversionLabel(conv.SourceType), conv.TargetType)
	fmt.Printf("  source   %s\n", path)
	if translateOut != "" {
		fmt.Printf("  written  %s\n", translateOut)
	}
	fmt.Printf("  fields   %d mapped, %d derived, %d truncated, %d unmapped\n\n",
		counts["mapped"], counts["derived"], counts["truncated"], counts["unmapped"])

	for _, r := range conv.Report {
		mark := tickMark
		switch r.Fidelity {
		case "unmapped":
			mark = crossMark
		case "truncated", "derived":
			mark = warnMark
		}
		// Going MT to MX the tag is a field; going the other way it is an
		// element path, which is already legible without the colons.
		label := r.Tag
		if !strings.Contains(label, "/") && !strings.HasPrefix(label, "(") {
			label = ":" + label + ":"
		}
		fmt.Printf("  %s %s  %s\n", mark, label, subtleStyle.Render(string(r.Fidelity)))
		if r.Path != "" {
			fmt.Printf("       → %s\n", r.Path)
		}
		if r.Note != "" {
			for _, line := range wrapNote(r.Note, 66) {
				fmt.Printf("       %s\n", subtleStyle.Render(line))
			}
		}
	}

	fmt.Println()
	if conv.Lossless() {
		// Nothing was lost, but a derived value was never in the source, and
		// saying only "intact" would let that pass unnoticed.
		if counts["derived"] > 0 {
			fmt.Printf("  %s every source field was carried across intact; %d value(s) had to be derived\n\n",
				tickMark, counts["derived"])
			return
		}
		fmt.Printf("  %s every field was carried across intact\n\n", tickMark)
		return
	}
	fmt.Printf("  %s this conversion is lossy — review the entries above before relying on it\n",
		warnMark)
	fmt.Printf("  %s check the CBPR+ address rules:      askiso lint <file> --profile cbpr-2026\n\n",
		subtleStyle.Render("→"))
}

// conversionLabel names the source: an MT type is three digits, an ISO 20022
// identifier is already self-describing.
func conversionLabel(sourceType string) string {
	if strings.Contains(sourceType, ".") {
		return sourceType
	}
	return "MT" + sourceType
}

// wrapNote breaks a note onto lines so long guidance stays readable.
func wrapNote(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	lines := []string{words[0]}
	for _, w := range words[1:] {
		last := len(lines) - 1
		if len(lines[last])+1+len(w) > width {
			lines = append(lines, w)
			continue
		}
		lines[last] += " " + w
	}
	return lines
}

func init() {
	translateCmd.Flags().BoolVarP(&showMatrix, "matrix", "m", false, "Display the complete MT <-> MX cross-reference table")
	translateCmd.Flags().StringVarP(&translateOut, "out", "o", "", "Write the converted message to a file")
	translateCmd.Flags().BoolVarP(&translateReport, "report", "r", false, "Print the fidelity report instead of the message")
	translateCmd.Flags().StringVar(&translateFormat, "format", "text", "Output format: text or json")
	RootCmd.AddCommand(translateCmd)
}
