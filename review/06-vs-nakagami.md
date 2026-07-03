# Fase 6 — Comparación sistemática vs nakagami/firebirdsql (2026-07-03)

Hallazgos crudos detrás de `COMPARISON.md` (público). Consolida F1.3 (comparación de
decisiones), F2.4 (corrección sobre bases reales) y F3.2 (benchmarks), más lo nuevo de
esta fase: matriz de features verificada, sonda de edge cases head-to-head y re-medición
contra la última release de nakagami.

## Versiones

- nakagami publicado: **v0.9.19** (para benchmarks y sonda). El clon local del workspace
  está en master (v0.9.19+23, incluye #278); la sonda dio idéntico contra ambos.
- Importante: nakagami mejoró mucho v0.9.15 → v0.9.19 (SELECT 1: 802→368µs; blobs 1KB:
  1300→704µs; allocs de Select1 104→62). El proyecto está activo — los claims públicos
  van con versión y fecha, y conviene re-medir antes del release 1.0 final.

## Matriz de features — cómo se verificó cada celda

- Protocolos: `internal/wire/protocol.go` (13/15/16/18) vs `consts.go` de nakagami
  (PROTOCOL_VERSION13/16 únicamente).
- Auth: nuestro `auth.go` solo Srp/Srp256, con rechazo explícito de plugins no soportados
  en `handshake.go` ("server requires unsupported auth plugin"); nakagami
  `defaultAuthPlugins = "Srp256,Srp,Legacy_Auth"`.
- Crypt: nuestro `handshake.go` negocia ChaCha→Arc4; nakagami además ChaCha64
  (`wireprotocol.go:487`).
- Compresión: nakagami `pflag_compress` + zlib; nosotros no (decisión documentada).
- Interfaces database/sql: nakagami implementa los 5 `ColumnType*` pero NO
  `DriverContext`/`Connector`, `SessionResetter`, `Validator` ni `NamedValueChecker`
  (grep sin resultados en su repo).
- op_cancel: ambos lo envían (nakagami desde #278 aprox); su modelo descarta la conexión
  tras cancelar ("wire state uncertain — mark the conn bad"); el nuestro la mantiene
  reutilizable para cancelación de ejecución/fetch y acota el lock-wait con read deadline.
- Errores: nakagami `FbError{GDSCodes, SQLCode, SQLState, Params, Warnings, Message}` —
  API programática completa (reciente); nuestro `firebird.Error` (= StatusError) con
  `GDSCode()`, `SQLState()`, `SV`.
- NUMERIC escalado: ambos entregan string (`formatDecimalGDA` en nakagami).
- Extras nakagami confirmados por archivos: `event.go`/`eventmanager.go`/`remoteEvent.go`,
  `service_manager.go`, `backup_manager.go`, `nbackup_manager.go`, `maintenance_manager.go`.

## Sonda de edge cases (scratchpad `edgeprobe/`, build tags, FB3 contenedor)

14 casos, idénticos en ambos salvo lo anotado:

| Caso | ours | nakagami v0.9.19 |
| --- | --- | --- |
| NUMERIC(18,4) = MinInt64 escalado | `-922337203685477.5808` | idéntico |
| VARCHAR(32765) roundtrip completo | OK | OK |
| `''` vs NULL | distinguidos | distinguidos |
| BLOB 0 bytes (texto y binario) | `""` / `[]byte{}` | idéntico |
| uint64 2^63 como parámetro | error del driver | error del driver |
| string basura → DOUBLE param | error del driver (cliente) | error -303 (server) |
| int → TIMESTAMP param | error del driver (cliente) | error -303 (server) |
| **Error a mitad de fetch (div/0 en fila 300)** | **200 filas entregadas + error con GDS 335544…** | **0 filas + error sin código GDS** |
| Conexión tras error de fetch | reutilizable | reutilizable |
| Cancel por contexto (query pesada) | cancela <100ms | cancela ~100ms |
| Conexión tras cancel | reutilizable | reutilizable |
| TIMESTAMP zona no-UTC | pared preservada | idéntico |
| UTF8 ñ/€ | OK | OK |
| **LastInsertId** | **error explícito** | **-1 sin error** |

Conclusión 6.2: cero corrupción/panic en ninguno de los dos; las diferencias son de
contrato y están documentadas en `COMPARISON.md`/`MIGRATION.md`. Las 37 bases reales ya
habían dado 0 discrepancias (F2.4, re-verificado tras el fix del blob vacío).

## Benchmarks (re-medidos contra v0.9.19, `review/bench/e2e_*.txt` regenerados)

Ver tabla en `COMPARISON.md`. Cambios relevantes vs la medición de F3 (v0.9.15):

- Connect pasó de +6% a **empate** (p=0.18).
- Select1 de 3.3× a **1.45×** (nakagami optimizó ese camino).
- **Pool20 pasó de empate a derrota por ~10%** (87µs vs 79µs) — verificado con 4 corridas
  intercaladas por binario (p=0.029), no es ruido térmico.
- B/op de Select1: nakagami ahora usa menos bytes (962 vs 1320) aunque 5× más allocs.
- Blobs y allocs generales: sin cambios en la conclusión (seguimos ganando fuerte).

## Backlog priorizado (áreas donde nakagami nos supera)

1. **Pool20 (~10% más lento bajo 20 conexiones concurrentes)** — único ítem de
   performance. Hipótesis a investigar en la pasada post-1.0: costo del camino ad-hoc
   (allocate+prepare+execute+fetch+free) por operación bajo concurrencia; comparar
   round-trips reales por op con captura de tráfico; revisar si `descCache` (por conexión)
   pierde efectividad con 20 conns. Nota: con statement preparado ganamos 1.45×, así que
   el gap es específico del camino ad-hoc concurrente.
2. **B/op de Select1Prepared** (1320 vs 962): revisar los buffers del camino de fetch
   corto; bajo impacto (allocs 12 vs 62 sigue a favor).
3. Features fuera de alcance 1.0, en orden de demanda esperada: events → services API →
   compresión wire → ChaCha64 → Legacy_Auth (dudoso: inseguro, quizás nunca).
4. `column_name_to_lower` como opción de conveniencia para migradores (evaluar para 1.x).

## Entregables públicos producidos

- `COMPARISON.md` (matriz + benchmarks + reproducción)
- `MIGRATION.md` (DSN, diferencias de comportamiento, tipos)
- README: sección "Why" ya apunta a los números reproducibles; se actualizó a v0.9.19.
- `BENCHMARK_BASELINE.md`: tabla head-to-head actualizada a v0.9.19.
