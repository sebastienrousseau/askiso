<!-- SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com> -->
<!-- SPDX-License-Identifier: Apache-2.0 OR MIT -->

# Changelog

What changed in each AskISO release. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and each release
is 0.0.1 above the one before it. Each release's
page on [GitHub](https://github.com/sebastienrousseau/askiso/releases) lists
its pull requests and the SHA-256 of every download.

## [Unreleased]

### Added

- Release pages in the portfolio layout: `scripts/releasepage` writes each
  release's page from `docs/releases/<tag>.md`, GitHub's pull-request notes
  and the SHA-256 of every asset, and the release is titled
  `askiso <version>`.
- `docs/ARCHITECTURE.md`, this changelog, a code of conduct, and issue and
  pull request templates.
- A Docs Lint workflow: the README is checked against the portfolio
  template and the repository's Markdown against markdownlint.
- A test that fails on any undocumented identifier in `pkg/iso20022`.
- Dependabot version updates for the Go module and the workflow actions.

### Changed

- The README follows the portfolio template, with a rendered demo.
- The website builds with ssg 0.0.71.
- `SECURITY.md` names the latest 0.0.x release as the supported version.

### Fixed

- The README's release verification command used `.sig` and `.pem` files
  that releases no longer ship; it now verifies the Sigstore bundle.

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

[Unreleased]: https://github.com/sebastienrousseau/askiso/compare/v0.0.5...HEAD
[0.0.5]: https://github.com/sebastienrousseau/askiso/compare/v0.0.4...v0.0.5
[0.0.4]: https://github.com/sebastienrousseau/askiso/compare/v0.0.3...v0.0.4
[0.0.3]: https://github.com/sebastienrousseau/askiso/compare/v0.0.2...v0.0.3
[0.0.2]: https://github.com/sebastienrousseau/askiso/compare/v0.0.1...v0.0.2
[0.0.1]: https://github.com/sebastienrousseau/askiso/releases/tag/v0.0.1
