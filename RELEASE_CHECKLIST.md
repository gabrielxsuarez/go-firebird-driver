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

- Kill/restart Firebird while a fetch is active.
- Kill/restart Firebird while reading or writing a large BLOB.
- Run pool stress with `MaxOpenConns > 1` for several minutes.
- Run application workloads from real projects for weeks/months and log any mismatch with `COMPATIBILITY.md`.

## Decisions To Close Before `v1.0.0`

- BLOB streaming: required for `v1.0.0` or documented as future work.
- High precision values: keep returning `string` or add optional helper types.
- Timezone names: offset/wall-clock preservation is currently stable; exact IANA name preservation is not guaranteed.
- Automation format: keep PowerShell script, add Makefile/Taskfile, or both.
