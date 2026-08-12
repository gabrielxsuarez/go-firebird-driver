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
  mid-run to exercise recovery). Last full run: 2026-07-03 — ~300k ops, kill/restart
  mid-run, recovery in ~20s, no goroutine/heap leaks.
- Kill/restart Firebird while a fetch is active (done 2026-07-03: fetch fails <2s, pool
  recovers, no goroutine leak).
- Kill/restart Firebird while reading or writing a large BLOB.
- Run application workloads from real projects for weeks/months and log any mismatch
  with `COMPATIBILITY.md` — **this is the release-candidate period for 1.0**.

## Frozen for 1.0

- Module path: `github.com/gabrielxsuarez/go-firebird-driver`.
- Go minimum: 1.25 (required by current `golang.org/x/crypto`; do not lower by pinning
  an old crypto dependency).
- Contract decisions (BLOB materialization, numerics-as-string, timezone wall-clock+offset,
  no extended API): see `COMPATIBILITY.md` ("Resolved Contract Decisions").
- Automation: PowerShell only (`scripts/validate.ps1`); no Makefile for 1.0. CI is
  deliberately out of scope until the project is published.

## Before the Actual `v1.0.0` Tag (after the months-long real-world soak)

- Re-run the full gates (quick/race/fuzz/bench + FB3/FB4/FB5 suite).
- **Re-measure `bench/compare/` against the then-latest nakagami release** and refresh
  `COMPARISON.md`/README numbers (they improve quickly: 0.9.15→0.9.19 closed several gaps).
- Re-read `COMPATIBILITY.md` against any issue found during the soak period.
- Code blockers for v1.0.0: none remaining — only real-world soak time.

## Post-1.0 Backlog (priority by expected demand)

1. **Pool20**: ~10% slower than nakagami v0.9.19 under 20 concurrent connections (only
   head-to-head loss). Investigate ad-hoc path cost under contention.
2. **`op_inline_blob`** (protocol 18 / FB5): largest available improvement on the blob path.
3. Select1Prepared B/op (higher bytes than nakagami; allocs still ~5× better).
4. Features: events → services API → wire compression → ChaCha64 → (Legacy_Auth? unlikely).
5. `column_name_to_lower` as a migration convenience option.
6. Batch API (`op_batch`, FB4+); streaming BLOBs; numeric helper types.
7. Internal refactor: unify ad-hoc vs prepared execution paths (deferred for risk).
