// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Command batch-audit answers the portfolio question: across everything we
// send, how much of it is ready for November 2026, and which rules are we
// failing most often?
//
// Institutions rarely have one bad message. They have a generator somewhere
// producing the same defect thousands of times, and the useful output is the
// ranking that points at it.
//
//	go run ./examples/batch-audit ./outbound
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sebastienrousseau/askiso/pkg/iso20022"
)

func main() {
	dir := "."
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	byRule := map[string]int{}
	var checked, clean int

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
			fmt.Fprintf(os.Stderr, "skipping %s: %v\n", path, err)
			return nil
		}
		checked++
		if res.Valid() {
			clean++
		}
		for _, f := range res.Findings {
			byRule[f.RuleID]++
		}
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if checked == 0 {
		fmt.Printf("no XML messages found under %s\n", dir)
		return
	}

	fmt.Printf("%d message(s): %d ready, %d not\n\n", checked, clean, checked-clean)

	type row struct {
		rule string
		n    int
	}
	rows := make([]row, 0, len(byRule))
	for r, n := range byRule {
		rows = append(rows, row{r, n})
	}
	// Most frequent first: the top row is usually one upstream defect, and
	// fixing it moves more messages than working through the list in order.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].n != rows[j].n {
			return rows[i].n > rows[j].n
		}
		return rows[i].rule < rows[j].rule
	})

	fmt.Println("findings by rule:")
	for _, r := range rows {
		fmt.Printf("  %6d  %s\n", r.n, r.rule)
	}
}
