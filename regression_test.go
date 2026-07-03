package firebird

// Tests de regresión de la revisión pre-1.0 (review/01a, 01b).
// Cada test reproduce un bug confirmado contra un servidor real.

import (
	"database/sql"
	"strings"
	"testing"
	"time"
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

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
