#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
# SPDX-License-Identifier: Apache-2.0 OR MIT
#
# Run every native fuzz target for a bounded time, and fail only on a finding.
#
# `go test -fuzz` sometimes exits non-zero with nothing but "context deadline
# exceeded" when -fuzztime expires while a worker is mid-input or minimising
# (golang/go#72088). That is the timer going off, not a defect, but under
# `set -e` it ended the campaign and failed the job. A real finding always
# writes the failing input to testdata/fuzz/<Target>/ in the package, so that
# is what decides: new corpus file, fail; deadline only, carry on.
#
#   scripts/fuzz-smoke.sh 15s 4      # fuzztime, parallelism
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"

fuzztime="${1:-15s}"
parallel="${2:-4}"

targets=(
  "./internal/xsd FuzzParse"
  "./internal/xsd FuzzStructuredSchema"
  "./internal/validator FuzzValidate"
  "./internal/validator FuzzStructuredValidation"
  "./internal/swift FuzzParse"
  "./internal/swift FuzzStructuredMT103"
  "./internal/converter FuzzRoundTrip"
  "./internal/converter FuzzStructuredRoundTrip"
  "./internal/lsp FuzzConnFrameRoundTrip"
  "./internal/rules FuzzCBPRPackMergeAlgebra"
  "./internal/codes FuzzExternalJSONSemanticShapes"
  "./internal/cbprworkspace FuzzWorkspaceMetadataRoundTrip"
)

failed=0
for entry in "${targets[@]}"; do
  pkg="${entry% *}"
  target="${entry#* }"
  corpus="$pkg/testdata/fuzz/$target"
  before="$(find "$corpus" -type f 2>/dev/null | sort || true)"

  echo "== $pkg $target ($fuzztime)"
  log="$(mktemp)"
  if go test "$pkg" -run '^$' -fuzz "^${target}\$" -fuzztime "$fuzztime" -parallel "$parallel" 2>&1 | tee "$log"; then
    rm -f "$log"
    continue
  fi

  after="$(find "$corpus" -type f 2>/dev/null | sort || true)"
  if [ "$before" != "$after" ]; then
    echo "::error::$target found a failing input in $corpus"
    failed=1
  elif grep -q 'context deadline exceeded' "$log" && ! grep -qE '^\s+[a-z_]+\.go:[0-9]+:|panic:' "$log"; then
    echo "note: $target hit the fuzztime deadline mid-run (golang/go#72088); no finding, carrying on"
  else
    echo "::error::$target failed for a reason other than the deadline"
    failed=1
  fi
  rm -f "$log"
done

exit "$failed"
