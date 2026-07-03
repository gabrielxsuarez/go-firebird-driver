# Release Checklist

This checklist is intended for the production soak period before a future `v1.0.0`.

## Per Change

- `go test ./internal/...`
- `go test -run "TestParseDSN|TestHandle|TestWithCancel|TestBuildConnectDPB" .`
- Integration test against Firebird 3 when `FB3_TEST_DSN` is available:
  - `go test -count=1 ./...`

Shortcut:

```powershell
.\scripts\validate.ps1 -Mode quick
```

## Before Wider Production Rollout

- `.\scripts\validate.ps1 -Mode race`
- `.\scripts\validate.ps1 -Mode fuzz -FuzzSeconds 30`
- `.\scripts\validate.ps1 -Mode bench -BenchCount 5`
- Verify Firebird 4/5 containers are reachable and run:
  - `go test -run "FB4|FB5|Decfloat|Int128|TimestampTZ|TimeTZ|ColumnMetadata" -v .`

## Before Release Candidate

- Run the quick, race, fuzz, and bench modes on a quiet machine.
- Compare benchmark output with `BENCHMARK_BASELINE.md`.
- Confirm there are no open production issues involving:
  - data corruption;
  - silent charset conversion;
  - protocol desynchronization;
  - connection leak;
  - goroutine leak;
  - data race;
  - panic in normal `database/sql` use.
- Re-read `COMPATIBILITY.md` and update any contract that changed.
- Update README examples and DSN parameter list.

## Manual/Long Tests

Keep these out of the normal test suite:

- Soak harness: `cd scripts/soak && go run . -duration 30m -out soak_report.txt`
  (sustained mixed workload over a pool; samples goroutines/heap every 10s and writes a
  report; tolerates and logs server outages, so the server may be killed/restarted
  mid-run to exercise recovery). Last run: 2026-07-03, results in
  `review/05-recommendations.md`.
- Kill/restart Firebird while a fetch is active (done 2026-07-03, phase 2: fetch fails
  <2s, pool recovers, no goroutine leak).
- Kill/restart Firebird while reading or writing a large BLOB.
- Run application workloads from real projects for weeks/months and log any mismatch
  with `COMPATIBILITY.md` — **this is the release-candidate period for 1.0**.

## Decisions Closed in the Pre-1.0 Review (2026-07-03)

- Module path: `github.com/gabrielxsuarez/go-firebird-driver` is final.
- Go minimum: 1.25 (required by current `golang.org/x/crypto`; do not lower by pinning
  an old crypto dependency).
- BLOB streaming, numerics-as-string (+`driver.Valuer`), timezone wall-clock+offset,
  and no extended API in 1.0: resolved in `COMPATIBILITY.md` ("Resolved Contract
  Decisions").
- Automation: PowerShell only (`scripts/validate.ps1`); no Makefile for 1.0. CI is
  deliberately out of scope until the project is published.

## Before the Actual `v1.0.0` Tag (after the months-long real-world soak)

- Re-run the full review gates (quick/race/fuzz/bench + FB3/FB4/FB5 suite).
- **Re-measure `bench/compare/` against the then-latest nakagami release** and refresh
  `COMPARISON.md`/README numbers (they improve quickly: 0.9.15→0.9.19 closed several gaps).
- Re-read `COMPATIBILITY.md` against any issue found during the soak period.
- Blockers list and backlog: `review/05-recommendations.md`.
