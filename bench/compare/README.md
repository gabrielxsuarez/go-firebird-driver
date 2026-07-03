# bench/compare — head-to-head vs nakagami/firebirdsql

Harness único que corre los mismos escenarios contra los dos drivers, alternándolos
por build tag (ambos registran el nombre `firebirdsql` en `database/sql`, así que no
pueden convivir en un proceso).

## Escenarios

| Benchmark | Qué mide |
| --- | --- |
| `BenchmarkConnect` | handshake completo: connect + auth SRP + attach + detach |
| `BenchmarkSelect1Prepared` | overhead fijo por roundtrip (statement preparado) |
| `BenchmarkFetch10kx10` | throughput de fetch + decode (10k filas × 10 columnas mixtas), reporta rows/s |
| `BenchmarkInsertPrepared` | camino de escritura (INSERT preparado, tx por lote de 100) |
| `BenchmarkBlob1KB` / `BenchmarkBlob1MB` | camino de blob (materialización), reporta MB/s |
| `BenchmarkPool20` | contención con ~20 goroutines sobre pool de 20 conexiones |

## Requisitos

- Servidor Firebird accesible y una base de benchmarks **dedicada** (el harness hace
  `RECREATE TABLE`). Por defecto: `127.0.0.1:3050/C:/AlfaBeta/firebird/tmp/bench.fdb`;
  se cambia con `FBBENCH_TARGET=host:puerto/ruta.fdb`.
- Crearla con isql: `CREATE DATABASE 'localhost:C:\...\bench.fdb' USER 'sysdba' PASSWORD 'masterkey' DEFAULT CHARACTER SET UTF8;`
- Credenciales fijas del harness: `sysdba`/`masterkey`, charset UTF8, resto de opciones
  en default de cada driver (eso es parte de la comparación out-of-the-box).

## Cómo correr

```sh
cd bench/compare
go test -run '^$' -bench . -benchmem -count=6 -timeout 60m . > ours.txt
go test -run '^$' -bench . -benchmem -count=6 -timeout 60m -tags nak . > nak.txt
benchstat ours.txt nak.txt
```

Máquina quieta, mismo server, mismos datos: las tablas se recrean idénticas en el
primer uso de cada binario. `count>=6` para que benchstat tenga significancia.
