# AskISO Guidelines

## Versioning

Semantic Versioning 2.0.0 (<https://semver.org>).

- `MAJOR` — incompatible API or CLI changes
- `MINOR` — backwards-compatible functionality
- `PATCH` — backwards-compatible fixes

Pre-1.0, the CLI surface and `pkg/iso20022` API may change in any minor release.

## Non-negotiables

- AskISO does not redistribute ISO 20022 specifications. Users supply their own
  catalogue; AskISO embeds only factual metadata (identifiers, versions, the
  RA's own download URLs).
- A missing or unreadable catalogue is a hard error with an actionable message.
  Never report an empty catalogue as healthy.
- Generated output must validate against its schema and pass AskISO's own
  linter. `make conformance` enforces this and must be run before tagging.
