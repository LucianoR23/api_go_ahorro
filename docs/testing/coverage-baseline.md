# Coverage baseline (API)

Starting point of the coverage ratchet (plan §0 "Coverage gates — ratchet",
item 0.21i). Measured on 2026-11-05 on `next` after the 0.21 test backfill.

## How it is measured

`scripts/coverage.sh` (run by `scripts/check.sh` and CI):

- Statement coverage of every package under `internal/`, **across packages**
  (`-coverpkg`): code exercised by another package's tests counts. Balances
  and split rules, for example, are tested through the settlements tests.
- Excluded: generated sqlc code (`internal/db/sqlc`), test helpers
  (`internal/testutil`), `cmd/` (process wiring; the wiring itself lives in
  `internal/app` and is covered).
- The gate fails when the total drops below `.coverage-floor`, or when the
  floor is lowered compared with `origin/next`.

## Numbers

| | Total |
|---|---|
| Before 0.21 (per-package, 0.11 unit tests only) | 3.5% |
| After 0.21 (cross-package, this method) | **79.0%** |
| Floor set in `.coverage-floor` | **78.0%** (one point of headroom for run-to-run variance) |

Plan Phase 0 exit criteria: ≥ 70% overall (met) and ≥ 80% on
`internal/**/service.go` and pure packages (met except `push/service.go`).

### `service.go` files

| File | Coverage |
|---|---|
| auth | 93.2% |
| balances | 91.2% |
| categories | 83.7% |
| creditperiods | 95.1% |
| expenses | 91.6% |
| fxrates | 91.8% |
| goals | 89.0% |
| households | 96.4% |
| incomes | 86.9% |
| insights | 89.7% |
| paymethods | 94.0% |
| push | **79.5%** |
| recurringexpenses | 90.2% |
| reports | 89.9% |
| settlements | 91.1% |
| splitrules | 89.6% |
| support | 93.8% |

### Known gaps

- `internal/auth/google` (0%): the real Google ID-token verifier; tests use
  a fake (`apitest.FakeGoogle`). Covered when the verifier gets a fake JWKS
  server, if ever needed.
- `internal/email` (0%): the Resend HTTP client; tests use a fake mailer and
  never call Resend. `reports.ResendSender` is a duplicate and is deleted in
  0.15b.
- `internal/config` (63%): the `.env` loader paths.
- Background workers' schedulers (`Start` loops) are not run in tests; their
  units of work are, through `App.Jobs`.

## Known bugs pinned

Audit bugs and bugs found during 0.21 are `testutil.KnownBug` tests: they pass
while the bug reproduces and fail once it is fixed, so the fixing item turns
them into regular tests. List them with
`go test ./... -run . -v 2>&1 | grep "known bug"`; each line names its plan
item (0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9).

## Raising the floor

When a PR raises coverage, raise `.coverage-floor` in the same PR (keep about
one point of headroom). Never lower it.
