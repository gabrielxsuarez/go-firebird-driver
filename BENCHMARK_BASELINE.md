# Benchmark Baseline

Baseline runs are local and environment-sensitive. Use them to catch large allocation or
latency regressions, not as universal performance promises.

## Environment

- Date: 2026-07-03 (review pre-1.0, Fase 3)
- OS: Windows/amd64
- Go: `go1.26.x windows/amd64`
- CPU: Intel Core i5-10210U (laptop)
- Integration server: Firebird 3.0.14 native on loopback
  (`firebird://sysdba:masterkey@127.0.0.1:3050/C:/AlfaBeta/firebird/tmp/bench.fdb`)

**Thermal throttling warning**: this CPU sustains ~half its turbo clock. Micro-benchmark
`ns/op` measured cold (first seconds) is up to 2× faster than sustained (`-count=10`).
The numbers below are *sustained*. `allocs/op` and `B/op` are the primary regression
signal on this machine; compare `ns/op` only between interleaved runs on the same
thermal state.

## Commands

```powershell
go test -run '^$' -bench . -benchmem -count=10 ./wire > review/bench/micro_wire.txt
go test -run '^$' -bench . -benchmem -count=10 ./internal/charset > review/bench/micro_charset.txt
$env:FB3_TEST_DSN='firebird://sysdba:masterkey@127.0.0.1:3050/C:/AlfaBeta/firebird/tmp/bench.fdb'
go test -run '^$' -bench "^Benchmark(Ping|QuerySingleRow|ExecInsert|PreparedExec|QueryManyRows)$" -benchmem -count=6 .
```

Summarize with `benchstat` (raw outputs of this baseline live in `review/bench/`).
For the head-to-head comparison against nakagami/firebirdsql see `bench/compare/README.md`.

## Regression Rules

- `allocs/op` in hot paths must not increase without an explicit note.
- `B/op` must not increase more than 5% without justification.
- `ns/op`: compare only against a same-thermal-state rerun (see warning above); >10%
  sustained regression on the same machine state needs investigation.
- `Ping`, parameter encoding, DPB/TPB/BLR building and small XDR writes must stay at
  zero allocations. Row decode must stay at ~1 alloc per non-NULL value delivered.

## Current Baseline Summary (sustained)

### Wire Hot Paths

```text
BenchmarkEncodeParamsOptimal_3Ints-8       235 ns/op      0 B/op     0 allocs/op
BenchmarkEncodeParamsOptimal_MixedTypes-8  310 ns/op      0 B/op     0 allocs/op
BenchmarkDecodeRow_5Cols-8                 934 ns/op     72 B/op     5 allocs/op
BenchmarkDecodeRow_5Cols_2Nulls-8          239 ns/op     16 B/op     2 allocs/op
BenchmarkDecodeRow_30Cols-8               5336 ns/op    432 B/op    30 allocs/op
BenchmarkDecodeRow_30Cols_HalfNulls-8     2716 ns/op    216 B/op    15 allocs/op
BenchmarkBuildDPB_Connect-8               25.0 ns/op      0 B/op     0 allocs/op
BenchmarkBuildTPB_ReadCommitted-8         11.8 ns/op      0 B/op     0 allocs/op
BenchmarkAppendBLR_5Cols-8                65.3 ns/op      0 B/op     0 allocs/op
BenchmarkWriteInt32-8                     4.57 ns/op      0 B/op     0 allocs/op
BenchmarkReadInt32-8                      39.1 ns/op      0 B/op     0 allocs/op
BenchmarkReadString_Long-8                 161 ns/op     96 B/op     1 allocs/op
BenchmarkCryptWriter_1KB-8                3.60 µs/op      0 B/op     0 allocs/op
BenchmarkCryptReader_1KB-8                3.68 µs/op      0 B/op     0 allocs/op
```

### Charset Hot Paths

```text
BenchmarkDecodeUTF8Direct-8       16 B/op    1 allocs/op
BenchmarkDecodeISO88591ASCII-8    16 B/op    1 allocs/op
BenchmarkDecodeISO88591Latin1-8   16 B/op    1 allocs/op
BenchmarkEncodeISO88591ASCII-8     0 B/op    0 allocs/op
```

### Driver Integration Hot Paths (FB3 native, loopback, wire crypt ARC4)

```text
BenchmarkPing-8              104 µs/op        0 B/op       0 allocs/op
BenchmarkQuerySingleRow-8    505 µs/op      960 B/op       8 allocs/op
BenchmarkExecInsert-8       1.18 ms/op ±45%  372 B/op      8 allocs/op   (disk-bound: forced writes)
BenchmarkPreparedExec-8      713 µs/op ±68%  181 B/op      7 allocs/op   (disk-bound: forced writes)
BenchmarkQueryManyRows-8    7.33 ms/op    111.7 KiB/op  7627 allocs/op
```

Allocation counts are identical to (or slightly better than) the 2026-05-08 baseline;
that baseline's `ns/op` was measured cold and is superseded by this one.

### Head-to-head vs nakagami/firebirdsql v0.9.19 (2026-07-03, benchstat n=6)

Same server, same data, per-driver defaults. Full report: `COMPARISON.md` and
`review/06-vs-nakagami.md`; reproduce with `bench/compare/`.

```text
                    ours       nakagami    time delta          allocs (ours vs nak)
Connect             63.8 ms    66.5 ms     tie (p=0.18)        211 vs 3089
Select1Prepared      254 µs     368 µs     +45%                 12 vs 62
Fetch10kx10        107.1 ms   128.4 ms     +20%               130k vs 500k
InsertPrepared       232 µs     268 µs     +15%                  8 vs 62
Blob1KB              557 µs     704 µs     +26%                 17 vs 127
Blob1MB             25.5 ms   118.1 ms     +363%                36 vs 13487
Pool20                87 µs      79 µs     nakagami +10%        11 vs 95
```

Pool20 is the one scenario nakagami wins (verified with interleaved runs); it is a
backlog item (`review/06-vs-nakagami.md`). nakagami improved considerably between
v0.9.15 and v0.9.19 — re-measure against their latest release before the 1.0 announce.
