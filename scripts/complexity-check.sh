#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Sebastien Rousseau <sebastian.rousseau@gmail.com>
# SPDX-License-Identifier: Apache-2.0 OR MIT
#
# Holds every Go function to cyclomatic complexity <= 10 and cognitive
# complexity <= 15. Functions already over a ceiling are recorded in
# scripts/complexity-baseline.txt; they may only improve. The check fails
# when a function not in the baseline crosses a ceiling, or a baselined one
# gets worse.
#
#   scripts/complexity-check.sh           check against the baseline
#   scripts/complexity-check.sh --update  record improvements (refuses while
#                                         anything has regressed)
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

GOCYCLO=github.com/fzipp/gocyclo/cmd/gocyclo@v0.6.0
GOCOGNIT=github.com/uudashr/gocognit/cmd/gocognit@v1.3.0
CYCLO_MAX=10
COGNIT_MAX=15
BASELINE=scripts/complexity-baseline.txt
IGNORE='(^|/)(node_modules|\.claude|testdata)/'

# Each line: <metric> <score> <package> <function> <file>. The line number is
# dropped so that unrelated edits above a function do not look like a change.
measure() {
  {
    go run "$GOCYCLO" -over "$CYCLO_MAX" -ignore "$IGNORE" . |
      sed 's/^/cyclomatic /'
    go run "$GOCOGNIT" -over "$COGNIT_MAX" -ignore "$IGNORE" . |
      sed 's/^/cognitive /'
  } | sed -E 's/:[0-9]+:[0-9]+$//' | LC_ALL=C sort -k1,1 -k5,5 -k3,4
}

current="$(mktemp)"
trap 'rm -f "$current"' EXIT
# Both tools exit 1 when they report anything, which is the normal case here.
measure >"$current" || true

failed=0
while read -r metric score pkg fn file; do
  base="$(awk -v m="$metric" -v p="$pkg" -v f="$fn" -v l="$file" \
    '$1==m && $3==p && $4==f && $5==l {print $2}' "$BASELINE")"
  if [ -z "$base" ]; then
    echo "new:      $metric $score $fn ($file)"
    failed=1
  elif [ "$score" -gt "$base" ]; then
    echo "worse:    $metric $base -> $score $fn ($file)"
    failed=1
  fi
done <"$current"

if [ "$failed" -ne 0 ]; then
  echo "complexity-check: over the ceiling (cyclomatic $CYCLO_MAX, cognitive $COGNIT_MAX); simplify the function rather than raising the baseline" >&2
  exit 1
fi

if [ "${1:-}" = "--update" ]; then
  cp "$current" "$BASELINE"
  echo "complexity-check: baseline rewritten ($(wc -l <"$BASELINE" | tr -d ' ') entries)"
  exit 0
fi

if ! cmp -s "$current" "$BASELINE"; then
  echo "complexity-check: some baselined functions improved or are gone; run scripts/complexity-check.sh --update to record it" >&2
  exit 1
fi

echo "complexity-check: ok ($(wc -l <"$BASELINE" | tr -d ' ') baselined offenders, none worse)"
