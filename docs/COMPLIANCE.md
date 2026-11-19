# Compliance, Provenance & SR2027 Readiness

## Supply-Chain & Binary Verification

Release artifacts are signed with Sigstore Cosign, accompanied by CycloneDX/Syft SBOMs, and attested with SLSA build provenance (`actions/attest-build-provenance`):

```bash
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp 'https://github.com/sebastienrousseau/askiso/.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## SWIFT SR2027 & CBPR+ Alignment

* **Dated Profiles:** `cbpr-2026` and `RuleOverlay` distinguish advisory warnings from hard errors so CI pipelines never break prematurely.
* **Executive Readiness (`askiso audit`):** Scans XML corpora and reports structured, hybrid, and unstructured postal-address distribution with a risk rating (`low`, `medium`, `high`).
* **Offline Address Remediation (`internal/remediate`):** Deterministic parsing of `<AdrLine>` into hybrid or structured `<PstlAdr>` elements with confidence scoring and zero external network calls.
