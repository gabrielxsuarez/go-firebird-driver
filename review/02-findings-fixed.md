# Fase 1-2 — Hallazgos verificados y corregidos (2026-07-02)

Todos los hallazgos de los informes `01a-wire-matrix.md` y `01b-database-sql-audit.md` fueron
verificados adversarialmente con reproducción contra servidores reales antes de arreglarse.
Cada fix tiene test de regresión en `regression_test.go` / `wire/handshake_keys_test.go`.

## Bugs corregidos (commit f34a921)

| # | Bug | Gravedad | Evidencia de reproducción |
| --- | --- | --- | --- |
| 1 | BOOLEAN param: byte significativo al final → `true` llegaba como `false` | **Corrupción de datos** | roundtrip devolvió (false,false) |
| 2 | DATE/TIMESTAMP: encode mezclaba fecha UTC con hora de pared → fecha corrida ±1 día con zonas no-UTC | **Corrupción de datos** | 15-ene 23:30 UTC-3 se guardó como 16-ene |
| 3 | Aritmética de fechas con `time.Duration` (satura ±292 años) → año 3000 se guardaba como 2151, año 1 como 1566 | **Corrupción de datos** | verificado |
| 4 | Error del server a mitad de fetch → "unexpected opcode 9", error real perdido, conexión desincronizada e inutilizable en el pool | **Alta** | división por cero en fila 300: error perdido + conexión rota |
| 5 | `stmt.Exec` preparado en autocommit: commit diferido a `stmt.Close()` que database/sql nunca llama → INSERTs invisibles/no durables | **Alta** | count=0 desde otra conexión |
| 6 | DML vía Query (`INSERT..RETURNING` con QueryRow, UPDATE vía Query) nunca commiteaba | **Alta** | count=0 desde otra conexión |
| 7 | `RowsAffected` lazy con I/O de wire sin lock sobre conexión del pool | Alta (carrera) | por inspección + fix eager |
| 8 | Args faltantes → NULL silencioso; args sobrantes ignorados | Media | fila insertada con NULL |
| 9 | `uint64 > MaxInt64` → wraparound negativo silencioso | Media | 2^63 → -9223372036854775808 |
| 10 | String no numérica → FLOAT 0.0 silencioso; no-time → zero-time silencioso | Media | "not-a-number" → 0 |
| 11 | `sql.Named` bindeado posicionalmente en silencio | Media | aceptado sin error |

## Bugs corregidos (commit d3e8972)

| # | Bug | Gravedad |
| --- | --- | --- |
| 12 | Las keys de wire-crypt del server llegaban en `resp.Data` del último op_cont_auth y se descartaban → **nunca se negociaba ChaCha**, siempre RC4 | Alta (seguridad) |
| 13 | Parser de `p_acpt_keys` con formato inventado (el real es clumplet `[tag][len][data]`) | (causa del 12) |
| 14 | Clave ChaCha con doble SHA-256 (spec/jaybird: uno solo) | Alta (latente) |
| 15 | `net.Dial` sin contexto → timeout de conexión de database/sql ignorado | Media |
| 16 | `wire_crypt=required` sin session key seguía en texto plano sin error | Media (seguridad) |
| 17 | Server que exige plugin no soportado (Legacy_Auth): se renombraba el plugin y se mandaba data SRP → isc_login confuso; ahora error claro | Baja |
| 18 | Errores GDS: solo se imprimía la primera entrada de la cadena (casi siempre sin texto); ahora se formatea la cadena completa con nombres de objetos y SQLCODE | Media (DX) |

Verificación post-fix:
- Suite completa FB3+FB4+FB5: verde (0 skips). `-race`: verde. `go vet`: limpio.
- FB4/FB5 ahora negocian **ChaCha20** verificado end-to-end (antes RC4); FB3 queda en Arc4 (lo único que soporta).
- Barrido de 37 bases reales: 663 tablas, 97.149 filas, 533.484 valores → **0 errores**.
- Comparación contra nakagami/firebirdsql en las 37 bases reales (200 filas/tabla, valor por valor):
  **0 discrepancias** (herramienta en scratchpad `oracle/`, formato canónico por líneas).

## Diferencias de comportamiento vs nakagami detectadas (documentar, no bugs)

- `time.Time` de columnas DATE/TIMESTAMP sin zona: nuestro driver devuelve Location=UTC,
  nakagami devuelve Location=Local. Misma hora de pared. → documentar en guía de migración.

## Decisiones pendientes (requieren al usuario)

1. **Tabla de mensajes de error**: hoy los errores muestran códigos GDS + parámetros pero no el
   texto ("arithmetic exception..."). nakagami embebe una tabla generada de 2944 mensajes
   (~212KB de fuente, licencia IPL, generada de `all.h` de Firebird). Opciones:
   (a) embeber tabla completa como nakagami; (b) dejar códigos+params como está;
   (c) tabla parcial con los ~100 errores más comunes.
2. **Dialecto 1**: el DSN acepta `dialect=1` pero prepare hardcodea dialecto 3
   (`wire/database.go:300,346`). Las 9 bases dialecto 1 reales funcionan correctamente vía
   dialecto 3 de cliente (verificado con 0 diffs vs nakagami, que hace lo mismo). Opciones:
   (a) propagar el dialecto del DSN; (b) rechazar `dialect=1` con error; (c) documentar que
   el parámetro se ignora.
3. **Describe truncation**: buffer fijo de 65535 para metadata de prepare sin detección de
   `isc_info_truncated` → SELECTs de cientos de columnas fallarían confuso. ¿Implementar
   re-fetch con buffer mayor o documentar límite?

## Hallazgos menores pendientes (dudosos H11-H19 de 01b, no urgentes)

- Niveles de aislamiento desconocidos degradan a Default en silencio; RepeatableRead→SNAPSHOT sin documentar.
- `getRowsAffected` traga errores de transporte sin markBad.
- `ColumnTypeLength` devuelve bytes, no chars.
- `descCache` puede quedar stale tras DDL ejecutado por otra conexión.
- `ResetSession` no detecta transacción explícita huérfana.
- `FirebirdError` en errors.go es código muerto (los errores reales son `*wire.StatusError`) → decidir API pública de errores antes de 1.0.
- `ColumnTypeScanType` declara float64 para NUMERIC escalado pero entrega string.
