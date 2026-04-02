# go-firebird-driver

Pure-Go [Firebird](https://firebirdsql.org/) driver for `database/sql`, built to **minimize allocations**, reduce memory usage, and improve response times.

It supports Firebird 3.0, 4.0, and 5.0 through a native wire protocol implementation (`v13` to `v18`), with no CGO and no `fbclient` dependency.

## Installation

```bash
go get github.com/gabrielxsuarez/go-firebird-driver
```

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

In the most recent benchmarks in this repository, `go-firebird-driver` was consistently more efficient in time, memory, and allocations.

| Operation | go-firebird-driver | nakagami/firebirdsql | Improvement |
|-----------|-------------------:|---------------------:|------------:|
| Ping | **587,114 ns/op** · **0 allocs** | 93,254,153 ns/op · 69 allocs | **158.8x faster** |
| Single-row query | **51,630,597 ns/op** · **8 allocs** | 98,583,298 ns/op · 91 allocs | **1.9x faster** |
| Ad-hoc insert | **5,844,778 ns/op** · **5 allocs** | 122,372,619 ns/op · 110 allocs | **20.9x faster** |
| Prepared insert | **717,707 ns/op** · **7 allocs** | 1,476,732 ns/op · 55 allocs | **2.1x faster** |
| 1000-row query | **69,000,338 ns/op** · **7,628 allocs** | 110,013,506 ns/op · 20,738 allocs | **1.6x faster** |

Notable reductions versus `nakagami/firebirdsql`:

- **100% fewer allocations** in `Ping`
- **91.2% fewer allocations** in single-row queries
- **95.5% fewer allocations** in ad-hoc inserts
- **63.2% fewer allocations** even in 1000-row queries

That matters because a database driver runs in the hot path of the application: fewer allocations mean less GC pressure, lower latency, and better stability under load.

> Data taken from the repository's comparison profiles, measured on April 2, 2026, on Windows/amd64 with Go 1.26.0. Exact results will vary depending on network, server, dataset, and workload.

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

## Requirements

- Go **1.25+**
- Firebird **3.0+**

## License

MIT
