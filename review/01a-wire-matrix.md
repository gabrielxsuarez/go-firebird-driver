# 01a — Matriz de completitud del protocolo wire (orientada a database/sql)

Fecha: 2026-07-02.
Fuentes: spec `wire-protocol/` (caps. 02–09, 14, apéndices A/B) contra el paquete `wire/` del driver (más los puntos de uso en `connection.go`, `rows.go`, `statement.go`, `transport.go`).

Objetivo: demostrar que todo lo que `database/sql` puede necesitar está implementado correctamente según la spec. Services, events, arrays, batches y cursores scrollables están **fuera de alcance** por diseño; se verifica que su ausencia no rompa nada.

Leyenda de estado:
- ✅ implementado — conforme a la spec
- 🟡 parcial — funciona en el camino feliz, con huecos
- ❌ no implementado
- ⛔ no aplica a database/sql (fuera de alcance, ausencia verificada como segura)
- 🐞 bug — desviación de la spec con impacto funcional

---

## 1. Negociación de conexión y versiones de protocolo (caps. 02, 05)

| Aspecto de la spec | Estado | Evidencia | Riesgo si falta / notas |
|---|---|---|---|
| `op_connect` con CONNECT_VERSION3, arch_generic, path, count, user_id | ✅ | `wire/handshake.go:119-136` | — |
| Oferta de protocolos 13/15/16/18 con pesos crecientes | ✅ | `wire/handshake.go:55-63` | No se ofrece 19 (inline blobs) ni 17 (batch sync) — decisión coherente: al no negociarlos, el server jamás envía `op_inline_blob` ni exige `op_batch_sync`. ⛔ correcto. |
| Máscara `FB_PROTOCOL_FLAG` (0x8000) al comparar versión aceptada | ✅ | `wire/handshake.go:180` (`acceptedVersion & FBProtocolMask`); comparaciones `>= 16`, `>= 18` usan la versión desenmascarada | — |
| `op_accept_data` / `op_cond_accept` (campos version/arch/type/data/plugin/authenticated/keys) | ✅ | `wire/handshake.go:157-169` | Ambos opcodes se tratan idéntico; para el flujo SRP puro es equivalente en la práctica. |
| `op_accept` simple (protocolos 10–12) | ❌ | `wire/handshake.go:175-177` (default → "unexpected opcode") | Firebird ≤2.5 no soportado. Como solo se ofrecen protocolos 13+, un server 2.5 responde `op_reject` → error limpio. Si un server enviara `op_accept`, el error "unexpected opcode 3" es críptico pero no cuelga. Aceptable para 1.0; documentar FB mínimo = 3.0. |
| `op_reject` → error `isc_connect_reject` | 🟡 | `wire/handshake.go:171-173` | Devuelve error genérico "connection rejected by server" sin código GDS 335544421. Cosmético. |
| `op_crypt_key_callback` (BD cifradas, proto 15) | ❌ | no manejado; caería en `default` de `handshake.go:175` | BD con cifrado de datos + callback de clave no soportadas. Error claro ("unexpected opcode 97"), no cuelga. Aceptable si se documenta. |
| Campos condicionales v16 (`p_sqldata_timeout`) en execute/execute2 | ✅ | `wire/database.go:387-389` (`writeExecuteOp`), `wire/database.go:524-526` (`Execute2`) | Siempre 0 (usa timeout de conexión); correcto — database/sql cancela vía `op_cancel`/context. |
| Campos condicionales v18 (`p_sqldata_cursor_flags`) | ✅ | `wire/database.go:390-392`, `wire/database.go:527-529` | Siempre 0 (no scrollable). `op_fetch` no cambia en v18 → nada más que hacer. ✅ |
| `op_fetch_scroll`, `op_info_cursor` (v18) | ⛔ | constantes en `wire/protocol.go:49,53` | database/sql solo itera hacia adelante. Correctamente omitido. |
| `ptype_lazy_send` negociado y detectado | ✅ | `wire/handshake.go:343` (`acceptedType & PtypeMask`) | — |
| Wire **compression**: ¿se anuncia? ¿se rechaza si el server la propone? | ✅ (no soportada, y es seguro) | `wire/handshake.go:55-63` nunca hace OR de `pflag_compress` (0x100) en `MaxType`; según spec §5.3 el server solo activa compresión como *acknowledgement* de una petición del cliente, por lo que nunca la propondrá espontáneamente. El bit en `p_acpt_type` se ignora sin riesgo. | Sin riesgo de desincronización. Feature-gap benigno. |
| `op_disconnect` al cerrar (evita "connection reset by peer" en firebird.log) | ✅ | `wire/database.go:138-141` (`Detach`) | — |

## 2. Autenticación (cap. 05 §5.4–5.5, §5.11.2)

| Aspecto | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| SRP (Srp) y SRP-SHA256 (Srp256), lista `Srp256,Srp` | ✅ | `wire/auth.go` completo; proof M1 con hash según plugin (`auth.go:159-191`); N/g/k en `internal/bignum` | Matemática SRP correcta incl. la peculiaridad Firebird `n3 = ModPow(n1,n2,N)` (`auth.go:168-170`). Verificada por tests unitarios con vectores servidor (`auth_test.go`). |
| CNCT user-id block (user, host, login, plugin_name, specific_data multiparte, plugin_list, client_crypt) | ✅ | `wire/auth.go:233-308`; multiparte de `CNCT_specific_data` con secuencia (`auth.go:289-303`) conforme a spec §5.11.2 paso 1 | — |
| `op_cont_auth` (proof, plugin, lista, keys) — ronda con challenge en accept | ✅ | `wire/handshake.go:187-211` | — |
| Ronda extra cuando el server cambia de plugin sin data (reenvío de clave pública) | ✅ | `wire/handshake.go:212-290` | Maneja `op_cont_auth` y `op_response`; otros opcodes → error claro. |
| **Legacy_Auth** | ❌ sin error claro | `wire/handshake.go:183-185`: si el server pide otro plugin se hace `srp.SetPlugin(serverPlugin)` a ciegas, aunque sea `Legacy_Auth`, y se le envían datos SRP bajo ese nombre. El DPB nunca lleva `isc_dpb_password`/`password_enc` (`handshake.go:440-462`), así que Legacy no puede funcionar. | Contra un server con `AuthServer = Legacy_Auth` **no hay cuelgue**: el server termina devolviendo `op_response` con error y el cliente lo reporta — pero como `isc_login` genérico, no como "plugin Legacy_Auth no soportado; configure Srp en el servidor". La spec §5.11.2 paso 3 pide explícitamente reportar error si el cliente no puede satisfacer el plugin pedido. **Falta-para-1.0**: detectar plugin no soportado y devolver error explicativo. |
| `p_acpt_authenticated == 1` (auth en un paso) | 🟡 | `wire/handshake.go:187,212`: si `authenticated==1` se salta la fase auth (correcto), pero entonces `sessionKey` queda vacío y el bloque de cifrado se salta silenciosamente incluso con `wire_crypt=required` (ver §3). | — |
| Keys del server en `p_resp_data` del `op_response` final (spec §5.11.2 paso 3) | ❌ | `handshake.go:206-211,267-272` descartan `resp.Data` | Si las claves de crypt llegaran solo ahí, `selectCipher` vería un buffer vacío. En la práctica llegan en accept/cont_auth. Riesgo bajo. |
| Retry ante `isc_login` transitorio bajo ráfagas | ✅ (capa driver) | `connection.go:58-65,107-112` | Mitigación pragmática de una condición real de FB. |

## 3. Cifrado de wire (cap. 05 §5.8)

| Aspecto | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| `op_crypt` (plugin + "Symmetric"), activar cifrado en ambos sentidos antes de leer la respuesta | ✅ | `wire/handshake.go:300-321`; respuesta leída ya descifrada, conforme a spec | — |
| ARC4 | ✅ | `wire/crypt.go:21-23`; clave = session key SRP directa (correcto) | — |
| **ChaCha** | 🐞 | `wire/handshake.go:364-373` calcula `keyHash = sha256(sessionKey)` y llama `newChaCha20Cipher(keyHash[:], nonce)`; pero `wire/crypt.go:28-33` vuelve a hacer `sha256.Sum256(sessionKey)` dentro. Clave efectiva = `SHA256(SHA256(K))`; Firebird usa `SHA256(K)`. | **Doble hash**: si el camino ChaCha se activa alguna vez, el handshake muere tras `op_crypt` (basura descifrada). Que las pruebas de integración pasen sugiere que `extractChaChaData` (`handshake.go:389-437`, formato de `p_acpt_keys` adivinado) nunca encuentra el nonce y siempre se cae a Arc4 — es decir, **el soporte ChaCha está muerto o roto**. Verificar contra FB4/5 real; arreglar el doble hash y el parser de keys, o eliminar ChaCha y documentar Arc4-only. |
| ChaCha64 | ❌ | sin referencia en el código | Fallback a Arc4 — aceptable. |
| Selección de cipher según lo que el server ofrece | 🟡 | `wire/handshake.go:359-385`: no valida que Arc4 esté en `p_acpt_keys`; con `serverKeys` vacío igual construye Arc4 y envía `op_crypt`. | Contra server con `WireCrypt=Disabled`: el cliente envía `op_crypt`, activa cifrado local, y la respuesta de error del server (en claro) se "descifra" → basura → error confuso o cuelgue hasta deadline. **Falta-para-1.0**: si `serverKeys` no ofrece ningún plugin conocido → con `enabled` seguir en claro sin enviar `op_crypt`; con `required` devolver error claro. |
| `wire_crypt=required` sin session key (p.ej. auth completada en un paso) | 🐞 | `wire/handshake.go:294`: `if cfg.WireCrypt != WireCryptDisabled && len(sessionKey) > 0` — con `required` y sin clave **continúa en texto plano sin error**. | Viola la semántica de `required` (garantía de seguridad silenciosamente incumplida). Debe fallar con error explícito. |
| `CNCT_client_crypt` en el connect | ✅ | `wire/auth.go:305-308` (LE de 4 bytes, correcto) | — |
| Cifrados por dirección con estado independiente | ✅ | `wire/conn.go:61-69`, `wire/crypt.go:56-88` | Diseño de pipe/pivote limpio. |

## 4. Respuestas comunes y status vector (cap. 03)

| Aspecto | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| `op_response`: object, blob-id (Int64), data (Buffer), status vector — en ese orden | ✅ | `wire/response.go:28-47` | `Data` apunta a `auxBuf` reutilizable: válido solo hasta el siguiente `readGenericResponse`. Documentado, pero es una trampa latente (ver Notas de arquitectura). |
| Status vector: `isc_arg_gds`, `isc_arg_string`, `isc_arg_cstring`, `isc_arg_interpreted`, `isc_arg_number`, `isc_arg_warning`, `isc_arg_sql_state`, `isc_arg_end` | ✅ | `wire/status.go:87-172`; separación errores/warnings; SQLSTATE; params acumulados; primer error = código primario | Tags desconocidos se saltan leyendo un Int32 (`status.go:164-169`) — correcto para todos los tags numéricos históricos (vms/unix/dos…), se rompería solo con un tag string desconocido (ninguno definido hoy). Fast-path de éxito con peek de 12 bytes (`status.go:71-82`) — conforme al patrón `gds 0 end`. |
| Wrapping como error Go inspectable | ✅ | `wire/status.go:49-64` (`StatusError`, `GDSCode()`, `errors.As`) | — |
| `op_fetch_response` (status 0/100, messages) | ✅ | `wire/response.go:49-56` | Semántica correcta: `messages==0 && status==100` → EOF; `messages==0 && status==0` → fin de lote (ver §7 para el hueco de error). |
| `op_sql_response` (execute2) | ✅ | `wire/response.go:58-64`, consumido en `wire/database.go:540-589` | — |
| `op_dummy` keep-alive: leer e ignorar | ✅ | `wire/reader.go:234-242` (`ReadOpcode` los salta en bucle) | — |
| Respuestas diferidas (lazy send): drenar antes de leer la propia | ✅ | `wire/database.go:70-92` (`deferResponse`, `consumeDeferred`, `readResponse` sobre `WireConnection`, definida en `wire/database.go:12-44`) | `op_free_statement` difiere su respuesta con `lazySend` (`wire/database.go:797-800`) exactamente como describe la spec §8.2.1. |
| `op_inline_blob` (v19) | ⛔ | opcode definido (`wire/protocol.go:68`), nunca esperado | Seguro: protocolo 19 no se ofrece. |
| `op_exit`/`op_disconnect` en aux connection | ⛔ | no hay aux connections (events fuera de alcance) | — |

Nota de organización: `Conn` (transporte, cifrado, deadlines) vive en `wire/conn.go:29-91`, mientras que `WireConnection` (estado de la conexión de protocolo, helpers lazy y **todas** las operaciones) vive en `wire/database.go:12-100+`. El nombre del archivo no refleja su contenido — ver Notas de arquitectura (§12).

## 5. Peticiones de información (cap. 04) y base de datos (cap. 06)

| Operación | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| `op_info_database` | ✅ | `wire/database.go:105-121`; usado por `Ping` (`connection.go:613`) | — |
| `op_info_sql` (rows affected vía `isc_info_sql_records`) | ✅ | `wire/database.go:810-826`; parser `wire/info.go:349-411` (select/insert/update/delete) | — |
| `op_info_transaction`, `op_info_blob` | ✅ | `wire/database.go:244-260`, `936-952` | Disponibles aunque el driver apenas los usa. |
| Parser TLV de info (longitud Int16 LE, `isc_info_end`/`truncated`) | ✅ | `wire/info.go:166-193` | Tags 3+ tratados como TLV normal, con razonamiento documentado sobre reuso de valores de tag — correcto. |
| Truncación de info: reintento con buffer mayor / `isc_info_sql_sqlda_start` (spec §4.1, §8.3) | ❌ | `wire/info.go:207` corta el parse en `IscInfoTruncated`; ningún llamador reintenta (prepare usa buffer fijo 65535: `connection.go:192,318,430`) | SELECT con cientos de columnas (~>500) → descriptores incompletos → BLR con menos columnas que el statement → error del server o filas mal decodificadas. Caso borde; **falta-para-1.0** al menos detectar truncación y devolver error explícito en vez de continuar. |
| `op_detach` + `op_disconnect` | ✅ | `wire/database.go:124-142` | — |
| `op_drop_database`, `op_create` | ❌ | — | No aplican a database/sql (no hay API estándar). Aceptable; documentar. |
| **`op_cancel`** (proto 12+) | ✅ | `wire/database.go:144-166`; kinds en `wire/protocol.go:206-211`; `fb_cancel_abort` → cierre de socket local, conforme a la nota de spec §6.6 | Usado de verdad: `connection.go:652-676` (`withCancel`) dispara `CancelRaise` cuando el context se cancela, desde goroutine aparte, con `cancelMu`+`writeMu` separados del lock de lectura — exactamente la precaución que pide la spec §6.6. ⚠️ Pero ver §7: la respuesta de error que genera la cancelación **durante un fetch** no se maneja bien. |

## 6. Transacciones (cap. 07)

| Operación | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| `op_transaction` (handle db = 0, TPB) | ✅ | `wire/database.go:171-185` | — |
| `op_commit` / `op_rollback` | ✅ | `wire/database.go:188-213` | — |
| `op_commit_retaining` / `op_rollback_retaining` | ✅ | `wire/database.go:216-241`; el driver usa commit_retaining para el auto-tx persistente | — |
| TPB builder (v3; read committed, rec_version, wait/nowait, lock timeout, read only, etc.) | ✅ | `wire/tpb.go`; TPB por defecto `connection.go:679-685` | Cubre los niveles de aislamiento que `database/sql` puede pedir; los no mapeables se rechazan con error claro (`connection.go:53-56`). |
| `op_prepare2` (2PC) | ❌ (constante definida) | `wire/protocol.go:36` | database/sql no expone 2PC. ⛔ correcto. |
| `op_reconnect` (limbo) | ❌ | — | Herramienta de recuperación, no de driver. ⛔ |
| Pipeline execute+commit_retaining en un flush | ✅ | `wire/database.go:439-459` | Lee ambas respuestas siempre para mantener el wire sincronizado incluso si la primera falla — correcto. |
| `TransactionExecuteCommit` (op_transaction + op_execute con tx=0xFFFF + op_commit 0xFFFF) | 🟡 sin uso | `wire/database.go:465-502`; **ningún llamador** | La spec (§2.2 CAUTION, §7.1.1) solo garantiza la sustitución de `0xFFFF` para el *último objeto creado* y la menciona para info requests; usar el handle diferido de transacción dentro de `op_execute` no está documentado como soportado. Al estar muerto, eliminar o marcar experimental. |

## 7. Statements (cap. 08)

| Operación | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| `op_allocate_statement` | ✅ | `wire/database.go:265-278` | — |
| Allocate+prepare en un solo round-trip con `0xFFFF` (spec §8.3.1) | ✅ | `wire/database.go:325-370`; lee respuesta de allocate y luego la de prepare, en orden | Conforme. Con pool de handles reutiliza y hace solo prepare (`database.go:329-336`) — la spec lo permite explícitamente. |
| `op_prepare_statement` (dialecto, SQL, items, buffer len) | 🟡 | `wire/database.go:296-314` | 🐞 **Dialecto hardcodeado**: escribe `SQLDialectCurrent` (3) siempre (`database.go:300,346`), aunque el DSN acepte `dialect=1` (`dsn.go:84-89`) y el DPB lo declare (`handshake.go:443`). Un usuario con `dialect=1` obtiene semántica de dialecto 3 en prepare y BLR `blr_version5` (spec §14.1.1 exige `blr_version4` para dialecto 1, `wire/blr.go:8`). **O se propaga el dialecto o se rechaza `dialect != 3` en el DSN.** Lo segundo es razonable para 1.0. |
| Items de describe (tipo, subtipo, escala, longitud, null, field, alias; stmt_type; bind) | ✅ | `wire/info.go:61-122`; parser `wire/info.go:197-305` con guardas de índice | Variante ligera para exec-only (`PrepareExecInfoItems`) — buena optimización. |
| `op_execute` (BLR params, message count 0/1, row data) | ✅ | `wire/database.go:373-408` | Conforme, incl. campos v16/v18. |
| `op_execute2` + `op_sql_response` + `op_response` | ✅ | `wire/database.go:506-590` | Maneja la secuencia completa y también el caso de fallo (solo `op_response`), conforme a spec §8.6. |
| `op_fetch` (BLR solo en el primer fetch, fetch size) | ✅ | `wire/database.go:644-649`; `rows.go:149-161` cachea el BLR y envía vacío después | La spec dice que el BLR de fetches posteriores se ignora; el driver lo reenvía igual (r.blr no se vacía) — inocuo, solo bytes extra. |
| Bucle de fetch: status 0/100, `messages`, lotes parciales, server devuelve menos filas de las pedidas | ✅ | `wire/database.go:696-756` | Lee `op_fetch_response` hasta `messages==0`; distingue fin-de-lote de EOF; si el server enviara **más** filas de las pedidas, el buffer crece dinámicamente (`database.go:718-747`) — no se rompe. |
| **`op_response` (error) en medio de la secuencia de fetch** (spec §8.8: "It is possible to receive Generic response with an error after one or more fetch responses") | 🐞 | `wire/database.go:702-704`: `if op != opFetchResponse` → error "unexpected opcode 9" **sin leer el cuerpo** del `op_response` | Doble daño: (1) el error real del server (deadlock, `isc_cancelled` por `op_cancel`, etc.) se pierde y se reporta un error de protocolo; (2) el stream queda desincronizado y `isTransportError` (`transport.go:15-43`) **no** lo considera fatal → la conexión corrupta **vuelve al pool**. Como la cancelación por context durante un fetch produce exactamente este `op_response`, el caso no es exótico: **cancelar un query en mitad de un fetch corrompe la conexión**. Fix: en el bucle, si `op == opResponse` parsear `readGenericResponse` y devolver su `StatusError`; y/o marcar bad toda desincronización de protocolo. **Bug, prioridad máxima.** |
| `op_free_statement` (DSQL_close / DSQL_drop) + respuesta diferida con lazy | ✅ | `wire/database.go:788-807`; pool de handles con `DSQLClose` (`database.go:763-785`) | Conforme a §8.2.1. El pool (8 handles) se drena en el cierre (`DrainStatementPool`). |
| `op_set_cursor` | ❌ | constante `wire/protocol.go:51` | Solo necesario para `WHERE CURRENT OF`; database/sql no lo expone. ⛔ |
| `op_exec_immediate(2)` | ❌ | constantes `wire/protocol.go:45-46` | El driver siempre usa allocate+prepare+execute; funcionalmente equivalente. ⛔ |
| Cursor info (v18) | ⛔ | — | Scrollable fuera de alcance. |

## 8. Blobs (cap. 09)

| Aspecto | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| `op_create_blob2` / `op_open_blob2` (BPB, tx, blob id) | ✅ | `wire/database.go:831-864` | Orden de campos conforme (BPB primero). |
| `op_get_segment` (máx 65535, buffer vacío) y desempaquetado de segmentos `[len LE16][data]` | ✅ | `wire/database.go:886-901`, `unpackSegments` `1062-1073` | Estado tomado de `p_resp_object` (0/1/2-EOF) — truco estándar, comentado. Segmento final malformado se descarta en silencio (`1067-1069`); preferible error. |
| Blobs > 64 KB (bucle multi-segmento en lectura; escritura en trozos de 32768 ≤ 65533) | ✅ | lectura `database.go:1016-1026`; escritura `database.go:1079-1153` | — |
| Blob NULL / blob_id 0 | ✅ (capa driver) | `rows.go:199-201`: `blobID == 0` → se deja tal cual (no se intenta abrir); NULL viene por null-bitmap | — |
| BPB | 🟡 | builder completo (`wire/bpb.go`) pero **nunca se pasa** un BPB (`database.go:975,1087`: nil) | Siempre blobs segmentados por defecto, sin transliteración de charset del lado servidor. Suficiente para database/sql ([]byte/string); los blobs de texto se decodifican en cliente (`rows.go:209-211`). |
| Pipelining con `0xFFFF` (open+get, create+puts+close en un flush) | ✅ | `database.go:972-1032`, `1084-1135` | Conforme a §9.x.1 (mismo objeto, secuencia create→use). Drena todas las respuestas incluso en error — mantiene sincronía. |
| `op_seek_blob`, `op_batch_segments` | ❌ | constantes definidas | No necesarios sin streamed blobs ni API de seek. ⛔ |
| `op_cancel_blob` en error de escritura | ✅ | `database.go:1148-1150` | Solo en el camino secuencial; en el pipelined un fallo de put deja el blob sin cancelar (el rollback de la tx lo limpia). Menor. |

## 9. Row data y BLR (cap. 14)

| Aspecto | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| Envelope BLR (`blr_version5, begin, message 0, count*2 LE16, …, end, eoc`) | ✅ | `wire/blr.go:5-51` | `blr_version4` para dialecto 1 no existe (ver hallazgo dialecto). |
| Par tipo + null indicator `blr_short 0` por columna | ✅ | `wire/blr.go:14-19` | — |
| Tipos BLR: short/long/int64/int128 con escala; float/double; text2/varying2 con charset (collation=0); bool; date/time/timestamp; TZ extendidos; dec64/dec128; blob2 con subtipo; SQL_NULL como `blr_text 0 0` | ✅ | `wire/blr.go:54-132` | Pedir siempre las formas TZ **extendidas** (`blr_ex_*`, `blr.go:102-106`) y decodificar el offset explícito (`types.go:198-209`) es la elección robusta (inmune a desfases de tzdata). |
| Fallback de tipos no soportados → `blr_varying2` (server convierte a string) | ✅ coherente | `wire/blr.go:125-128` + `types.go:276-279` (default decode = ReadString) | BLR y decode están **alineados**: para `SQL_D_FLOAT`, `SQL_QUAD`, etc. el server envía varying y el cliente lee varying → sin desincronización. Arrays darán error de conversión del server — aceptable (fuera de alcance). |
| **Null bitmap proto 13+** (lectura y escritura, LE bit por columna, padding a 4) | ✅ | lectura `types.go:285-339`; escritura `types.go:344-393` (`reserveNullBitset`) | Columnas null se saltan en el stream — correcto. Buffer inline de 256 columnas con crecimiento (`types.go:318-324`). |
| Params DEC16/DEC34 se envían como VARYING UTF8 (server convierte) | ✅ | `wire/blr.go:134-151` + `types.go:859-863` | Truco válido según §14.1 (tipos convertibles); evita codificar DPD en cliente. Los encoders DPD (`stringToDecfloat64/128`, `types.go:1782-1964`) quedan **muertos**. |
| CHAR: padding a `desc.Length` con 0x20 y truncación con error | 🟡 | `types.go:483-519` | 🐞 menor: para `CHAR CHARACTER SET OCTETS` (BINARY) la spec §14.2.2 exige padding 0x00; `copyTextParam` rellena siempre con 0x20 (`types.go:515-517`). VARYING sí distingue (`varyingPaddingByte`, `types.go:521-526`). Un param BINARY(n) corto llega con bytes 0x20 espurios. |
| VARCHAR: buffer con longitud real, padding 0x20/0x00 según binario | ✅ | `types.go:528-565` | Valida longitud máxima con error claro. |
| BOOLEAN | 🐞 en encode | decode `types.go:181-183` (ReadInt32 != 0 — funciona con "byte + 3 pad"); encode `types.go:803-809` escribe `WriteInt32(1)` **big-endian** → en el wire `00 00 00 01` | La spec §14.2.2 da dos interpretaciones válidas: byte en **primera** posición, o Int32 **little-endian** — ambas ponen el 1 en el primer byte. BE pone el 1 en el último: si el server lee el primer byte (xdr_opaque de 1 byte), `true` llega como `false`. **Ninguna prueba cubre un parámetro bool** (`driver_test.go:315-351` usa literales TRUE/FALSE). Verificar contra server real y corregir a `byte + pad`. |
| DATE/TIME/TIMESTAMP encode | 🐞 | `types.go:43-47` (`DateToMJD` convierte a **UTC**) vs `types.go:55-60` (`TimeToTicks` usa reloj **local**) | Mezcla inconsistente: para un `time.Time` con zona no-UTC, el MJD se calcula del instante UTC y los ticks del reloj de pared local → un TIMESTAMP `2024-05-01 01:00+02:00` se almacena como `2024-04-30 01:00`. Para DATE, cualquier fecha cuyo día UTC difiera del local se corre un día. Fix: usar los campos de pared (`Date()`/`Clock()`) sin conversión UTC en ambos, o convertir en ambos. El decode devuelve `time.UTC` con valores de pared — convención consistente del lado lectura. Además `int32(days)` trunca hacia cero → off-by-one para fechas < 1858 (cosmético). |
| TZ types encode (UTC + tz id de offset + offset) | ✅ | `types.go:874-885`, `tzOffsetToID` `types.go:2044-2048` (offset+1439, conforme §14.2.2) | Zonas con nombre se envían como offset fijo — pérdida de fidelidad aceptable para database/sql. |
| Escalados NUMERIC/DECIMAL: decode a string con punto decimal; encode desde int/float/string con redondeo half-away y chequeo de overflow | ✅ | decode `types.go:1212-1293`; encode `types.go:941-1131` | Float→scaled pasa por string decimal exacto (`strconv.FormatFloat(-1)`) — evita errores binarios. Overflow → error claro (`numericOverflow`). Muy sólido. |
| INT128 (decode two's complement → string; encode desde todo tipo numérico/string con escala) | ✅ | `types.go:260-274`, `1966-2041` | — |
| DECFLOAT decode (DPD, NaN/Inf, combo field) | ✅ | `types.go:1329-1632` | Implementación completa decimal64/128. |
| Charset en decode/encode | 🟡 | `types.go:222-240` pasa `desc.SubType` **sin enmascarar** a `fbcharset.Decode`, mientras el BLR enmascara `SubType & 0xFF` (`blr.go:78-88`) | Si el subtipo trae collation en el byte alto (columnas con collation no-default), `Decode` no matchea el charset y cae a passthrough → mojibake potencial con charsets de un byte + collation explícita. Alinear: enmascarar 0xFF también al decodificar. |
| Dialecto 1: numerics como DOUBLE y DATE viejo como TIMESTAMP | ✅ (decode) | double `types.go:173-179`, timestamp `types.go:193-196` — el server describe esos tipos y el decode existe | El problema del dialecto está en prepare/BLR (ver §7), no en los tipos. |

## 10. XDR (apéndices A y B)

| Aspecto | Estado | Evidencia | Riesgo / notas |
|---|---|---|---|
| Enteros big-endian | ✅ | `wire/reader.go:140-175`, `wire/writer.go:154-173` | — |
| Buffer = len + data + padding `(4-len)&3` | ✅ | `writer.go:176-200`, `reader.go:183-199` | Idéntico a la fórmula de la spec. |
| Opaque de n bytes + padding (CHAR, null bitmap) | ✅ | `writer.go:202-211`, `reader.go:212-227` | — |
| Fixup de longitudes con sign-extension de servers viejos (`0xFFFF____`) | ✅ | `reader.go:188-191` | Detalle de compatibilidad correcto (spec §4.1). |
| Riesgo de desalineación por tipo | ✅ ninguno detectado | Todos los caminos de decode consumen tamaños múltiplos de 4 (SQLText consume `length+pad`, varying vía ReadBuffer, bitset padded) | Revisados uno a uno contra la tabla §14.2.2; los tamaños coinciden (short/long/float/date/time=4; double/timestamp/int64/blob/dec16=8; ttz_ex=12; tstz_ex=16; int128/dec34=16). `IOLength` (`wire/blr.go:153-176`) tiene valores incorrectos (BOOLEAN=2, y no distingue TZ ex) pero **solo lo usan los tests** — eliminar o corregir. |
| Lector con ventana deslizante, error sticky, `readView` zero-copy | ✅ | `reader.go:46-133` | Diseño sólido; el contrato "válido hasta el próximo read" está documentado. |

## 11. Hallazgos priorizados

### Bugs
1. **(P0) Error del server en mitad de un fetch desincroniza y no invalida la conexión** — `wire/database.go:702-704` + `transport.go:15-43`. La cancelación vía context durante un fetch (camino soportado y frecuente) lo dispara. Manejar `op_response` en el bucle de fetch y marcar bad toda desincronización de protocolo.
2. **(P0, verificar en server real) Parámetro BOOLEAN codificado como Int32 big-endian** — `types.go:803-809`; la spec exige el valor en el primer byte. Sin cobertura de test (solo literales SQL). `true` puede llegar como `false`.
3. **(P0) Encode de DATE/TIMESTAMP mezcla UTC y reloj local** — `types.go:43-47` vs `55-60`; corrimiento de un día para `time.Time` con zona ≠ UTC.
4. **(P1) ChaCha con doble SHA-256 y parser de `p_acpt_keys` no verificado** — `handshake.go:364-373` + `crypt.go:28-33`. O ChaCha está roto o está muerto (siempre cae a Arc4). Arreglar o eliminar.
5. **(P1) `wire_crypt=required` no es exigido** — sin session key continúa en claro (`handshake.go:294`); con server sin crypt envía `op_crypt` a ciegas y lee basura (`handshake.go:359-385`). Validar oferta del server y fallar con error claro.
6. **(P2) Padding de CHAR OCTETS con 0x20 en vez de 0x00** — `types.go:515-517`.

### Falta para 1.0
7. **Error claro ante server solo-Legacy_Auth** — hoy: `isc_login` genérico tras enviar datos SRP bajo el nombre del plugin del server (`handshake.go:183-185`). No cuelga, pero el mensaje no orienta. (Implementar Legacy completo es opcional; el error claro no lo es.)
8. **Dialecto 1: propagarlo a prepare/BLR o rechazarlo en el DSN** — `database.go:300,346` hardcodea 3; `dsn.go:84-89` acepta 1 en silencio.
9. **Truncación del buffer de describe sin detección** — `info.go:207`, buffer fijo 65535; SELECTs muy anchos fallan de forma confusa. Mínimo: detectar `isc_info_truncated` y devolver error explícito.
10. `op_reject` debería mapear a `isc_connect_reject` y `op_crypt_key_callback` a un error "bases cifradas no soportadas" (mensajes, no lógica).

### Fuera de alcance — verificado seguro
- **Services, events, arrays, batches (v17), scrollable (v18), inline blobs (v19), 2PC, `op_exec_immediate`, `op_set_cursor`, compresión**: ninguno se anuncia ni se negocia, por lo que el server no puede iniciarlos; las constantes existen pero no hay caminos muertos peligrosos. La única mejora deseable es mensaje de error específico cuando el server envía algo inesperado (hoy "unexpected opcode N").

## 12. Notas de arquitectura

**Tamaño y cohesión de archivos**
- `wire/types.go` (2048 líneas) mezcla cinco responsabilidades: conversiones fecha/hora, codec de filas (decode+encode), conversión numérica/escalado, DPD/DECFLOAT y helpers de formato. Dividir en `xdr_decode.go`, `xdr_encode.go`, `datetime.go`, `decfloat.go`, `numeric.go` haría revisable cada pieza.
- `wire/database.go` (1159 líneas) contiene *todas* las operaciones (db, tx, stmt, blob) sobre `WireConnection`. Separar por capítulo de la spec (statements.go, blobs.go, transactions.go) daría un mapeo 1:1 código↔spec que habría hecho esta auditoría (y las futuras) mucho más barata.

**Duplicación**
- `encodeValue` (`types.go:762-899`) y `encodeValueStack` (`types.go:568-711`) son ~140 líneas duplicadas que solo difieren en el tipo de writer; una interfaz mínima (`WriteInt32/WriteInt64/raw`) o generics eliminaría la copia. Hay además **seis** variantes de `EncodeParams*` (`types.go:344-481`) para las combinaciones {Writer, StackWriter} × {[]any, []driver.NamedValue} × {err, no-err}; las variantes sin error son wrappers que descartan el error — invitación a bugs silenciosos.
- Dos bucles de fetch: `Fetch` (una respuesta, `database.go:595-625`) y `FetchRowsReuse` (`database.go:636-757`). `Fetch` y `FetchRows` no tienen llamadores fuera de wire — eliminar o fusionar. El fix del hallazgo #1 debe aplicarse en un único lugar.
- `readBlobDataPipelined` / `readBlobDataSequential` y los dos caminos de `WriteBlobData` duplican la lógica de segmentación; el pipelined podría degradar a flush-por-op con el mismo cuerpo.
- `formatDecfloat` (`types.go:1635-1714`) es un duplicado muerto de `formatDecimalCoefficient`; `stringToDecfloat64/128`, `stringToInt128`, `applyScale`, `ReadNullBitset`, `AllocateStatementLazy`, `ReadLazyResponse`, `TransactionExecuteCommit`, `ExecuteAndCommit` y `IOLength` no tienen llamadores en producción — un pase de limpieza pre-1.0 reduciría superficie de auditoría.

**Manejo de estado y errores**
- El handshake completo es una función de 290 líneas (`handshake.go:68-356`) con el estado en variables locales y ramas anidadas; una struct `handshakeState` con pasos nombrados (connect → auth → crypt → attach) reflejaría la máquina de estados de la spec §5.11.2 y haría insertables los casos que hoy faltan (crypt_key_callback, Legacy, op_accept).
- Patrón sticky-error del `Reader` es bueno, pero convive con dos convenciones de retorno (error explícito vs `r.Err()` chequeado por el llamador); `readGenericResponse` devuelve struct sin error y obliga al llamador a mirar `r.err` — fácil de olvidar (ocurre en `handshake.go:276-284`, donde está bien, pero el patrón es frágil).
- `GenericResponse.Data` aliasa un buffer interno reutilizado (`response.go:36-44`): el contrato "válido hasta el próximo ReadResponse" es correcto hoy porque los llamadores copian o consumen inmediato, pero no hay nada que lo haga cumplir; un comentario en cada caller o un `Copy()` explícito reduciría el riesgo.
- La clasificación fatal/no-fatal vive en la capa driver (`transport.go`) y se basa en tipos de error de red + substrings; los errores de **desincronización de protocolo** ("unexpected opcode") generados en wire no llevan ningún marcador tipado, de ahí el hallazgo #1. Sugerencia: un `type ProtocolError` en wire que la capa driver trate siempre como fatal.
- Consistencia de mensajes: wire usa prefijos `op_x:` de forma bastante disciplinada; la capa driver mezcla `firebird:` y errores sin prefijo. Menor.

**Concurrencia**
- La separación `cancelMu`/`writeMu` y el `withCancel` con goroutine + stop bloqueante (`connection.go:652-676`) es un diseño correcto y testeado (`transport_test.go:155-164`) para el requisito de asincronía de la spec §6.6.

**Tests (observación transversal)**
- La cobertura unitaria de wire es buena (SRP con vectores, XDR, BLR, DPD), pero los tres bugs P0 caen exactamente en huecos de test: parámetros BOOLEAN (solo literales), `time.Time` con zona no-UTC (tests usan UTC), y errores del server a mitad de fetch (ningún test lo simula). Los tests unitarios de crypt validan round-trips consigo mismos, no vectores de Firebird — por eso el doble-SHA256 de ChaCha es invisible. Añadir vectores conocidos (nonce/clave/keystream de una traza real) y un mock-server que inyecte `op_response` durante fetch.
