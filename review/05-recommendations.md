# Fase 5 — Recomendaciones y cierre pre-1.0 (2026-07-03)

Cierre de la revisión. Alcance ajustado por decisión del usuario: **sin DevOps** (nada de
GitHub Actions/CI, sin push, sin tags) — el período de release candidate es el rodaje de
meses en proyectos propios.

## 5.1 Decisiones cerradas (confirmadas por el usuario)

Registradas en `COMPATIBILITY.md` ("Resolved Contract Decisions") y `RELEASE_CHECKLIST.md`:

1. **BLOBs materializados** como contrato 1.0; streaming = 1.x aditivo.
2. **Numéricos de precisión como `string`**; decimales de terceros ya funcionan como
   parámetros vía `driver.Valuer` (documentado).
3. **Timezones: wall-clock + offset**; el nombre IANA no se garantiza (limitación documentada).
4. **Sin API extendida** (events/services/protocolo) en 1.0; `wire` es `internal/`.
5. **Module path definitivo**: `github.com/gabrielxsuarez/go-firebird-driver`.
6. **Go mínimo: 1.25** — lo exige `golang.org/x/crypto` actual; bajarlo implicaría congelar
   la dependencia de criptografía en una versión vieja.
7. **PowerShell only** (`validate.ps1`); sin Makefile para 1.0.

## 5.2 Trabajo ejecutado en esta fase

- **API de errores completada**: `firebird.Error` ya exponía `GDSCode()`; se agregó
  **`SQLState()`** (prometido por COMPARISON/MIGRATION y pedido por el plan) con aserción
  en `TestPublicErrorType`.
- **Seguridad**:
  - `ParseDSN` ya no propaga el error crudo de `url.Parse`, que **incluía la contraseña**
    dentro de la URL en el texto del error. Test de regresión
    `TestParseDSNInvalidDoesNotLeakPassword`.
  - Eliminados los prints de debug `FBDEBUG_CRYPT` que volcaban material del handshake
    de cifrado a stderr.
  - Auditado: la contraseña no aparece en ningún otro camino de log/error; el reporte del
    soak redacta el DSN.
  - `wire_crypt=required` contra server sin crypt: probado en F2 (matriz); fuzz de inputs
    del servidor: F2 (7 fuzzers, fix de OOM).
- **Docs de usuario**: sección "Usage Notes" en el README (NUMERIC como string, blobs
  materializados, timezone, charset NONE, RETURNING en vez de LastInsertId, API de errores).
- **Limpieza menor**: helpers SRP de lado-servidor movidos a `auth_test.go` (solo los usan
  los tests), `deferResponse` huérfano eliminado.

## 5.3 Soak harness (`scripts/soak/`) y sus hallazgos

Herramienta nueva: carga mixta sostenida (point selects, scans de 2000 filas, tx con
inserts+rollbacks, blobs 64KB, 5% de queries canceladas por contexto) sobre un pool con
churn (`ConnMaxLifetime=2m`), muestreo de goroutines/heap cada 10s, detección de outage
con recuperación (se puede matar/reiniciar el server a mitad de corrida) y veredicto
automático de fugas al final. `cd scripts/soak && go run . -duration 30m -out reporte.txt`.

**Falsa alarma instructiva**: la primera versión del workload de cancelación usaba un CTE
recursivo de 500k niveles. Bajo carga aparecían errores GDS 335544663 ("Too many concurrent
executions of the same request") que parecían un bug del driver en el reciclado de
statement handles tras cancelar. La investigación (5 repros: secuencial, concurrente,
SQL único por goroutine, pool sin reuso, query única sin timeout) demostró que **esa query
falla por sí sola** contra el límite de clones de recursión de Firebird a los ~50ms — el
timeout corto la cancelaba antes de llegar al límite y lo enmascaraba. El driver quedó
exonerado: 400 ciclos secuenciales de cancelar+reciclar handle = 0 errores. Se revirtió el
"fix" tentativo y se corrigió la query del workload. Bonus: la misma query inválida
enmascaraba el resultado de cancelación de la sonda de F6 → corregida, expuso que la
cancelación de nakagami v0.9.19 no interrumpe operaciones bloqueadas (19.2s para un
deadline de 300ms) mientras la nuestra corta en ~300ms; COMPARISON/MIGRATION actualizados.

**Resultado del soak final (30 min, con kill/restart del servidor a mitad de corrida)**:
ver la sección de abajo, completada al ejecutarlo.

## 5.4 Bloqueantes de 1.0 vs post-1.0

**Bloqueantes restantes de v1.0.0: ninguno de código.** Lo que falta es tiempo de rodaje:

1. Período RC: meses de uso real en proyectos propios (en curso a partir de ahora).
2. Antes del tag: re-correr gates (quick/race/fuzz/bench + suite FB3/4/5) y **re-medir
   contra la última release de nakagami** (0.9.15→0.9.19 achicó brechas en semanas).
3. Publicar el repo con el module path ya decidido.

**Backlog post-1.0** (prioridad según demanda esperada):

1. Pool20: nakagami v0.9.19 ~10% más rápido bajo 20 conexiones concurrentes (hipótesis en
   `review/06-vs-nakagami.md`).
2. `op_inline_blob` (protocolo 18/FB5): la mayor mejora disponible del camino de blobs.
3. B/op de Select1Prepared (1320 vs 962 de nakagami; allocs siguen 5× a favor).
4. Features: events → services API → compresión wire → ChaCha64 → (¿Legacy_Auth? dudoso).
5. `column_name_to_lower` como opción de conveniencia para migradores.
6. Batch API (op_batch, FB4+); streaming de blobs; helper types numéricos.
7. Refactor diferido: unificar caminos ad-hoc vs preparado (`review/025-architecture.md`).

## Resultado del soak final (2026-07-03, 30 min, reporte crudo en `review/bench/soak_final.txt`)

- **302.571 operaciones exitosas**, 109,2M filas leídas, **15.918 cancelaciones por
  contexto** limpias; 16 workers sobre pool de 8 con `ConnMaxLifetime=2m` (churn constante).
- **Kill/restart real del servidor al minuto 12:20** (contenedor FB3 parado 30s):
  ops/s cayó a 0, los 1.936 errores del outage se clasificaron todos como errores de
  conexión (cero errores SQL, cero pánicos, cero errores mal clasificados — incluso un
  `op_commit` en vuelo al morir el server salió como error de conexión limpio), las
  goroutines *bajaron* durante el outage (workers en backoff, no acumulación), y
  **recuperación completa en 20s** tras el reinicio con throughput idéntico al previo
  (~171-175 ops/s antes y después).
- **Sin fugas**: goroutines baseline=3 → final=1 tras Close+GC; heap 0,5MB → 0,5MB;
  heap estable en 2-4MB durante los 30 minutos bajo carga.
- **VEREDICTO: OK** — cero errores SQL inesperados en toda la corrida.
