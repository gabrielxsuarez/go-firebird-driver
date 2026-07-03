# Fase 2.5 — Organización del código y refactorizaciones (2026-07-03)

Diagnóstico de arquitectura recolectado durante las Fases 1 y 2, decisiones tomadas y
refactors ejecutados. Regla aplicada: solo refactors internos, cero cambio de comportamiento
observable; la suite completa de la Fase 2 pasa sin modificar ningún test.

## Diagnóstico

### Fronteras entre capas — SANAS

- La división raíz (`database/sql` adapter) / `wire` (protocolo) / `internal` (charset,
  timezone, bignum, errmsg) está respetada: la capa raíz consume solo la API exportada de
  `wire`; no hay opcodes ni XDR filtrados hacia arriba, ni lógica de pool/contextos filtrada
  hacia abajo.
- Única permeabilidad deliberada: `wire` importa `database/sql/driver` porque
  `EncodeNamedParams*` recibe `[]driver.NamedValue`. Es la lingua franca de valores del
  ecosistema (nakagami hace lo mismo) y evita una capa de conversión con costo por fila.
  Se acepta y se documenta; no se cambia.
- La cancelación cruza capas de forma controlada: `conn.withCancel` (raíz) usa solo
  `wc.Cancel`/`wc.SetReadDeadline` (wire), con la semántica documentada en el sitio.

### Archivos con demasiadas responsabilidades — CORREGIDO

Los dos hallazgos de F1/F2, resueltos con splits puros (solo movimientos de código):

- `wire/types.go` (2124 líneas, "types" significaba seis cosas distintas) →
  `types_datetime.go` (147: MJD/ticks/timezones), `types_decode.go` (211: DecodeColumn/
  DecodeRow/NULL bitmap), `types_encode.go` (610: encode de parámetros), `types_numeric.go`
  (460: NUMERIC escalado, strings decimales), `types_decfloat.go` (651: DECFLOAT/DPD),
  `types_int128.go` (86). Commit `ade7892`.
- `wire/database.go` (1216 líneas, mezclaba attach/tx/statements/fetch/blobs) → particionado
  como los capítulos de la spec: `database.go` (177: núcleo WireConnection + lazy send +
  cap. 6), `transaction.go` (97: cap. 7), `statement.go` (206: allocate/prepare/free/pool/
  describe, cap. 8), `execute.go` (417: execute/execute2/fetch, cap. 8), `blob.go`
  (331: cap. 9). Commit `13ff22d`.
- Capa raíz: `connection.go` (895) bajó a 770 moviendo lo que no era del ciclo de vida de la
  conexión: TPBs y `buildTPB` → `transaction.go` (junto al tipo `transaction`),
  `getRowsAffected` → `result.go` (junto al tipo `result`). Commit `541febe`.
- `driver_test.go` (3421 líneas) es grande pero es un inventario plano de tests de
  integración; partirlo no mejora nada y rompe `git blame`. Se deja.

### Duplicación — evaluada, mayormente aceptada con justificación

- **Caminos ad-hoc vs preparado** (`conn.ExecContext/QueryContext` vs los de `stmt`):
  comparten ~70% de estructura, pero las diferencias son reales y de rendimiento
  (descCache + allocate por llamada + RecycleStatement en el ad-hoc; descriptores estables
  y BLR cacheado en el preparado). Unificarlos exige un tipo "execution plan" común que
  toque los cuatro caminos calientes a la vez. **Diferido a post-1.0**: el riesgo de
  introducir una regresión sutil en los caminos más ejercitados supera el beneficio estético.
- **Materialización de blobs**: la duplicación real que existía (fetch vs Execute2) ya se
  eliminó en la Fase 2 con `conn.materializeBlobRowsLocked` (commit `2459725`).
- **`Fetch`/`FetchRows` vs `FetchRowsReuse`**: `Fetch` duplica el write de op_fetch y el
  manejo de respuesta; hoy **nadie los llama** (ver "código muerto" abajo). No se dedupe
  código que probablemente se borre en la Fase 4.
- **Encode duplicado por destino** (`encodeValue` vs `encodeValueStack`, `writeVaryingParam`
  vs `writeVaryingParamStack`): es el clásico costo de tener un camino sin allocs
  (StackWriter) y uno general. Deduplicarlo con genéricos/interfaz reintroduce las allocs
  que el camino stack existe para evitar. Se acepta; ambos viven juntos en
  `types_encode.go` donde la simetría se ve.

### Manejo de estado — CORRECTO, un solo lugar

- Ciclo de vida de la conexión: `closed`/`bad`/`activeTx`/`autoTx`/`dirtyAutoTx` se mutan
  solo bajo `c.mu`; el paso a "bad" está centralizado en `markBadLocked` (cierra transporte,
  invalida handles) y la clasificación de errores en `handleErrorLocked`
  (transporte→ErrBadConn, resto→pasa limpio). La desincronización de protocolo se maneja
  en el punto donde ocurre manteniendo el stream consumido (p.ej. `fetchServerError`,
  respuestas dobles de los caminos pipelined) — patrón consistente.
- Statements: el pool de handles (`RecycleStatement`) es la única fuente de reuso; el
  estado del cursor viaja explícito (`hasCursor`).
- Los estados inválidos representables que quedan (p.ej. `rows` usado tras `Close` del
  stmt) están cubiertos por tests de la Fase 2, no por tipos. Rediseñar con state machines
  explícitas sería invasivo sin bug conocido que lo justifique: post-1.0 si aparece evidencia.

### Manejo de errores — CONSISTENTE

- `wire`: cada operación envuelve con su opcode (`op_fetch: ...`), los errores del servidor
  son `*StatusError` (código GDS + SQLSTATE + args) — estructural, no textual.
- Raíz: `firebird.Error = wire.StatusError` (alias público desde F2), `wrapBadConn` con `%w`
  doble (`errors.Is(driver.ErrBadConn)` y `errors.As(*Error)` funcionan a la vez),
  distinción explícita retryable/fatal por sitio de llamada.
- Sin hallazgos accionables en esta fase.

### Cosas difíciles de testear (hallazgos de la Fase 2)

- La cancelación durante lock-wait requirió un test con dos conexiones y sincronización
  fina, pero el acoplamiento estaba en el problema (op_cancel no interrumpe WAIT), no en el
  diseño del driver.
- Los fuzzers de inputs del servidor se escribieron sin fricción porque `Reader`/parseo de
  status vector son funciones puras sobre bytes — señal de que la capa de decode está bien
  aislada.
- Sin hallazgos de arquitectura derivados de testing.

### Contraste con las referencias

- **jaybird** particiona el wire protocol por versión (`JavaGDSImpl` v10..v18 apilados por
  herencia) porque soporta divergencias grandes entre versiones; nosotros condicionamos por
  `protocolVersion` en los pocos puntos que difieren (execute/fetch/handshake), lo cual es
  proporcional a nuestro alcance (13-18) y evita una jerarquía.
- **firebirdsql (nakagami)** concentra todo el protocolo en `wireprotocol.go` (~3400 líneas)
  — exactamente el problema que `wire/database.go` estaba empezando a reproducir y que este
  split corrige. La organización nueva (un archivo por capítulo de la spec) hace que el
  código se navegue con la spec al lado.

## Código muerto detectado (decisión para Fase 4, no se toca acá)

Métodos exportados de `wire.WireConnection` sin ningún llamador (ni producción ni tests):
`ExecuteAndCommit`, `TransactionExecuteCommit`, `Fetch`, `FetchRows`, `PrepareStatement`,
`AllocateStatement`, `RollbackRetaining`. Son variantes históricas superadas por los caminos
pipelined (`ExecuteAndCommitRetaining`, `AllocateAndPrepareWithItems`, `FetchRowsReuse`).
La decisión de borrarlos o documentarlos es de la Fase 4 (punto 5 del PLAN, junto con las
constantes tapadas por `//lint:file-ignore`) porque `wire` es un paquete exportado y su
superficie pública se congela en 4.7.

## Refactors ejecutados (un commit cada uno, suite completa verde entre cada uno)

| Commit | Refactor | Validación |
| --- | --- | --- |
| `ade7892` | Split `wire/types.go` → 6 archivos por área de tipos | build+vet+suite FB3 |
| `13ff22d` | Split `wire/database.go` → 5 archivos por capítulo de la spec | build+vet+suite FB3 |
| `541febe` | TPB → `transaction.go`, rows-affected → `result.go` | build+vet+suite FB3 |

Los tres son movimientos de código puros (mismas funciones, mismos cuerpos); ningún test
se modificó.

## Refactors diferidos a post-1.0 (con justificación)

1. **Unificar caminos ad-hoc vs preparado** en Exec/Query: beneficio de mantenibilidad
   real pero toca los cuatro caminos más calientes a la vez; hacerlo justo antes de medir
   performance (F3) y congelar API (F4) maximiza el riesgo. Reevaluar en 1.x con la
   baseline de benchmarks como red adicional.
2. **State machine explícita para conn/stmt/rows**: sin bug conocido que lo motive; el
   estado actual es chico y está centralizado.
3. **Deduplicar encode Writer/StackWriter**: costaría las allocs que el camino stack evita.
4. **Partición por versión de protocolo estilo jaybird**: solo si 1.x incorpora
   features con divergencia fuerte entre versiones (batch/events/scrollable).
