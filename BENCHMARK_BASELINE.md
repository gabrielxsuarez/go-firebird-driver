# Benchmark Baseline

Baseline runs are local and environment-sensitive. Use them to catch large allocation or latency regressions, not as universal performance promises.

## Environment

- Date: 2026-05-08
- OS: Windows/amd64
- Go: `go1.26.0 windows/amd64`
- CPU: Intel Core i5-10210U
- Firebird integration DSN used locally:
  - `firebird://sysdba:masterkey@127.0.0.1:3050/C:/AlfaBeta/firebird/tmp/go_firebird_driver_test.fdb`

## Commands

```powershell
$env:FB3_TEST_DSN='firebird://sysdba:masterkey@127.0.0.1:3050/C:/AlfaBeta/firebird/tmp/go_firebird_driver_test.fdb'
go test -run=^$ -bench="^(BenchmarkRead|BenchmarkWrite|BenchmarkArc4|BenchmarkChaCha|BenchmarkCrypt|BenchmarkEncodeParams|BenchmarkEstimate|BenchmarkStackWriter|BenchmarkToString|BenchmarkRepeatZeros|BenchmarkScaledInt)" -benchmem -count=3 ./wire
go test -run=^$ -bench=. -benchmem -count=3 ./internal/charset
go test -run=^$ -bench="^Benchmark(Ping|QuerySingleRow|ExecInsert|PreparedExec|QueryManyRows)$" -benchmem -count=3 .
```

## Regression Rules

- `allocs/op` in hot paths should not increase without an explicit note.
- `B/op` should not increase more than 5% without justification.
- `ns/op` should not increase more than 10% on a quiet comparable machine.
- `Ping`, parameter encoding, and small XDR operations should stay at zero allocations.

## Current Baseline Summary

The baseline below records the hot paths most useful for regression checks. Full raw output can be regenerated with the commands above.

### Wire Hot Paths

```text
BenchmarkEncodeParamsOptimal_3Ints-8         106.1-108.5 ns/op       0 B/op       0 allocs/op
BenchmarkEncodeParamsOptimal_MixedTypes-8    130.4-132.5 ns/op       0 B/op       0 allocs/op
BenchmarkStackWriterInt32-8                   28.7-40.5 ns/op        0 B/op       0 allocs/op
BenchmarkStackWriterInt64-8                   28.2-28.6 ns/op        0 B/op       0 allocs/op
BenchmarkWriteInt32-8                          2.16-2.27 ns/op       0 B/op       0 allocs/op
BenchmarkReadInt32-8                          17.3-21.0 ns/op        0 B/op       0 allocs/op
BenchmarkReadString_Long-8                    93.0-95.0 ns/op       96 B/op       1 allocs/op
BenchmarkCryptWriter_1KB-8                  1570-1620 ns/op          0 B/op       0 allocs/op
BenchmarkCryptReader_1KB-8                  1639-1658 ns/op          0 B/op       0 allocs/op
```

### Charset Hot Paths

```text
BenchmarkDecodeUTF8Direct-8        37.75 ns/op      16 B/op       1 allocs/op
BenchmarkDecodeISO88591ASCII-8     49.54 ns/op      16 B/op       1 allocs/op
BenchmarkDecodeISO88591Latin1-8    69.73 ns/op      16 B/op       1 allocs/op
BenchmarkEncodeISO88591ASCII-8     16.28 ns/op       0 B/op       0 allocs/op
BenchmarkEncodeISO88591Latin1-8    65.22 ns/op      16 B/op       1 allocs/op
BenchmarkEncodeWIN1251-8           349.3 ns/op     288 B/op       3 allocs/op
```

### Driver Integration Hot Paths

```text
BenchmarkPing-8              187418-205404 ns/op        0 B/op       0 allocs/op
BenchmarkQuerySingleRow-8    687329-733140 ns/op      960 B/op       8 allocs/op
BenchmarkExecInsert-8        1003692-1068576 ns/op    380 B/op       8 allocs/op
BenchmarkPreparedExec-8      252194-258465 ns/op      191 B/op       7 allocs/op
BenchmarkQueryManyRows-8     4646610-4657845 ns/op 114355 B/op    7627 allocs/op
```

Notes:

- Integration latency is highly dependent on local Firebird state, storage, CPU throttling, and wire encryption. Allocation counts are the primary regression signal.
- The charset baseline was regenerated with `BenchCount=1` after fixing PowerShell argument quoting in `scripts/validate.ps1`.
