# Fase 1b — Auditoría de comportamiento de las interfaces database/sql/driver (2026-07-02)

Alcance: `driver.go`, `connection.go`, `statement.go`, `rows.go`, `result.go`, `transaction.go`,
`transport.go`, `dsn.go`, `errors.go`, con seguimiento a `wire/handshake.go`, `wire/database.go`,
`wire/types.go`, `wire/info.go`, `wire/status.go`.

Formato por punto: **Contrato esperado** | **Comportamiento actual** (evidencia `archivo:línea`) | **Veredicto**.

Leyenda de veredictos: **BUG** (violación del contrato o corrupción/pérdida de datos),
**DUDOSO** (funciona pero contradice el espíritu del contrato o depende de suerte),
**FALTA** (capacidad esperada ausente), **OK**.

---

## Resumen ejecutivo

| # | Hallazgo | Veredicto | Severidad |
|---|----------|-----------|-----------|
| H1 | `stmt.ExecContext` difiere el commit del auto-tx a `stmt.Close()`: escrituras vía `db.Prepare`+`stmt.Exec` quedan **sin commitear** (invisibles para otras conexiones, no durables) mientras la conexión rota por el pool | BUG | Crítica |
| H2 | `wire.Connect` no recibe `ctx`: dial TCP + handshake SRP + attach sin timeout ni cancelación | BUG | Alta |
| H3 | Args faltantes en Execer/Queryer se bindean como **NULL silencioso**; args sobrantes se ignoran | BUG | Alta |
| H4 | `sql.Named()` se ignora por completo: bindeo posicional silencioso sin error | BUG | Alta |
| H5 | `CheckNamedValue`: `uint64 > MaxInt64` → wraparound negativo silencioso | BUG | Alta |
| H6 | `string`→FLOAT/DOUBLE y no-time→fecha se convierten a `0`/zero-time silenciosamente (`toFloat64`/`toTime` sin error) | BUG | Alta |
| H7 | `result.RowsAffected()` lazy (path prepared stmt) hace I/O de wire **sin `conn.mu`** → desync de protocolo si el pool ya reasignó la conexión | BUG | Alta |
| H8 | `invalidateAutoTx` abandona la transacción server-side sin rollback → leak de transacciones (OIT/OAT) en cada error de prepare/exec autocommit | BUG | Media |
| H9 | `FirebirdError`/`NewFirebirdError` es código muerto: los errores reales son `*wire.StatusError`; `errors.As(*FirebirdError)` nunca matchea | BUG (API) | Media |
| H10 | `ColumnTypeScanType` declara `float64` para NUMERIC/DECIMAL escalados pero el driver entrega `string` | BUG | Media |
| H11 | `getRowsAffected` traga errores (incl. de transporte, sin `markBad`) y devuelve 0 | DUDOSO | Media |
| H12 | Niveles de aislamiento desconocidos degradan silenciosamente a Default; `RepeatableRead`→SNAPSHOT silencioso (upgrade) | DUDOSO | Media |
| H13 | `BeginTx` ignora el `ctx` (sin watcher ni chequeo previo); commit del autoTx pendiente con error ignorado | FALTA | Media |
| H14 | Sin chequeo temprano de `ctx.Err()` en Exec/Query/Prepare: carrera entre op_cancel y la operación; cancel puede quedar pendiente y afectar la siguiente op | DUDOSO | Media |
| H15 | `ColumnTypeLength` devuelve bytes, no caracteres (UTF8 CHAR(10) → 40) | DUDOSO | Baja |
| H16 | `isc_info_truncated` en el describe se trata como fin silencioso → descriptores incompletos en tablas muy anchas | DUDOSO | Baja |
| H17 | `descCache` por texto SQL puede quedar stale tras DDL (BLR de descriptores viejos) | DUDOSO | Baja |
| H18 | `ResetSession` solo hace ping: no detecta tx huérfana, no resetea estado de sesión, y agrega un round-trip por checkout | DUDOSO/FALTA | Baja |
| H19 | `wrapBadConn` pierde la cadena del error original (`%v`) → `errors.As` sobre la causa no funciona en errores retryables | DUDOSO | Baja |

Lo que está **bien** (y es lo difícil de lograr): cancelación vía `op_cancel` real que mantiene el
wire sincronizado sin quemar la conexión; disciplina ErrBadConn correcta en los paths de
ejecución (retryable solo antes de escribir efectos, fatal después); `LastInsertId` con error
explícito; `Close`/`Commit`/`Rollback` idempotentes; las 5 interfaces opcionales de Rows
implementadas; pool de handles y descCache correctamente serializados bajo `conn.mu`.

---

## 1. driver.DriverContext / driver.Connector

**Contrato**: `OpenConnector` parsea el DSN una sola vez; `Connector.Connect(ctx)` debe respetar
`ctx` (timeout/cancelación de la conexión completa, incluido dial y handshake) y devolver
`ctx.Err()` si expira.

**Comportamiento actual**:
- `OpenConnector` parsea una vez y guarda `*Config` (`driver.go:56-62`); `Connect` reutiliza
  (`driver.go:73-75`). `Driver.Open` delega en el connector (`driver.go:46-52`). Nadie muta el
  `Config` compartido después (`connection.go:115-130` construye un `wire.ProtocolConfig` nuevo
  por conexión). ✔
- `newConnection` solo consulta `ctx` en el sleep de reintento por fallo transitorio de SRP
  (`connection.go:145-149`). La conexión en sí — `wire.Connect` — hace `net.Dial("tcp", addr)`
  **sin timeout ni ctx** (`wire/handshake.go:96`), y todo el handshake (op_connect, vueltas SRP,
  op_crypt, op_attach: `wire/handshake.go:119-341`) se ejecuta sin deadlines. Un servidor que
  acepta TCP y no responde bloquea `Connect` indefinidamente; `context.WithTimeout` en
  `db.PingContext`/`db.Conn` no lo interrumpe (database/sql abandona la espera pero la goroutine
  del opener queda colgada).

**Veredicto**: parseo único **OK**; respeto de ctx en Connect **BUG (H2)**. Corrección: usar
`(&net.Dialer{}).DialContext(ctx, ...)` + `SetDeadline` derivado de `ctx` durante el handshake.

---

## 2. ConnBeginTx — mapeo sql.IsolationLevel → TPB

**Contrato**: niveles no soportados deben devolver error (nunca degradar en silencio);
`ReadOnly` debe respetarse; `ctx` aplica al inicio de la transacción.

**Comportamiento actual** (`buildTPB`, `connection.go:741-776`; los valores literales 0..7
coinciden con las constantes de `database/sql`):

| sql.IsolationLevel | TPB resultante | Evidencia | Veredicto |
|---|---|---|---|
| Default (0) | `read_committed + rec_version + wait` | `connection.go:744-748, 693-706` | OK |
| ReadUncommitted (1) | `errReadUncommitted` | `connection.go:764-765, 53` | OK (error, no degrada) |
| ReadCommitted (2) | `read_committed + rec_version` (sin `wait` explícito; el default del servidor es wait) | `connection.go:749-753, 707-718` | OK (inconsistencia cosmética con Default, que sí lleva `isc_tpb_wait`) |
| WriteCommitted (3) | `errWriteCommitted` | `connection.go:766-767` | OK |
| RepeatableRead (4) | `isc_tpb_concurrency` (SNAPSHOT) | `connection.go:754-758` | **DUDOSO**: upgrade silencioso a un nivel más fuerte. Defendible (SNAPSHOT ⊇ repeatable read) pero debería documentarse en README/COMPATIBILITY |
| Snapshot (5) | `isc_tpb_concurrency` | `connection.go:754-758, 719-728` | OK |
| Serializable (6) | `isc_tpb_consistency` | `connection.go:759-763, 729-739` | OK |
| Linearizable (7) | `errLinearizable` | `connection.go:768-769` | OK |
| **default** (cualquier otro valor) | degrada a Default **en silencio** | `connection.go:770-775` | **BUG menor (H12)**: debe ser `fmt.Errorf("isolation level %d not supported")` |

- `ReadOnly` se respeta en todas las ramas (`isc_tpb_read`/`isc_tpb_write`). ✔
- `BeginTx` **ignora `ctx`** por completo: sin `withCancel`, sin chequeo de `ctx.Err()`
  (`connection.go:261-292`). Un `BeginTx` con ctx ya cancelado ejecuta igual. **FALTA (H13)**.
- Antes de abrir la tx explícita commitea el autoTx pendiente con `_ = c.wc.Commit(...)`
  (`connection.go:270-274`): si ese commit falla por transporte, el error se descarta y el
  fallo aparecerá recién en `wc.Transaction`; si falla por otra razón, datos "dirty" del caso H1
  se pierden sin señal. **DUDOSO**.

---

## 3. ExecerContext / QueryerContext

**Contrato**: pueden recibir args vacíos o poblados; el driver debe ejecutar la sentencia o
devolver `driver.ErrSkip` para que database/sql use el path prepared. No hay validación de
NumInput por parte de database/sql en este path — la responsabilidad de validar el conteo de
args es del driver.

**Comportamiento actual**:
- Ambos implementados como allocate+prepare transitorio por llamada
  (`connection.go:295-404`, `407-510`), con dos cachés:
  - pool de hasta 8 handles de statement reutilizables (`wire/database.go:325-336, 763-776`),
  - `descCache` de 32 slots por texto SQL para descriptores (`connection.go:69-105`), usado solo
    por `QueryContext` (`connection.go:439-443`); `ExecContext` re-parsea siempre
    (`connection.go:326`). Inconsistencia menor.
- Liberación del statement transitorio: `RecycleStatement` en éxito
  (`connection.go:360, 395`), `FreeStatement(DSQLDrop)` en error (`connection.go:335, 345,...`);
  en `QueryContext` el handle se recicla al cerrar las rows (`autoFreeStmt`, `rows.go:98-99`). ✔
- **Validación de conteo de args: no existe.** El gate es `len(inputs) > 0 && len(args) > 0`
  (`connection.go:330, 447`):
  - `args` parciales (p.ej. 1 arg para 2 placeholders): `EncodeNamedParams*` marca **NULL** para
    todo índice `i >= len(values)` (`wire/types.go:381-386, 469-474`) → el INSERT/UPDATE se
    ejecuta con NULLs no solicitados. **BUG (H3)** — corrupción de datos silenciosa si la
    columna admite NULL.
  - `args` sobrantes: ignorados en silencio (el loop itera sobre `descs`). **BUG (H3)**.
  - 0 args con placeholders requeridos: se ejecuta sin mensaje de parámetros
    (`blr=nil, message count 0`, `wire/database.go:380-385`) → error del servidor (críptico,
    pero al menos no silencioso).
- Fallo de prepare → `handleRetryableErrorLocked` (ErrBadConn solo si transporte): correcto,
  nada se ejecutó aún. Fallo post-execute/commit → `handleFatalErrorLocked` (sin ErrBadConn):
  correcto (`connection.go:319-323, 352-357, 378-391`).
- En cada rama de error autocommit se llama `invalidateAutoTx()` (`connection.go:250-253`), que
  **pone `autoTx=0` sin hacer rollback**: la transacción sigue viva en el servidor hasta el
  detach. Cada query con error de sintaxis/constraint en modo autocommit filtra una transacción
  → gap OIT/OAT creciente, impacto en GC/sweep del servidor en procesos de vida larga.
  **BUG (H8)**.

**Veredicto**: estructura y liberación **OK**; validación de args **BUG (H3)**; leak de
transacciones **BUG (H8)**; staleness del descCache tras DDL **DUDOSO (H17)** (el BLR se
construye de descriptores cacheados de un prepare anterior — el servidor coerciona al BLR
pedido, pero si cambió el número de columnas el decode se desincroniza).

---

## 4. Pinger

**Contrato**: verificar la conexión con un round-trip real; devolver `driver.ErrBadConn` si la
conexión está caída (para que el pool la descarte).

**Comportamiento actual**: `Ping` → `pingLocked` → `wc.InfoDatabase(isc_info_base_level)`
(`connection.go:513-523, 605-615`; `wire/database.go:105-121`) — round-trip
`op_info_database` real (no hay `op_ping` como tal en el protocolo; esto es el equivalente
estándar). Con `closed/bad` devuelve `ErrBadConn` inmediato; errores de transporte pasan por
`handleRetryableErrorLocked` → `markBadLocked` + `wrapBadConn` (`connection.go:617-650`,
`transport.go:45-50`). Respeta ctx vía `withCancel` (`connection.go:610-611`).

**Veredicto**: **OK**.

---

## 5. SessionResetter / Validator

**Contrato**: `ResetSession` se invoca antes de reusar una conexión del pool; debe devolver
`ErrBadConn` si la conexión no sirve y dejar la sesión en estado limpio. `IsValid` se invoca al
devolver la conexión al pool.

**Comportamiento actual**:
- `ResetSession` = ping (`connection.go:526-534`). No resetea **nada**:
  - No detecta ni aborta una `activeTx` huérfana (si `activeTx != 0` al volver al pool, algo se
    rompió; hoy pasaría desapercibido y la próxima query correría dentro de esa tx). En la
    práctica database/sql garantiza el Rollback del `sql.Tx`, pero un assert defensivo
    (`activeTx != 0 → rollback + return ErrBadConn`) costaría dos líneas. **FALTA**.
  - **No commitea `dirtyAutoTx`** — y aquí está el hallazgo más grave de la auditoría:
    `stmt.ExecContext` en modo autocommit **no commitea**; marca `dirtyAutoTx=true` y difiere el
    commit a `stmt.Close()` (`statement.go:141-159`, comentario "commit is deferred to
    stmt.Close"; el commit está en `statement.go:46-55`). Con el patrón estándar
    `stmt, _ := db.Prepare("INSERT ..."); stmt.Exec(...)`, database/sql **mantiene el driver.Stmt
    cacheado por conexión y no lo cierra tras cada Exec**. Resultado: el Exec retorna éxito, la
    conexión vuelve al pool con el INSERT **sin commitear**, invisible para cualquier otra
    conexión (READ COMMITTED) y **no durable** (crash del proceso = datos perdidos, pese al éxito
    reportado). El commit ocurre recién si (a) se cierra el `sql.Stmt`, (b) otra operación
    autocommit de conexión (`conn.ExecContext`/`BeginTx`/`conn.Close`) reutiliza esa misma
    conexión, o (c) nunca. **BUG crítico (H1)**. Mitigación mínima: commitear `dirtyAutoTx` en
    `ResetSession`; corrección real: `CommitRetaining` al final de cada `stmt.ExecContext`
    autocommit (como hace el path Execer con `ExecuteAndCommitRetaining`,
    `connection.go:351-357`).
  - Tampoco resetea estado de sesión servidor (p.ej. `SET SESSION TZ`, roles) — FB4+ tiene
    `ALTER SESSION RESET`. **FALTA** (menor, documentable).
  - Nota de costo: un round-trip de red por **cada** checkout del pool duplica la latencia de
    queries cortas. La mayoría de los drivers hacen `ResetSession` sin I/O y dejan la detección
    a `IsValid` + el primer uso.
- `IsValid` = `!closed && !bad` (`connection.go:537-539`), sin I/O — correcto para su rol. Se lee
  sin mutex; en la práctica el pool secuencia los accesos, pero es una carrera teórica que el
  race detector podría señalar si algún día un watcher marca `bad` fuera de `mu` (hoy
  `markBadLocked` siempre corre bajo `mu`). **OK** con nota.

**Veredicto**: `IsValid` **OK**; `ResetSession` **DUDOSO/FALTA (H18)**; commit diferido de
prepared statements **BUG crítico (H1)**.

---

## 6. NamedValueChecker — matriz de tipos

**Contrato**: convertir/aceptar valores o devolver error claro; `driver.ErrSkip` delega en el
convertidor por defecto de database/sql. Named args no soportados deben rechazarse.

**Comportamiento actual** (`CheckNamedValue`, `connection.go:542-570` + capa wire
`wire/types.go:762-899, 941-985, 1133-1172`):

| Tipo | Comportamiento | Veredicto |
|---|---|---|
| `nil` | aceptado; null bit en encode (`types.go:381-386`) | OK |
| `int`, `int8/16/32`, `int64` | → `int64`; overflow contra SMALLINT/INTEGER chequeado en encode (`types.go:771-784`) | OK |
| `uint`, `uint8/16/32` | → `int64` sin pérdida | OK |
| `uint64` | `nv.Value = int64(v)` **sin chequear > MaxInt64** (`connection.go:562-563`) → wraparound negativo silencioso. El chequeo que sí existe en `numericInt64` (`types.go:964-968`) nunca ve un `uint64` porque CheckNamedValue ya lo destruyó. Nótese que el convertidor default de database/sql **rechaza** uint64 con bit alto; el driver lo empeora | **BUG (H5)** |
| `float32` | → `float64` | OK |
| `float64` | aceptado; a NUMERIC va por string con redondeo controlado (`types.go:969-972`) | OK |
| `bool` | aceptado; a BOOLEAN ✔; a numérico 1/0 (`types.go:973-977`) | OK |
| `string` | aceptado. A CHAR/VARCHAR: encode con charset y error por longitud (`types.go:483-519, 528-544`) ✔. A NUMERIC/DECIMAL: parseo decimal estricto con error (`types.go:978-981, 1000-1009`) ✔ — decimales como string funcionan. **Pero** a FLOAT/DOUBLE: `toFloat64` devuelve **0 silencioso** (`types.go:793-801, 1133-1148`), y a DATE/TIME/TIMESTAMP: `toTime` devuelve **zero time silencioso** (`types.go:811-822, 1165-1172`) | **BUG (H6)** |
| `[]byte` | aceptado; a VARCHAR/CHAR sin re-encode (raw) ✔; a BLOB se materializa antes (`connection.go:574-603`) ✔ | OK |
| `time.Time` | aceptado; DATE/TIME/TIMESTAMP/TZ ✔ (`types.go:811-822, 874-885`) | OK |
| `driver.Valuer` | `default:` → `driver.ErrSkip` (`connection.go:566-567`) → el convertidor default resuelve el Valuer y reintenta | OK |
| **Named args** (`sql.Named`, placeholders `@p`/`:p`) | `nv.Name` **no se consulta en ningún punto del driver** (grep: cero usos fuera de tests). `sql.Named("a", v)` se bindea **por posición de aparición en la llamada**, sin error. Firebird solo soporta `?`, así que un SQL con `:name` falla en el servidor, pero `db.Exec("... VALUES (?, ?)", sql.Named("x", 1), sql.Named("y", 2))` "funciona" con semántica posicional accidental | **BUG (H4)**: `CheckNamedValue` debe devolver `error` si `nv.Name != ""` |

---

## 7. StmtExecContext / StmtQueryContext

**Contrato**: `NumInput()` exacto (database/sql valida el conteo de args antes de llamar);
`Close()` idempotente; statement inutilizable tras cerrar la conexión debe fallar limpio;
reuso tras error debe funcionar.

**Comportamiento actual**:
- `NumInput() = len(s.inputs)` de los descriptores del prepare (`statement.go:61-63`). Correcto —
  y hace que el path prepared **sí** esté protegido contra el mismatch de args que afecta al
  path Execer (punto 3). **OK**.
- `Close()` idempotente (`s.closed`, `statement.go:35-37`); con conexión cerrada/bad devuelve
  `nil` sin tocar el wire (`statement.go:39-41`). Recicla el handle con `hasCursor=false`
  (`statement.go:43`): si el usuario cierra el stmt con rows aún abiertas, un handle con cursor
  abierto entra al pool y el próximo `PrepareStatement` sobre él fallará en el servidor
  ("cursor open"). database/sql normalmente cierra rows primero, pero el orden no está
  garantizado con `sql.Stmt` compartidos. **DUDOSO** (menor).
- Uso tras `conn.Close()`: `ExecContext/QueryContext` devuelven `driver.ErrBadConn`
  (`statement.go:82-84, 173-175`) → database/sql reintenta en otra conexión re-preparando: el
  comportamiento resultante es correcto. Que `s.closed` (stmt cerrado, conexión sana) también
  devuelva `ErrBadConn` es discutible — es un error de uso, no una conexión mala; provoca un
  retry innecesario — pero es inofensivo porque database/sql nunca llama con stmt cerrado.
  **DUDOSO** (cosmético).
- Reuso tras error: el handle sigue preparado tras un error de ejecución (no se libera,
  `statement.go:150-155`), Firebird permite re-ejecutar. **OK**.
- `stmt.ExecContext` autocommit: **no commitea** (`statement.go:141-159`) → ver H1 (punto 5).
  Además devuelve un `result` **lazy** (`computed=false`, `statement.go:161-165`) → ver H7
  (punto 9).
- Blobs como parámetros: materialización previa con la tx correcta, incluida la creación
  anticipada del autoTx solo si hay blobs (`statement.go:94-118`). **OK**.
- `Exec`/`Query` legacy convierten a NamedValue con ordinales (`statement.go:66-75, 271-286`).
  Nota: la variante "fast path" devuelve un slice respaldado por un array de stack
  (`stackNamed[:len(args)]`, `statement.go:273-278`) — en Go esto **escapa y se aloca en heap**
  igualmente; es solo ruido, no un bug.

**Veredicto**: NumInput/idempotencia/reuso **OK**; commit diferido **BUG (H1)**; result lazy
**BUG (H7)**; recycle con cursor abierto **DUDOSO**.

---

## 8. Rows

**Contrato**: `Columns()` estable; `Close()` a mitad de fetch debe liberar el cursor (drenar o
cancelar) dejando la conexión reutilizable; `Next` tras EOF debe devolver `io.EOF`
consistentemente; interfaces opcionales con valores veraces.

**Comportamiento actual**:
- `Columns()`: alias con fallback a field name, cacheado (`rows.go:60-80`). **OK**.
- `Close()` a mitad de fetch: **cancela** server-side — `DSQLClose` del cursor (vía
  `RecycleStatement(handle, true)` para ad-hoc o `FreeStatement(DSQLClose)` para prepared,
  `rows.go:97-103`; `wire/database.go:763-776, 788-807`). No drena filas pendientes por el
  socket (el fetch de Firebird es pull, no hay filas en vuelo sin pedir) → conexión queda
  sincronizada. Idempotente (`rows.go:86-89`). **OK**.
- `Next` tras EOF: `eof=true` + buffer agotado → `io.EOF` (`rows.go:126-135`); tras `Close` →
  `io.EOF` (`rows.go:118-120`). No hay re-lectura accidental. **OK**.
- Chequea `ctx` en cada `Next`/`fetch` y traduce el error de cancelación del servidor al
  `ctx.Err()` (`rows.go:121-123, 179-186`). **OK**.
- Blobs se materializan durante el fetch bajo `conn.mu` (`rows.go:192-216`). **OK**.

Interfaces opcionales (todas implementadas, `rows.go:50-57`):

| Interfaz | Comportamiento | Veredicto |
|---|---|---|
| `ColumnTypeDatabaseTypeName` | mapeo completo CHAR/VARCHAR/SMALLINT/INTEGER/BIGINT/INT128/NUMERIC (scale<0 o subtype 1)/FLOAT/DOUBLE PRECISION/fechas/TZ/BLOB (+SUB_TYPE TEXT)/BOOLEAN/DECFLOAT (`rows.go:250-309`). No distingue NUMERIC de DECIMAL (subtype 2 también reporta "NUMERIC") | OK (nota menor) |
| `ColumnTypeLength` | CHAR/VARCHAR: `col.Length` = **bytes**, no caracteres (`rows.go:319-321`): con UTF8, `CHAR(10)` reporta 40. BLOB: `(0,false)` en vez de `(MaxInt64,true)` que usan otros drivers para "variable length" | **DUDOSO (H15)**: dividir por bytes-por-char del charset; BLOB debería reportar variable |
| `ColumnTypeNullable` | del descriptor (`rows.go:329-334`) | OK |
| `ColumnTypePrecisionScale` | solo si `scale<0`; precisión = capacidad del tipo de almacenamiento (4/9/18/38), no la declarada en el DDL (`rows.go:337-356`) — el wire protocol no trae la precisión declarada, para eso haría falta consultar RDB$FIELDS | OK con nota (documentar); `NUMERIC(10,0)` reporta `(0,0,false)` por `scale=0` — coherente con la limitación |
| `ColumnTypeScanType` | CHAR/VARCHAR OCTETS→`[]byte`, resto→`string` ✔ (charset id 1 = OCTETS); FLOAT→`float32` ✔ (coincide con `DecodeColumn`, `types.go:169-171`); **pero NUMERIC escalado (Short/Long/Int64 con scale<0) declara `float64`** (`rows.go:371-385`) **y el driver entrega `string`** (`DecodeColumn` → `scaledInt64` → string, `types.go:148-167, 1212`). INT128/DECFLOAT→`string` ✔ | **BUG (H10)**: `sql.Rows.Scan` guiado por ScanType (p.ej. `*any` + reflexión en ORMs) recibe string donde el driver prometió float64. Corrección: declarar `string` (el decode a string exacto es la decisión correcta, es el metadato el que miente) |

- Robustez del describe: `ParseSQLDescribeInfo` trata `isc_info_truncated` igual que
  `isc_info_end` (`wire/info.go:207-209`) con buffer fijo de 64KB
  (`connection.go:192, 318, 430`): una SELECT de cientos de columnas con nombres largos puede
  truncar y producir menos descriptores que columnas reales → BLR incompleto → error o desync.
  nakagami re-pide con buffer mayor. **DUDOSO (H16)**.

---

## 9. Result

**Contrato**: `LastInsertId` → error explícito si el motor no lo soporta. `RowsAffected` veraz
por tipo de sentencia; ambos utilizables después de que la conexión volvió al pool sin romper
nada.

**Comportamiento actual**:
- `LastInsertId`: error explícito con sugerencia de `RETURNING` (`result.go:24-26`). **OK**.
- `RowsAffected` por tipo (`getRowsAffected`, `connection.go:780-798` sobre
  `isc_info_sql_records`): INSERT→insert, UPDATE→update, DELETE→delete, default (MERGE, EXECUTE
  PROCEDURE, DDL, UPDATE OR INSERT)→suma insert+update+delete (DDL suma 0). Coincide con la
  práctica de otros drivers. **OK**.
- Path Execer (`conn.ExecContext`): el count se computa **antes** de reciclar el handle y se
  cachea (`computed:true`, `connection.go:359-368, 394-403`). **OK**.
- Path prepared (`stmt.ExecContext`): devuelve `result` **lazy** (`computed:false`,
  `statement.go:161-165`). `RowsAffected()` dispara entonces `InfoSQL` — I/O de wire — **sin
  tomar `conn.mu` y sin chequear closed/bad** (`result.go:31-39`). database/sql permite usar el
  `sql.Result` después de que la conexión volvió al pool: si otra goroutine está usando esa
  conexión, dos escritores/lectores concurrentes desincronizan el protocolo (el `writeMu` de
  wire solo serializa el flush, no el par request/response). Además, si el mismo stmt se
  re-ejecutó, devuelve el count de la **última** ejecución; y si el handle fue reciclado y
  re-preparado con otro SQL, el count es de otra sentencia. **BUG (H7)**. Corrección: computar
  eager también en el path prepared (como ya hace el Execer), o retener `conn` y tomar `mu`.
- `getRowsAffected` **traga todos los errores** y devuelve 0 (`connection.go:781-784`): un error
  de transporte ahí no marca la conexión como bad → conexión con wire potencialmente
  desincronizado vuelve al pool (fallará en el próximo uso, pero con un error confuso).
  `driver.Result.RowsAffected` permite devolver error; debería. **DUDOSO (H11)**.

---

## 10. Cancelación por contexto

**Contrato**: ctx cancelado antes → no ejecutar; cancelado durante → interrumpir y devolver
`ctx.Err()`; la conexión debe quedar o bien sincronizada y reutilizable, o bien marcada bad —
nunca desincronizada dentro del pool.

**Comportamiento actual**:
- Mecanismo: goroutine watcher por operación (`withCancel`, `connection.go:656-676`) que envía
  **`op_cancel` con `fb_cancel_raise`** por el mismo socket (`wire/database.go:146-166`) — es el
  mecanismo correcto del protocolo, no corta el socket. La operación en curso recibe
  `isc_cancelled` como respuesta normal → el wire queda **sincronizado** y la conexión sigue
  siendo válida para el pool (el error no es de transporte, así que `handleFatalErrorLocked` no
  marca bad — comportamiento deseado aquí). `stop()` espera la salida del watcher antes de
  retornar (`connection.go:672-675`). `rows.fetch` traduce el error a `ctx.Err()` cuando aplica
  (`rows.go:179-186`). Diseño **correcto**.
- Escrituras del watcher serializadas con la operación principal vía `cancelMu` + `writeMu`
  (`wire/database.go:36-42, 95-100, 146-165`) — sin carrera de bytes en el socket. **OK**.
- ctx cancelado **antes** de llamar: no hay chequeo `ctx.Err()` al inicio de
  `ExecContext`/`QueryContext`/`PrepareContext`/`Ping` — el watcher dispara `op_cancel`
  inmediatamente y **corre una carrera** contra la propia operación: según el timing, la
  sentencia puede ejecutarse completa y devolver éxito con un ctx ya cancelado. **DUDOSO (H14)**
  — un `if err := ctx.Err(); err != nil { return nil, err }` tras tomar `mu` lo resuelve.
- Cancelación **entre** el fin real de la operación y `stop()`: el watcher puede alcanzar a
  enviar `op_cancel` cuando ya no hay nada que cancelar. Si el servidor tratara el raise como
  pendiente, la **siguiente** operación en esa conexión (posiblemente de otro consumidor del
  pool) podría recibir un `isc_cancelled` espurio. Ventana pequeña y dependiente de la semántica
  del servidor; merece un test de estrés dedicado. **DUDOSO (H14)**.
- `BeginTx`: sin soporte de ctx (ver punto 2). **FALTA (H13)**.
- ¿Queda la conexión "mala" tras cancelar? No — y eso es lo correcto con `op_cancel`: la
  conversación request/response se completó. Los errores de transporte durante una cancelación
  (p.ej. socket muerto) sí marcan bad por la vía normal. **OK**.

---

## 11. Auditoría de driver.ErrBadConn

**Regla**: devolver `ErrBadConn` solo cuando database/sql puede reintentar sin riesgo de efecto
duplicado (nada escrito con efectos); nunca tras enviar un Exec. Inverso: todo error de red debe
terminar marcando la conexión para que el pool no la reuse.

Puntos donde se devuelve (todos auditados):

| Punto | Evidencia | ¿Seguro reintentar? |
|---|---|---|
| Guards `closed/bad` al entrar a Prepare/Exec/Query/Ping/BeginTx/stmt.* | `connection.go:172-174, 265-267, 299-301, 411-413, 517-519, 530-532`; `statement.go:82-84, 173-175` | Sí — nada enviado. **OK** |
| `handleRetryableErrorLocked` en fallo de `getAutoTx` (op_transaction) | `connection.go:186-188, 241-243, 311-314, 421-426` | Sí — una tx creada-y-abandonada no tiene efectos visibles (solo contribuye al leak H8 si efectivamente se creó). **OK** |
| `handleRetryableErrorLocked` en fallo de allocate/prepare | `connection.go:193-197, 319-323, 430-435` | Sí — prepare no tiene efectos. **OK** |
| `pingLocked` | `connection.go:605-614` | Sí. **OK** |
| Ejecución/commit/fetch/blobs → `handleFatalErrorLocked` (marca bad, **no** devuelve ErrBadConn) | `connection.go:336, 356, 383, 390`; `statement.go:111, 154`; `rows.go:99-102, 181, 205`; `transaction.go:28, 47` | Correcto: efectos posibles → error real + `markBadLocked` → `IsValid()=false` → pool descarta. **OK** |
| `transaction.Commit/Rollback` con conn bad → `ErrBadConn` | `transaction.go:24-27, 43-46` | El commit nunca se envió; database/sql no reintenta commits, solo descarta la conexión. Aceptable. **OK** (nota: el estado real de la tx en el servidor es "se hará rollback al reconectar/timeout") |
| `stmt.ExecContext` con `s.closed` → `ErrBadConn` | `statement.go:82` | Uso inválido reportado como conexión mala — inofensivo pero semánticamente incorrecto. **DUDOSO** |

Inverso (errores de red que **no** marcan bad):

- `getRowsAffected` ignora el error de `InfoSQL` (`connection.go:781-784`) — incluye errores de
  transporte: la conexión rota (o desincronizada, si el flush salió y el read falló a mitad)
  vuelve al pool sin marca. Fallará en el siguiente uso, pero viola la regla. **DUDOSO (H11)**.
- `result.RowsAffected` lazy — mismo problema, agravado por la falta de lock (H7).
- `BeginTx` ignora el error del commit del autoTx (`connection.go:271`) — si fue de transporte,
  se detecta una línea después en `wc.Transaction`; ventana benigna. **OK** con nota.
- `stmt.Close`/`rows.Close`/`conn.Close` usan `handleFatalErrorLocked` o descartan con la
  conexión ya marcada — sin fugas detectadas. **OK**.

Detalle de `wrapBadConn` (`transport.go:45-50`): `fmt.Errorf("%w: %v", driver.ErrBadConn, err)`
hace `errors.Is(err, driver.ErrBadConn)` ✔ pero **aplana la causa con `%v`**: `errors.As(err,
*net.OpError)` o inspección del error original dejan de funcionar en el camino retryable.
database/sql solo necesita el `Is`, así que funciona, pero para el usuario que loguea el error
la cadena se pierde. Go 1.20+ permite `fmt.Errorf("%w: %w", ...)`. **DUDOSO (H19)**.

---

## 12. Concurrencia

**Contrato**: database/sql garantiza una goroutine por conexión para las operaciones, pero
`Rows`, `Stmt` y `Result` pueden usarse desde otras goroutines (no concurrentemente entre sí
sobre el mismo objeto), y el watcher de ctx es una goroutine propia del driver.

**Comportamiento actual**:
- Todas las operaciones de conn/stmt/tx y `rows.Close`/`rows.fetch` toman `c.mu`
  (`connection.go`, `statement.go`, `transaction.go`, `rows.go:84, 163`). `descCache`, pool de
  handles y `dirtyAutoTx` solo se tocan bajo `mu`. **OK**.
- Watcher de cancelación: solo **escribe** (`op_cancel`) y esa escritura está serializada contra
  el flush principal por `writeMu` (`wire/database.go:95-100, 162-165`) y contra otros cancels
  por `cancelMu`. No lee del socket, así que no compite con el `Reader`. `stop()` es
  join-blocking (`connection.go:672-675`). `markBadLocked → CloseTransport` concurrente con una
  escritura del watcher es seguro (`net.Conn` es thread-safe). **OK** — este es el punto que
  suele estar mal en drivers y aquí está bien resuelto.
- **Excepción**: `result.RowsAffected()` lazy hace `InfoSQL` sin `mu` (`result.go:31-39`) —
  única vía real de acceso concurrente al wire fuera de sincronización. **BUG (H7)** (ya
  contabilizado en punto 9).
- `IsValid` lee `closed/bad` sin `mu` (`connection.go:537-539`): en la práctica el pool lo llama
  con la conexión quiesced; carrera solo teórica. **OK** con nota (un `sync/atomic` o tomar `mu`
  la elimina gratis).
- `rows` no tiene mutex propio: sus campos se tocan desde la goroutine que itera y desde
  `Close` — database/sql no llama `Next` y `Close` concurrentemente, y el trabajo con el wire va
  bajo `c.mu`. **OK**.

---

## 13. Semántica de errores

**Contrato implícito pre-1.0**: los errores del servidor deben exponer código GDS/SQLSTATE de
forma programática mediante un tipo exportado del paquete del driver, funcional con
`errors.Is/As`.

**Comportamiento actual**:
- Existe `firebird.FirebirdError` con `GDSCode`, `SQLState`, `Message`, un `Is()` por código y
  constructor `NewFirebirdError` (`errors.go:10-55`)… **y es código muerto**: `NewFirebirdError`
  no tiene ni un solo call site fuera de `errors.go` (grep sobre todo el repo). Los errores que
  realmente llegan al usuario son `*wire.StatusError` (`wire/status.go:49-64`) envueltos en
  cadenas `fmt.Errorf("%w")` (p.ej. `wire/database.go:573`).
- Consecuencias:
  - `errors.As(err, **firebird.FirebirdError)` **nunca** matchea → cualquier usuario que siga la
    API "natural" del paquete obtiene falsos negativos. **BUG de API (H9)**.
  - Lo que sí funciona: `errors.As(err, **wire.StatusError)` + `GDSCode()` (así lo hace el propio
    driver en `connection.go:109-112`), y `SQLState` está accesible vía `se.SV.Errors[0].SQLState`
    (sin accessor). Pero eso obliga a importar el paquete `wire`, que es la capa de protocolo —
    mala superficie pública para 1.0.
  - `errors.Is(err, driver.ErrBadConn)` ✔ en los paths retryables (`transport.go:45-50`), con la
    pérdida de causa ya anotada (H19).
- `FirebirdWarning` (`errors.go:57-67`) tampoco se construye nunca — los warnings del status
  vector se parsean (`wire/status.go:116-128`) y se descartan.

**Veredicto**: **BUG/FALTA (H9)**. Para 1.0: convertir `StatusError → FirebirdError` en la
frontera adapter (en `handleErrorLocked` o en cada retorno del wire), o exportar accessors
(`GDSCode()`, `SQLState()`) y documentar `wire.StatusError` como API estable — pero una sola de
las dos historias, no ambas a medias.

---

## Notas de arquitectura

1. **El bug H1 es sistémico, no puntual**: hay tres políticas de commit distintas para el modo
   autocommit según el camino de entrada (Execer: `ExecuteAndCommitRetaining` inmediato,
   `connection.go:351-357`; prepared Exec: diferido a `stmt.Close`, `statement.go:141-159`;
   Query: "no hace falta", `rows.go:108-113`). La noción de "transacción autocommit persistente
   con dirty flag" es un invariante distribuido entre `conn`, `stmt` y `rows` que ningún tipo
   posee. Extraer un tipo `autoTxManager` con una única política (commit-retaining al final de
   toda escritura) eliminaría H1 y H8 por construcción.

2. **Duplicación de los caminos de ejecución**: `conn.ExecContext`, `conn.QueryContext`,
   `stmt.ExecContext` y `stmt.QueryContext` repiten el mismo bloque de ~40 líneas
   (materializar blobs → BLR → encode params → execute/execute2 → manejo de error con
   `invalidateAutoTx` + `FreeStatement`) con variaciones sutiles no intencionales:
   `ExecContext` no usa `descCache` pero `QueryContext` sí (`connection.go:326` vs `439-443`);
   `cachedDefaultTPB` y `tpbDefaultWrite` son el mismo TPB con los bytes en distinto orden
   (`connection.go:679-699`). Cada rama de error repite el trío
   `invalidateAutoTx/FreeStatement/handle*` — 8+ copias. Un helper
   `execPrepared(tx, handle, ...)` colapsaría el riesgo.

3. **Estados inválidos representables**: `dirtyAutoTx=true` con `autoTx=0`; `activeTx != 0` y
   `autoTx != 0` a la vez (hoy imposible solo por disciplina); `result` lazy que sobrevive al
   recycle de su `stmtHandle`; handle en el pool de wire con cursor abierto (punto 7). Ninguno
   está prohibido por tipos ni por asserts.

4. **`buildTPB` compara contra literales mágicos** `0..7` con comentarios
   (`connection.go:743-770`) en lugar de `sql.LevelSnapshot` etc. — `driver.go` ya importa
   `database/sql`, no hay ciclo. Fragilidad gratuita frente a niveles nuevos (que además hoy
   caen en el `default` silencioso, H12).

5. **Capa wire con superficie duplicada**: la familia
   `EncodeParams/EncodeParamsErr/EncodeNamedParams/EncodeNamedParamsErr/EncodeNamedParamsStack(Err)/EncodeParamsOptimal(Err)`
   (`wire/types.go:344-481`) mantiene 4 variantes "sin error" que tragan fallos de conversión
   (`_ = ...Err(...)`) y son exactamente el tipo de API que produjo H6. Las variantes silenciosas
   deberían eliminarse. Ídem `toFloat64/toBool/toTime/toInt64` con `default: return 0` — los
   conversores del wire deberían devolver `(T, error)` siempre; el adapter decide.

6. **Responsabilidades mezcladas en `connection.go`** (798 líneas): retry de auth SRP, TPBs,
   descCache FNV, materialización de blobs, parsing de rows-affected, watcher de ctx y el
   adapter propiamente dicho. Sugerencia mínima sin reorganizar paquetes: `tpb.go` (TPBs +
   buildTPB), `autotx.go` (autoTx + dirty), `cancel.go` (withCancel).

7. **`errors.go` es una fachada sin cableado** (H9) y `transport.go` clasifica errores de red en
   parte por **substring del mensaje** (`transport.go:39-42`) — frágil; ya se cubren los casos
   reales con `errors.Is`/`net.OpError`, el fallback textual merece un comentario o eliminarse.

8. **`ResetSession` con round-trip por checkout** (punto 5) es una decisión de
   latencia/robustez que debería ser explícita (¿flag de DSN?) — hoy el usuario paga un RTT por
   query en workloads de pool caliente sin forma de opinar.

9. **La capa adapter llama ~30 métodos públicos de `WireConnection` directamente**; no hay
   interfaz intermedia, así que el adapter solo es testeable contra un servidor real (los tests
   de integración lo confirman: `driver_test.go` requiere FB3/4/5). Una interfaz estrecha
   permitiría tests de tabla para los caminos de error (justo donde viven H1, H7, H8).
