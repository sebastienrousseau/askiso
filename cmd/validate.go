// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sebastienrousseau/askiso/internal/catalog"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
	"github.com/spf13/cobra"
)

var (
	validateJSON          bool
	validateEngine        string
	validateStream        bool
	validateExternalCodes string
)

// headBytes is how much of a large document is read to find its namespace. The
// XML declaration and the document element are always within the first few
// hundred bytes.
const headBytes = 64 << 10

// readInstanceHead reads the whole file, or just enough of a large one to
// resolve its schema.
func readInstanceHead(path string, streaming bool) ([]byte, error) {
	if !streaming {
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		return data, nil
	}

	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	buf := make([]byte, headBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return buf[:n], nil
}

var validateCmd = &cobra.Command{
	Use:   "validate <xml-file> [xsd-file]",
	Short: "Validate an XML message against its ISO 20022 schema",
	Long: `Validate checks an ISO 20022 message against its XML Schema.

Validation is pure Go: no libxml2, no cgo, and identical results on every
platform. When the schema is not given, AskISO resolves it from the document's
namespace against your installed catalogue.

Diagnostics carry the element path, the schema rule that fired, and what was
expected versus what was found.

A file of 8 MiB or more is validated as it is read rather than held in memory,
which is what makes a month-long camt.053 checkable on an ordinary machine. The
verdict is identical either way; --stream forces it on a smaller file.`,
	Example: `  askiso validate payment.xml
  askiso validate payment.xml pacs.008.001.10.xsd
  askiso validate payment.xml --json
  askiso validate payment.xml --engine libxml2
  askiso validate statement.xml --stream`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		engine, err := normalizeChoice("engine", validateEngine, "go", "libxml2")
		if err != nil {
			return err
		}
		validateEngine = engine
		if validateExternalCodes != "" && validateEngine == "libxml2" {
			return errors.New("--external-codes requires the Go validation engine")
		}
		xmlPath := filepath.Clean(args[0])

		info, err := os.Stat(xmlPath)
		if err != nil {
			return fmt.Errorf("reading %s: %w", xmlPath, err)
		}

		// A large statement is read as it is validated rather than held. The
		// verdict is the same either way; the difference is whether the machine
		// needs enough memory for the whole file.
		streaming := validateStream || info.Size() >= iso20022.StreamThreshold

		// The namespace still has to be read to resolve the schema, and the
		// declaration is in the first few hundred bytes.
		xmlData, err := readInstanceHead(xmlPath, streaming)
		if err != nil {
			return err
		}

		var schemaPath string
		if len(args) > 1 {
			schemaPath = filepath.Clean(args[1])
			if _, err := os.Stat(schemaPath); err != nil {
				return fmt.Errorf("schema not found: %s", schemaPath)
			}
		} else {
			schemaPath, err = resolveSchemaForInstance(xmlData)
			if err != nil {
				return err
			}
		}

		if err := catalog.CheckEvicted(schemaPath); err != nil {
			return err
		}

		if validateEngine == "libxml2" {
			return validateWithXmllint(schemaPath, xmlPath)
		}

		var external *iso20022.ExternalCodeSets
		if validateExternalCodes != "" {
			external, err = iso20022.ReadExternalCodeSets(validateExternalCodes)
			if err != nil {
				return err
			}
		} else {
			external = externalSets()
		}

		var res *iso20022.SchemaResult
		if streaming {
			f, err := os.Open(xmlPath)
			if err != nil {
				return fmt.Errorf("reading %s: %w", xmlPath, err)
			}
			res, err = iso20022.ValidateStreamWithExternalCodes(f, schemaPath, external)
			_ = f.Close()
			if err != nil {
				return err
			}
		} else {
			res, err = iso20022.ValidateFileWithExternalCodes(xmlData, schemaPath, external)
			if err != nil {
				return err
			}
		}

		if validateJSON {
			payload := struct {
				File   string                 `json:"file"`
				Schema string                 `json:"schema"`
				Valid  bool                   `json:"valid"`
				Errors []iso20022.SchemaError `json:"errors"`
			}{xmlPath, schemaPath, res.Valid, res.Errors}
			data, err := json.MarshalIndent(payload, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(data))
			if !res.Valid {
				return errSilent
			}
			return nil
		}

		if res.Valid {
			fmt.Printf("\n%s %s validates against %s\n\n",
				badgeStyle.Render(" VALID "), filepath.Base(xmlPath), filepath.Base(schemaPath))
			return nil
		}

		fmt.Printf("\n%s %s does not validate against %s\n\n",
			badgeStyle.Render(" INVALID "), filepath.Base(xmlPath), filepath.Base(schemaPath))

		for _, e := range res.Errors {
			fmt.Printf("  %s:%d:%d\n", filepath.Base(xmlPath), e.Line, e.Column)
			fmt.Printf("    %s  %s\n", subtleStyle.Render("["+e.Rule+"]"), e.Message)
			fmt.Printf("    %s %s\n", subtleStyle.Render("at      "), e.Path)
			if e.Expected != "" {
				fmt.Printf("    %s %s\n", subtleStyle.Render("expected"), e.Expected)
			}
			if e.Actual != "" {
				fmt.Printf("    %s %s\n", subtleStyle.Render("found   "), e.Actual)
			}
			fmt.Println()
		}

		fmt.Printf("  %d error(s)\n\n", len(res.Errors))
		return errSilent
	},
}

// errSilent reports failure through the exit code without printing again; the
// diagnostics have already been shown.
var errSilent = &silentError{}

type silentError struct{}

func (*silentError) Error() string { return "" }

// resolveSchemaForInstance finds the schema for a document from its namespace.
func resolveSchemaForInstance(xmlData []byte) (string, error) {
	msgID, err := iso20022.MessageIDFromInstance(xmlData)
	if err != nil {
		return "", fmt.Errorf("%w\n\nPass the schema explicitly: askiso validate <xml> <xsd>", err)
	}

	cat, err := iso20022.OpenCatalogue(catalogPath)
	if err != nil {
		return "", notInstalled(msgID, "validate against its schema", err)
	}

	path, err := cat.SchemaPath(msgID)
	if err != nil {
		return "", notInstalled(msgID, "validate against its schema", err)
	}
	return path, nil
}

func init() {
	validateCmd.Flags().BoolVar(&validateJSON, "json", false, "Output the result as JSON")
	validateCmd.Flags().StringVar(&validateEngine, "engine", "go",
		"Validation engine: \"go\" (built in) or \"libxml2\" (external xmllint, for cross-checking)")
	validateCmd.Flags().BoolVar(&validateStream, "stream", false,
		"Validate as the file is read rather than holding it in memory (automatic above 8 MiB)")
	validateCmd.Flags().StringVar(&validateExternalCodes, "external-codes", "",
		"Local Registration Authority XLSX or JSON code-set publication")
	RootCmd.AddCommand(validateCmd)
}

// validateWithXmllint delegates to libxml2, so a result can be cross-checked
// against the reference implementation.
func validateWithXmllint(schemaPath, xmlPath string) error {
	path, err := lookXmllint()
	if err != nil {
		return fmt.Errorf("xmllint is not installed; omit --engine to use the built-in validator")
	}

	out, runErr := runXmllint(path, schemaPath, xmlPath)
	if runErr != nil {
		fmt.Printf("\n%s %s\n\n", badgeStyle.Render(" INVALID "), filepath.Base(xmlPath))
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fmt.Printf("  %s\n", line)
		}
		fmt.Println()
		return errSilent
	}

	fmt.Printf("\n%s %s validates against %s (libxml2)\n\n",
		badgeStyle.Render(" VALID "), filepath.Base(xmlPath), filepath.Base(schemaPath))
	return nil
}

func lookXmllint() (string, error) { return exec.LookPath("xmllint") }

// runXmllint invokes libxml2 with network access disabled and a hard timeout,
// so a hostile schema cannot hang the command or reach the network.
func runXmllint(bin, schemaPath, xmlPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "--noout", "--nonet", "--schema", schemaPath, xmlPath)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
