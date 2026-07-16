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
| `none_charset` | value of `charset` | Character set used to decode text columns declared `CHARACTER SET NONE`, and to encode parameters bound to them. `NONE` yields raw `[]byte`. See *Charset Behavior*. |
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
| `NONE` | Decoded with `none_charset`, which defaults to the connection charset. Raw `[]byte` when that resolves to `NONE`. See below. |
| `OCTETS` | Binary, exposed as `[]byte`. |
| `ASCII` | Encodes only ASCII and decodes invalid bytes as replacement runes. |
| `UTF8`, `UNICODE_FSS` | Passthrough UTF-8 strings. |
| `ISO8859_1` | Fast path implemented without external encoder allocation. |
| `ISO8859_2`, `ISO8859_3`, `ISO8859_4`, `ISO8859_5`, `ISO8859_6`, `ISO8859_7`, `ISO8859_8`, `ISO8859_9`, `ISO8859_13` | Backed by `golang.org/x/text/encoding/charmap` where available. |
| `WIN1250` through `WIN1258` | Backed by `golang.org/x/text/encoding/charmap`. |
| `SJIS_0208`, `EUCJ_0208`, `KSC_5601`, `BIG_5`, `GBK`, `GB18030` | Backed by `golang.org/x/text` encoders. |
| Legacy DOS/TIS620 mappings | Names are recognized, but unsupported transcoders currently fall back to passthrough. |

### `CHARACTER SET NONE` columns

A `NONE` column carries no character set, so the bytes alone do not say how to
read them. `none_charset` supplies that character set, and defaults to the
connection `charset` — matching nakagami, which decodes text with the
connection charset, and Jaybird, which redefines the `NONE` encoding to the
connection's charset.

Reading a `NONE` column holding `0xD1` (`Ñ` in Latin-1):

| DSN | Result |
| --- | --- |
| `?charset=UTF8` (default) | `"\xD1"` — `string`, not valid UTF-8 (decoding as UTF8 is a pass-through) |
| `?charset=ISO8859_1` | `"Ñ"` |
| `?charset=NONE` | `[]byte{0xD1}` |
| `?charset=NONE&none_charset=ISO8859_1` | `"Ñ"` |

The last form is recommended for legacy databases with mixed character sets:
`charset=NONE` keeps the server from transliterating (a lossy connection
charset aborts the fetch with *Malformed string* on data it cannot represent),
while `none_charset` decodes client-side. The character set requested over the
wire always stays `NONE` for these columns, so reinterpreting them never makes
the server transliterate. Parameters bound to `NONE` columns are encoded with
the same character set, so reads and writes stay symmetric.

Columns that declare their own character set are unaffected, and `OCTETS` stays
raw: it is binary by declaration, not text without a character set.
`ColumnTypeLength` reports the length declared in the database (`NONE` is one
byte per character), independent of `none_charset`.

Text blobs follow the same rules: the describe reports the blob's effective
character set (the connection charset when the server transliterates, or the
declared column charset under `charset=NONE`), and the driver decodes reads
and encodes string parameters with it. A `BLOB SUB_TYPE TEXT CHARACTER SET
NONE` follows `none_charset`, like `CHAR`/`VARCHAR NONE` — including raw
`[]byte` when it resolves to `NONE`. `[]byte` parameters are always written
as-is.

## Errors

- Server errors are `*firebird.Error` (inspect with `errors.As`); `GDSCode()` returns the
  primary GDS code, `SQLState()` the 5-char SQLSTATE (empty if the server did not send
  one), and the full status vector is available in `SV`. These three are stable 1.0 API.
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
| `NUMERIC`/`DECIMAL` | Scaled values are returned as strings when needed to preserve precision. As parameters they accept `string`, integer/float types, and any third-party decimal implementing `driver.Valuer` (resolved by `database/sql` before reaching the driver). |
| `INT128` | Returned as string. |
| `DECFLOAT(16)` / `DECFLOAT(34)` | Returned as string, including `Infinity`, `-Infinity`, and `NaN`. Parameters are sent as UTF-8 text and converted by Firebird. |
| `DATE`, `TIME`, `TIMESTAMP` | Returned as `time.Time`. |
| `TIME/TIMESTAMP WITH TIME ZONE` | Returned as `time.Time`; the driver preserves wall clock and offset. |
| `CHAR/VARCHAR CHARACTER SET OCTETS` | Returned as `[]byte`. |
| `CHAR/VARCHAR CHARACTER SET NONE` | Returned as string decoded with `none_charset` (defaults to the connection charset), or as `[]byte` when that resolves to `NONE`. |
| `BLOB SUB_TYPE 0` | Materialized as `[]byte`. |
| `BLOB SUB_TYPE TEXT` | Materialized as string decoded with the blob's effective charset as reported by the describe (connection charset when the server transliterates; declared column charset under `charset=NONE`). `CHARACTER SET NONE` text blobs follow `none_charset`, returning `[]byte` when it resolves to `NONE`. |

Known limitation:

- Time zone values currently preserve wall clock and offset. The original IANA zone name is not guaranteed to survive every Firebird binary path.
- Blob values are currently materialized. Streaming BLOB APIs are not part of the initial stable contract.

## Stability Gates

Before tagging a release candidate:

```powershell
$env:FB3_TEST_DSN='firebird://sysdba:masterkey@127.0.0.1:3050/path/to/test-database.fdb'
.\scripts\validate.ps1 -Mode quick
.\scripts\validate.ps1 -Mode race
.\scripts\validate.ps1 -Mode fuzz -FuzzSeconds 30
.\scripts\validate.ps1 -Mode bench -BenchCount 5
```

With Firebird 4/5 containers available, the normal test suite also validates the FB4/FB5 type and metadata coverage.

## Resolved Contract Decisions (2026-07-03, pre-1.0 review)

These are the 1.0 contract; all four leave room for additive 1.x extensions:

- **BLOBs are fully materialized** (`[]byte`/`string`). Memory cost validated up to
  hundreds of MB in phase-2 testing. A streaming API can be added in 1.x without breaking
  this contract.
- **High-precision numerics are `string`** (lossless). Third-party decimal types work as
  parameters today via `driver.Valuer`. Optional helper types may come in 1.x.
- **Time zone values preserve wall clock and offset**; the original IANA zone name is not
  guaranteed to survive every Firebird binary path. Documented limitation, not a bug.
- **No non-`database/sql` API in 1.0** (no events, services, or protocol access). The wire
  implementation is `internal/`; exposing a curated low-level API later is additive.
