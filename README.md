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
`benchstat` n=6), `go-firebird-driver` was faster in 6 of 7 scenarios and allocated less
memory in all 7:

| Scenario | go-firebird-driver | nakagami/firebirdsql | Time | Allocations |
|----------|-------------------:|---------------------:|-----:|------------:|
| Connect (SRP + attach + detach) | 62.8 ms · 211 allocs | 66.8 ms · 3,118 allocs | **1.06x** | **14.8x fewer** |
| Prepared `SELECT 1` | 241 µs · 12 allocs | 802 µs · 104 allocs | **3.3x faster** | **8.7x fewer** |
| Fetch 10k rows × 10 columns | 108.8 ms · 130k allocs | 133.9 ms · 480k allocs | **1.2x faster** | **3.7x fewer** |
| Prepared INSERT (batched tx) | 226 µs · 8 allocs | 359 µs · 105 allocs | **1.6x faster** | **13x fewer** |
| 1 KB BLOB read | 573 µs · 17 allocs | 1,300 µs · 198 allocs | **2.3x faster** | **11.6x fewer** |
| 1 MB BLOB read | 24.8 ms · 36 allocs | 119.9 ms · 15,603 allocs | **4.8x faster** | **433x fewer** |
| Pool, 20 concurrent goroutines | 83.0 µs · 11 allocs | 81.7 µs · 106 allocs | tie (server-bound) | **9.6x fewer** |

That matters because a database driver runs in the hot path of the application: fewer allocations mean less GC pressure, lower latency, and better stability under load.

> Measured 2026-07-03 against Firebird 3.0.14 on loopback (Windows/amd64, Go 1.26).
> Fully reproducible: see [bench/compare/README.md](bench/compare/README.md).
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
- `dialect` (`3` by default)
- `role`
- `wire_crypt` (`enabled` by default)
- `fetch_size` (`200` by default)
- `data_type_bind` / `dataTypeBind` / `set_bind` for Firebird 4+ `isc_dpb_set_bind`
- `session_time_zone` / `sessionTimeZone` / `timezone` for Firebird 4+ session time zone

See [COMPATIBILITY.md](COMPATIBILITY.md) for the current compatibility contract, type behavior, charset behavior, and known limitations.

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
