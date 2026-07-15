# go-firebird-driver

Pure-Go [Firebird](https://firebirdsql.org/) driver for `database/sql`, built to **minimize allocations**, reduce memory usage, and improve response times.

It supports Firebird 3.0, 4.0, and 5.0 through a native wire protocol implementation (`v13` to `v18`), with no CGO and no `fbclient` dependency.

## Installation

```bash
go get github.com/gabrielxsuarez/go-firebird-driver
```

## Migration from `nakagami/firebirdsql`

For easier migrations, this package registers both `firebird` and `firebirdsql`.

Use `firebird` in new code. The `firebirdsql` name is kept as a compatibility alias so existing `sql.Open("firebirdsql", ...)` calls do not need to change when switching imports.

See [MIGRATION.md](MIGRATION.md) for the full migration guide (DSN parameter mapping and behavioral differences) and [COMPARISON.md](COMPARISON.md) for a verified feature and performance comparison.

## Quick Start

```go
package main

import (
    "database/sql"
    "log"

    _ "github.com/gabrielxsuarez/go-firebird-driver"
)

func main() {
    db, err := sql.Open("firebird", "firebird://sysdba:masterkey@localhost:3050/path/to/database.fdb")
    if err != nil {
        log.Fatal(err)
    }
    defer db.Close()

    var name string
    err = db.QueryRow("SELECT RDB$RELATION_NAME FROM RDB$RELATIONS WHERE RDB$RELATION_ID = ?", 0).Scan(&name)
    if err != nil {
        log.Fatal(err)
    }
}
```

## Why This Project Exists

Today, the de facto Firebird driver in Go is [nakagami/firebirdsql](https://github.com/nakagami/firebirdsql). That project was the pioneer and remains an important reference, but this driver was built with a different priority:

- **primary goal:** be aggressively more efficient in allocations and memory usage
- **secondary goal:** improve CPU efficiency and latency
- **tertiary goal:** support newer wire protocol versions

The focus is on implementing the `database/sql` contract well, not on covering advanced APIs that naturally sit outside that interface.

## Why Use It Instead of `nakagami/firebirdsql`

In the head-to-head benchmarks in this repository (`bench/compare/`, same server, same data,
`benchstat` n=6), `go-firebird-driver` was faster in 5 of 7 scenarios and allocated less
memory in all 7:

| Scenario | go-firebird-driver | nakagami/firebirdsql v0.9.19 | Time | Allocations |
|----------|-------------------:|-----------------------------:|-----:|------------:|
| Connect (SRP + attach + detach) | 63.8 ms · 211 allocs | 66.5 ms · 3,089 allocs | tie | **14.6x fewer** |
| Prepared `SELECT 1` | 254 µs · 12 allocs | 368 µs · 62 allocs | **1.45x faster** | **5.2x fewer** |
| Fetch 10k rows × 10 columns | 107.1 ms · 130k allocs | 128.4 ms · 500k allocs | **1.2x faster** | **3.9x fewer** |
| Prepared INSERT (batched tx) | 232 µs · 8 allocs | 268 µs · 62 allocs | **1.15x faster** | **7.8x fewer** |
| 1 KB BLOB read | 557 µs · 17 allocs | 704 µs · 127 allocs | **1.26x faster** | **7.5x fewer** |
| 1 MB BLOB read | 25.5 ms · 36 allocs | 118.1 ms · 13,487 allocs | **4.6x faster** | **375x fewer** |
| Pool, 20 concurrent goroutines | 87 µs · 11 allocs | 79 µs · 95 allocs | nakagami 1.10x faster | **8.6x fewer** |

That matters because a database driver runs in the hot path of the application: fewer allocations mean less GC pressure, lower latency, and better stability under load. We publish the scenario nakagami wins too (Pool20, on our backlog) — the numbers are only useful if they're honest.

> Measured 2026-07-03 against Firebird 3.0.14 on loopback (Windows/amd64, Go 1.26),
> nakagami/firebirdsql v0.9.19 (latest release at measurement time).
> Fully reproducible: see [bench/compare/README.md](bench/compare/README.md).
> Full feature-by-feature comparison: [COMPARISON.md](COMPARISON.md).
> Exact results will vary depending on network, server, dataset, and workload.

## Project Scope

This driver prioritizes what a normal Go application using `database/sql` actually needs:

- connections, queries, exec, transactions, and prepared statements
- SRP authentication (`Srp`, `Srp256`)
- wire encryption (`ARC4`, `ChaCha20`)
- standard Firebird types and column metadata
- support for Firebird 3.0+

Explicit scope decisions:

- **Firebird 1.x and 2.x are not supported**
- **advanced APIs outside `database/sql` were not implemented**, such as events
- **compression is not implemented for now**, because it added too much complexity and significantly increased CPU and memory usage at this stage

Compression is not ruled out for the future, but it was not a priority for the first version.

## Main Features

- pure Go, no CGO
- native wire protocol implementation
- compatible with `database/sql`
- registers both `firebird` and `firebirdsql`
- support for Firebird 3, 4, and 5
- optimized to reduce allocations in hot paths
- ready to evolve with newer protocol versions

## DSN

```text
firebird://user:password@host:port/path/to/database.fdb?param=value
```

Main parameters:

- `charset` (`UTF8` by default)
- `none_charset` (defaults to `charset`)
- `dialect` (`3` by default)
- `role`
- `wire_crypt` (`enabled` by default)
- `fetch_size` (`200` by default)
- `data_type_bind` / `dataTypeBind` / `set_bind` for Firebird 4+ `isc_dpb_set_bind`
- `session_time_zone` / `sessionTimeZone` / `timezone` for Firebird 4+ session time zone

### `none_charset`

Text columns declared `CHARACTER SET NONE` carry no character set, so the bytes
alone do not say how to read them. `none_charset` is the character set used to
decode those columns and to encode parameters bound to them. It defaults to
`charset`, which matches nakagami's driver; Jaybird does the same by redefining
the `NONE` encoding to the connection's charset.

Reading a `NONE` column that holds `0xD1` (`Ñ` in Latin-1):

```text
?charset=UTF8                          → "\xD1"      (string, not valid UTF-8)
?charset=ISO8859_1                     → "Ñ"
?charset=NONE                          → []byte{0xD1}
?charset=NONE&none_charset=ISO8859_1   → "Ñ"
```

Decoding as `UTF8` is a pass-through, so the first form hands back the bytes
unchanged inside a `string`, which is not valid UTF-8 and degrades to `U+FFFD`
if the consumer iterates runes. Point `none_charset` at the character set the
data is really in to get a well-formed string.

The last form is the Jaybird-style passthrough recommended for legacy databases
with mixed character sets: `charset=NONE` keeps the server from transliterating
(a lossy connection charset aborts the fetch with *Malformed string* on data it
cannot represent), while `none_charset` decodes the `NONE` columns client-side.
Columns that declare their own character set are unaffected, and `OCTETS` stays
raw: it is binary by declaration, not text without a character set.

Setting `none_charset=NONE` always yields raw `[]byte`, whatever the connection
charset is.

See [COMPATIBILITY.md](COMPATIBILITY.md) for the current compatibility contract, type behavior, charset behavior, and known limitations.

## Usage Notes

The four things most likely to surprise you:

- **NUMERIC/DECIMAL scan as `string`** (lossless — no float rounding). Scan into a
  `string` and parse with your decimal library; third-party decimal types that implement
  `driver.Valuer` work directly as parameters.

  ```go
  var price string
  err := db.QueryRow("SELECT price FROM products WHERE id = ?", 1).Scan(&price) // "1234.50"
  ```

- **BLOBs are fully materialized**: `[]byte` for binary, `string` for `SUB_TYPE TEXT`.
  A 100 MB blob means 100 MB of memory; there is no streaming API in 1.0.
- **`TIMESTAMP`/`TIME WITH TIME ZONE`** come back as `time.Time` preserving wall clock
  and offset; the original IANA zone *name* is not guaranteed to survive.
- **`CHARACTER SET NONE`** columns carry no character set, so they are decoded with
  `none_charset`, which defaults to the connection charset. Point it at the character
  set the data is really in (`?none_charset=ISO8859_1`), or set it to `NONE` to get raw
  `[]byte`. See [`none_charset`](#none_charset) above.
- **No `LastInsertId`**: Firebird has no such concept; use `INSERT ... RETURNING`:

  ```go
  var id int64
  err := db.QueryRow("INSERT INTO t (name) VALUES (?) RETURNING id", "x").Scan(&id)
  ```

- **Errors are `*firebird.Error`**: `errors.As` gives you `GDSCode()` and `SQLState()`
  for programmatic handling.

## Validation

The repository includes a PowerShell validation script for the normal stability workflow:

```powershell
$env:FB3_TEST_DSN='firebird://sysdba:masterkey@127.0.0.1:3050/path/to/test-database.fdb'
.\scripts\validate.ps1 -Mode quick
.\scripts\validate.ps1 -Mode race
.\scripts\validate.ps1 -Mode fuzz -FuzzSeconds 30
.\scripts\validate.ps1 -Mode bench -BenchCount 5
```

See [RELEASE_CHECKLIST.md](RELEASE_CHECKLIST.md) for the release-candidate checklist and longer manual tests.

## Requirements

- Go **1.25+**
- Firebird **3.0+**

## License

MIT
