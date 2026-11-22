<!-- SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com> -->
<!-- SPDX-License-Identifier: Apache-2.0 OR MIT -->

# Changelog

What changed in each AskISO release. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and each release
is 0.0.1 above the one before it. Each release's
page on [GitHub](https://github.com/sebastienrousseau/askiso/releases) lists
its pull requests and the SHA-256 of every download.

## [Unreleased]

## [0.0.6] - 2026-11-22

### Added

- `pkg/iso20022/ffi` (`askiso-abi/v1` JSON envelope dispatcher) and
  `cmd/askiso-cabi` (`-buildmode=c-shared` shared library entrypoint), which
  any language with a C FFI can load.
- `internal/server` and `cmd/askiso-server` stateless HTTP validation/linting
  daemon, shipped in `.goreleaser.yaml` archives.
- `askiso-server -catalog` and `ffi.DispatchPinned`: the HTTP daemon reads
  only the operator's catalogue and ignores `catalog_path` in request
  bodies, so a client cannot make it read a directory of its choosing.
  Its responses give file paths relative to the catalogue root and its
  errors leave out host paths.
- `internal/remediate` deterministic offline `<AdrLine>` remediator and
  `askiso audit` (`--format text|json|html`) SR2027 readiness scorecard.
- `SetLogger(*slog.Logger)` on `internal/lsp` and `internal/mcp`, and
  `(*ExternalSets).IsStale` in `internal/codes`.
- `docs/ABI.md`, `docs/BENCHMARKS.md`, `docs/COMPARISON.md`,
  `docs/COMPLIANCE.md`, and `docs/SECURITY.md`.
- Release pages in the portfolio layout: `scripts/releasepage` writes each
  release's page from `docs/releases/<tag>.md`, GitHub's pull-request notes
  and the SHA-256 of every asset, and the release is titled
  `askiso <version>`.
- `docs/ARCHITECTURE.md`, this changelog, a code of conduct, and issue and
  pull request templates.
- An OpenSSF Scorecard workflow, which publishes the score the README badge
  shows.
- A Docs Lint workflow: the README is checked against the portfolio
  template and the repository's Markdown against markdownlint.
- A test that fails on any undocumented identifier in `pkg/iso20022`.
- Dependabot version updates for the Go module and the workflow actions.

### Changed

- `internal/validator/stream.go` reuses 64 KiB `bufio.Reader` buffers via
  `sync.Pool`; `internal/cbprworkspace` shards locks per workspace path;
  `internal/lsp` bounds and `os.Stat`-validates its XSD cache;
  `internal/ai.NormalizeQuery` avoids double regex matching.
- CI's test jobs take their Go version from go.mod and are named `Test (<os>, go go.mod)`, so the checks that branch protection requires keep their names across toolchain bumps.
- The README follows the portfolio template, with a rendered demo.
- The website builds with ssg 0.0.76.
- `SECURITY.md` names the latest 0.0.x release as the supported version.

### Fixed

- Schema-driven generation wrote `head.001.001.01`'s `CreDt` with a `+00:00`
  offset, which its `ISONormalisedDateTime` pattern (`.*Z`) rejects. A
  generated `dateTime` now takes the form its own type's pattern accepts, and
  all 4,746 catalogue schemas generate valid messages again.
- `make conformance` reported success when any suite but the last failed,
  which is how the `head.001.001.01` failure went unnoticed. It now stops at
  the first failing suite.
- The README, `docs/ABI.md`, `docs/ARCHITECTURE.md`, `docs/COMPARISON.md` and
  `docs/COMPLIANCE.md` no longer name language packages that are not
  published, claim the WebAssembly build uses the FFI layer, compare AskISO
  with other tools without a measurement, or call the SPDX SBOMs CycloneDX.
  The README covers `askiso-server`, the C library, `ask`, `audit` and
  `list`, and its smoke test count and Go floor advisory are corrected.
- The README's release verification command used `.sig` and `.pem` files
  that releases no longer ship; it now verifies the Sigstore bundle.
- Every action in the Website workflow is pinned by commit, and the browser
  checks install puppeteer-core and axe-core with `npm ci` from a committed
  lockfile, which clears the three Pinned-Dependencies findings Scorecard
  raised on its first run.

### Security

- Go 1.26.11 floor and workflow pins (resolving `GO-2026-6859` in `net/http`)
  and `golang.org/x/sys` `0.50.0`.
- puppeteer-core 25.16.0 for the browser checks. The 23.x line pulled in
  basic-ftp, extract-zip and zod versions with published high-severity
  advisories.

## [0.0.5] - 2026-11-01

### Security

- Go 1.26.10 and golang.org/x/net v0.62.0, which resolve GO-2026-6809,
  GO-2026-6860 and GO-2026-6866.

### Changed

- Files AskISO writes (an imported catalogue, `translate --out` output and
  generated pages) are created 0600, and the directories it creates are no
  longer readable by other users.
- The website builds with ssg 0.0.70.

### Fixed

- The 46 gosec findings code scanning reported.
- The search button's accessible name now includes its keyboard shortcut.

## [0.0.4] - 2026-10-12

### Security

- Go 1.26.9, which fixes nine standard library vulnerabilities in net/http
  and html/template, and golang.org/x/net v0.60.0.

## [0.0.3] - 2026-09-05

### Added

- The private CBPR+ workspace: build a versioned workspace from an
  institution's own SR2025 Usage Guidelines export, check messages against
  it, and keep the evidence, with no Swift content leaving the machine.
- Social descriptions, breadcrumbs and software markup on every page of the
  website.
- The GitHub Action is listed on the Marketplace.

### Fixed

- Facts on the website checked against the code and Swift's notices.
- The build on Linux, macOS and Windows.

## [0.0.2] - 2026-08-28

### Added

- The website's navigation, four hub pages, a contact page, the vision and
  knowledge centre pages, a 404 page, and the analysis of Swift deferring the
  structured address migration.
- A photographic masthead on every page.
- An accessibility gate that fails the deployment on a WCAG 2.2 AA
  regression.

### Fixed

- The tool no longer asserts a cutover date for the structured address
  requirement, which no longer has one.

## [0.0.1] - 2026-08-26

### Added

- `askiso` (CLI and terminal UI), `askiso-mcp` (Model Context Protocol
  server) and `askiso-lsp` (language server) for ISO 20022 messages.
- The askiso.io website, with a page for every ISO 20022 message
  definition, the playground and the workspace.
- Release binaries for Linux, macOS and Windows with an SBOM per archive and
  a Sigstore-signed checksum file.

[Unreleased]: https://github.com/sebastienrousseau/askiso/compare/v0.0.6...HEAD
[0.0.6]: https://github.com/sebastienrousseau/askiso/compare/v0.0.5...v0.0.6
[0.0.5]: https://github.com/sebastienrousseau/askiso/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/sebastienrousseau/askiso/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/sebastienrousseau/askiso/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/sebastienrousseau/askiso/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/sebastienrousseau/askiso/releases/tag/v0.0.1
