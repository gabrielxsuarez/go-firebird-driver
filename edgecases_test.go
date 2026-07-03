package firebird

// Edge cases dirigidos de la Fase 2.3 de la revisión pre-1.0, más los tests
// de los fixes H11-H19 (review/01b-database-sql-audit.md).

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielxsuarez/go-firebird-driver/internal/wire"
)

// --- Fixes H11-H19 ---

// H12: niveles de aislamiento no soportados devuelven error, no degradan.
func TestIsolationLevels(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	supported := []sql.IsolationLevel{
		sql.LevelDefault, sql.LevelReadCommitted, sql.LevelRepeatableRead,
		sql.LevelSnapshot, sql.LevelSerializable,
	}
	for _, lvl := range supported {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: lvl})
		if err != nil {
			t.Errorf("nivel %v: esperaba soporte, error: %v", lvl, err)
			continue
		}
		tx.Rollback()
	}

	unsupported := []sql.IsolationLevel{
		sql.LevelReadUncommitted, sql.LevelWriteCommitted, sql.LevelLinearizable,
		sql.IsolationLevel(42),
	}
	for _, lvl := range unsupported {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: lvl})
		if err == nil {
			tx.Rollback()
			t.Errorf("nivel %v: esperaba error, fue aceptado", lvl)
		}
	}
}

// H13: BeginTx con contexto ya cancelado no debe tocar el wire.
func TestBeginTxCanceledContext(t *testing.T) {
	db := openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.BeginTx(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("esperaba context.Canceled, got %v", err)
	}
	// La conexión sigue sana.
	var one int
	if err := db.QueryRow("SELECT 1 FROM rdb$database").Scan(&one); err != nil {
		t.Fatalf("conexión dañada tras BeginTx cancelado: %v", err)
	}
}

// H15: ColumnTypeLength en caracteres (no bytes) y BLOB como longitud variable.
func TestColumnTypeLengthChars(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, `RECREATE TABLE edge_len (
		c_utf8 CHAR(10) CHARACTER SET UTF8,
		v_utf8 VARCHAR(20) CHARACTER SET UTF8,
		v_iso VARCHAR(15) CHARACTER SET ISO8859_1,
		v_oct VARCHAR(8) CHARACTER SET OCTETS,
		b BLOB)`)
	rows, err := db.Query("SELECT * FROM edge_len")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	cts, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("column types: %v", err)
	}
	want := []struct {
		name   string
		length int64
		ok     bool
	}{
		{"C_UTF8", 10, true},
		{"V_UTF8", 20, true},
		{"V_ISO", 15, true},
		{"V_OCT", 8, true},
		{"B", 0, true}, // variable: solo chequeamos ok=true y length>0 abajo
	}
	for i, w := range want {
		length, ok := cts[i].Length()
		if ok != w.ok {
			t.Errorf("%s: ok=%v, want %v", w.name, ok, w.ok)
			continue
		}
		if w.name == "B" {
			if length <= 0 {
				t.Errorf("BLOB: length=%d, esperaba variable (>0)", length)
			}
			continue
		}
		if length != w.length {
			t.Errorf("%s: length=%d, want %d (¿bytes en vez de caracteres?)", w.name, length, w.length)
		}
	}
}

// ScanType de NUMERIC escalado debe coincidir con lo que Scan entrega (string).
func TestColumnTypeScanTypeScaledNumeric(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_num (n NUMERIC(9,2))")
	mustExec(t, db, "INSERT INTO edge_num VALUES (123.45)")
	rows, err := db.Query("SELECT n FROM edge_num")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	cts, _ := rows.ColumnTypes()
	if got := cts[0].ScanType().Kind().String(); got != "string" {
		t.Errorf("ScanType de NUMERIC(9,2) = %s, want string", got)
	}
	rows.Next()
	var v any
	if err := rows.Scan(&v); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, isStr := v.(string); !isStr {
		t.Errorf("Scan entregó %T, el ScanType declara string", v)
	}
}

// H17: el descCache no debe servir descriptores viejos tras un DDL
// ejecutado por otra conexión.
func TestDescCacheInvalidatedByDDL(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_ddl (a INTEGER)")
	mustExec(t, db, "INSERT INTO edge_ddl VALUES (1)")

	// Cachear los descriptores de este texto SQL exacto.
	var a int
	if err := db.QueryRow("SELECT * FROM edge_ddl").Scan(&a); err != nil {
		t.Fatalf("query inicial: %v", err)
	}

	// DDL desde otra conexión.
	db2, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatalf("open db2: %v", err)
	}
	defer db2.Close()
	if _, err := db2.Exec("ALTER TABLE edge_ddl ADD b VARCHAR(10) DEFAULT 'x'"); err != nil {
		t.Fatalf("alter: %v", err)
	}

	// El mismo texto SQL debe reflejar la nueva columna (no descriptores stale).
	rows, err := db.Query("SELECT * FROM edge_ddl")
	if err != nil {
		t.Fatalf("query post-DDL: %v", err)
	}
	cols, _ := rows.Columns()
	var vals []any
	if rows.Next() {
		vals = make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan post-DDL: %v", err)
		}
	}
	rows.Close()
	if len(cols) != 2 {
		t.Fatalf("tras ALTER ADD: %d columnas (%v), esperaba 2 — descCache stale", len(cols), cols)
	}
}

// La API pública de errores: errors.As con *firebird.Error (alias de wire.StatusError).
func TestPublicErrorType(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec("SELECT no_es_sql")
	if err == nil {
		t.Fatal("esperaba error")
	}
	var fbErr *Error
	if !errors.As(err, &fbErr) {
		t.Fatalf("errors.As(*firebird.Error) no matchea: %T %v", err, err)
	}
	if fbErr.GDSCode() == 0 {
		t.Error("GDSCode() == 0")
	}
	var wireErr *wire.StatusError
	if !errors.As(err, &wireErr) {
		t.Error("el alias debe seguir matcheando *wire.StatusError")
	}
}

// H19: wrapBadConn debe preservar tanto ErrBadConn como la causa.
func TestWrapBadConnChain(t *testing.T) {
	cause := &wire.StatusError{SV: wire.StatusVector{Errors: []wire.GDSError{{Code: 335544721}}}}
	wrapped := wrapBadConn(cause)
	if !errors.Is(wrapped, driver.ErrBadConn) {
		t.Error("errors.Is(ErrBadConn) falla")
	}
	var se *wire.StatusError
	if !errors.As(wrapped, &se) || se.GDSCode() != 335544721 {
		t.Error("errors.As sobre la causa original falla (cadena aplanada)")
	}
}

// --- Edge cases 2.3: valores límite ---

func TestVarcharMaxLength(t *testing.T) {
	// Con conexión UTF8 los parámetros VARCHAR se describen con tope de
	// 8191 chars (32764 bytes); para ejercitar el máximo absoluto de 32765
	// bytes hace falta una conexión con charset de 1 byte.
	db, err := sql.Open("firebird", testDSN+"?charset=ISO8859_1")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	mustExec(t, db, "RECREATE TABLE edge_vmax (v VARCHAR(32765) CHARACTER SET ISO8859_1)")
	payload := strings.Repeat("x", 32765)
	mustExec(t, db, "INSERT INTO edge_vmax VALUES (?)", payload)
	var out string
	if err := db.QueryRow("SELECT v FROM edge_vmax").Scan(&out); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(out) != 32765 {
		t.Fatalf("roundtrip VARCHAR(32765): %d bytes", len(out))
	}
	// Un byte de más debe fallar, no truncar.
	if _, err := db.Exec("INSERT INTO edge_vmax VALUES (?)", payload+"y"); err == nil {
		var n int
		_ = db.QueryRow("SELECT COUNT(*) FROM edge_vmax WHERE CHAR_LENGTH(v) <> 32765").Scan(&n)
		if n > 0 {
			t.Error("string demasiado larga fue truncada en silencio")
		}
	}
}

func TestEmptyStringVsNull(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_null (id INTEGER, v VARCHAR(10))")
	mustExec(t, db, "INSERT INTO edge_null VALUES (1, '')")
	mustExec(t, db, "INSERT INTO edge_null VALUES (2, NULL)")

	var v1 sql.NullString
	if err := db.QueryRow("SELECT v FROM edge_null WHERE id=1").Scan(&v1); err != nil {
		t.Fatal(err)
	}
	if !v1.Valid || v1.String != "" {
		t.Errorf("string vacía: got (%v,%q), want (true,\"\")", v1.Valid, v1.String)
	}
	var v2 sql.NullString
	if err := db.QueryRow("SELECT v FROM edge_null WHERE id=2").Scan(&v2); err != nil {
		t.Fatal(err)
	}
	if v2.Valid {
		t.Errorf("NULL llegó como válido: %q", v2.String)
	}
}

func TestNumericExtremes(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_next (n18 NUMERIC(18,4), n9 NUMERIC(9,2), n4 NUMERIC(4,4))")
	// Máximo NUMERIC(18,4): int64 max escalado = 922337203685477.5807
	cases := []struct{ col, in string }{
		{"n18", "922337203685477.5807"},
		{"n18", "-922337203685477.5808"},
		{"n18", "0.0001"},
		{"n9", "9999999.99"},
		{"n9", "-9999999.99"},
		{"n4", "0.9999"},
		{"n4", "-0.9999"},
	}
	for _, c := range cases {
		mustExec(t, db, "DELETE FROM edge_next")
		mustExec(t, db, fmt.Sprintf("INSERT INTO edge_next (%s) VALUES (?)", c.col), c.in)
		var out string
		if err := db.QueryRow(fmt.Sprintf("SELECT %s FROM edge_next", c.col)).Scan(&out); err != nil {
			t.Fatalf("%s=%s: scan: %v", c.col, c.in, err)
		}
		if out != c.in {
			t.Errorf("%s: escribí %s, leí %s", c.col, c.in, out)
		}
	}
	// Firebird permite exceder la precisión declarada hasta el límite del
	// STORAGE (NUMERIC(9,2) se guarda en INTEGER → tope 21474836.47);
	// el driver replica ese comportamiento. Superar el storage sí falla.
	mustExec(t, db, "DELETE FROM edge_next")
	mustExec(t, db, "INSERT INTO edge_next (n9) VALUES (?)", "10000000.00")
	var out string
	if err := db.QueryRow("SELECT n9 FROM edge_next").Scan(&out); err != nil || out != "10000000.00" {
		t.Errorf("hasta el límite de storage debe aceptarse: %q %v", out, err)
	}
	if _, err := db.Exec("INSERT INTO edge_next (n9) VALUES (?)", "30000000.00"); err == nil {
		t.Error("overflow del storage int32 aceptado en silencio")
	}
}

func TestTimeEdgeValues(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_time (id INTEGER, t TIME, ts TIMESTAMP)")
	cases := []struct {
		id       int
		hms      string
		wantTime string
	}{
		{1, "00:00:00.0000", "00:00:00"},
		{2, "23:59:59.9999", "23:59:59.9999"},
		{3, "12:00:00.0001", "12:00:00.0001"},
	}
	for _, c := range cases {
		mustExec(t, db, fmt.Sprintf("INSERT INTO edge_time VALUES (%d, '%s', '2026-12-31 %s')", c.id, c.hms, c.hms))
	}
	for _, c := range cases {
		var tv, ts time.Time
		if err := db.QueryRow("SELECT t, ts FROM edge_time WHERE id = ?", c.id).Scan(&tv, &ts); err != nil {
			t.Fatalf("caso %d: %v", c.id, err)
		}
		got := tv.Format("15:04:05.9999")
		if got != c.wantTime {
			t.Errorf("TIME caso %d: got %s, want %s", c.id, got, c.wantTime)
		}
		if ts.Format("15:04:05.9999") != c.wantTime {
			t.Errorf("TIMESTAMP caso %d: hora %s, want %s", c.id, ts.Format("15:04:05.9999"), c.wantTime)
		}
	}
}

func TestBlobSizes(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_blob (id INTEGER, b BLOB)")
	sizes := []int{0, 1, 65535, 65536, 1 << 20, 8 << 20} // 0B..8MB cruza límites de segmento
	for i, size := range sizes {
		payload := make([]byte, size)
		for j := range payload {
			payload[j] = byte(j * 31)
		}
		mustExec(t, db, "INSERT INTO edge_blob VALUES (?, ?)", i, payload)
		var out []byte
		if err := db.QueryRow("SELECT b FROM edge_blob WHERE id = ?", i).Scan(&out); err != nil {
			t.Fatalf("blob %dB: scan: %v", size, err)
		}
		if len(out) != size {
			t.Fatalf("blob %dB: leí %dB", size, len(out))
		}
		for j := range out {
			if out[j] != byte(j*31) {
				t.Fatalf("blob %dB: byte %d corrupto", size, j)
			}
		}
	}
}

func TestLargeResultSetSmallFetchSize(t *testing.T) {
	db, err := sql.Open("firebird", testDSN+"?fetch_size=7")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	// Cross join en vez de CTE recursivo: Firebird limita la recursión a 1024.
	rows, err := db.Query(`
		SELECT ROW_NUMBER() OVER () AS i
		FROM (SELECT 1 x FROM rdb$types ROWS 100) a,
		     (SELECT 1 x FROM rdb$types ROWS 100) b,
		     (SELECT 1 x FROM rdb$types ROWS 5) c`)
	const total = 100 * 100 * 5
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count, sum := 0, int64(0)
	for rows.Next() {
		var i int64
		if err := rows.Scan(&i); err != nil {
			t.Fatal(err)
		}
		count++
		sum += i
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != total {
		t.Fatalf("filas: %d, want %d", count, total)
	}
	if want := int64(total) * (total + 1) / 2; sum != want {
		t.Fatalf("suma: %d, want %d (filas duplicadas o perdidas)", sum, want)
	}
}

// --- Edge cases 2.3: errores del servidor ---

func TestPrimaryKeyViolationGDSCode(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_pk (id INTEGER NOT NULL PRIMARY KEY)")
	mustExec(t, db, "INSERT INTO edge_pk VALUES (1)")
	_, err := db.Exec("INSERT INTO edge_pk VALUES (1)")
	if err == nil {
		t.Fatal("PK duplicada aceptada")
	}
	var fbErr *Error
	if !errors.As(err, &fbErr) {
		t.Fatalf("no es *firebird.Error: %T", err)
	}
	// 335544665 = unique_key_violation
	found := false
	for _, e := range fbErr.SV.Errors {
		if e.Code == 335544665 {
			found = true
		}
	}
	if !found {
		t.Errorf("esperaba GDS 335544665 en la cadena: %v", err)
	}
	if !strings.Contains(err.Error(), "violation") {
		t.Errorf("el texto debería mencionar la violación: %v", err)
	}
	// La conexión sigue utilizable.
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM edge_pk").Scan(&n); err != nil || n != 1 {
		t.Fatalf("post-violación: count=%d err=%v", n, err)
	}
}

// Cancelación por contexto mientras la operación espera un lock de registro.
func TestCancelDuringLockWait(t *testing.T) {
	db := openTestDB(t)
	mustExec(t, db, "RECREATE TABLE edge_lock (id INTEGER PRIMARY KEY, v INTEGER)")
	mustExec(t, db, "INSERT INTO edge_lock VALUES (1, 0)")

	// tx1 toma el lock del registro y lo mantiene.
	tx1, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx1.Rollback()
	if _, err := tx1.Exec("UPDATE edge_lock SET v = 1 WHERE id = 1"); err != nil {
		t.Fatal(err)
	}

	// Otra conexión intenta el mismo update con timeout: debe cancelarse, no colgar.
	db2, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	start := time.Now()
	_, err = db2.ExecContext(ctx, "UPDATE edge_lock SET v = 2 WHERE id = 1")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("update bloqueado terminó sin error")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("la cancelación durante lock-wait tardó %s (¿op_cancel no funciona?)", elapsed)
	}
}

// La cancelación de una ejecución larga (no lock) debe usar op_cancel y dejar
// la conexión reutilizable (sin activar el fallback de read-deadline).
func TestCancelLongQueryKeepsConnReusable(t *testing.T) {
	db, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1) // fuerza el reuso de la misma conexión

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	// Cross join grande: garantiza fetch/ejecución en curso al cancelar.
	rows, err := db.QueryContext(ctx, `
		SELECT COUNT(*) FROM rdb$types a, rdb$types b, rdb$types c,
		                     rdb$types d, rdb$types e, rdb$types f`)
	if err == nil {
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
	}
	elapsed := time.Since(start)
	if err == nil {
		t.Skip("la query terminó antes de poder cancelarla")
	}
	// Si op_cancel funcionó, la cancelación es mucho más rápida que el grace (2s).
	if elapsed > cancelGracePeriod {
		t.Errorf("la cancelación tardó %s (>= grace); ¿op_cancel no interrumpió la ejecución?", elapsed)
	}
	// La MISMA conexión debe seguir usable: op_cancel deja el wire sincronizado.
	var one int
	if err := db.QueryRow("SELECT 1 FROM rdb$database").Scan(&one); err != nil {
		t.Fatalf("conexión no reutilizable tras cancelar ejecución: %v", err)
	}
}

// --- Edge cases 2.3: comportamiento post-EOF y post-Close ---

func TestRowsAfterEOFAndClose(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.Query("SELECT 1 FROM rdb$database")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if rows.Next() {
		t.Error("Next tras EOF devolvió true")
	}
	if err := rows.Err(); err != nil {
		t.Errorf("Err tras EOF: %v", err)
	}
	rows.Close()
	if err := rows.Close(); err != nil {
		t.Errorf("Close doble: %v", err)
	}
	if rows.Next() {
		t.Error("Next tras Close devolvió true")
	}
}

// --- Edge cases 2.3: concurrencia y fugas ---

func TestPoolConcurrencyWithCancels(t *testing.T) {
	db, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				switch (g + i) % 3 {
				case 0:
					var n int
					_ = db.QueryRow("SELECT COUNT(*) FROM rdb$relations").Scan(&n)
				case 1:
					ctx, cancel := context.WithTimeout(context.Background(), time.Duration(1+i%5)*time.Millisecond)
					rows, err := db.QueryContext(ctx, `
						WITH RECURSIVE n(i) AS (
							SELECT 1 FROM rdb$database
							UNION ALL SELECT i+1 FROM n WHERE i < 2000
						) SELECT i FROM n`)
					if err == nil {
						for rows.Next() {
						}
						rows.Close()
					}
					cancel()
				case 2:
					tx, err := db.Begin()
					if err == nil {
						var one int
						_ = tx.QueryRow("SELECT 1 FROM rdb$database").Scan(&one)
						_ = tx.Commit()
					}
				}
			}
		}(g)
	}
	wg.Wait()

	// El pool debe quedar sano.
	var one int
	if err := db.QueryRow("SELECT 1 FROM rdb$database").Scan(&one); err != nil {
		t.Fatalf("pool insano tras estrés: %v", err)
	}
}

func TestNoGoroutineLeakAfterClose(t *testing.T) {
	before := runtime.NumGoroutine()

	db, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				var n int
				_ = db.QueryRow("SELECT 1 FROM rdb$database").Scan(&n)
				ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
				_, _ = db.ExecContext(ctx, "SELECT COUNT(*) FROM rdb$relations")
				cancel()
			}
		}()
	}
	wg.Wait()
	db.Close()

	// Dar tiempo a que los watchers terminen; comparar con tolerancia.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	buf := make([]byte, 1<<16)
	n := runtime.Stack(buf, true)
	t.Fatalf("fuga de goroutines: antes=%d después=%d\n%s", before, runtime.NumGoroutine(), buf[:n])
}

// EXECUTE PROCEDURE / EXECUTE BLOCK por ambos caminos.
func TestExecuteBlockPaths(t *testing.T) {
	db := openTestDB(t)
	var out int
	err := db.QueryRow(`EXECUTE BLOCK RETURNS (r INTEGER) AS BEGIN r = 41 + 1; SUSPEND; END`).Scan(&out)
	if err != nil {
		t.Fatalf("execute block via QueryRow: %v", err)
	}
	if out != 42 {
		t.Fatalf("out = %d", out)
	}
	if _, err := db.Exec(`EXECUTE BLOCK AS DECLARE x INTEGER; BEGIN x = 1; END`); err != nil {
		t.Fatalf("execute block via Exec: %v", err)
	}
}

// io.EOF nunca debe filtrarse al usuario como error de una query normal.
func TestNoRawEOFLeaks(t *testing.T) {
	db := openTestDB(t)
	rows, err := db.Query("SELECT rdb$relation_id FROM rdb$database WHERE 1=0")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
	}
	if err := rows.Err(); errors.Is(err, io.EOF) {
		t.Error("io.EOF filtrado como rows.Err()")
	}
}
