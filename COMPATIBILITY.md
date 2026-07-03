# Compatibility and Stability Contract

This document captures the current intended contract while the driver is being exercised in production workloads before a future `v1.0.0`.

## Supported Environment

| Area | Status |
| --- | --- |
| Go | Go 1.25+ |
| Firebird | Firebird 3, 4, and 5 |
| Platforms | Developed and validated primarily on Windows/amd64 |
| Driver API | `database/sql` |
| CGO/fbclient | Not required |

The driver registers both `firebird` and `firebirdsql`. New code should prefer `firebird`; `firebirdsql` is kept as a migration alias.

## DSN

Base format:

```text
firebird://user:password@host:port/path/to/database.fdb?param=value
```

Supported parameters:

| Parameter | Default | Notes |
| --- | --- | --- |
| `charset` | `UTF8` | Known aliases are canonicalized, unknown names are passed through. |
| `dialect` | `3` | Client SQL dialect. Only `3` is accepted; `dialect=1`/`2` return an explicit error. Dialect-1 *databases* work fine with client dialect 3. |
| `role` | empty | SQL role name. |
| `wire_crypt` | `enabled` | Accepts `enabled`, `disabled`, `required`, `true`, `false`, `1`, `0`. |
| `fetch_size` | `200` | Number of rows requested per fetch. |
| `data_type_bind` | empty | Firebird 4+ `isc_dpb_set_bind`; aliases: `dataTypeBind`, `set_bind`, `isc_dpb_set_bind`. |
| `session_time_zone` | empty | Firebird 4+ `isc_dpb_session_time_zone`; aliases: `sessionTimeZone`, `isc_dpb_session_time_zone`, `timezone`. Value `server` is intentionally omitted from the DPB. |

Compatibility guidance:

- Compatibility with `nakagami/firebirdsql` is desirable when it does not harm correctness or resource usage.
- Stability, correct data handling, and low allocation pressure take priority over exact DSN compatibility.

## Charset Behavior

| Charset group | Status |
| --- | --- |
| `NONE` | Passthrough bytes. Strings written as UTF-8 read back as strings; arbitrary bytes should be scanned as `[]byte`. |
| `OCTETS` | Binary, exposed as `[]byte`. |
| `ASCII` | Encodes only ASCII and decodes invalid bytes as replacement runes. |
| `UTF8`, `UNICODE_FSS` | Passthrough UTF-8 strings. |
| `ISO8859_1` | Fast path implemented without external encoder allocation. |
| `ISO8859_2`, `ISO8859_3`, `ISO8859_4`, `ISO8859_5`, `ISO8859_6`, `ISO8859_7`, `ISO8859_8`, `ISO8859_9`, `ISO8859_13` | Backed by `golang.org/x/text/encoding/charmap` where available. |
| `WIN1250` through `WIN1258` | Backed by `golang.org/x/text/encoding/charmap`. |
| `SJIS_0208`, `EUCJ_0208`, `KSC_5601`, `BIG_5`, `GBK`, `GB18030` | Backed by `golang.org/x/text` encoders. |
| Legacy DOS/TIS620 mappings | Names are recognized, but unsupported transcoders currently fall back to passthrough. |

Known limitation:

- `BLOB SUB_TYPE TEXT` parameters are encoded with the connection charset. This matches the practical behavior used by the current implementation, but it is not a per-column charset-aware BPB implementation.

## Errors

- Server errors are `*wire.StatusError` (inspect with `errors.As`); `GDSCode()` returns the
  primary GDS code and the full status vector is available in `SV`.
- Error strings render the complete GDS chain with the embedded Firebird message templates
  (e.g. `unsuccessful metadata update; DROP TABLE X failed; ... (GDS 335544351)`).
  The template table lives in `internal/errmsg` (generated from the Firebird source tree,
  message texts under IDPL; regenerate with `go generate ./internal/errmsg`).

## Context cancellation

- Cancelling the context of a query/exec sends `op_cancel` to the server, which
  interrupts execution and fetches cleanly and leaves the connection reusable.
- A row **lock wait** (transactions use WAIT mode) is not interruptible by
  `op_cancel`. When a cancelled context can't be honored that way within a short
  grace period, the driver forces the blocked read to return and discards the
  connection, so a cancelled context is always bounded and never hangs forever.

## Wire encryption

- `wire_crypt=enabled` (default) negotiates ChaCha20 against Firebird 4/5 and Arc4 against
  Firebird 3. `required` fails fast if no session key/cipher can be negotiated.

## Type Behavior

| Firebird type | Go scan behavior |
| --- | --- |
| Integer types | Native integer or scaled string for scaled numeric paths depending on descriptor. |
| `NUMERIC`/`DECIMAL` | Scaled values are returned as strings when needed to preserve precision. |
| `INT128` | Returned as string. |
| `DECFLOAT(16)` / `DECFLOAT(34)` | Returned as string, including `Infinity`, `-Infinity`, and `NaN`. Parameters are sent as UTF-8 text and converted by Firebird. |
| `DATE`, `TIME`, `TIMESTAMP` | Returned as `time.Time`. |
| `TIME/TIMESTAMP WITH TIME ZONE` | Returned as `time.Time`; the driver preserves wall clock and offset. |
| `CHAR/VARCHAR CHARACTER SET OCTETS` | Returned as `[]byte`. |
| `BLOB SUB_TYPE 0` | Materialized as `[]byte`. |
| `BLOB SUB_TYPE TEXT` | Materialized as string using connection charset. |

Known limitation:

- Time zone values currently preserve wall clock and offset. The original IANA zone name is not guaranteed to survive every Firebird binary path.
- Blob values are currently materialized. Streaming BLOB APIs are not part of the initial stable contract.

## Stability Gates

Before tagging a release candidate:

```powershell
$env:FB3_TEST_DSN='firebird://sysdba:masterkey@127.0.0.1:3050/C:/AlfaBeta/firebird/tmp/go_firebird_driver_test.fdb'
.\scripts\validate.ps1 -Mode quick
.\scripts\validate.ps1 -Mode race
.\scripts\validate.ps1 -Mode fuzz -FuzzSeconds 30
.\scripts\validate.ps1 -Mode bench -BenchCount 5
```

With Firebird 4/5 containers available, the normal test suite also validates the FB4/FB5 type and metadata coverage.

## Pending Product Decisions

These do not block production soak testing, but should be resolved before a strict `v1.0.0`:

- Whether streaming BLOB support is required for the first stable public contract.
- Whether high-precision numeric values should remain `string` only, or gain optional helper types.
- Whether exact IANA timezone name preservation is required, beyond preserving wall clock and offset.
- Whether to add a stable non-`database/sql` API for Firebird-specific features.
