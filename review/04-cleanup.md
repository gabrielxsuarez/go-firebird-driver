# Fase 4 — Limpieza del proyecto (2026-07-03)

Qué se eliminó/cambió y por qué. Commits: `ad8c286` (código muerto), `2cf5f39`
(wire → internal), más el commit de este informe con README/scripts/higiene.

## 1. `docs/` con clones de repos ajenos

- **Nunca estuvo commiteado**: ya estaba en `.gitignore` (`/docs`), así que el repo git no
  cargaba los 104MB — el hallazgo original aplicaba solo al disco.
- Verificación previa al borrado: NO eran duplicados exactos de las carpetas hermanas del
  workspace (`docs/firebirdsql` difería del hermano; `docs/wire-protocol-es` era la
  traducción al español del `wire-protocol` inglés). Se borró del disco **con confirmación
  del usuario** ("quedó vieja").
- Efecto colateral corregido: `internal/timezone/named_zones_generated.go` decía
  "generated from docs/firebirdsql/timezonemap.go" — ahora referencia la fuente upstream
  y documenta cuándo/cómo regenerar.

## 2. Código muerto eliminado (verificado con `deadcode -test` + grep)

En `wire` (commit `ad8c286`, −~600 líneas):

- **Variantes no-pipelined superadas**: `Fetch`, `FetchRows`, `ExecuteAndCommit`,
  `TransactionExecuteCommit`, `AllocateStatement`, `AllocateStatementLazy`,
  `PrepareStatement`, `RollbackRetaining`, `Connect` (sin contexto). Producción usa las
  versiones pipelined (`FetchRowsReuse`, `ExecuteAndCommitRetaining`,
  `AllocateAndPrepareWithItems`, `ConnectContext`).
- **Camino de encode binario de DECFLOAT abandonado** (`stringToDecfloat64/128`,
  `digitsToDpd`, `parseDecimalString`, `formatDecfloat`, `dpdToDigits`): los parámetros
  DECFLOAT se envían como texto por diseño (el BLR lo pide así y
  `TestBuildParamBLRSendsDecfloatAsText` lo asegura). `types_decfloat.go` bajó de 651 a
  289 líneas.
- **Decoders TZ no extendidos** (`TimestampTZToTime`, `TimeTZToTime`): el BLR del cliente
  siempre pide la forma extendida (`BlrExTimestampTZ`), el servidor nunca manda la corta.
- **`bpb.go` completo** (builder de BPB sin ningún uso; los blobs 1.0 usan BPB nil) y
  huérfanos menores (`ReadNullBitset`, `EncodeNamedParamsStack`, `stringToInt128`,
  `applyScale`, `WriteInt32LE` de DPB/TPB).
- **Decisión sobre `//lint:file-ignore U1000` de `protocol.go`**: las constantes de
  services/events/batch/etc. **se quedan** — son el registro declarativo espejo de la
  spec (costo cero, sirven para navegar con la spec al lado). El comentario del ignore
  ahora lo explica. `deadcode -test` queda limpio.

## 3. API pública congelable: `wire` → `internal/wire` (commit `2cf5f39`)

La decisión más importante de la fase. El protocolo no es contrato del driver:

- Ocultarlo ahora es lo conservador (re-exportar después es aditivo; ocultar después de
  1.0 sería breaking).
- La API pública queda en exactamente **5 símbolos**: `Driver`, `Connector`, `Config`,
  `ParseDSN`, `Error` (alias de el status error interno — sigue siendo usable con
  `errors.As`, `GDSCode()`, `SQLState()`).
- Actualizados: imports (13 archivos), `scripts/validate.ps1`, `scripts/profile.py`,
  `RELEASE_CHECKLIST.md`, `COMPATIBILITY.md` (`*wire.StatusError` → `*firebird.Error`).

## 4. Auditoría de archivos de la raíz

- `BENCHMARK_BASELINE.md`: regenerado en Fase 3. ✔
- `README.md`: la tabla comparativa de abril (números no reproducibles, p.ej. Ping de
  nakagami 93ms) se reemplazó por la tabla real de la Fase 3 con link a `bench/compare/`;
  DSN de ejemplo obsoleto corregido; tabla de parámetros DSN verificada contra `dsn.go`
  (completa). ✔
- `COMPATIBILITY.md` / `RELEASE_CHECKLIST.md`: referencias de paths/tipos actualizadas;
  la revisión de contenido de fondo queda para la Fase 5 como estaba planeado.
- `review_bench_baseline.txt`: borrado (superseded por `review/bench/`).

## 5. `scripts/`

- `validate.ps1`: los 4 modos apuntan a los paths nuevos; **modo quick verificado
  end-to-end** (verde contra FB3). Deduplicado `./internal/...` que ya incluye wire.
- `profile.py`: paths actualizados a `internal/wire`; estructura sana (usa el compose,
  chequea puertos, docker o podman).
- **Decisión PowerShell vs Makefile**: se queda PowerShell solo para 1.0. El desarrollo
  del proyecto es Windows-first y la cobertura multiplataforma real la dará el CI de la
  Fase 5 (GitHub Actions corre los mismos comandos `go test`); un Makefile paralelo sería
  una segunda fuente de verdad sin usuarios hoy.

## 6. `docker/`

`docker/firebird/docker-compose.yml` ya define FB3+FB4+FB5 con exactamente la config que
usan los tests (puertos 3063/3064/3065, sysdba/masterkey, `driver.fdb` UTF8) — quedó
alineado desde la Fase 0. Sin cambios.

## 7. Higiene

- `.gitignore`: agregado `*.fdb`/`*.FDB` (bases temporales/bench).
- `go mod tidy`: sin cambios (ya estaba limpio).
- Generados: `internal/errmsg/messages_generated.go` tiene `go:generate` y fuente
  documentada ✔; `named_zones_generated.go` ahora documenta fuente y criterio de
  regeneración (tabla estática, sin generador propio — aceptado).
- `go.mod` exige Go 1.25: la decisión de bajar el mínimo queda para la Fase 5 (CI puede
  probar con versiones anteriores antes de decidir).

## Validación

`go build`, `go vet`, `deadcode -test` limpio, suite completa FB3+FB4+FB5 verde tras cada
commit, `validate.ps1 -Mode quick` verde end-to-end.
