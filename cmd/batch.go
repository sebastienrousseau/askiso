// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/sebastienrousseau/askiso/internal/rules"
	"github.com/sebastienrousseau/askiso/pkg/iso20022"
	"github.com/spf13/cobra"
)

var (
	batchProfile       string
	batchFormat        string
	batchSchema        bool
	batchWorkers       int
	batchQuiet         bool
	batchCBPRPack      string
	batchCBPRWorkspace string
)

// FileReport is the outcome for one message.
type FileReport struct {
	File       string                 `json:"file"`
	MessageID  string                 `json:"message_id,omitempty"`
	Lint       *iso20022.LintResult   `json:"lint,omitempty"`
	Schema     *iso20022.SchemaResult `json:"schema,omitempty"`
	Profile    *rules.Result          `json:"profile,omitempty"`
	Err        string                 `json:"error,omitempty"`
	ErrorCount int                    `json:"error_count"`
}

// BatchReport aggregates a run.
type BatchReport struct {
	Files    int                 `json:"files"`
	Passed   int                 `json:"passed"`
	Failed   int                 `json:"failed"`
	Errors   int                 `json:"error_count"`
	Profile  string              `json:"profile,omitempty"`
	Pack     *rules.CBPRPackInfo `json:"cbpr_pack,omitempty"`
	Reports  []FileReport        `json:"reports"`
	Skipped  int                 `json:"skipped"`
	Duration string              `json:"-"`
}

var batchCmd = &cobra.Command{
	Use:   "batch <file-or-directory>...",
	Short: "Validate and lint many messages at once",
	Long: `Batch runs the business-rule linter, and optionally schema validation and a
scheme rule profile, across every message in a directory or glob.

This is the shape real operational work takes: a folder of messages, one report,
one exit code. With --format sarif the output uploads straight to GitHub code
scanning, so a pull request that introduces a non-compliant address is annotated
rather than silently merged.`,
	Example: `  askiso batch ./messages
  askiso batch ./messages --profile cbpr-2026
  askiso batch ./messages --profile cbpr-2026 --format sarif > askiso.sarif
  askiso batch ./out --schema --profile cbpr-2026`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := normalizeChoice("format", batchFormat, "text", "json", "sarif")
		if err != nil {
			return err
		}
		batchFormat = format
		if batchWorkers < 0 {
			return fmt.Errorf("--workers must be zero or greater")
		}
		files, err := collectMessages(args)
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("no .xml files found in %s", strings.Join(args, ", "))
		}

		var runtimeProfile *rules.Profile
		var workspaceExternal *iso20022.ExternalCodeSets
		if batchProfile != "" || batchCBPRPack != "" || batchCBPRWorkspace != "" {
			profile, workspaceRuntime, profileErr := resolveRuleProfileWithWorkspace(batchProfile, batchCBPRPack, batchCBPRWorkspace)
			if profileErr != nil {
				return profileErr
			}
			batchProfile = profile.Name
			runtimeProfile = &profile
			if workspaceRuntime != nil {
				workspaceExternal = workspaceRuntime.ExternalCodes
			}
		}

		var cat *iso20022.Catalogue
		if batchSchema {
			cat, err = iso20022.OpenCatalogue(catalogPath)
			if err != nil {
				return fmt.Errorf("--schema needs an installed catalogue:\n\n%w", err)
			}
		}

		report := runBatchWithRuntime(files, cat, runtimeProfile, workspaceExternal)

		switch batchFormat {
		case "sarif":
			if err := rules.WriteDiagnosticsSARIF(os.Stdout, batchSARIFDiagnostics(report)); err != nil {
				return err
			}
		case "json":
			out, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return err
			}
			fmt.Println(string(out))
		default:
			printBatchText(report)
		}

		if report.Failed > 0 {
			return errSilent
		}
		return nil
	},
}

// collectMessages expands the arguments into a sorted list of XML files.
func collectMessages(args []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string

	add := func(p string) {
		if !strings.EqualFold(filepath.Ext(p), ".xml") || seen[p] {
			return
		}
		seen[p] = true
		files = append(files, p)
	}

	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			// Not a path: treat it as a glob.
			matches, gerr := filepath.Glob(arg)
			if gerr != nil || len(matches) == 0 {
				return nil, fmt.Errorf("no such file or directory: %s", arg)
			}
			for _, m := range matches {
				add(m)
			}
			continue
		}

		if !info.IsDir() {
			add(arg)
			continue
		}
		err = filepath.Walk(arg, func(p string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() {
				return nil
			}
			add(p)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(files)
	return files, nil
}

// runBatch checks every file, in parallel. Reports come back in input order so
// output is deterministic regardless of scheduling.
func runBatchWithProfile(files []string, cat *iso20022.Catalogue, profile *rules.Profile) *BatchReport {
	return runBatchWithRuntime(files, cat, profile, nil)
}

func runBatchWithRuntime(files []string, cat *iso20022.Catalogue, profile *rules.Profile, external *iso20022.ExternalCodeSets) *BatchReport {
	workers := batchWorkers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	if workers > len(files) {
		workers = len(files)
	}

	reports := make([]FileReport, len(files))
	jobs := make(chan int)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				reports[i] = checkOneWithRuntime(files[i], cat, profile, external)
			}
		}()
	}
	for i := range files {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	out := &BatchReport{Files: len(files), Profile: batchProfile, Reports: reports}
	if profile != nil {
		out.Profile = profile.Name
		out.Pack = profile.Pack
	}
	for _, r := range reports {
		if r.ErrorCount > 0 || r.Err != "" {
			out.Failed++
			out.Errors += r.ErrorCount
		} else {
			out.Passed++
		}
	}
	return out
}

func checkOne(path string, cat *iso20022.Catalogue) FileReport {
	var profile *rules.Profile
	if batchProfile != "" {
		if resolved, err := rules.Get(batchProfile); err == nil {
			profile = &resolved
		}
	}
	return checkOneWithProfile(path, cat, profile)
}

func checkOneWithProfile(path string, cat *iso20022.Catalogue, profile *rules.Profile) FileReport {
	return checkOneWithRuntime(path, cat, profile, nil)
}

func checkOneWithRuntime(path string, cat *iso20022.Catalogue, profile *rules.Profile, external *iso20022.ExternalCodeSets) FileReport {
	rep := FileReport{File: path}

	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		rep.Err = err.Error()
		rep.ErrorCount = 1
		return rep
	}
	if id, err := iso20022.MessageIDFromInstance(data); err == nil {
		rep.MessageID = id
	}

	lintRes, err := iso20022.Lint(data, path)
	if err != nil {
		rep.Err = err.Error()
		rep.ErrorCount = 1
		return rep
	}
	rep.Lint = lintRes
	rep.ErrorCount += lintRes.Errors

	if cat != nil {
		addSchemaResult(&rep, data, cat, external)
	}
	if profile != nil {
		addProfileResult(&rep, data, path, *profile)
	}

	return rep
}

// addSchemaResult validates data against the catalogue schema, through the
// external code sets when supplied, and records the outcome on rep.
func addSchemaResult(rep *FileReport, data []byte, cat *iso20022.Catalogue, external *iso20022.ExternalCodeSets) {
	var schemaRes *iso20022.SchemaResult
	var err error
	if external == nil {
		schemaRes, err = cat.Validate(data)
	} else {
		var schemaPath string
		schemaPath, err = cat.SchemaPath(rep.MessageID)
		if err == nil {
			schemaRes, err = iso20022.ValidateFileWithExternalCodes(data, schemaPath, external)
		}
	}
	if err != nil {
		rep.Err = err.Error()
		rep.ErrorCount++
		return
	}
	rep.Schema = schemaRes
	rep.ErrorCount += len(schemaRes.Errors)
}

// addProfileResult runs the resolved rule profile over data and records the
// outcome on rep.
func addProfileResult(rep *FileReport, data []byte, path string, profile rules.Profile) {
	profRes, err := runResolvedProfile(data, path, profile)
	if err != nil {
		rep.Err = err.Error()
		rep.ErrorCount++
		return
	}
	rep.Profile = profRes
	rep.ErrorCount += profRes.Errors
}

func sarifRuleID(prefix, name string) string {
	var b strings.Builder
	b.WriteString(prefix)
	lastDash := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
		} else if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func batchSARIFDiagnostics(report *BatchReport) []rules.SARIFDiagnostic {
	var out []rules.SARIFDiagnostic
	profileRules := map[string]rules.Rule{}
	if report.Profile != "" {
		if p, err := rules.Get(report.Profile); err == nil {
			for _, rule := range p.Rules {
				profileRules[rule.ID] = rule
			}
		}
	}
	for _, rep := range report.Reports {
		if rep.Lint != nil {
			for _, issue := range rep.Lint.Issues {
				severity := rules.SeverityInfo
				switch issue.Severity {
				case iso20022.SeverityError:
					severity = rules.SeverityError
				case iso20022.SeverityWarning:
					severity = rules.SeverityWarning
				}
				description := issue.Expected
				if description == "" {
					description = issue.Rule
				}
				help := issue.Remediation
				if help == "" {
					help = "Correct the field and run AskISO again."
				}
				out = append(out, rules.SARIFDiagnostic{
					RuleID: sarifRuleID("lint/", issue.Rule), Name: issue.Rule,
					Description: description, Help: help, Severity: severity,
					Message: issue.Message, File: rep.File, Path: issue.Path,
					Properties: map[string]string{"engine": "linter"},
				})
			}
		}
		if rep.Schema != nil {
			for _, issue := range rep.Schema.Errors {
				out = append(out, rules.SARIFDiagnostic{
					RuleID: sarifRuleID("xsd/", issue.Rule), Name: issue.Rule,
					Description: "ISO 20022 XML Schema validation failure",
					Help:        "Change the element or attribute to satisfy the message schema.",
					Severity:    rules.SeverityError, Message: issue.Message,
					File: rep.File, Path: issue.Path,
					Properties: map[string]string{"engine": "schema"},
				})
			}
		}
		if rep.Profile != nil {
			for _, finding := range rep.Profile.Findings {
				rule := profileRules[finding.RuleID]
				help := finding.Remediation
				if help == "" {
					help = rule.Remediation
				}
				out = append(out, rules.SARIFDiagnostic{
					RuleID: finding.RuleID, Name: finding.Rule,
					Description: rule.Description, HelpURI: finding.Reference,
					Help: help, Severity: finding.Severity, Message: finding.Message,
					File: rep.File, Path: finding.Path,
					Properties: map[string]string{"engine": "profile", "profile": report.Profile},
				})
			}
		}
		if rep.Err != "" && rep.ErrorCount > 0 && rep.Lint == nil {
			out = append(out, rules.SARIFDiagnostic{
				RuleID: "askiso/input", Name: "Input could not be checked",
				Description: "AskISO could not read or parse the input document.",
				Help:        "Correct the input or catalogue error and run AskISO again.",
				Severity:    rules.SeverityError, Message: firstLineOf(rep.Err), File: rep.File,
				Properties: map[string]string{"engine": "askiso"},
			})
		}
	}
	return out
}

func printBatchText(report *BatchReport) {
	fmt.Printf("\n%s %d file(s)\n\n", headStyle.Render(" BATCH "), report.Files)

	for _, r := range report.Reports {
		if r.ErrorCount == 0 && r.Err == "" {
			if !batchQuiet {
				fmt.Printf("  %s %s\n", tickMark, filepath.Base(r.File))
			}
			continue
		}

		fmt.Printf("  %s %s", crossMark, filepath.Base(r.File))
		if r.MessageID != "" {
			fmt.Printf("  %s", subtleStyle.Render(r.MessageID))
		}
		fmt.Println()

		if r.Err != "" {
			fmt.Printf("      %s\n", firstLineOf(r.Err))
		}
		if r.Lint != nil {
			for _, i := range r.Lint.Issues {
				if i.Severity == iso20022.SeverityError {
					fmt.Printf("      [%s] %s\n", i.Rule, i.Message)
				}
			}
		}
		if r.Schema != nil {
			for _, e := range r.Schema.Errors {
				fmt.Printf("      %d:%d [%s] %s\n", e.Line, e.Column, e.Rule, e.Message)
			}
		}
		if r.Profile != nil {
			for _, f := range r.Profile.Findings {
				if f.Severity == rules.SeverityError {
					fmt.Printf("      [%s] %s\n         at %s\n", f.RuleID, f.Message, f.Path)
				}
			}
		}
		fmt.Println()
	}

	fmt.Printf("  %d passed, %d failed, %d error(s) total\n\n",
		report.Passed, report.Failed, report.Errors)
}

func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func init() {
	batchCmd.Flags().StringVar(&batchProfile, "profile", "",
		"Scheme rule profile to apply ("+strings.Join(rules.Names(), ", ")+")")
	batchCmd.Flags().StringVar(&batchFormat, "format", "text", "Output format: text, json, or sarif")
	batchCmd.Flags().BoolVar(&batchSchema, "schema", false,
		"Also validate each message against its schema (needs an installed catalogue)")
	batchCmd.Flags().IntVar(&batchWorkers, "workers", 0, "Parallel workers (default: one per CPU)")
	batchCmd.Flags().BoolVarP(&batchQuiet, "quiet", "s", false, "Only list files that failed")
	batchCmd.Flags().StringVar(&batchCBPRPack, "cbpr-pack", "",
		"Local CBPR+ PDF directory or compiled .cbpr-pack.json (implies --profile cbpr-plus)")
	batchCmd.Flags().StringVar(&batchCBPRWorkspace, "cbpr-workspace", "",
		"Verified private CBPR+ workspace and external-code index")
	RootCmd.AddCommand(batchCmd)
}
