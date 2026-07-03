# Fase 0 — Baseline (2026-07-02)

## Estado de partida

- Commit base: `bf464ad` ("fix: reject oversized SQL_VARYING parameters"), master limpio.
- Rama de trabajo: `review/pre-1.0`.
- Go 1.26.4 windows/amd64. Podman 5.8.3.
- golangci-lint: **no instalado** en la máquina (hay `.golangci.yml` en el repo). `go vet ./...` limpio.

## Suite de tests

| Corrida | Resultado |
| --- | --- |
| `go test ./internal/... ./wire/...` (unitarios) | OK |
| `go test -count=1 ./...` (integración FB3+FB4+FB5) | OK, 60s |
| Tests raíz ejecutados | 169, **0 skips** (FB3/4/5 todos ejercitados) |
| `go test -race ./...` | OK, sin data races |

## Cobertura (`-coverpkg=./...`)

| Paquete | Propia | Nota |
| --- | --- | --- |
| raíz (`firebird`) | 73.8% | |
| `wire` | 28.6% unitaria | la integración desde raíz cubre más; medir por función en Fase 2 |
| `internal/charset` | 84.3% | |
| `internal/timezone` | 94.7% | |
| `internal/bignum` | 0% (sin tests unitarios) | cubierto solo vía integración (SRP) |
| **Total combinado** | **66.9%** | `go test -coverpkg=./... ./...` |

## Entorno de servidores

| Servidor | Endpoint | Estado |
| --- | --- | --- |
| FB3 contenedor (`firebirdsql/firebird:3`) | localhost:3063, `/var/lib/firebird/data/driver.fdb` | OK (podman run; no hay provider de compose) |
| FB4 contenedor | localhost:3064 | OK |
| FB5 contenedor | localhost:3065 | OK |
| FB3 nativo Windows 3.0.14 | 127.0.0.1:3050 | OK (servicio `FirebirdServerDefaultInstance`) |

Los defaults de `driver_test.go` ya apuntan a 3063/3064/3065 — la suite corre sin variables de entorno.

Binarios nativos útiles (isql, gbak, etc.): `C:\Program Files\Firebird\Firebird_3_0\` (no está en PATH).

Contenedores creados con (replicar si se recrean):

```
podman run -d --name go-firebird-driver-fbN -p 306N:3050 \
  -e FIREBIRD_ROOT_PASSWORD=masterkey -e FIREBIRD_DATABASE=driver.fdb \
  -e FIREBIRD_DATABASE_DEFAULT_CHARSET=UTF8 -v fbN-data:/var/lib/firebird/data \
  firebirdsql/firebird:N
```

## Inventario de bases reales (C:\AlfaBeta\firebird\, 37 bases, todas conectan OK)

Todas engine 3.0.14, ODS 12.0. Distribución relevante para tests:

| Atributo | Bases |
| --- | --- |
| **Dialecto 1** (DATE viejo, NUMERIC como DOUBLE) | ifarmaciaMFO, interacciones, iobras, listas, prospectosAR, prospectosCL, winfarma, winfarmaLaPlata, winfarmaMarDelPlata (9) |
| Charset NONE | appp, clientes, encuestas, listas, padronesLP, padronesMDP, publicidadessoftware, qrmp, suiza, usuariosweb (+1) (11) |
| Charset ISO8859_1 | 24 bases |
| Charset UTF8 | pedidosya |
| Charset UNICODE_FSS | suizo |

Las 9 bases dialecto 1 son el hallazgo más importante: el driver default es dialecto 3 y la suite
actual no cubre dialecto 1 en absoluto (verificar en Fase 2).

## Benchmarks

- Baseline nueva corriendo con `-count=5 -benchmem` sobre `wire` y `internal/charset`
  → `review_bench_baseline.txt` (comparar contra `BENCHMARK_BASELINE.md` del 8-may).

## Pendientes / no bloqueantes

- golangci-lint no instalado (decidir si instalarlo o quitar `.golangci.yml` del contrato).
- No hay provider de compose para podman (`docker-compose`/`podman-compose`); los contenedores se
  levantaron con `podman run` directo. Actualizar docs o script en Fase 4.
