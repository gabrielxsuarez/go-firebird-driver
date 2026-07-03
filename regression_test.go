package firebird

// Tests de regresión de la revisión pre-1.0 (review/01a, 01b).
// Cada test reproduce un bug confirmado contra un servidor real.

import (
	"context"
	"fmt"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// Bug: BOOLEAN bindeado como parámetro se codificaba con el byte significativo
// al final (WriteInt32) en lugar del primero, y el servidor leía siempre false.
func TestRegressionBoolParamRoundTrip(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE regr_bool (id INTEGER, b BOOLEAN)")
	mustExec(t, db, "INSERT INTO regr_bool VALUES (?, ?)", 1, true)
	mustExec(t, db, "INSERT INTO regr_bool VALUES (?, ?)", 2, false)

	var b1, b2 bool
	if err := db.QueryRow("SELECT b FROM regr_bool WHERE id = 1").Scan(&b1); err != nil {
		t.Fatalf("scan true: %v", err)
	}
	if err := db.QueryRow("SELECT b FROM regr_bool WHERE id = 2").Scan(&b2); err != nil {
		t.Fatalf("scan false: %v", err)
	}
	if !b1 || b2 {
		t.Fatalf("roundtrip BOOLEAN: escribí (true,false), leí (%v,%v)", b1, b2)
	}
}

// Bug: DateToMJD convertía a UTC mientras TimeToTicks usaba hora de pared:
// un time.Time con zona no-UTC podía almacenarse con la fecha corrida un día.
func TestRegressionTimestampNonUTCWallClock(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE regr_ts (id INTEGER, ts TIMESTAMP, d DATE)")

	cases := []struct {
		id   int
		in   time.Time
		wall string // wall clock esperado del TIMESTAMP
		date string // fecha esperada del DATE
	}{
		// 23:30 en UTC-3 → en UTC ya es el día siguiente: debe preservarse la pared.
		{1, time.Date(2026, 1, 15, 23, 30, 0, 0, time.FixedZone("AR", -3*3600)), "2026-01-15 23:30:00", "2026-01-15"},
		// 00:30 en UTC+3 → en UTC todavía es el día anterior.
		{2, time.Date(2026, 1, 15, 0, 30, 0, 0, time.FixedZone("EAT", 3*3600)), "2026-01-15 00:30:00", "2026-01-15"},
		// Fecha lejana: time.Duration satura a ±292 años; la aritmética de fechas no debe usarla.
		{3, time.Date(3000, 6, 15, 12, 0, 0, 0, time.UTC), "3000-06-15 12:00:00", "3000-06-15"},
		{4, time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), "0001-01-01 00:00:00", "0001-01-01"},
		// Anterior a la época MJD (1858-11-17) con hora: el truncamiento hacia cero redondeaba mal.
		{5, time.Date(1800, 7, 4, 12, 0, 0, 0, time.UTC), "1800-07-04 12:00:00", "1800-07-04"},
	}
	for _, c := range cases {
		mustExec(t, db, "INSERT INTO regr_ts VALUES (?, ?, ?)", c.id, c.in, c.in)
	}
	for _, c := range cases {
		var ts, d time.Time
		if err := db.QueryRow("SELECT ts, d FROM regr_ts WHERE id = ?", c.id).Scan(&ts, &d); err != nil {
			t.Fatalf("caso %d: scan: %v", c.id, err)
		}
		if got := ts.Format("2006-01-02 15:04:05"); got != c.wall {
			t.Errorf("caso %d: TIMESTAMP escribí pared %s, leí %s", c.id, c.wall, got)
		}
		if got := d.Format("2006-01-02"); got != c.date {
			t.Errorf("caso %d: DATE escribí %s, leí %s", c.id, c.date, got)
		}
	}
}

// Bug: pasar menos args que placeholders bindeaba NULL en silencio;
// args de más se ignoraban.
func TestRegressionArgCountMismatch(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE regr_args (a INTEGER, b INTEGER)")

	if _, err := db.Exec("INSERT INTO regr_args VALUES (?, ?)", 1); err == nil {
		t.Error("insert con 1 arg para 2 placeholders: esperaba error, fue aceptado")
	}
	if _, err := db.Exec("INSERT INTO regr_args VALUES (?, ?)", 1, 2, 3); err == nil {
		t.Error("insert con 3 args para 2 placeholders: esperaba error, fue aceptado")
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM regr_args").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("se insertaron %d filas con conteo de args inválido", n)
	}
	// Query también
	if rows, err := db.Query("SELECT a FROM regr_args WHERE a = ? AND b = ?", 1); err == nil {
		rows.Close()
		t.Error("query con 1 arg para 2 placeholders: esperaba error")
	}
}

// Bug: uint64 > MaxInt64 hacía wraparound a negativo en silencio.
func TestRegressionUint64Overflow(t *testing.T) {
	db := openTestDB(t)
	var out string
	err := db.QueryRow("SELECT CAST(? AS BIGINT) FROM rdb$database", uint64(1)<<63).Scan(&out)
	if err == nil {
		t.Fatalf("uint64 2^63: esperaba error de overflow, se insertó como %s", out)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "uint64") {
		t.Errorf("el error debería mencionar uint64: %v", err)
	}
}

// Bug: una string no numérica bindeada a FLOAT/DOUBLE se convertía a 0 en silencio,
// y un valor no-time bindeado a DATE/TIMESTAMP se convertía a zero-time.
func TestRegressionSilentParamCoercion(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE regr_coerce (f DOUBLE PRECISION, ts TIMESTAMP)")

	if _, err := db.Exec("INSERT INTO regr_coerce (f) VALUES (?)", "not-a-number"); err == nil {
		var f float64
		_ = db.QueryRow("SELECT f FROM regr_coerce").Scan(&f)
		t.Errorf("string basura a DOUBLE: esperaba error, se insertó %v", f)
	}
	if _, err := db.Exec("INSERT INTO regr_coerce (ts) VALUES (?)", 12345); err == nil {
		var ts time.Time
		_ = db.QueryRow("SELECT ts FROM regr_coerce WHERE ts IS NOT NULL").Scan(&ts)
		t.Errorf("int a TIMESTAMP: esperaba error, se insertó %v", ts)
	}
}

// Bug: un error del servidor en medio de un fetch por lotes (op_response en lugar
// de op_fetch_response) devolvía "unexpected opcode 9", perdía el error real y
// dejaba la conexión desincronizada (inutilizable) dentro del pool.
func TestRegressionFetchMidStreamError(t *testing.T) {
	db := openTestDB(t)
	// MaxOpenConns=1 (openTestDB) garantiza que la siguiente query reusa la conexión.
	rows, err := db.Query(`
		WITH RECURSIVE n(i) AS (
			SELECT 1 FROM rdb$database
			UNION ALL SELECT i+1 FROM n WHERE i < 400
		)
		SELECT i, 1000 / (300 - i) FROM n`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	count := 0
	for rows.Next() {
		var i, v int
		if err := rows.Scan(&i, &v); err != nil {
			break
		}
		count++
	}
	ferr := rows.Err()
	rows.Close()

	if ferr == nil {
		t.Fatal("error del servidor a mitad de fetch: se perdió (rows.Err() == nil)")
	}
	if strings.Contains(ferr.Error(), "unexpected opcode") {
		t.Errorf("se esperaba el error real del servidor (división por cero), no desincronización: %v", ferr)
	}
	// GDS 335544321 = arithmetic exception / division by zero
	if !strings.Contains(ferr.Error(), "335544") {
		t.Errorf("el error debería contener el código GDS del servidor: %v", ferr)
	}

	// La conexión debe seguir siendo utilizable (el error fue del statement, no del transporte).
	var one int
	if err := db.QueryRow("SELECT 1 FROM rdb$database").Scan(&one); err != nil {
		t.Fatalf("conexión inutilizable tras error de fetch: %v", err)
	}
}

// Bug: stmt.Exec en autocommit difería el commit a stmt.Close(). database/sql
// cachea statements preparados y no los cierra: los INSERT quedaban invisibles
// para otras conexiones (y no durables) hasta cerrar el stmt o la base.
func TestRegressionPreparedExecAutocommit(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE regr_prep (id INTEGER)")

	stmt, err := db.Prepare("INSERT INTO regr_prep (id) VALUES (?)")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()
	if _, err := stmt.Exec(42); err != nil {
		t.Fatalf("exec: %v", err)
	}

	// Segunda conexión independiente: la fila debe ser visible SIN cerrar stmt.
	db2, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()
	var n int
	if err := db2.QueryRow("SELECT COUNT(*) FROM regr_prep").Scan(&n); err != nil {
		t.Fatalf("count desde otra conexión: %v", err)
	}
	if n != 1 {
		t.Fatalf("INSERT preparado no visible desde otra conexión antes de stmt.Close(): count=%d", n)
	}

	// RowsAffected debe calcularse en el momento del Exec (no lazy sobre una
	// conexión que el pool pudo reasignar).
	res, err := stmt.Exec(43)
	if err != nil {
		t.Fatalf("exec 2: %v", err)
	}
	if ra, err := res.RowsAffected(); err != nil || ra != 1 {
		t.Errorf("RowsAffected: got (%d, %v), want (1, nil)", ra, err)
	}
}

// sql.Named debe rechazarse con un error claro (no bindear posicional en silencio).
func TestRegressionNamedArgsRejected(t *testing.T) {
	db := openTestDB(t)
	var out int
	err := db.QueryRow("SELECT COUNT(*) FROM rdb$database WHERE rdb$relation_id = ?",
		sql.Named("x", 0)).Scan(&out)
	if err == nil {
		t.Error("sql.Named aceptado en silencio; esperaba error explícito")
	} else if !strings.Contains(strings.ToLower(err.Error()), "named") {
		t.Errorf("el error debería explicar que named args no están soportados: %v", err)
	}
}

// Bug: DML ejecutado por el camino de Query en autocommit (INSERT ... RETURNING
// vía QueryRow, UPDATE vía Query) nunca se commiteaba: rows.Close no hacía nada
// y la transacción autocommit persistente quedaba con cambios invisibles.
func TestRegressionQueryDMLAutocommit(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE regr_ret (id INTEGER GENERATED BY DEFAULT AS IDENTITY, txt VARCHAR(20))")

	var id int
	if err := db.QueryRow("INSERT INTO regr_ret (txt) VALUES (?) RETURNING id", "hola").Scan(&id); err != nil {
		t.Fatalf("insert returning: %v", err)
	}
	if id == 0 {
		t.Fatal("RETURNING no devolvió id")
	}

	db2, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()
	var n int
	if err := db2.QueryRow("SELECT COUNT(*) FROM regr_ret").Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("INSERT..RETURNING no commiteado: visible count=%d", n)
	}

	// UPDATE por el camino de Query (sin RETURNING)
	rows, err := db.Query("UPDATE regr_ret SET txt = 'chau'")
	if err != nil {
		t.Fatalf("update via query: %v", err)
	}
	rows.Close()
	var txt string
	if err := db2.QueryRow("SELECT txt FROM regr_ret ROWS 1").Scan(&txt); err != nil {
		t.Fatalf("select txt: %v", err)
	}
	if txt != "chau" {
		t.Fatalf("UPDATE vía Query no commiteado: txt=%q", txt)
	}
}

// Bug: wire.Connect usaba net.Dial sin contexto ni deadline: el timeout de
// conexión de database/sql no se respetaba (colgaba hasta el timeout del SO).
func TestRegressionConnectContextTimeout(t *testing.T) {
	db, err := sql.Open("firebird", "firebird://sysdba:masterkey@10.255.255.1:3050/none.fdb")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = db.PingContext(ctx)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("ping a IP no ruteable: esperaba error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("el timeout del contexto (500ms) no se respetó: tardó %s", elapsed)
	}
}

// wire_crypt=required debe funcionar contra un servidor con crypt disponible
// (y fallar con error claro si la negociación no produce session key).
func TestRegressionWireCryptRequired(t *testing.T) {
	sep := "?"
	if strings.Contains(testDSN, "?") {
		sep = "&"
	}
	db, err := sql.Open("firebird", testDSN+sep+"wire_crypt=required")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	var one int
	if err := db.QueryRow("SELECT 1 FROM rdb$database").Scan(&one); err != nil {
		t.Fatalf("query con wire_crypt=required: %v", err)
	}
}

// Decisión pre-1.0: el dialecto de cliente 1 no está soportado (prepare usa
// dialecto 3 siempre); aceptarlo en el DSN y usar otro en silencio era peor
// que rechazarlo. Las BASES dialecto 1 funcionan con cliente dialecto 3.
func TestRegressionDialect1Rejected(t *testing.T) {
	for _, d := range []string{"1", "2"} {
		_, err := ParseDSN("user:pass@localhost/db.fdb?dialect=" + d)
		if err == nil {
			t.Errorf("dialect=%s: esperaba error, fue aceptado", d)
		} else if !strings.Contains(err.Error(), "dialect") {
			t.Errorf("dialect=%s: el error debería mencionar el dialecto: %v", d, err)
		}
	}
	if _, err := ParseDSN("user:pass@localhost/db.fdb?dialect=3"); err != nil {
		t.Errorf("dialect=3 debería aceptarse: %v", err)
	}
}

// Bug: el describe del prepare usaba un buffer fijo de 64KB sin detectar
// isc_info_truncated: statements muy anchos devolvían metadata parcial y
// fallaban de forma confusa. Ahora se sigue la continuación con
// isc_info_sql_sqlda_start hasta completar (como jaybird).
func TestRegressionDescribeTruncation(t *testing.T) {
	db := openTestDB(t)

	const numCols = 800
	colName := func(i int) string {
		return fmt.Sprintf("COL_ABCDEFGHIJKLMNOPQRS_%04d", i)
	}
	var ddl strings.Builder
	ddl.WriteString("RECREATE TABLE regr_wide (")
	for i := 0; i < numCols; i++ {
		if i > 0 {
			ddl.WriteString(", ")
		}
		ddl.WriteString(colName(i))
		ddl.WriteString(" SMALLINT")
	}
	ddl.WriteString(")")
	mustExec(t, db, ddl.String())

	// Camino QueryContext (PrepareInfoItems: nombres+alias → describe más grande)
	rows, err := db.Query("SELECT * FROM regr_wide")
	if err != nil {
		t.Fatalf("select ancho: %v", err)
	}
	cols, err := rows.Columns()
	rows.Close()
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	if len(cols) != numCols {
		t.Fatalf("describe truncado: %d columnas, esperaba %d", len(cols), numCols)
	}
	for i, c := range cols {
		if c != colName(i) {
			t.Fatalf("columna %d: %q, esperaba %q (descriptores desalineados tras continuación)", i, c, colName(i))
		}
	}

	// Camino PrepareContext
	stmt, err := db.Prepare("SELECT * FROM regr_wide")
	if err != nil {
		t.Fatalf("prepare ancho: %v", err)
	}
	stmt.Close()

	// Camino ExecContext con la sección BIND truncada (800 parámetros)
	var ins strings.Builder
	ins.WriteString("INSERT INTO regr_wide VALUES (")
	args := make([]any, numCols)
	for i := 0; i < numCols; i++ {
		if i > 0 {
			ins.WriteString(",")
		}
		ins.WriteString("?")
		args[i] = i % 100
	}
	ins.WriteString(")")
	mustExec(t, db, ins.String(), args...)

	var v0, v799 int
	if err := db.QueryRow(fmt.Sprintf("SELECT %s, %s FROM regr_wide", colName(0), colName(numCols-1))).Scan(&v0, &v799); err != nil {
		t.Fatalf("readback: %v", err)
	}
	if v0 != 0 || v799 != (numCols-1)%100 {
		t.Fatalf("valores mal bindeados tras continuación: col0=%d col799=%d", v0, v799)
	}
	mustExec(t, db, "DROP TABLE regr_wide")
}

// Mejora pre-1.0: los errores del servidor incluyen el texto humano de la
// tabla de mensajes embebida (antes: "GDS 335544351: " sin texto).
func TestRegressionErrorMessageText(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec("DROP TABLE tabla_que_no_existe_xyz")
	if err == nil {
		t.Fatal("esperaba error")
	}
	msg := err.Error()
	for _, want := range []string{"unsuccessful metadata update", "TABLA_QUE_NO_EXISTE_XYZ", "does not exist", "GDS 335544351"} {
		if !strings.Contains(msg, want) {
			t.Errorf("el error debería contener %q: %s", want, msg)
		}
	}
}

// Bug: un BLOB no-NULL con blob id 0 (blob vacío; aparece en bases legacy,
// p.ej. interacciones.fdb col MANEJO) se filtraba al usuario como int64(0)
// en vez de ""/[]byte{}. Un servidor moderno no genera ids 0, así que el caso
// se cubre a nivel unitario sobre la materialización.
func TestRegressionEmptyBlobIDZero(t *testing.T) {
	c := &conn{config: &Config{Charset: "UTF8"}}
	outputs := []wire.ColumnDescriptor{
		{SQLType: wire.SQLBlob, SubType: 1}, // texto
		{SQLType: wire.SQLBlob, SubType: 0}, // binario
		{SQLType: wire.SQLBlob, SubType: 1}, // NULL real: no se toca
	}
	rowsData := [][]any{{int64(0), int64(0), nil}}
	if err := c.materializeBlobRowsLocked(0, outputs, rowsData); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got, ok := rowsData[0][0].(string); !ok || got != "" {
		t.Errorf("blob texto vacío: esperaba \"\", obtuve %#v", rowsData[0][0])
	}
	if got, ok := rowsData[0][1].([]byte); !ok || len(got) != 0 {
		t.Errorf("blob binario vacío: esperaba []byte{}, obtuve %#v", rowsData[0][1])
	}
	if rowsData[0][2] != nil {
		t.Errorf("blob NULL: esperaba nil, obtuve %#v", rowsData[0][2])
	}
}

// Bug: las filas iniciales de EXECUTE PROCEDURE (camino Execute2, tanto ad-hoc
// como preparado) se entregaban sin materializar blobs: el usuario recibía el
// blob id interno (int64) en vez del contenido.
func TestRegressionExecProcedureBlobMaterialized(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, `RECREATE PROCEDURE regr_blob_proc (modo INTEGER)
RETURNS (bt BLOB SUB_TYPE TEXT, bb BLOB SUB_TYPE 0) AS
BEGIN
  IF (modo = 1) THEN
  BEGIN
    bt = 'contenido del blob';
    bb = 'bytes';
  END
  ELSE IF (modo = 2) THEN
  BEGIN
    bt = '';
    bb = '';
  END
END`)
	t.Cleanup(func() { _, _ = db.Exec("DROP PROCEDURE regr_blob_proc") })

	check := func(t *testing.T, scan func(modo int, dest ...any) error) {
		t.Helper()
		var bt sql.NullString
		var bb []byte
		// Con datos: antes del fix llegaba el blob id como int64.
		if err := scan(1, &bt, &bb); err != nil {
			t.Fatalf("modo 1: %v", err)
		}
		if !bt.Valid || bt.String != "contenido del blob" {
			t.Errorf("modo 1 texto: esperaba contenido, obtuve %#v", bt)
		}
		if string(bb) != "bytes" {
			t.Errorf("modo 1 binario: esperaba \"bytes\", obtuve %#v", bb)
		}
		// Vacío: debe llegar ""/[]byte{}, nunca un número.
		if err := scan(2, &bt, &bb); err != nil {
			t.Fatalf("modo 2: %v", err)
		}
		if !bt.Valid || bt.String != "" {
			t.Errorf("modo 2 texto: esperaba \"\" no-NULL, obtuve %#v", bt)
		}
		if bb == nil || len(bb) != 0 {
			t.Errorf("modo 2 binario: esperaba []byte{}, obtuve %#v", bb)
		}
		// NULL: los outputs sin asignar son NULL.
		if err := scan(3, &bt, &bb); err != nil {
			t.Fatalf("modo 3: %v", err)
		}
		if bt.Valid {
			t.Errorf("modo 3 texto: esperaba NULL, obtuve %#v", bt)
		}
		if bb != nil {
			t.Errorf("modo 3 binario: esperaba nil, obtuve %#v", bb)
		}
	}

	t.Run("adhoc", func(t *testing.T) {
		check(t, func(modo int, dest ...any) error {
			return db.QueryRow("EXECUTE PROCEDURE regr_blob_proc(?)", modo).Scan(dest...)
		})
	})
	t.Run("preparado", func(t *testing.T) {
		stmt, err := db.Prepare("EXECUTE PROCEDURE regr_blob_proc(?)")
		if err != nil {
			t.Fatalf("prepare: %v", err)
		}
		defer stmt.Close()
		check(t, func(modo int, dest ...any) error {
			return stmt.QueryRow(modo).Scan(dest...)
		})
	})
}

// Regresión del contrato de blobs por SELECT normal: con datos, vacío y NULL
// deben distinguirse (vacío ≠ NULL, y nunca un int64 interno).
func TestRegressionBlobEmptyVsNull(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE regr_blob (id INTEGER, bt BLOB SUB_TYPE TEXT, bb BLOB SUB_TYPE 0)")
	mustExec(t, db, "INSERT INTO regr_blob VALUES (1, ?, ?)", "hola", []byte{1, 2, 3})
	mustExec(t, db, "INSERT INTO regr_blob VALUES (2, ?, ?)", "", []byte{})
	mustExec(t, db, "INSERT INTO regr_blob VALUES (3, NULL, NULL)")

	var bt sql.NullString
	var bb []byte
	if err := db.QueryRow("SELECT bt, bb FROM regr_blob WHERE id = 1").Scan(&bt, &bb); err != nil {
		t.Fatalf("id 1: %v", err)
	}
	if !bt.Valid || bt.String != "hola" || string(bb) != "\x01\x02\x03" {
		t.Errorf("id 1: esperaba (hola, 010203), obtuve (%#v, %#v)", bt, bb)
	}
	if err := db.QueryRow("SELECT bt, bb FROM regr_blob WHERE id = 2").Scan(&bt, &bb); err != nil {
		t.Fatalf("id 2: %v", err)
	}
	if !bt.Valid || bt.String != "" || bb == nil || len(bb) != 0 {
		t.Errorf("id 2: esperaba vacíos no-NULL, obtuve (%#v, %#v)", bt, bb)
	}
	if err := db.QueryRow("SELECT bt, bb FROM regr_blob WHERE id = 3").Scan(&bt, &bb); err != nil {
		t.Fatalf("id 3: %v", err)
	}
	if bt.Valid || bb != nil {
		t.Errorf("id 3: esperaba NULLs, obtuve (%#v, %#v)", bt, bb)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
