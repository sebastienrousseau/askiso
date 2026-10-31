// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Command ci-sarif checks every message under a directory and writes one SARIF
// 2.1.0 log for the lot, which is what a code-scanning pipeline ingests.
//
// Catching a malformed message in CI costs a build. Catching it after a
// counterparty rejects it costs an investigation and a customer call.
//
//	go run ./examples/ci-sarif ./messages > askiso.sarif
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sebastienrousseau/askiso/pkg/iso20022"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	var results []*iso20022.RuleResult
	var failed int

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(filepath.Ext(path), ".xml") {
			return err
		}
		data, err := os.ReadFile(filepath.Clean(path))
		if err != nil {
			return err
		}
		res, err := iso20022.CheckProfile(data, "cbpr-2026", path)
		if err != nil {
			// One unreadable file must not abandon the whole run: a pipeline
			// that reports nothing because of a single bad input is a pipeline
			// nobody can rely on.
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", path, err)
			return nil
		}
		results = append(results, res)
		failed += res.Errors
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	log, err := iso20022.SARIF(results...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(log)

	fmt.Fprintf(os.Stderr, "%d file(s) checked, %d error(s)\n", len(results), failed)
	if failed > 0 {
		os.Exit(1)
	}
}
