// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/atotto/clipboard"
	"github.com/sebastienrousseau/askiso/internal/catalog"
	"github.com/spf13/cobra"
)

var (
	schemaCopy bool
	schemaRaw  bool
)

var schemaCmd = &cobra.Command{
	Use:     "schema <message-id>",
	Aliases: []string{"xsd"},
	Short:   "Display syntax-highlighted XML Schema Definition (XSD)",
	Long: `Schema locates and outputs the official XML Schema Definition (.xsd) 
for any ISO 20022 message with terminal syntax highlighting and clipboard export.`,
	Example: `  askiso schema pacs.008.001.10
  askiso schema pain.001 --copy
  askiso schema camt.053 --raw`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := args[0]

		msg, idx, err := resolveMessage(query, "read its schema")
		if err != nil {
			return err
		}

		targetPath := msg.XSDPath
		if targetPath == "" {
			for _, m := range idx.Search(query) {
				if m.XSDPath != "" {
					targetPath = m.XSDPath
					break
				}
			}
		}
		if targetPath == "" {
			return notInstalled(query, "read its schema", nil)
		}
		if err := catalog.CheckEvicted(targetPath); err != nil {
			return err
		}

		data, err := os.ReadFile(filepath.Clean(targetPath))
		if err != nil {
			return fmt.Errorf("failed to read schema file: %w", err)
		}

		content := string(data)

		if schemaCopy {
			if err := clipboard.WriteAll(content); err == nil {
				fmt.Printf("\n%s Copied XSD schema for %s to clipboard!\n\n", badgeStyle.Render(" COPIED "), query)
			}
		}

		if schemaRaw || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
			if !schemaCopy {
				fmt.Println(content)
			}
			return nil
		}

		highlighted := highlightXML(content)
		if !schemaCopy {
			fmt.Println(highlighted)
		}
		return nil
	},
}

func init() {
	schemaCmd.Flags().BoolVarP(&schemaCopy, "copy", "y", false, "Copy XSD schema to system clipboard")
	schemaCmd.Flags().BoolVarP(&schemaRaw, "raw", "r", false, "Output raw uncolored XSD schema text")
	RootCmd.AddCommand(schemaCmd)
}
