# Fase 3 — Eficiencia: allocs, CPU, latencia (2026-07-03)

> **Adenda (Fase 6)**: la tabla head-to-head de este informe midió nakagami **v0.9.15**.
> En la Fase 6 se re-midió contra **v0.9.19** (última release), que mejoró varios caminos:
> Connect pasó a empate, Select1 de 3.3× a 1.45×, y **Pool20 pasó a derrota nuestra por
> ~10%** (backlog). Los números vigentes están en `COMPARISON.md` y
> `review/06-vs-nakagami.md`; los raw en `review/bench/e2e_*.txt` (regenerados).

Medición sistemática por capa y comparación head-to-head contra nakagami/firebirdsql.
Raw data en `review/bench/` (micro) y `bench/compare/ours.txt`/`nak.txt` (e2e).

## Entorno

- Intel i5-10210U (laptop), Windows 10, Go 1.26.x.
- **Advertencia de throttling**: este CPU sostiene ~2× menos reloj que en turbo frío. Los
  micro-benchmarks con `-count=10` miden el estado sostenido (throttled); la primera corrida
  en frío reproduce los números de la baseline de mayo (verificado: EncodeParamsOptimal_3Ints
  116ns en frío ≈ 106-108ns de mayo; 234ns sostenido). **allocs/op y B/op son la señal de
  regresión confiable en esta máquina; ns/op solo comparando corridas intercaladas.**
  Verificado también que la VM de podman corriendo no afecta los números.
- e2e: FB3 nativo 3.0.14 (loopback 3050, wire crypt ARC4 negociado por defecto),
  base dedicada `C:/AlfaBeta/firebird/tmp/bench.fdb`, misma base y datos para ambos drivers.

## 3.1 Micro-benchmarks por capa (sin red)

Cobertura completada: a los benches existentes (XDR por campo, charset, crypt, encode de
params) se sumaron **decode de fila completa** (NULL bitmap + columnas, perfiles 5/30
columnas × con/sin NULLs) y **construcción de DPB/TPB/BLR** (`wire/rowdecode_bench_test.go`).
Raw: `review/bench/micro_wire.txt`, `micro_charset.txt` (`-benchmem -count=10`).

Puntos de referencia (sostenido, throttled):

| Benchmark | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| DecodeRow_5Cols (int/varchar/bigint/double/ts) | 934 | 72 | 5 |
| DecodeRow_5Cols_2Nulls | 239 | 16 | 2 |
| DecodeRow_30Cols | 5336 | 432 | 30 |
| DecodeRow_30Cols_HalfNulls | 2716 | 216 | 15 |
| BuildDPB_Connect / BuildTPB / AppendBLR_5Cols | 25 / 12 / 65 | 0 | 0 |
| EncodeParamsOptimal_3Ints / MixedTypes | 235 / 310 | 0 | 0 |
| WriteInt32 / ReadInt32 | 4.6 / 39 | 0 | 0 |

Lectura clave: **1 alloc por valor no-NULL entregado** en el decode (el boxing a `any` +
strings para el usuario) y **0 allocs** en todos los caminos de escritura (params, DPB/TPB/
BLR, XDR). Los NULLs no cuestan nada (fila 30 cols mitad NULL = exactamente la mitad).

## 3.2 e2e head-to-head vs nakagami (harness `bench/compare/`, benchstat n=6)

Ambos drivers, mismo server FB3 nativo, mismos datos, defaults de cada driver, alternados
por build tag (ambos registran `firebirdsql` y no conviven en un proceso).

| Escenario | ours | nakagami | Δ tiempo | Δ allocs |
| --- | --- | --- | --- | --- |
| Connect (handshake SRP+attach+detach) | 62.8ms | 66.8ms | **+6%** | 211 vs 3118 (**14.8×**) |
| SELECT 1 preparado | 241µs | 802µs | **+232%** | 12 vs 104 (**8.7×**) |
| Fetch 10k filas × 10 cols | 108.8ms | 133.9ms | **+23%** (91.9k vs 74.7k rows/s) | 130k vs 480k (**3.7×**) |
| INSERT preparado (tx/lote 100) | 226µs | 359µs | **+59%** | 8 vs 105 (**13×**) |
| Blob 1KB | 573µs | 1300µs | **+127%** | 17 vs 198 (**11.6×**) |
| Blob 1MB | 24.8ms | 119.9ms | **+385%** (40.4 vs 8.3 MB/s) | 36 vs 15603 (**433×**) |
| Pool 20 goroutines | 83.0µs | 81.7µs | **empate** (p=0.132) | 11 vs 106 (**9.6×**) |

Todos los deltas de tiempo con p=0.002 salvo Pool20. **Ganamos 6 de 7 escenarios en tiempo
(geomean +85%) y los 7 en memoria (geomean 4.8× menos bytes, 15× menos allocs).**

Honestidad sobre el empate: en Pool20 el cuello es el servidor (mismo µs/op con 20
conexiones concurrentes para ambos clientes); el driver deja de ser la variable. Sigue
siendo 9.6× menos allocs, que bajo carga sostenida es menos presión de GC.

## 3.3 Perfilado dirigido (escenario Fetch 10k×10, nuestro driver)

- **CPU**: 22% syscalls de red (irreducible), 6% ARC4 (wire crypt del server FB3; con FB4+
  se negocia ChaCha20, y `wire_crypt=disabled` existe para quien no lo quiera), 48%
  acumulado en `DecodeRow` del cual la mayor parte es `mallocgc` de los valores de salida.
- **Allocs**: 90% de los objetos vienen de `DecodeColumn` → boxing a `any` (43%),
  `charset.Decode` a string (34%), `scaledInt64` NUMERIC→string (13%). Son los **valores
  que recibe el usuario** — no son pooleables sin romper el contrato de `database/sql`.
  El objetivo del plan ("O(1) buffers por fetch, no por fila") ya está cumplido:
  `FetchRowsReuse` reutiliza los buffers de filas/valores entre lotes; el residuo de
  ~13 allocs/fila (10 columnas) es ≈1 por valor no trivial + overhead fijo de `Rows.Scan`.
- **Writes coalescidos**: verificado en F1 y visible en el perfil — un flush por operación,
  y los caminos pipelined (allocate+prepare, blob write, execute+commit) baten N ops por flush.
- **`fetch_size` (50/100/200/500/1000, intercalado para aislar throttling)**: p50 entre 93
  y 100ms — estadísticamente indistinguible en loopback. Una corrida secuencial previa
  sugería que 50 era mejor; era deriva térmica (por eso el harness intercala). **Decisión
  con datos: el default 200 se queda** (en redes con RTT real, mayor fetch_size solo ayuda,
  y 200×fila típica ≈ decenas de KB por batch, bien dentro del buffer de 16KB+crecimiento).
- **Histograma de latencia SELECT 1** (20k muestras): p99=715µs, p99.9=868µs, max=1.45ms
  con media de ~241µs — max apenas 6× la media, **sin outliers de GC/flush**. (p50 quedó
  bajo la granularidad del reloj monotónico de Windows ~0.5ms; los percentiles altos son
  los relevantes para outliers y están limpios.)

## 3.4 Optimizaciones

**Aplicadas en esta pasada: ninguna.** El perfil no muestra ningún candidato de bajo riesgo
con impacto: el CPU es red+crypt+allocs de salida, y las allocs de salida son el contrato.
Ya estaba aplicado lo grande (buffers reutilizados por fetch, encode sin allocs, pipelining
de round-trips, statement handle pool, descCache).

Candidatas post-1.0 (documentadas, invasivas o de API):

1. **`op_inline_blob` (protocolo 18/FB5)**: el server manda blobs chicos inline con la fila
   → eliminaría los round-trips open/get/close por blob en fetch. Es la mayor mejora
   posible del camino de blobs y nakagami tampoco la tiene. Requiere cambios en fetch y
   ciclo de vida de blobs: 1.x.
2. **API no-`database/sql`** (ya fuera de alcance 1.0): permitiría decode sin boxing
   (`Scan` directo a tipos concretos) y bajaría el residuo de ~1 alloc/valor.
3. **Batch API (op_batch, FB4+)**: para inserts masivos; hoy el tx-por-lote ya rinde 59%
   mejor que nakagami.

## Baseline

`BENCHMARK_BASELINE.md` regenerado con estos números (sostenidos) + regla de que
allocs/B/op son la señal primaria de regresión en esta máquina. La tabla comparativa vs
nakagami de arriba es la base del `COMPARISON.md` público (Fase 6).
