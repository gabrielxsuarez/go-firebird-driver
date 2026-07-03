# Migrando desde nakagami/firebirdsql

Guía práctica para pasar una aplicación de `github.com/nakagami/firebirdsql` a
`github.com/gabrielxsuarez/go-firebird-driver` con el mínimo de fricción.

## El cambio mínimo

```go
import (
    _ "github.com/gabrielxsuarez/go-firebird-driver" // antes: _ "github.com/nakagami/firebirdsql"
)

db, err := sql.Open("firebirdsql", "sysdba:masterkey@localhost:3050/ruta/base.fdb")
```

Eso es todo para la mayoría de las aplicaciones: este driver **también registra el nombre
`firebirdsql`** como alias de compatibilidad, y acepta la forma de DSN de nakagami
(`user:pass@host:puerto/ruta?params`, sin esquema). Para código nuevo se recomienda
`sql.Open("firebird", "firebird://user:pass@host:puerto/ruta?params")`.

## DSN: parámetros compatibles

| Parámetro nakagami | Acá | Nota |
| --- | --- | --- |
| `charset` | ✅ igual | default UTF8 en ambos |
| `role` | ✅ igual | |
| `timezone` | ✅ igual | alias de `session_time_zone` |
| `wire_crypt=true/false` | ✅ igual | además acepta `enabled/disabled/required` |
| `auth_plugin_name` / `auth_plugin_list` | se ignora | la negociación Srp256→Srp es automática |
| `wire_crypt_plugin` | se ignora | se negocia ChaCha→Arc4 automáticamente |
| `wire_compress` | se ignora | compresión wire no implementada (decisión 1.0) |
| `column_name_to_lower` | se ignora | **ver diferencia de comportamiento abajo** |

Parámetros propios que nakagami no tiene: `fetch_size` (default 200),
`data_type_bind`, `dialect` (solo 3; las bases dialecto 1 funcionan igual).

## Diferencias de comportamiento a revisar

1. **Nombres de columna**: nakagami con `column_name_to_lower=true` baja los nombres a
   minúsculas; acá llegan como los entrega el servidor (típicamente MAYÚSCULAS). Si tu
   código dependía de eso (p.ej. mapeo a structs por nombre), ajustá el mapeo.
2. **`LastInsertId()`**: nakagami devuelve `-1` sin error (Firebird no tiene last-insert-id);
   acá devuelve un **error explícito** que te dirige a `INSERT ... RETURNING`. Si tu código
   ignoraba el `-1`, ahora va a ver el error — usá `RETURNING`, que funciona en ambos.
3. **Errores**: son `*firebird.Error` — `errors.As` + `GDSCode()`/`SQLState()` para manejo
   programático. El texto incluye la cadena completa de mensajes de Firebird.
4. **Cancelación por contexto**: acá `op_cancel` se envía asíncrono e **interrumpe la
   operación en el servidor** (una query pesada se corta en cuanto vence el contexto), la
   conexión queda reutilizable, y una cancelación durante un lock-wait está acotada en el
   tiempo. En nakagami v0.9.19 la cancelación se aplica entre operaciones: una query
   bloqueada en el servidor corre hasta terminar aunque el contexto haya vencido.
5. **Error a mitad de fetch**: acá recibís las filas ya leídas y después el error real con
   su código GDS en `rows.Err()`; nakagami puede perder las filas del lote fallido.
6. **Fuera de alcance 1.0** (si los usás, quedate con nakagami para esas partes): events
   (`POST_EVENT`), services API (backup/restore/maintenance), compresión wire,
   `Legacy_Auth` (server FB2.x-style; acá da error claro pidiendo SRP).

## Tipos

| Firebird | Scan a | Igual en nakagami |
| --- | --- | --- |
| INTEGER/BIGINT/SMALLINT | int64 | sí |
| NUMERIC/DECIMAL escalado | string | sí (también string) |
| FLOAT/DOUBLE | float64 | sí |
| VARCHAR/CHAR | string | sí |
| BLOB SUB_TYPE TEXT | string | sí |
| BLOB SUB_TYPE 0 | []byte | sí |
| DATE/TIME/TIMESTAMP | time.Time | sí |
| TIMESTAMP/TIME WITH TIME ZONE (FB4+) | time.Time (pared + offset) | similar |
| BOOLEAN | bool | sí |
| INT128 / DECFLOAT (FB4+) | string | similar |

Ver [COMPATIBILITY.md](COMPATIBILITY.md) por el contrato completo y
[COMPARISON.md](COMPARISON.md) por la comparación feature-por-feature y de performance.
