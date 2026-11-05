#!/usr/bin/env bash
# Runs the tests with cross-package coverage and enforces the coverage floor
# (plan §0 "Coverage gates — ratchet"). Used by scripts/check.sh and CI.
#
#   scripts/coverage.sh [extra go test flags, e.g. -race]
#
# - Coverage counts every package under internal/ except generated sqlc code
#   and test helpers, measured across packages (-coverpkg): code exercised from
#   another package's tests counts (e.g. balances via the settlements tests).
# - Fails when the total drops below .coverage-floor.
# - Fails when .coverage-floor is lower than on origin/next: the floor only
#   goes up. Raise it in the same PR that raises coverage.
set -euo pipefail
cd "$(dirname "$0")/.."

pkgs=$(go list ./internal/... | grep -v -e /internal/db/sqlc -e /internal/testutil | paste -sd, -)
go test -count=1 "$@" -coverpkg="$pkgs" -coverprofile=coverage.out ./...

total=$(go tool cover -func=coverage.out | awk '/^total:/ {sub("%", "", $NF); print $NF}')
floor=$(tr -d '[:space:]' < .coverage-floor)

echo
echo "Coverage by package (statements, cross-package):"
go tool cover -func=coverage.out | awk '
  /^total:/ { next }
  {
    file = $1; sub(/:[0-9.]+:?$/, "", file); sub(/\/[^\/]+\.go$/, "", file)
    sub(/^github.com\/LucianoR23\/api_go_ahorra\//, "", file)
    pct = $NF; sub("%", "", pct); sum[file] += pct; n[file]++
  }
  END { for (p in sum) printf "  %-32s %6.1f%%\n", p, sum[p] / n[p] }' | sort
echo "(per-package figures are averages over functions; the gate uses the statement total)"

echo
echo "service.go files (plan Phase 0 exit: >= 80%):"
go tool cover -func=coverage.out | awk '
  /\/service\.go:/ {
    file = $1; sub(/:[0-9.]+:?$/, "", file); sub(/^github.com\/LucianoR23\/api_go_ahorra\//, "", file)
    pct = $NF; sub("%", "", pct); sum[file] += pct; n[file]++
  }
  END { for (f in sum) printf "  %-44s %6.1f%%\n", f, sum[f] / n[f] }' | sort

echo
echo "Total: ${total}%  (floor ${floor}%)"
if awk -v t="$total" -v f="$floor" 'BEGIN { exit !(t + 0 < f + 0) }'; then
  echo "FAIL: coverage ${total}% is below the floor ${floor}%." >&2
  exit 1
fi

if git rev-parse --verify -q origin/next >/dev/null; then
  base=$(git show origin/next:.coverage-floor 2>/dev/null | tr -d '[:space:]' || true)
  if [ -n "$base" ] && awk -v f="$floor" -v b="$base" 'BEGIN { exit !(f + 0 < b + 0) }'; then
    echo "FAIL: .coverage-floor (${floor}) is lower than on origin/next (${base}); the floor only goes up." >&2
    exit 1
  fi
fi
