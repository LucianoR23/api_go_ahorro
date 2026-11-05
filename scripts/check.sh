#!/usr/bin/env bash
# Local mirror of CI (.github/workflows/ci.yml). Run from Git Bash before pushing:
#   ./scripts/check.sh
# Tool versions must match the workflow. Needs: Go (toolchain from go.mod), sqlc 1.30.0.
# Integration tests need Docker running (testcontainers) or TEST_DATABASE_URL
# (superuser URL to a Postgres 18 server); without either they are skipped.
set -euo pipefail
cd "$(dirname "$0")/.."

GOLANGCI_LINT_VERSION=v2.15.0
GOVULNCHECK_VERSION=v1.9.0
SQLC_VERSION=1.30.0
export TZ=America/Argentina/Buenos_Aires

step() { printf '\n== %s\n' "$1"; }

step "go mod verify";  go mod verify
step "build";          go build ./...
step "vet";            go vet ./...
step "golangci-lint";  go run "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}" run ./...
step "sqlc diff"
if have=$(sqlc version 2>/dev/null); then
  [ "$have" = "v${SQLC_VERSION}" ] || echo "warning: local sqlc is $have, CI uses v${SQLC_VERSION}"
  sqlc diff
else
  echo "sqlc not installed: skipped (CI runs it)"
fi
step "test";           TESTDB_REQUIRED=1 go test -count=1 -coverprofile=coverage.out ./...
step "coverage";       go tool cover -func=coverage.out | tail -1
step "govulncheck";    go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" ./...
step "gitleaks";       go run github.com/zricethezav/gitleaks/v8@v8.30.1 git . --redact --no-banner

printf '\nAll checks passed.\n'
