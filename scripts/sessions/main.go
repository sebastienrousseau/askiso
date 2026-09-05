// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

// Command sessions keeps the terminal sessions on askiso.io true.
//
// The website shows shell sessions — a command, then the output it produced.
// Written by hand, those rot: a flag is renamed, a diagnostic is reworded, and
// the site keeps showing output the tool has not produced for months. On a site
// whose argument is "measured, not asserted", that is the worst possible thing
// to get wrong.
//
// So the sessions are executable. Every ```console block in the content whose
// commands start with `askiso` is run against the fixtures in
// testdata/sessions, and its recorded output is compared with what actually
// came back.
//
//	sessions            verify — fails if the site disagrees with the binary
//	sessions -record    re-record, after a deliberate change to the output
//
// Commands that are not `askiso` — `go install`, a shell redirect — are shown
// but not executed, and are reported as such rather than silently trusted.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// ansi matches the escape sequences the CLI writes for colour. The site shows
// plain text, so they are stripped from the recording.
var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type session struct {
	start int // line index of the ```console fence
	end   int // line index of the closing fence
}

func main() {
	os.Exit(mainExit(os.Args[1:], os.Stderr))
}

func mainExit(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("sessions", flag.ContinueOnError)
	flags.SetOutput(stderr)
	record := flags.Bool("record", false, "rewrite the recorded output instead of verifying it")
	content := flags.String("content", "web/content", "directory of content to check")
	fixtures := flags.String("fixtures", "testdata/sessions", "directory the sessions run in")
	bin := flags.String("bin", "", "askiso binary to run (default: build one)")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if err := run(*content, *fixtures, *bin, *record); err != nil {
		_, _ = fmt.Fprintf(stderr, "sessions: %v\n", err)
		return 1
	}
	return 0
}

func run(contentDir, fixtures, bin string, record bool) error {
	if bin == "" {
		built, cleanup, err := build()
		if err != nil {
			return err
		}
		defer cleanup()
		bin = built
	}

	abs, err := filepath.Abs(fixtures)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("fixtures: %w", err)
	}

	files, err := filepath.Glob(filepath.Join(contentDir, "*.md"))
	if err != nil {
		return err
	}

	var checked, drifted, skipped int

	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		lines := strings.Split(string(src), "\n")

		sessions := find(lines)
		if len(sessions) == 0 {
			continue
		}

		changed := false
		// Rewriting shifts line numbers, so replay from the last block back.
		for i := len(sessions) - 1; i >= 0; i-- {
			s := sessions[i]
			want := lines[s.start+1 : s.end]
			got, skips, err := replay(bin, abs, want)
			if err != nil {
				return fmt.Errorf("%s: %w", f, err)
			}
			skipped += skips
			checked++

			if equal(want, got) {
				continue
			}

			drifted++
			if !record {
				fmt.Printf("\n%s:%d — the recorded session no longer matches the binary\n",
					f, s.start+1)
				fmt.Print(diff(want, got))
				continue
			}

			out := append([]string{}, lines[:s.start+1]...)
			out = append(out, got...)
			out = append(out, lines[s.end:]...)
			lines = out
			changed = true
		}

		if changed {
			if err := os.WriteFile(f, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
				return err
			}
			fmt.Printf("re-recorded %s\n", f)
		}
	}

	fmt.Printf("sessions: %d checked, %d not executed, %d drifted\n",
		checked, skipped, drifted)

	if drifted > 0 && !record {
		return fmt.Errorf("%d session(s) disagree with the binary; "+
			"run `make sessions-record` if the change was deliberate", drifted)
	}
	return nil
}

// build compiles the binary the sessions run against, so verification is
// always against this working tree rather than whatever is on PATH.
func build() (string, func(), error) {
	root, err := moduleRoot()
	if err != nil {
		return "", nil, err
	}

	dir, err := os.MkdirTemp("", "askiso-sessions")
	if err != nil {
		return "", nil, err
	}
	bin := filepath.Join(dir, "askiso")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", bin, "./cmd/askiso")
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("building askiso: %w", err)
	}
	return bin, func() { _ = os.RemoveAll(dir) }, nil
}

// moduleRoot finds the directory holding go.mod. Building ./cmd/askiso only
// works from there, and assuming the process was started from the repository
// root makes the tool depend on how it happens to be invoked — `make sessions`
// works, running it from anywhere else does not.
func moduleRoot() (string, error) {
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		return "", fmt.Errorf("locating the module: %w", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", fmt.Errorf("not inside a Go module")
	}
	return filepath.Dir(gomod), nil
}

func find(lines []string) []session {
	var out []session
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```console" {
			continue
		}
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				out = append(out, session{start: i, end: j})
				i = j
				break
			}
		}
	}
	return out
}

// replay runs the commands in a session and returns what the session should
// say. Output between commands is replaced by what the binary actually wrote.
func replay(bin, dir string, lines []string) ([]string, int, error) {
	var out []string
	var skipped int

	for _, line := range lines {
		if !strings.HasPrefix(line, "$ ") {
			// Output from the previous command; regenerated below.
			continue
		}

		command := strings.TrimSpace(line[2:])
		out = append(out, line)

		fields := strings.Fields(command)
		// Only askiso commands are executed. A `go install` line is
		// instruction for the reader, and a redirect is the shell's job.
		if len(fields) == 0 || fields[0] != "askiso" || strings.ContainsAny(command, ">|") {
			skipped++
			continue
		}

		cmd := exec.Command(bin, fields[1:]...)
		cmd.Dir = dir
		// A catalogue is a developer's own download and CI has none, so the
		// sessions must be the ones that work without it. Pointing at an empty
		// directory makes that explicit rather than accidental.
		cmd.Env = append(os.Environ(),
			"ASKISO_CATALOG="+filepath.Join(dir, ".no-catalogue"),
			"NO_COLOR=1",
		)

		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		err := cmd.Run()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			return nil, skipped, fmt.Errorf("starting %q: %w", bin, err)
		}

		out = append(out, clean(buf.String())...)
	}

	return out, skipped, nil
}

// clean turns raw command output into the lines a session records: no colour,
// no trailing blank run, no trailing spaces.
func clean(s string) []string {
	s = ansi.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")

	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	// Leading and trailing blank lines are chrome, not output.
	for len(lines) > 0 && lines[0] == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimRight(a[i], " \t") != strings.TrimRight(b[i], " \t") {
			return false
		}
	}
	return true
}

func diff(want, got []string) string {
	var b strings.Builder
	n := len(want)
	if len(got) > n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		var w, g string
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if strings.TrimRight(w, " \t") == strings.TrimRight(g, " \t") {
			continue
		}
		if w != "" {
			fmt.Fprintf(&b, "  - %s\n", w)
		}
		if g != "" {
			fmt.Fprintf(&b, "  + %s\n", g)
		}
	}
	return b.String()
}
