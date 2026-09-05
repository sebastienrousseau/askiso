// SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
// SPDX-License-Identifier: Apache-2.0 OR MIT

package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sebastienrousseau/askiso/internal/cbprworkspace"
	"github.com/spf13/cobra"
)

var (
	cbprWorkspace               string
	cbprRelease                 string
	cbprExternalCodes           string
	cbprExternalCodesAsOf       string
	cbprRuleOverlay             string
	cbprWorkspaceJSON           bool
	cbprGenerateSamples         bool
	cbprAcknowledgeEntitlement  bool
	cbprAsOf                    string
	cbprEvidence                string
	cbprRequireUserSamples      bool
	cbprRequireExternalEvidence bool
	cbprSampleOutput            string
	cbprTransportProfile        string
	cbprSenderDN                string
	cbprReceiverDN              string
	cbprTransportService        string
	cbprReviewer                string
	cbprProvider                string
	cbprEvidenceTime            string
	cbprEvidenceCases           int
	cbprEvidencePassed          bool
	cbprAcknowledgeReview       bool
	cbprAcknowledgeVerdict      bool
	cbprFromRelease             string
	cbprToRelease               string
	cbprGenerationKeep          int
	cbprGenerationConfirm       bool
)

var cbprPackImportCmd = &cobra.Command{
	Use:   "import <private-source-directory>",
	Short: "Index private CBPR+ PDF, Excel, JSON and XML sources locally",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := cbprworkspace.Import(cbprworkspace.Options{
			Source: args[0], Workspace: cbprWorkspace, Release: cbprRelease,
			ExternalCodes: cbprExternalCodes, GenerateSamples: cbprGenerateSamples,
			ExternalCodesAsOf:       cbprExternalCodesAsOf,
			RuleOverlay:             cbprRuleOverlay,
			EntitlementAcknowledged: cbprAcknowledgeEntitlement,
		})
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, manifest)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CBPR+ %s private workspace ready at %s\n", manifest.Release, cbprWorkspace)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Usage Guidelines : %d/%d\n",
			manifest.Coverage.PresentUsageGuidelines, manifest.Coverage.ExpectedUsageGuidelines)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Executable XML UGs: %d/%d\n",
			manifest.Coverage.ExecutableUsageGuidelines, manifest.Coverage.ExpectedUsageGuidelines)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Constraints      : %d\n", manifest.Coverage.Constraints)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Schemas / samples: %d / %d user + %d exported + %d workspace-generated\n",
			manifest.Coverage.Schemas, manifest.Coverage.Samples, manifest.Coverage.AskISOGeneratedSamples,
			manifest.Coverage.GeneratedSamples)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Guideline JSON   : %d\n", manifest.Coverage.JSONSchemas)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Suite cases      : %d\n", manifest.SuiteCases)
		if manifest.ExternalCodes != nil {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  External codes   : %d across %d sets (%s)\n",
				manifest.ExternalCodes.Codes, manifest.ExternalCodes.Sets, manifest.ExternalCodes.Publication)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Fingerprint      : %s\n", manifest.Fingerprint)
		if len(manifest.Coverage.MissingUsageGuidelines) > 0 {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  Missing:")
			for _, missing := range manifest.Coverage.MissingUsageGuidelines {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "    - %s\n", strings.Replace(missing, "|", " / ", 1))
			}
		}
		for _, warning := range manifest.Warnings {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Warning: %s\n", warning)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  Local only: AskISO did not copy or upload any source artefact.")
		return nil
	},
}

var cbprPackStatusCmd = &cobra.Command{
	Use:   "status <private-workspace>",
	Short: "Show release completeness and provenance",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := cbprworkspace.LoadManifest(args[0])
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, manifest)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CBPR+ %s workspace %s\n", manifest.Release, manifest.Fingerprint)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Usage Guidelines : %d/%d (%d missing)\n",
			manifest.Coverage.PresentUsageGuidelines, manifest.Coverage.ExpectedUsageGuidelines,
			len(manifest.Coverage.MissingUsageGuidelines))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Executable XML UGs: %d/%d (%d missing)\n",
			manifest.Coverage.ExecutableUsageGuidelines, manifest.Coverage.ExpectedUsageGuidelines,
			len(manifest.Coverage.MissingExecutableUsageGuidelines))
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Constraints      : %d\n", manifest.Coverage.Constraints)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Guideline JSON   : %d\n", manifest.Coverage.JSONSchemas)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Samples          : %d user + %d exported + %d workspace-generated\n",
			manifest.Coverage.Samples, manifest.Coverage.AskISOGeneratedSamples,
			manifest.Coverage.GeneratedSamples)
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Suite cases      : %d\n", manifest.SuiteCases)
		return nil
	},
}

var cbprPackGenerationsCmd = &cobra.Command{
	Use:   "generations <private-workspace>",
	Short: "List retained immutable workspace generations",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		generations, err := cbprworkspace.ListGenerations(args[0])
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, generations)
		}
		for _, generation := range generations {
			state := "retained"
			if generation.Active {
				state = "active"
			}
			validity := "valid"
			if !generation.Valid {
				validity = "INVALID: " + generation.Error
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s  %-8s %-8s %8d bytes  %s\n", generation.Fingerprint, state, generation.Release, generation.SizeBytes, validity)
		}
		return nil
	},
}

var cbprPackActivateCmd = &cobra.Command{
	Use:   "activate <private-workspace> <fingerprint>",
	Short: "Atomically activate or roll back to a retained generation",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := cbprworkspace.ActivateGeneration(args[0], args[1])
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, manifest)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Activated CBPR+ %s workspace generation %s\n", manifest.Release, manifest.Fingerprint)
		return nil
	},
}

var cbprPackPruneCmd = &cobra.Command{
	Use: "prune <private-workspace>", Short: "Prune old inactive generations (requires --confirm)", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		removed, err := cbprworkspace.PruneGenerations(args[0], cbprGenerationKeep, cbprGenerationConfirm)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pruned %d inactive CBPR+ generations\n", removed)
		return nil
	},
}

var cbprPackVerifyCmd = &cobra.Command{
	Use:   "verify <private-source-directory>",
	Short: "Run the pinned local conformance sample suite",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.Verify(args[0], cbprWorkspace)
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			if err := writeCBPRJSON(cmd, report); err != nil {
				return err
			}
		} else {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CBPR+ %s local suite: %d passed, %d failed, %d total\n",
				report.Release, report.Passed, report.Failed, report.Cases)
			for _, result := range report.Results {
				mark := "PASS"
				if !result.Passed {
					mark = "FAIL"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s %s (expected %s, got %s)\n",
					mark, result.ID, result.Expected, result.Actual)
			}
		}
		if report.Failed > 0 {
			return fmt.Errorf("%d local conformance case(s) failed", report.Failed)
		}
		return nil
	},
}

var cbprPackConformanceCmd = &cobra.Command{
	Use:   "conformance <private-source-directory>",
	Short: "Run the strict local CBPR+ release and evidence gate",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.CheckConformance(cbprworkspace.ConformanceOptions{
			Source: args[0], Workspace: cbprWorkspace, AsOf: cbprAsOf,
			Evidence: cbprEvidence, RequireUserSamples: cbprRequireUserSamples,
			RequireExternalEvidence: cbprRequireExternalEvidence,
		})
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			if err := writeCBPRJSON(cmd, report); err != nil {
				return err
			}
		} else {
			status := "NOT READY"
			if report.Ready {
				status = "READY"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CBPR+ %s conformance: %s (as of %s)\n", report.Release, status, report.AsOf)
			for _, check := range report.Checks {
				mark := "PASS"
				if !check.Passed {
					mark = "FAIL"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %s %-28s %s (required: %s)\n", mark, check.ID, check.Actual, check.Required)
			}
			for _, missing := range append(append([]string{}, report.MissingPositiveSamples...), report.MissingNegativeSamples...) {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Missing sample: %s\n", strings.Replace(missing, "|", " / ", 1))
			}
			for _, scenario := range report.MissingScenarios {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Missing scenario: %s\n", scenario)
			}
		}
		if !report.Ready {
			return fmt.Errorf("CBPR+ strict conformance gate failed")
		}
		return nil
	},
}

var cbprPackExportValidSamplesCmd = &cobra.Command{
	Use:   "export-valid-samples <private-source-directory>",
	Short: "Export AskISO-generated positive BAH and payload fixtures locally",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.ExportValidSamplesWithOptions(args[0], cbprWorkspace, cbprSampleOutput, cbprworkspace.SampleExportOptions{
			Profile: cbprTransportProfile, SenderDN: cbprSenderDN, ReceiverDN: cbprReceiverDN, Service: cbprTransportService,
		})
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CBPR+ %s: exported %d AskISO-generated valid samples to %s\n",
			report.Release, report.Generated, report.Output)
		for _, name := range report.Files {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", name)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  Provenance: synthetic fixtures generated from the operator's private schemas; not independent conformance evidence.")
		return nil
	},
}

var cbprPackExportInvalidSamplesCmd = &cobra.Command{
	Use:   "export-invalid-samples <private-source-directory>",
	Short: "Derive rejection fixtures from AskISO-generated positive samples",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.ExportNegativeSamples(args[0], cbprWorkspace, cbprSampleOutput)
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "CBPR+ %s: exported %d AskISO-generated invalid samples to %s\n", report.Release, report.Generated, report.Output)
		for scenario, count := range report.Scenarios {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  %-20s %d\n", scenario, count)
		}
		for _, warning := range report.Warnings {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Warning: %s\n", warning)
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  Provenance: synthetic rejection fixtures; not independent conformance evidence.")
		return nil
	},
}

var cbprPackChecklistCmd = &cobra.Command{
	Use:   "review-checklist <private-workspace>",
	Short: "Write a content-free independent sample review checklist",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.WriteReviewChecklist(args[0], cbprSampleOutput, cbprEvidenceTime)
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Wrote %d pending review items to %s\n", len(report.Items), cbprSampleOutput)
		return nil
	},
}

var cbprPackAuditSamplesCmd = &cobra.Command{
	Use:   "audit-samples <private-source-directory>",
	Short: "Audit sample provenance, duplicates, pairing and live-data patterns",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.AuditSamples(args[0], cbprWorkspace)
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Sample audit: %d independently eligible, %d synthetic, ready=%t\n", report.Eligible, report.Synthetic, report.ReadyForAttestation)
		for _, warning := range report.SensitiveDataWarnings {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Sensitive-data warning: %s\n", warning)
		}
		return nil
	},
}

var cbprPackAnonymiseSamplesCmd = &cobra.Command{
	Use:   "anonymise-samples <private-source-directory>",
	Short: "Create labelled local copies with common account identifiers scrubbed",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.AnonymiseSamples(args[0], cbprWorkspace, cbprSampleOutput)
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Anonymised %d samples (%d changed) into %s\n", report.Processed, report.Changed, report.Output)
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  Review required: pattern-based anonymisation cannot guarantee removal of every sensitive value.")
		return nil
	},
}

var cbprPackAttestSamplesCmd = &cobra.Command{
	Use:   "attest-samples <private-source-directory>",
	Short: "Record a human independent-review attestation using sample hashes",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.WriteSampleAttestation(args[0], cbprWorkspace, cbprSampleOutput, cbprReviewer, cbprProvider, cbprEvidenceTime, cbprAcknowledgeReview)
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Recorded %d reviewed sample hashes in %s\n", len(report.Cases), cbprSampleOutput)
		return nil
	},
}

var cbprPackRecordEvidenceCmd = &cobra.Command{
	Use:   "record-external-evidence <private-workspace>",
	Short: "Record a content-free verdict obtained from an external test service",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.WriteExternalEvidence(args[0], cbprSampleOutput, cbprProvider, cbprEvidenceTime, cbprEvidenceCases, cbprEvidencePassed, cbprAcknowledgeVerdict)
		if err != nil {
			return err
		}
		if cbprWorkspaceJSON {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Recorded external verdict from %s for %d cases in %s\n", report.Provider, report.Cases, cbprSampleOutput)
		return nil
	},
}

var cbprPackDiffCmd = &cobra.Command{
	Use:   "diff <from-source-directory> <to-source-directory>",
	Short: "Compare two entitled release exports using disclosure-safe metadata",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		report, err := cbprworkspace.CompareReleaseSources(args[0], args[1], cbprFromRelease, cbprToRelease)
		if err != nil {
			return err
		}
		if cbprSampleOutput != "" {
			if err := cbprworkspace.WriteReleaseDiff(cbprSampleOutput, report); err != nil {
				return err
			}
		}
		if cbprWorkspaceJSON || cbprSampleOutput == "" {
			return writeCBPRJSON(cmd, report)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s to %s: %d added, %d removed, %d changed, %d unchanged\n",
			report.FromRelease, report.ToRelease, len(report.Added), len(report.Removed), len(report.Changed), report.Unchanged)
		return nil
	},
}

func writeCBPRJSON(cmd *cobra.Command, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}

func init() {
	cbprPackImportCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private output directory (required)")
	_ = cbprPackImportCmd.MarkFlagRequired("workspace")
	cbprPackImportCmd.Flags().StringVar(&cbprRelease, "release", "SR2025", "CBPR+ release")
	cbprPackImportCmd.Flags().StringVar(&cbprExternalCodes, "external-codes", "", "Local Registration Authority XLSX or JSON publication")
	cbprPackImportCmd.Flags().StringVar(&cbprExternalCodesAsOf, "external-codes-as-of", "", "Select newest publication in a directory effective by YYYY-MM-DD")
	cbprPackImportCmd.Flags().StringVar(&cbprRuleOverlay, "rule-overlay", "", "Operator-authored conditional rule overlay JSON")
	cbprPackImportCmd.Flags().BoolVar(&cbprGenerateSamples, "generate-samples", false, "Generate private positive/negative XML validator fixtures from local XSDs")
	cbprPackImportCmd.Flags().BoolVar(&cbprAcknowledgeEntitlement, "acknowledge-entitlement", false, "Record that the operator is authorised to process the selected private artefacts")
	cbprPackImportCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the manifest as JSON")

	cbprPackStatusCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the manifest as JSON")
	cbprPackGenerationsCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the generation inventory as JSON")
	cbprPackActivateCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the activated manifest as JSON")
	cbprPackPruneCmd.Flags().IntVar(&cbprGenerationKeep, "keep", 2, "Number of total valid generations to retain (including active)")
	cbprPackPruneCmd.Flags().BoolVar(&cbprGenerationConfirm, "confirm", false, "Confirm destructive pruning")

	cbprPackVerifyCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private workspace directory (required)")
	_ = cbprPackVerifyCmd.MarkFlagRequired("workspace")
	cbprPackVerifyCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the report as JSON")

	cbprPackConformanceCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private workspace directory (required)")
	_ = cbprPackConformanceCmd.MarkFlagRequired("workspace")
	cbprPackConformanceCmd.Flags().StringVar(&cbprAsOf, "as-of", "", "Validation date in YYYY-MM-DD (default: today)")
	cbprPackConformanceCmd.Flags().StringVar(&cbprEvidence, "evidence", "", "Content-free independent evidence JSON")
	cbprPackConformanceCmd.Flags().BoolVar(&cbprRequireUserSamples, "require-user-samples", true, "Require positive and negative user samples for all 31 Usage Guidelines")
	cbprPackConformanceCmd.Flags().BoolVar(&cbprRequireExternalEvidence, "require-external-evidence", false, "Require matching independent test evidence")
	cbprPackConformanceCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the conformance report as JSON")

	cbprPackExportValidSamplesCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private workspace directory (required)")
	_ = cbprPackExportValidSamplesCmd.MarkFlagRequired("workspace")
	cbprPackExportValidSamplesCmd.Flags().StringVar(&cbprSampleOutput, "output", "", "Directory inside the private source tree (required)")
	_ = cbprPackExportValidSamplesCmd.MarkFlagRequired("output")
	cbprPackExportValidSamplesCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the export report as JSON")
	cbprPackExportValidSamplesCmd.Flags().StringVar(&cbprTransportProfile, "transport", cbprworkspace.TransportEnvelope, "Wrapper: envelope, request-payload, or swift-datapdu")
	cbprPackExportValidSamplesCmd.Flags().StringVar(&cbprSenderDN, "sender-dn", "", "DataPDU sender distinguished name")
	cbprPackExportValidSamplesCmd.Flags().StringVar(&cbprReceiverDN, "receiver-dn", "", "DataPDU receiver distinguished name")
	cbprPackExportValidSamplesCmd.Flags().StringVar(&cbprTransportService, "transport-service", "", "DataPDU network service (default: swift.finplus)")

	cbprPackExportInvalidSamplesCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private workspace directory (required)")
	_ = cbprPackExportInvalidSamplesCmd.MarkFlagRequired("workspace")
	cbprPackExportInvalidSamplesCmd.Flags().StringVar(&cbprSampleOutput, "output", "", "Directory inside the private source tree (required)")
	_ = cbprPackExportInvalidSamplesCmd.MarkFlagRequired("output")
	cbprPackExportInvalidSamplesCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the export report as JSON")

	cbprPackChecklistCmd.Flags().StringVar(&cbprSampleOutput, "output", "", "Checklist JSON file (required)")
	_ = cbprPackChecklistCmd.MarkFlagRequired("output")
	cbprPackChecklistCmd.Flags().StringVar(&cbprEvidenceTime, "created-at", "", "RFC3339 creation time (default: now)")
	cbprPackChecklistCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the checklist as JSON")

	cbprPackAuditSamplesCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private workspace directory (required)")
	_ = cbprPackAuditSamplesCmd.MarkFlagRequired("workspace")
	cbprPackAuditSamplesCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the audit as JSON")

	cbprPackAnonymiseSamplesCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private workspace directory (required)")
	_ = cbprPackAnonymiseSamplesCmd.MarkFlagRequired("workspace")
	cbprPackAnonymiseSamplesCmd.Flags().StringVar(&cbprSampleOutput, "output", "", "Directory inside the private source tree (required)")
	_ = cbprPackAnonymiseSamplesCmd.MarkFlagRequired("output")
	cbprPackAnonymiseSamplesCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the anonymisation report as JSON")

	cbprPackAttestSamplesCmd.Flags().StringVar(&cbprWorkspace, "workspace", "", "Private workspace directory (required)")
	_ = cbprPackAttestSamplesCmd.MarkFlagRequired("workspace")
	cbprPackAttestSamplesCmd.Flags().StringVar(&cbprSampleOutput, "output", "", "Attestation JSON file (required)")
	_ = cbprPackAttestSamplesCmd.MarkFlagRequired("output")
	cbprPackAttestSamplesCmd.Flags().StringVar(&cbprReviewer, "reviewer", "", "Human reviewer identity (required)")
	cbprPackAttestSamplesCmd.Flags().StringVar(&cbprProvider, "provider", "", "Independent sample provider (required)")
	cbprPackAttestSamplesCmd.Flags().StringVar(&cbprEvidenceTime, "reviewed-at", "", "RFC3339 review time (default: now)")
	cbprPackAttestSamplesCmd.Flags().BoolVar(&cbprAcknowledgeReview, "acknowledge-independent-review", false, "Assert that the named human independently reviewed the samples")
	cbprPackAttestSamplesCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the attestation as JSON")

	cbprPackRecordEvidenceCmd.Flags().StringVar(&cbprSampleOutput, "output", "", "Evidence JSON file (required)")
	_ = cbprPackRecordEvidenceCmd.MarkFlagRequired("output")
	cbprPackRecordEvidenceCmd.Flags().StringVar(&cbprProvider, "provider", "", "External test provider (required)")
	cbprPackRecordEvidenceCmd.Flags().StringVar(&cbprEvidenceTime, "tested-at", "", "RFC3339 test time (default: now)")
	cbprPackRecordEvidenceCmd.Flags().IntVar(&cbprEvidenceCases, "cases", 0, "Number of externally tested cases (required)")
	cbprPackRecordEvidenceCmd.Flags().BoolVar(&cbprEvidencePassed, "passed", false, "Record that every submitted case passed")
	cbprPackRecordEvidenceCmd.Flags().BoolVar(&cbprAcknowledgeVerdict, "acknowledge-external-verdict", false, "Assert that this verdict was obtained externally")
	cbprPackRecordEvidenceCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the evidence as JSON")

	cbprPackDiffCmd.Flags().StringVar(&cbprFromRelease, "from-release", "SR2025", "Source release label")
	cbprPackDiffCmd.Flags().StringVar(&cbprToRelease, "to-release", "SR2026", "Target release label")
	cbprPackDiffCmd.Flags().StringVar(&cbprSampleOutput, "output", "", "Write content-free release delta JSON")
	cbprPackDiffCmd.Flags().BoolVar(&cbprWorkspaceJSON, "json", false, "Emit the release delta as JSON")

	cbprPackCmd.AddCommand(cbprPackImportCmd, cbprPackStatusCmd, cbprPackGenerationsCmd, cbprPackActivateCmd, cbprPackPruneCmd,
		cbprPackVerifyCmd, cbprPackConformanceCmd,
		cbprPackExportValidSamplesCmd, cbprPackExportInvalidSamplesCmd, cbprPackChecklistCmd,
		cbprPackAuditSamplesCmd, cbprPackAnonymiseSamplesCmd, cbprPackAttestSamplesCmd,
		cbprPackRecordEvidenceCmd, cbprPackDiffCmd)
}
