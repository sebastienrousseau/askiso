// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The tests need a stand-in for the askiso binary. Writing a shell script is
// the obvious way and does not work on Windows, which has no /bin/sh — the
// command simply fails to start, no output is recorded, and the test reports a
// recording bug that is not there.
//
// Re-executing the test binary itself is portable: it is an executable this
// platform can definitely run, and the environment tells it to behave as the
// stand-in rather than run tests.
const (
	fakeEnv     = "ASKISO_SESSIONS_FAKE"
	fakeOutEnv  = "ASKISO_SESSIONS_FAKE_OUT"
	fakeExitEnv = "ASKISO_SESSIONS_FAKE_EXIT"
)

func TestMain(m *testing.M) {
	if os.Getenv(fakeEnv) == "1" {
		if out := os.Getenv(fakeOutEnv); out != "" {
			fmt.Println(out)
		}
		code, _ := strconv.Atoi(os.Getenv(fakeExitEnv))
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// fakeBinary makes this test binary stand in for askiso, printing out and
// exiting with code.
func fakeBinary(t *testing.T, out string, code int) string {
	t.Helper()
	t.Setenv(fakeEnv, "1")
	t.Setenv(fakeOutEnv, out)
	t.Setenv(fakeExitEnv, strconv.Itoa(code))

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("locating the test binary: %v", err)
	}
	return self
}

func TestFindLocatesConsoleBlocks(t *testing.T) {
	lines := strings.Split(`# Title

Some prose.

`+"```console"+`
$ askiso version
AskISO version 0.0.1
`+"```"+`

More prose.

`+"```bash"+`
not a session
`+"```"+`

`+"```console"+`
$ askiso list
`+"```"+`
`, "\n")

	got := find(lines)
	if len(got) != 2 {
		t.Fatalf("found %d console blocks, want 2", len(got))
	}
	// The bash block between them must not be picked up.
	for _, s := range got {
		if strings.TrimSpace(lines[s.start]) != "```console" {
			t.Errorf("block starts at %q, want a console fence", lines[s.start])
		}
		if strings.TrimSpace(lines[s.end]) != "```" {
			t.Errorf("block ends at %q, want a closing fence", lines[s.end])
		}
		if s.end <= s.start {
			t.Errorf("block ends at %d, before it starts at %d", s.end, s.start)
		}
	}
}

func TestFindIgnoresAnUnclosedFence(t *testing.T) {
	lines := strings.Split("```console\n$ askiso version\n", "\n")
	if got := find(lines); len(got) != 0 {
		t.Errorf("found %d blocks in an unclosed fence, want 0", len(got))
	}
}

// The CLI writes colour. A session records what the text says, not how it was
// painted, or every recording would be full of escape sequences.
func TestCleanStripsColourAndTrailingBlanks(t *testing.T) {
	raw := "\n\x1b[32mok\x1b[0m   \nsecond line\n\n\n"
	got := clean(raw)

	want := []string{"ok", "second line"}
	if len(got) != len(want) {
		t.Fatalf("got %d lines %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCleanHandlesWindowsLineEndings(t *testing.T) {
	got := clean("one\r\ntwo\r\n")
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("got %q, want [one two]", got)
	}
}

func TestCleanOnEmptyOutput(t *testing.T) {
	if got := clean(""); len(got) != 0 {
		t.Errorf("got %q, want no lines", got)
	}
	if got := clean("\n\n\n"); len(got) != 0 {
		t.Errorf("blank-only output gave %q, want no lines", got)
	}
}

func TestEqualIgnoresTrailingWhitespace(t *testing.T) {
	if !equal([]string{"a  ", "b"}, []string{"a", "b   "}) {
		t.Error("trailing whitespace should not count as a difference")
	}
	if equal([]string{"a"}, []string{"a", "b"}) {
		t.Error("different lengths are not equal")
	}
	if equal([]string{"a"}, []string{"b"}) {
		t.Error("different content is not equal")
	}
}

func TestDiffShowsBothSides(t *testing.T) {
	got := diff([]string{"same", "old"}, []string{"same", "new"})

	if strings.Contains(got, "same") {
		t.Error("an unchanged line should not appear in the diff")
	}
	if !strings.Contains(got, "- old") {
		t.Errorf("the recorded line is missing:\n%s", got)
	}
	if !strings.Contains(got, "+ new") {
		t.Errorf("the actual line is missing:\n%s", got)
	}
}

func TestDiffHandlesDifferentLengths(t *testing.T) {
	if got := diff([]string{"a"}, []string{"a", "b"}); !strings.Contains(got, "+ b") {
		t.Errorf("an added line is missing:\n%s", got)
	}
	if got := diff([]string{"a", "b"}, []string{"a"}); !strings.Contains(got, "- b") {
		t.Errorf("a removed line is missing:\n%s", got)
	}
}

// Only askiso commands run. A `go install` line is instruction for the reader
// and a redirect belongs to the shell; executing either would be wrong, and
// silently trusting them would defeat the point of the check.
func TestReplayExecutesOnlyAskISOCommands(t *testing.T) {
	dir := t.TempDir()

	got, skipped, err := replay("/nonexistent-binary", dir, []string{
		"$ go install github.com/sebastienrousseau/askiso/cmd/askiso@latest",
		"$ askiso batch . --format sarif > out.sarif",
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if skipped != 2 {
		t.Errorf("skipped %d commands, want 2", skipped)
	}
	if len(got) != 2 {
		t.Fatalf("got %d lines, want the 2 commands back unchanged: %q", len(got), got)
	}
	for i, line := range got {
		if !strings.HasPrefix(line, "$ ") {
			t.Errorf("line %d = %q, want the command preserved", i, line)
		}
	}
}

func TestReplayRunsTheBinaryAndRecordsItsOutput(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBinary(t, "ran: version", 0)

	got, skipped, err := replay(bin, dir, []string{"$ askiso version"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if skipped != 0 {
		t.Errorf("skipped %d, want 0", skipped)
	}
	if len(got) != 2 {
		t.Fatalf("got %q, want the command and one output line", got)
	}
	if got[0] != "$ askiso version" {
		t.Errorf("command = %q", got[0])
	}
	if got[1] != "ran: version" {
		t.Errorf("output = %q, want the binary's own output", got[1])
	}
}

// A failing command is a real result — a linter that finds problems exits
// non-zero, and that session still has to record.
func TestReplayRecordsOutputFromAFailingCommand(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBinary(t, "found 3 errors", 1)

	got, _, err := replay(bin, dir, []string{"$ askiso lint bad.xml"})
	if err != nil {
		t.Fatalf("a non-zero exit should not be an error: %v", err)
	}
	if len(got) != 2 || got[1] != "found 3 errors" {
		t.Errorf("got %q, want the failure output recorded", got)
	}
}

// setupContent returns a content directory holding body, a fixtures directory
// for the commands to run in, and the stand-in binary they should invoke.
func setupContent(t *testing.T, body string) (string, string, string) {
	t.Helper()
	content := t.TempDir()
	fixtures := t.TempDir()
	bin := fakeBinary(t, "real output", 0)

	if err := os.WriteFile(filepath.Join(content, "page.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return content, fixtures, bin
}

func TestRunFailsWhenTheSessionDisagreesWithTheBinary(t *testing.T) {
	body := "```console\n$ askiso version\nstale output\n```\n"
	content, fixtures, bin := setupContent(t, body)

	err := run(content, fixtures, bin, false)
	if err == nil {
		t.Fatal("a drifted session should fail verification")
	}
	if !strings.Contains(err.Error(), "disagree") {
		t.Errorf("error should say the session disagrees: %v", err)
	}
}

func TestRunRecordsTheRealOutput(t *testing.T) {
	body := "```console\n$ askiso version\nstale output\n```\n"
	content, fixtures, bin := setupContent(t, body)

	if err := run(content, fixtures, bin, true); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(content, "page.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "stale output") {
		t.Error("the stale output survived recording")
	}
	if !strings.Contains(string(got), "real output") {
		t.Errorf("the real output was not recorded:\n%s", got)
	}

	// Recording must leave the file verifiable.
	if err := run(content, fixtures, bin, false); err != nil {
		t.Errorf("a freshly recorded session should verify: %v", err)
	}
}

func TestRunRewritesLaterBlocksWithoutShiftingEarlierOnes(t *testing.T) {
	body := "```console\n$ askiso a\nstale one\n```\n\ntext\n\n```console\n$ askiso b\nstale two\n```\n"
	content, fixtures, bin := setupContent(t, body)

	if err := run(content, fixtures, bin, true); err != nil {
		t.Fatalf("record: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(content, "page.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if strings.Contains(s, "stale one") || strings.Contains(s, "stale two") {
		t.Errorf("a stale block survived:\n%s", s)
	}
	if !strings.Contains(s, "$ askiso a") || !strings.Contains(s, "$ askiso b") {
		t.Errorf("a command was lost:\n%s", s)
	}
	if !strings.Contains(s, "text") {
		t.Error("prose between the blocks was lost")
	}
}

func TestRunIgnoresContentWithoutSessions(t *testing.T) {
	content, fixtures, bin := setupContent(t, "# Just prose\n\n```bash\nnot a session\n```\n")
	if err := run(content, fixtures, bin, false); err != nil {
		t.Errorf("a page with no sessions should pass: %v", err)
	}
}

func TestRunReportsMissingFixtures(t *testing.T) {
	content := t.TempDir()
	err := run(content, filepath.Join(content, "nope"), "/bin/echo", false)
	if err == nil || !strings.Contains(err.Error(), "fixtures") {
		t.Errorf("a missing fixture directory should be named in the error: %v", err)
	}
}

func TestRunReportsInvalidGlobAndReplayFailure(t *testing.T) {
	fixtures := t.TempDir()
	if err := run("[", fixtures, "/bin/echo", false); err == nil {
		t.Fatal("invalid content glob was accepted")
	}

	content := t.TempDir()
	if err := os.WriteFile(filepath.Join(content, "page.md"),
		[]byte("```console\n$ askiso version\n```\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(content, fixtures, filepath.Join(t.TempDir(), "missing-binary"), false)
	if err == nil || !strings.Contains(err.Error(), "page.md") {
		t.Fatalf("replay launch error = %v", err)
	}
}

// build() is what makes verification trustworthy: it compiles the binary from
// this working tree rather than trusting whatever `askiso` happens to be on
// PATH. If it silently produced nothing, every session would verify against a
// stale install and the check would be worse than useless.
func TestBuildCompilesFromThisTree(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the CLI")
	}

	bin, cleanup, err := build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer cleanup()

	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("the built binary is not there: %v", err)
	}
	if info.Size() == 0 {
		t.Error("the built binary is empty")
	}

	// It has to actually run, not merely exist.
	out, err := exec.Command(bin, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("running the built binary: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "version") {
		t.Errorf("unexpected output from the built binary:\n%s", out)
	}

	cleanup()
	if _, err := os.Stat(bin); !os.IsNotExist(err) {
		t.Error("cleanup left the temporary directory behind")
	}
}

func TestRunReportsAnUnreadableContentFile(t *testing.T) {
	content, fixtures, bin := setupContent(t, "```console\n$ askiso version\nreal output\n```\n")

	// A directory where a .md file is expected: the read must fail loudly
	// rather than being treated as a page with no sessions.
	if err := os.Mkdir(filepath.Join(content, "broken.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := run(content, fixtures, bin, false); err == nil {
		t.Error("an unreadable content file should be an error")
	}
}

// A session whose commands are all non-askiso has nothing to verify, and must
// not be reported as drifted just because it produced no output.
func TestRunAcceptsASessionWithNothingToExecute(t *testing.T) {
	body := "```console\n$ go install example.com/thing@latest\n```\n"
	content, fixtures, bin := setupContent(t, body)

	if err := run(content, fixtures, bin, false); err != nil {
		t.Errorf("a session with no executable commands should pass: %v", err)
	}
}

func TestRunBuildsTheDefaultBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the CLI")
	}
	content := t.TempDir()
	fixtures := t.TempDir()
	if err := run(content, fixtures, "", false); err != nil {
		t.Fatalf("automatic build: %v", err)
	}
}

func TestBuildReportsAMissingGoTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, _, err := build(); err == nil || !strings.Contains(err.Error(), "locating the module") {
		t.Fatalf("build without go = %v", err)
	}
}

func TestBuildReportsTemporaryDirectoryFailure(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", blocked)
	t.Setenv("TMP", blocked)
	t.Setenv("TEMP", blocked)
	if _, _, err := build(); err == nil {
		t.Fatal("build ignored an unusable temporary directory")
	}
}

func TestModuleRootRejectsNoModuleAndBuildReportsCompilerFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX test executable")
	}
	dir := t.TempDir()
	fakeGo := filepath.Join(dir, "go")
	if err := os.WriteFile(fakeGo, []byte("#!/bin/sh\nprintf '/dev/null\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if _, err := moduleRoot(); err == nil || !strings.Contains(err.Error(), "not inside") {
		t.Fatalf("moduleRoot with GOMOD=/dev/null = %v", err)
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nif [ \"$1\" = env ]; then printf '%s/go.mod\\n' '" + root + "'; exit 0; fi\nexit 1\n"
	if err := os.WriteFile(fakeGo, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := build(); err == nil || !strings.Contains(err.Error(), "building askiso") {
		t.Fatalf("compiler failure = %v", err)
	}
}

func TestMainExitCodesAndDiagnostics(t *testing.T) {
	var stderr bytes.Buffer
	if code := mainExit([]string{"-definitely-not-a-flag"}, &stderr); code != 2 {
		t.Fatalf("parse error exit = %d, want 2", code)
	}
	if stderr.Len() == 0 {
		t.Fatal("parse error wrote no diagnostic")
	}

	stderr.Reset()
	code := mainExit([]string{"-content", t.TempDir(), "-fixtures", filepath.Join(t.TempDir(), "missing"), "-bin", "/bin/echo"}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "fixtures") {
		t.Fatalf("runtime failure = exit %d, stderr %q", code, stderr.String())
	}

	stderr.Reset()
	code = mainExit([]string{"-content", t.TempDir(), "-fixtures", t.TempDir(), "-bin", "/bin/echo"}, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("success = exit %d, stderr %q", code, stderr.String())
	}
}
