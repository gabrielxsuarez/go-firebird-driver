# Fase 2 — Cierre (2026-07-03)

Completa los pendientes menores (H11–H19) y los edge cases dirigidos de la Fase 2.3,
más la matriz multi-versión (2.2) y el refuerzo de fuzzers (2.1).

## Fixes H11–H19 (review/01b) — todos con test

| # | Fix | Test |
| --- | --- | --- |
| H11 | `getRowsAffected` devuelve error en vez de tragarlo; el error se propaga a `Result.RowsAffected` | (integrado) |
| H12 | Niveles de aislamiento no soportados devuelven error (antes degradaban a Default en silencio); RepeatableRead→SNAPSHOT documentado | `TestIsolationLevels` |
| H13 | `BeginTx` respeta `ctx` (chequeo temprano + watcher) y no ignora el error del commit del autoTx pendiente | `TestBeginTxCanceledContext` |
| H14 | Chequeo temprano de `ctx.Err()` en Prepare/Exec/Query (conn y stmt) | (integrado) |
| H15 | `ColumnTypeLength` en caracteres, no bytes (UTF8 CHAR(10)→10, no 40); BLOB reporta longitud variable | `TestColumnTypeLengthChars` |
| H17 | `descCache` valida los bytes crudos del describe: no sirve descriptores viejos tras un DDL de otra conexión | `TestDescCacheInvalidatedByDDL` |
| H18 | `ResetSession` sin round-trip por checkout; detecta transacción explícita huérfana y descarta la conexión | (integrado en pool tests) |
| H19 | `wrapBadConn` usa `%w` doble: `errors.Is(ErrBadConn)` y `errors.As(causa)` funcionan a la vez | `TestWrapBadConnChain` |
| — | API pública de errores: `firebird.Error = wire.StatusError` (alias); se eliminó el tipo muerto `FirebirdError`/`NewFirebirdError` | `TestPublicErrorType` |
| — | `ColumnTypeScanType` de NUMERIC escalado ahora declara `string` (coincide con lo que `Scan` entrega) | `TestColumnTypeScanTypeScaledNumeric` |

## Bugs nuevos encontrados durante la Fase 2

1. **OOM por length prefix malicioso** (encontrado por fuzz): `ReadBuffer` asignaba memoria según
   una longitud tomada del stream sin acotarla; un servidor corrupto/malicioso podía forzar una
   asignación de ~2GB (DoS). Acotado a `maxWireBufferLen` (64MB). Seeds de regresión en
   `wire/testdata/fuzz/FuzzReadStatusVector/`. El fuzzer ahora aguanta >4M ejecuciones.
2. **`scaledInt64` con MinInt64**: negar `-MinInt64` en int64 dejaba el valor negativo y el string
   salía con doble signo (`--922...`). Corregido usando magnitud en uint64. `TestNumericExtremes`.
3. **Cancelación de lock-wait colgaba indefinidamente**: `op_cancel` interrumpe ejecución y fetch
   pero no una espera de lock en modo WAIT; el deadline del contexto se ignoraba y la goroutine
   colgaba para siempre. Ahora, si tras `op_cancel` la operación no retorna en un período de gracia
   (2s), se fuerza el desbloqueo con un read deadline y se descarta la conexión. La cancelación de
   ejecución sigue dejando la conexión reutilizable. `TestCancelDuringLockWait`,
   `TestCancelLongQueryKeepsConnReusable`.

## Edge cases 2.3 cubiertos (edgecases_test.go)

- Valores límite: VARCHAR(32765) al máximo absoluto (conexión de 1 byte), NUMERIC(18,4)/(9,2)/(4,4)
  en extremos incl. MinInt64, TIME 00:00:00–23:59:59.9999, string vacía vs NULL, blobs 0B–8MB
  (cruza límites de segmento), resultset de 50k filas con `fetch_size=7`.
- Errores del servidor: violación de PK con verificación del código GDS y de que la conexión sobrevive.
- Post-EOF / post-Close: `Next` tras EOF, `Close` doble, `io.EOF` nunca se filtra al usuario.
- Concurrencia / fugas: pool de 4 conns con 16 goroutines mezclando queries, cancelaciones y tx;
  verificación de no-fuga de goroutines tras `db.Close()`.
- EXECUTE BLOCK por ambos caminos (QueryRow con SUSPEND y Exec).

## Matriz multi-versión 2.2 (protocol_matrix_test.go)

- Versión de protocolo negociada: FB3≥13, FB4≥16, FB5≥18 (verificado).
- `wire_crypt` disabled/enabled/required contra las tres versiones.

## Pruebas manuales (scratchpad, no en la suite)

- **Kill del servidor a mitad de fetch** (`killtest`): el fetch retorna con error en <2s (no cuelga),
  la conexión muerta se detecta, el pool se recupera solo al reiniciar el server, sin fuga de goroutines.

## Validación final

- Suite completa FB3+FB4+FB5: verde. `-race`: verde. `go vet`: limpio.
- 7 fuzzers (4 previos + 3 nuevos de inputs del servidor) ~90s c/u: verdes tras el fix del OOM.
- Barrido de 37 bases reales: 663 tablas, 97.149 filas → 0 errores.
- Comparación vs nakagami sobre las 37 bases reales: 0 discrepancias.

## Pendiente para fases posteriores (no Fase 2)

- Fase 2.5: refactors (`wire/types.go` ~2000 líneas, `wire/database.go` ~1200).
- Considerar exponer `lock_timeout` / control de WAIT en el DSN (hoy WAIT fijo) — post-1.0.
