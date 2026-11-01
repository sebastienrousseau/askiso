<!-- SPDX-License-Identifier: Apache-2.0 OR MIT -->

# Contributing to AskISO

Thank you for your interest in contributing to AskISO! This document outlines our development workflows, coding standards, and governance rules.

---

## Semantic Versioning Lifecycle Rule

All contributors and maintainers must strictly adhere to the project's semantic versioning lifecycle rule:

* **Initial Version**: The project starts at `v0.0.1`.
* **Increment Policy**: Every new release/iteration increments by `0.0.1` (e.g., `v0.0.1` → `v0.0.2` → `v0.0.3` ... → `v0.0.999` → `v0.1.0`).
* **Milestone Maturity**: To achieve `v0.1.0`, the product must have matured incrementally through `v0.0.999`.
* **Scope**: Mandatory standard across all project releases.

---

## Development Workflow

### Prerequisites

* Go 1.26.10 or higher - the `go` directive in `go.mod` requires it, because that
  release carries the standard library security fixes AskISO builds against
* Make

### Building & Testing

```bash
# Build binary
make build

# Run full test suite with race detector
go test -v -race ./...

# Run the automated Ecosystem Quality Scorecard (Must score >= 0.90)
./scripts/ecosystem-scorecard.sh
```

---

## Code Quality Standards

1. **Formatting**: All Go files must be cleanly formatted using `gofmt -s -w .`.
2. **Static Analysis**: Code must pass `go vet ./...` with zero warnings.
3. **SPDX Headers**: Every Go and script file must begin with:

   ```go
   // SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
   // SPDX-License-Identifier: Apache-2.0 OR MIT
   ```

4. **Zero Unchecked Errors**: Always handle or explicitly ignore errors with `_ =`.

---

## Pull Request Checklist

* [ ] Code is formatted with `gofmt -s -w .`
* [ ] `go test -v -race ./...` passes cleanly
* [ ] `./scripts/ecosystem-scorecard.sh` passes with 100% score (1.00 / 1.00)
* [ ] Added unit tests for any new features or bug fixes
* [ ] Updated `README.md` command documentation if CLI surface changed
