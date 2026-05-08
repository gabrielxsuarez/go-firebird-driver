package firebird

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

func defaultTestDSN(version string) string {
	switch version {
	case "3":
		return "firebird://sysdba:masterkey@localhost:3063//var/lib/firebird/data/driver.fdb"
	case "4":
		return "firebird://sysdba:masterkey@localhost:3064//var/lib/firebird/data/driver.fdb"
	case "5":
		return "firebird://sysdba:masterkey@localhost:3065//var/lib/firebird/data/driver.fdb"
	}
	return ""
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

var (
	testDSN     = envOrDefault("FB3_TEST_DSN", defaultTestDSN("3"))
	testDSN_FB4 = envOrDefault("FB4_TEST_DSN", defaultTestDSN("4"))
	testDSN_FB5 = envOrDefault("FB5_TEST_DSN", defaultTestDSN("5"))
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("firebird", testDSN)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

type testDBVersion struct {
	name string
	dsn  string
}

func testDBVersions() []testDBVersion {
	return []testDBVersion{
		{name: "FB3", dsn: testDSN},
		{name: "FB4", dsn: testDSN_FB4},
		{name: "FB5", dsn: testDSN_FB5},
	}
}

func openVersionedTestDB(t *testing.T, version testDBVersion) *sql.DB {
	t.Helper()
	if strings.TrimSpace(version.dsn) == "" {
		t.Skipf("%s DSN not configured", version.name)
	}
	db, err := sql.Open("firebird", version.dsn)
	if err != nil {
		t.Skipf("%s not available: %v", version.name, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("%s not reachable (%s): %v", version.name, version.dsn, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- Connection Tests ---

func TestDriverAliasesRegistered(t *testing.T) {
	for _, name := range []string{"firebird", "firebirdsql"} {
		found := false
		for _, registered := range sql.Drivers() {
			if registered == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("driver %q not registered", name)
		}

		db, err := sql.Open(name, testDSN)
		if err != nil {
			t.Fatalf("sql.Open(%q): %v", name, err)
		}
		db.Close()
	}
}

func TestConnect(t *testing.T) {
	db := openTestDB(t)
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestSimpleQuery(t *testing.T) {
	db := openTestDB(t)

	var result int
	err := db.QueryRow("SELECT 1+1 FROM RDB$DATABASE").Scan(&result)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if result != 2 {
		t.Fatalf("expected 2, got %d", result)
	}
}

func TestMultipleQueries(t *testing.T) {
	db := openTestDB(t)

	for i := range 10 {
		var v int
		if err := db.QueryRow("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE", i+1).Scan(&v); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if v != i+1 {
			t.Fatalf("iter %d: expected %d, got %d", i, i+1, v)
		}
	}
}

// --- DDL Tests ---

func TestDDLCreateDrop(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_DDL")

	_, err := db.Exec(`CREATE TABLE TEST_DDL (
		ID INTEGER NOT NULL PRIMARY KEY,
		NAME VARCHAR(100)
	)`)
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	_, err = db.Exec("INSERT INTO TEST_DDL (ID, NAME) VALUES (1, 'hello')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var name string
	if err := db.QueryRow("SELECT NAME FROM TEST_DDL WHERE ID=1").Scan(&name); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if name != "hello" {
		t.Fatalf("expected 'hello', got %q", name)
	}

	_, err = db.Exec("DROP TABLE TEST_DDL")
	if err != nil {
		t.Fatalf("DROP TABLE: %v", err)
	}
}

// --- Data Type Tests ---

func TestDataTypeInteger(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_INT")

	_, err := db.Exec(`CREATE TABLE TEST_INT (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_SMALL SMALLINT,
		V_INT INTEGER,
		V_BIG BIGINT
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_INT")

	_, err = db.Exec("INSERT INTO TEST_INT VALUES (1, 32767, 2147483647, 9223372036854775807)")
	if err != nil {
		t.Fatalf("INSERT max: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_INT VALUES (2, -32768, -2147483648, -9223372036854775808)")
	if err != nil {
		t.Fatalf("INSERT min: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_INT VALUES (3, 0, 0, 0)")
	if err != nil {
		t.Fatalf("INSERT zero: %v", err)
	}

	rows, err := db.Query("SELECT V_SMALL, V_INT, V_BIG FROM TEST_INT ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	expected := []struct {
		small int16
		mid   int32
		big   int64
	}{
		{32767, 2147483647, math.MaxInt64},
		{-32768, -2147483648, math.MinInt64},
		{0, 0, 0},
	}

	i := 0
	for rows.Next() {
		var s int16
		var m int32
		var b int64
		if err := rows.Scan(&s, &m, &b); err != nil {
			t.Fatalf("row %d: Scan: %v", i, err)
		}
		if s != expected[i].small || m != expected[i].mid || b != expected[i].big {
			t.Errorf("row %d: got (%d, %d, %d), want (%d, %d, %d)",
				i, s, m, b, expected[i].small, expected[i].mid, expected[i].big)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if i != 3 {
		t.Fatalf("expected 3 rows, got %d", i)
	}
}

func TestDataTypeFloat(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_FLOAT")

	_, err := db.Exec(`CREATE TABLE TEST_FLOAT (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_FLOAT FLOAT,
		V_DOUBLE DOUBLE PRECISION
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_FLOAT")

	_, err = db.Exec("INSERT INTO TEST_FLOAT VALUES (1, 3.14, 2.718281828459045)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var f float32
	var d float64
	if err := db.QueryRow("SELECT V_FLOAT, V_DOUBLE FROM TEST_FLOAT WHERE ID=1").Scan(&f, &d); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if math.Abs(float64(f)-3.14) > 0.001 {
		t.Errorf("FLOAT: got %f, want ~3.14", f)
	}
	if math.Abs(d-2.718281828459045) > 1e-12 {
		t.Errorf("DOUBLE: got %f, want ~2.718281828459045", d)
	}
}

func TestDataTypeString(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_STR")

	_, err := db.Exec(`CREATE TABLE TEST_STR (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_CHAR CHAR(10),
		V_VARCHAR VARCHAR(100)
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_STR")

	_, err = db.Exec("INSERT INTO TEST_STR VALUES (1, 'ABC', 'Hello World')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_STR VALUES (2, '', '')")
	if err != nil {
		t.Fatalf("INSERT empty: %v", err)
	}

	var ch, vc string
	if err := db.QueryRow("SELECT V_CHAR, V_VARCHAR FROM TEST_STR WHERE ID=1").Scan(&ch, &vc); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if ch != "ABC" {
		t.Errorf("CHAR: got %q, want %q", ch, "ABC")
	}
	if vc != "Hello World" {
		t.Errorf("VARCHAR: got %q, want %q", vc, "Hello World")
	}
}

func TestDataTypeBoolean(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_BOOL")

	_, err := db.Exec(`CREATE TABLE TEST_BOOL (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_BOOL BOOLEAN
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BOOL")

	_, err = db.Exec("INSERT INTO TEST_BOOL VALUES (1, TRUE)")
	if err != nil {
		t.Fatalf("INSERT true: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_BOOL VALUES (2, FALSE)")
	if err != nil {
		t.Fatalf("INSERT false: %v", err)
	}

	tests := []struct {
		id   int
		want bool
	}{{1, true}, {2, false}}

	for _, tt := range tests {
		var v bool
		if err := db.QueryRow("SELECT V_BOOL FROM TEST_BOOL WHERE ID=?", tt.id).Scan(&v); err != nil {
			t.Fatalf("Scan id=%d: %v", tt.id, err)
		}
		if v != tt.want {
			t.Errorf("id=%d: got %v, want %v", tt.id, v, tt.want)
		}
	}
}

func TestDataTypeDateTime(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_DT")

	_, err := db.Exec(`CREATE TABLE TEST_DT (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_DATE DATE,
		V_TIME TIME,
		V_TS TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_DT")

	_, err = db.Exec("INSERT INTO TEST_DT VALUES (1, '2024-06-15', '14:30:00', '2024-06-15 14:30:00')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var d, tm, ts time.Time
	if err := db.QueryRow("SELECT V_DATE, V_TIME, V_TS FROM TEST_DT WHERE ID=1").Scan(&d, &tm, &ts); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if d.Year() != 2024 || d.Month() != 6 || d.Day() != 15 {
		t.Errorf("DATE: got %v, want 2024-06-15", d)
	}
	if tm.Hour() != 14 || tm.Minute() != 30 {
		t.Errorf("TIME: got %v, want 14:30:00", tm)
	}
	if ts.Year() != 2024 || ts.Month() != 6 || ts.Day() != 15 || ts.Hour() != 14 || ts.Minute() != 30 {
		t.Errorf("TIMESTAMP: got %v, want 2024-06-15 14:30:00", ts)
	}
}

func TestDataTypeNumericDecimal(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_NUM")

	_, err := db.Exec(`CREATE TABLE TEST_NUM (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_NUM NUMERIC(10,2),
		V_DEC DECIMAL(18,4)
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_NUM")

	_, err = db.Exec("INSERT INTO TEST_NUM VALUES (1, 123.45, 9876.5432)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var num, dec float64
	if err := db.QueryRow("SELECT V_NUM, V_DEC FROM TEST_NUM WHERE ID=1").Scan(&num, &dec); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if math.Abs(num-123.45) > 0.001 {
		t.Errorf("NUMERIC: got %f, want 123.45", num)
	}
	if math.Abs(dec-9876.5432) > 0.0001 {
		t.Errorf("DECIMAL: got %f, want 9876.5432", dec)
	}
}

// --- NULL Handling Tests ---

func TestNullValues(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_NULL")

	_, err := db.Exec(`CREATE TABLE TEST_NULL (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_INT INTEGER,
		V_STR VARCHAR(50),
		V_DATE DATE
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_NULL")

	_, err = db.Exec("INSERT INTO TEST_NULL VALUES (1, NULL, NULL, NULL)")
	if err != nil {
		t.Fatalf("INSERT nulls: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_NULL VALUES (2, 42, 'hello', '2024-01-01')")
	if err != nil {
		t.Fatalf("INSERT non-null: %v", err)
	}

	var ni sql.NullInt32
	var ns sql.NullString
	var nt sql.NullTime
	if err := db.QueryRow("SELECT V_INT, V_STR, V_DATE FROM TEST_NULL WHERE ID=1").Scan(&ni, &ns, &nt); err != nil {
		t.Fatalf("Scan nulls: %v", err)
	}
	if ni.Valid || ns.Valid || nt.Valid {
		t.Errorf("expected all NULLs, got ni=%v ns=%v nt=%v", ni, ns, nt)
	}

	if err := db.QueryRow("SELECT V_INT, V_STR, V_DATE FROM TEST_NULL WHERE ID=2").Scan(&ni, &ns, &nt); err != nil {
		t.Fatalf("Scan non-nulls: %v", err)
	}
	if !ni.Valid || ni.Int32 != 42 {
		t.Errorf("V_INT: got %v, want 42", ni)
	}
	if !ns.Valid || ns.String != "hello" {
		t.Errorf("V_STR: got %v, want 'hello'", ns)
	}
	if !nt.Valid || nt.Time.Year() != 2024 {
		t.Errorf("V_DATE: got %v, want 2024-01-01", nt)
	}
}

// --- Parameter Tests ---

func TestParameterBinding(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_PARAMS")

	_, err := db.Exec(`CREATE TABLE TEST_PARAMS (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_INT INTEGER,
		V_STR VARCHAR(100),
		V_FLOAT DOUBLE PRECISION
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_PARAMS")

	_, err = db.Exec("INSERT INTO TEST_PARAMS VALUES (?, ?, ?, ?)", 1, 42, "test", 3.14)
	if err != nil {
		t.Fatalf("INSERT with params: %v", err)
	}

	var id, vi int
	var vs string
	var vf float64
	if err := db.QueryRow("SELECT ID, V_INT, V_STR, V_FLOAT FROM TEST_PARAMS WHERE ID=?", 1).Scan(&id, &vi, &vs, &vf); err != nil {
		t.Fatalf("SELECT with param: %v", err)
	}
	if id != 1 || vi != 42 || vs != "test" || math.Abs(vf-3.14) > 0.001 {
		t.Errorf("got (%d, %d, %q, %f), want (1, 42, 'test', 3.14)", id, vi, vs, vf)
	}
}

func TestUpdateDelete(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_UD")

	_, err := db.Exec(`CREATE TABLE TEST_UD (
		ID INTEGER NOT NULL PRIMARY KEY,
		NAME VARCHAR(50)
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_UD")

	_, err = db.Exec("INSERT INTO TEST_UD VALUES (1, 'original')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	res, err := db.Exec("UPDATE TEST_UD SET NAME=? WHERE ID=?", "updated", 1)
	if err != nil {
		t.Fatalf("UPDATE: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Errorf("UPDATE RowsAffected: got %d, want 1", n)
	}

	var name string
	db.QueryRow("SELECT NAME FROM TEST_UD WHERE ID=1").Scan(&name)
	if name != "updated" {
		t.Errorf("after UPDATE: got %q, want %q", name, "updated")
	}

	res, err = db.Exec("DELETE FROM TEST_UD WHERE ID=?", 1)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	n, _ = res.RowsAffected()
	if n != 1 {
		t.Errorf("DELETE RowsAffected: got %d, want 1", n)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM TEST_UD").Scan(&count)
	if count != 0 {
		t.Errorf("after DELETE: count=%d, want 0", count)
	}
}

// --- Transaction Tests ---

func TestTransactionCommit(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_TX")
	db.Exec(`CREATE TABLE TEST_TX (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	defer db.Exec("DROP TABLE TEST_TX")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, err = tx.Exec("INSERT INTO TEST_TX VALUES (1, 100)")
	if err != nil {
		t.Fatalf("INSERT in tx: %v", err)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var v int
	if err := db.QueryRow("SELECT V FROM TEST_TX WHERE ID=1").Scan(&v); err != nil {
		t.Fatalf("SELECT after commit: %v", err)
	}
	if v != 100 {
		t.Errorf("got %d, want 100", v)
	}
}

func TestTransactionRollback(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_TX_RB")
	db.Exec(`CREATE TABLE TEST_TX_RB (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	defer db.Exec("DROP TABLE TEST_TX_RB")

	db.Exec("INSERT INTO TEST_TX_RB VALUES (1, 100)")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	_, err = tx.Exec("INSERT INTO TEST_TX_RB VALUES (2, 200)")
	if err != nil {
		t.Fatalf("INSERT in tx: %v", err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM TEST_TX_RB").Scan(&count)
	if count != 1 {
		t.Errorf("after rollback: count=%d, want 1", count)
	}
}

func TestTransactionIsolation(t *testing.T) {
	db := openTestDB(t)

	tests := []sql.IsolationLevel{
		sql.LevelDefault,
		sql.LevelReadCommitted,
		sql.LevelRepeatableRead,
		sql.LevelSnapshot,
		sql.LevelSerializable,
	}

	for _, iso := range tests {
		t.Run(fmt.Sprintf("level_%d", iso), func(t *testing.T) {
			tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: iso})
			if err != nil {
				t.Fatalf("BeginTx(level=%d): %v", iso, err)
			}
			tx.Rollback()
		})
	}
}

// --- Multi-Row Fetch Tests ---

func TestMultiRowFetch(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_MULTI")
	db.Exec(`CREATE TABLE TEST_MULTI (ID INTEGER NOT NULL PRIMARY KEY, V VARCHAR(50))`)
	defer db.Exec("DROP TABLE TEST_MULTI")

	for i := range 50 {
		_, err := db.Exec("INSERT INTO TEST_MULTI VALUES (?, ?)", i+1, fmt.Sprintf("row_%d", i+1))
		if err != nil {
			t.Fatalf("INSERT %d: %v", i, err)
		}
	}

	rows, err := db.Query("SELECT ID, V FROM TEST_MULTI ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			t.Fatalf("row %d: Scan: %v", count, err)
		}
		if id != count+1 || v != fmt.Sprintf("row_%d", count+1) {
			t.Errorf("row %d: got (%d, %q), want (%d, %q)", count, id, v, count+1, fmt.Sprintf("row_%d", count+1))
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if count != 50 {
		t.Fatalf("expected 50 rows, got %d", count)
	}
}

// --- Prepared Statement Tests ---

func TestPreparedStatement(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_PREP")
	db.Exec(`CREATE TABLE TEST_PREP (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	defer db.Exec("DROP TABLE TEST_PREP")

	stmt, err := db.Prepare("INSERT INTO TEST_PREP VALUES (?, ?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer stmt.Close()

	for i := range 10 {
		_, err := stmt.Exec(i+1, (i+1)*10)
		if err != nil {
			t.Fatalf("Exec %d: %v", i, err)
		}
	}

	var count int
	db.QueryRow("SELECT COUNT(*) FROM TEST_PREP").Scan(&count)
	if count != 10 {
		t.Fatalf("expected 10 rows, got %d", count)
	}

	qstmt, err := db.Prepare("SELECT V FROM TEST_PREP WHERE ID=?")
	if err != nil {
		t.Fatalf("Prepare query: %v", err)
	}
	defer qstmt.Close()

	var v int
	if err := qstmt.QueryRow(5).Scan(&v); err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if v != 50 {
		t.Errorf("got %d, want 50", v)
	}
}

func TestStatementCloseIdempotentKeepsConnectionReusable(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	stmt, err := db.Prepare("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("Stmt.Close: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("Stmt.Close second call: %v", err)
	}

	assertConnectionReusable(t, db, "after idempotent stmt close")
}

// --- Error Handling Tests ---

func TestErrorSyntax(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec("INVALID SQL STATEMENT HERE")
	if err == nil {
		t.Fatal("expected error for invalid SQL")
	}
}

func TestErrorTableNotFound(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec("SELECT * FROM NONEXISTENT_TABLE_XYZ123")
	if err == nil {
		t.Fatal("expected error for nonexistent table")
	}
}

func TestErrorConstraintViolation(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_CONSTRAINT")
	db.Exec(`CREATE TABLE TEST_CONSTRAINT (ID INTEGER NOT NULL PRIMARY KEY)`)
	defer db.Exec("DROP TABLE TEST_CONSTRAINT")

	db.Exec("INSERT INTO TEST_CONSTRAINT VALUES (1)")
	_, err := db.Exec("INSERT INTO TEST_CONSTRAINT VALUES (1)")
	if err == nil {
		t.Fatal("expected error for duplicate primary key")
	}
}

// --- Lifecycle / Protocol Sync Tests ---

func assertConnectionReusable(t *testing.T, db *sql.DB, label string) {
	t.Helper()
	var v int
	if err := db.QueryRow("SELECT 42 FROM RDB$DATABASE").Scan(&v); err != nil {
		t.Fatalf("%s: connection not reusable: %v", label, err)
	}
	if v != 42 {
		t.Fatalf("%s: got %d, want 42", label, v)
	}
}

func TestRowsCloseBeforeNextKeepsConnectionReusable(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	rows, err := db.Query("SELECT RDB$RELATION_ID FROM RDB$RELATIONS ORDER BY RDB$RELATION_ID")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Rows.Close: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Rows.Close second call: %v", err)
	}

	assertConnectionReusable(t, db, "after close before next")
}

func TestRowsCloseAfterPartialFetchKeepsConnectionReusable(t *testing.T) {
	db, err := sqlOpenTestDBWithParam("fetch_size", "3")
	if err != nil {
		t.Fatalf("open fetch_size DB: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	db.Exec("DROP TABLE TEST_ROWS_CLOSE")
	_, err = db.Exec(`CREATE TABLE TEST_ROWS_CLOSE (ID INTEGER NOT NULL PRIMARY KEY, V VARCHAR(20))`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_ROWS_CLOSE")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	stmt, err := tx.Prepare("INSERT INTO TEST_ROWS_CLOSE VALUES (?, ?)")
	if err != nil {
		tx.Rollback()
		t.Fatalf("Prepare insert: %v", err)
	}
	for i := range 25 {
		if _, err := stmt.Exec(i+1, fmt.Sprintf("row_%02d", i+1)); err != nil {
			stmt.Close()
			tx.Rollback()
			t.Fatalf("INSERT %d: %v", i+1, err)
		}
	}
	if err := stmt.Close(); err != nil {
		tx.Rollback()
		t.Fatalf("stmt.Close: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	rows, err := db.Query("SELECT ID, V FROM TEST_ROWS_CLOSE ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !rows.Next() {
		t.Fatal("expected first row")
	}
	var id int
	var value string
	if err := rows.Scan(&id, &value); err != nil {
		rows.Close()
		t.Fatalf("Scan first row: %v", err)
	}
	if id != 1 || value != "row_01" {
		rows.Close()
		t.Fatalf("first row = (%d, %q), want (1, row_01)", id, value)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Rows.Close: %v", err)
	}

	assertConnectionReusable(t, db, "after partial rows close")

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM TEST_ROWS_CLOSE").Scan(&count); err != nil {
		t.Fatalf("COUNT after close: %v", err)
	}
	if count != 25 {
		t.Fatalf("COUNT = %d, want 25", count)
	}
}

func TestPreparedRowsCloseEarlyAllowsStatementReuse(t *testing.T) {
	db, err := sqlOpenTestDBWithParam("fetch_size", "2")
	if err != nil {
		t.Fatalf("open fetch_size DB: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	db.Exec("DROP TABLE TEST_PREP_ROWS_CLOSE")
	_, err = db.Exec(`CREATE TABLE TEST_PREP_ROWS_CLOSE (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_PREP_ROWS_CLOSE")

	for i := range 8 {
		if _, err := db.Exec("INSERT INTO TEST_PREP_ROWS_CLOSE VALUES (?, ?)", i+1, (i+1)*10); err != nil {
			t.Fatalf("INSERT %d: %v", i+1, err)
		}
	}

	stmt, err := db.Prepare("SELECT ID, V FROM TEST_PREP_ROWS_CLOSE WHERE ID >= ? ORDER BY ID")
	if err != nil {
		t.Fatalf("Prepare query: %v", err)
	}
	defer stmt.Close()

	rows, err := stmt.Query(1)
	if err != nil {
		t.Fatalf("stmt.Query first: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("expected first row")
	}
	var id, v int
	if err := rows.Scan(&id, &v); err != nil {
		rows.Close()
		t.Fatalf("Scan first query: %v", err)
	}
	if id != 1 || v != 10 {
		rows.Close()
		t.Fatalf("first query row = (%d, %d), want (1, 10)", id, v)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Rows.Close first query: %v", err)
	}

	rows, err = stmt.Query(5)
	if err != nil {
		t.Fatalf("stmt.Query second: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("expected second query row")
	}
	if err := rows.Scan(&id, &v); err != nil {
		t.Fatalf("Scan second query: %v", err)
	}
	if id != 5 || v != 50 {
		t.Fatalf("second query row = (%d, %d), want (5, 50)", id, v)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Rows.Close second query: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("Stmt.Close: %v", err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatalf("Stmt.Close second call: %v", err)
	}

	assertConnectionReusable(t, db, "after prepared rows close")
}

func TestBlobRowsCloseEarlyKeepsConnectionReusable(t *testing.T) {
	db, err := sqlOpenTestDBWithParam("fetch_size", "2")
	if err != nil {
		t.Fatalf("open fetch_size DB: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	db.Exec("DROP TABLE TEST_BLOB_CLOSE_EARLY")
	_, err = db.Exec(`CREATE TABLE TEST_BLOB_CLOSE_EARLY (
		ID INTEGER NOT NULL PRIMARY KEY,
		DATA BLOB SUB_TYPE TEXT
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_CLOSE_EARLY")

	for i := range 6 {
		data := strings.Repeat(fmt.Sprintf("blob_%d_", i+1), 300)
		if _, err := db.Exec("INSERT INTO TEST_BLOB_CLOSE_EARLY VALUES (?, ?)", i+1, data); err != nil {
			t.Fatalf("INSERT %d: %v", i+1, err)
		}
	}

	rows, err := db.Query("SELECT ID, DATA FROM TEST_BLOB_CLOSE_EARLY ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		t.Fatal("expected first blob row")
	}
	var id int
	var data string
	if err := rows.Scan(&id, &data); err != nil {
		rows.Close()
		t.Fatalf("Scan first blob row: %v", err)
	}
	if id != 1 || !strings.HasPrefix(data, "blob_1_") {
		rows.Close()
		t.Fatalf("first blob row = (%d, %.16q)", id, data)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("Rows.Close: %v", err)
	}

	assertConnectionReusable(t, db, "after early blob rows close")
}

func TestTransactionRowsCloseEarlyKeepsTransactionUsable(t *testing.T) {
	db, err := sqlOpenTestDBWithParam("fetch_size", "2")
	if err != nil {
		t.Fatalf("open fetch_size DB: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	db.Exec("DROP TABLE TEST_TX_ROWS_CLOSE")
	_, err = db.Exec(`CREATE TABLE TEST_TX_ROWS_CLOSE (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_TX_ROWS_CLOSE")

	for i := range 8 {
		if _, err := db.Exec("INSERT INTO TEST_TX_ROWS_CLOSE VALUES (?, ?)", i+1, (i+1)*100); err != nil {
			t.Fatalf("INSERT %d: %v", i+1, err)
		}
	}

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	rows, err := tx.Query("SELECT ID, V FROM TEST_TX_ROWS_CLOSE ORDER BY ID")
	if err != nil {
		tx.Rollback()
		t.Fatalf("tx.Query: %v", err)
	}
	if !rows.Next() {
		rows.Close()
		tx.Rollback()
		t.Fatal("expected first row")
	}
	var id, v int
	if err := rows.Scan(&id, &v); err != nil {
		rows.Close()
		tx.Rollback()
		t.Fatalf("Scan: %v", err)
	}
	if id != 1 || v != 100 {
		rows.Close()
		tx.Rollback()
		t.Fatalf("first row = (%d, %d), want (1, 100)", id, v)
	}
	if err := rows.Close(); err != nil {
		tx.Rollback()
		t.Fatalf("Rows.Close: %v", err)
	}

	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM TEST_TX_ROWS_CLOSE WHERE ID > 4").Scan(&count); err != nil {
		tx.Rollback()
		t.Fatalf("tx.QueryRow after close: %v", err)
	}
	if count != 4 {
		tx.Rollback()
		t.Fatalf("COUNT = %d, want 4", count)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit after early rows close: %v", err)
	}

	assertConnectionReusable(t, db, "after transaction early rows close")
}

func TestProtocolSyncAfterRepeatedStatementErrors(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for i := range 5 {
		if _, err := db.Exec("SELECT * FROM NONEXISTENT_TABLE_FOR_SYNC_TEST"); err == nil {
			t.Fatalf("iter %d: expected table-not-found error", i)
		}
		assertConnectionReusable(t, db, fmt.Sprintf("after table-not-found error %d", i))

		if _, err := db.Exec("THIS IS NOT VALID SQL"); err == nil {
			t.Fatalf("iter %d: expected syntax error", i)
		}
		assertConnectionReusable(t, db, fmt.Sprintf("after syntax error %d", i))
	}
}

func TestProtocolSyncAfterRepeatedExecuteErrors(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	db.Exec("DROP TABLE TEST_EXEC_ERROR_SYNC")
	_, err := db.Exec(`CREATE TABLE TEST_EXEC_ERROR_SYNC (ID INTEGER NOT NULL PRIMARY KEY)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_EXEC_ERROR_SYNC")

	if _, err := db.Exec("INSERT INTO TEST_EXEC_ERROR_SYNC VALUES (1)"); err != nil {
		t.Fatalf("initial INSERT: %v", err)
	}
	for i := range 5 {
		if _, err := db.Exec("INSERT INTO TEST_EXEC_ERROR_SYNC VALUES (1)"); err == nil {
			t.Fatalf("iter %d: expected duplicate key error", i)
		}
		assertConnectionReusable(t, db, fmt.Sprintf("after duplicate key error %d", i))
	}
}

func TestLifecycleSmokeFirebirdVersions(t *testing.T) {
	for _, version := range testDBVersions() {
		t.Run(version.name, func(t *testing.T) {
			db := openVersionedTestDB(t, version)

			table := "TEST_LIFE_" + version.name
			db.Exec("DROP TABLE " + table)
			_, err := db.Exec("CREATE TABLE " + table + " (ID INTEGER NOT NULL PRIMARY KEY, V VARCHAR(20))")
			if err != nil {
				t.Fatalf("CREATE: %v", err)
			}
			defer db.Exec("DROP TABLE " + table)

			for i := range 8 {
				if _, err := db.Exec("INSERT INTO "+table+" VALUES (?, ?)", i+1, fmt.Sprintf("%s_%02d", version.name, i+1)); err != nil {
					t.Fatalf("INSERT %d: %v", i+1, err)
				}
			}

			rows, err := db.Query("SELECT ID, V FROM " + table + " ORDER BY ID")
			if err != nil {
				t.Fatalf("SELECT: %v", err)
			}
			if !rows.Next() {
				rows.Close()
				t.Fatal("expected first row")
			}
			var id int
			var value string
			if err := rows.Scan(&id, &value); err != nil {
				rows.Close()
				t.Fatalf("Scan: %v", err)
			}
			if id != 1 || value != version.name+"_01" {
				rows.Close()
				t.Fatalf("first row = (%d, %q), want (1, %q)", id, value, version.name+"_01")
			}
			if err := rows.Close(); err != nil {
				t.Fatalf("Rows.Close: %v", err)
			}

			assertConnectionReusable(t, db, "after early close "+version.name)

			if _, err := db.Exec("INSERT INTO " + table + " VALUES (1, 'duplicate')"); err == nil {
				t.Fatal("expected duplicate key error")
			}
			assertConnectionReusable(t, db, "after duplicate key "+version.name)

			stmt, err := db.Prepare("SELECT ID FROM " + table + " WHERE ID >= ? ORDER BY ID")
			if err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			stmtRows, err := stmt.Query(4)
			if err != nil {
				stmt.Close()
				t.Fatalf("stmt.Query: %v", err)
			}
			if !stmtRows.Next() {
				stmtRows.Close()
				stmt.Close()
				t.Fatal("expected prepared row")
			}
			if err := stmtRows.Scan(&id); err != nil {
				stmtRows.Close()
				stmt.Close()
				t.Fatalf("prepared Scan: %v", err)
			}
			if id != 4 {
				stmtRows.Close()
				stmt.Close()
				t.Fatalf("prepared first row = %d, want 4", id)
			}
			if err := stmtRows.Close(); err != nil {
				stmt.Close()
				t.Fatalf("prepared Rows.Close: %v", err)
			}
			if err := stmt.Close(); err != nil {
				t.Fatalf("Stmt.Close: %v", err)
			}

			assertConnectionReusable(t, db, "after prepared early close "+version.name)
		})
	}
}

// --- Concurrency Tests ---

func TestConcurrentQueries(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(1) // serialize through one connection
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for i := range 10 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var v int
			if err := db.QueryRowContext(ctx, "SELECT CAST(? AS INTEGER) FROM RDB$DATABASE", n+1).Scan(&v); err != nil {
				errCh <- fmt.Errorf("goroutine %d: %v", n, err)
				return
			}
			if v != n+1 {
				errCh <- fmt.Errorf("goroutine %d: got %d, want %d", n, v, n+1)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
}

// --- Edge Cases ---

func TestLongVarchar(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_LONG")
	_, err := db.Exec(`CREATE TABLE TEST_LONG (ID INTEGER NOT NULL PRIMARY KEY, V VARCHAR(5000))`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_LONG")

	long := strings.Repeat("A", 5000)
	_, err = db.Exec("INSERT INTO TEST_LONG VALUES (1, ?)", long)
	if err != nil {
		t.Fatalf("INSERT long: %v", err)
	}

	var v string
	if err := db.QueryRow("SELECT V FROM TEST_LONG WHERE ID=1").Scan(&v); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if v != long {
		t.Errorf("length: got %d, want %d", len(v), len(long))
	}
}

func TestEmptyResultSet(t *testing.T) {
	db := openTestDB(t)

	rows, err := db.Query("SELECT 1 FROM RDB$DATABASE WHERE 1=0")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	if rows.Next() {
		t.Error("expected no rows")
	}
	if err := rows.Err(); err != nil {
		t.Errorf("rows.Err: %v", err)
	}
}

func TestExpressionsAndCasts(t *testing.T) {
	db := openTestDB(t)

	var vi int32
	if err := db.QueryRow("SELECT CAST(1 AS INTEGER) FROM RDB$DATABASE").Scan(&vi); err != nil {
		t.Fatalf("CAST INT: %v", err)
	}
	if vi != 1 {
		t.Errorf("CAST INT: got %d, want 1", vi)
	}

	var vf float64
	if err := db.QueryRow("SELECT CAST(3.14 AS DOUBLE PRECISION) FROM RDB$DATABASE").Scan(&vf); err != nil {
		t.Fatalf("CAST DOUBLE: %v", err)
	}
	if math.Abs(vf-3.14) > 0.001 {
		t.Errorf("CAST DOUBLE: got %f, want ~3.14", vf)
	}

	var vs string
	if err := db.QueryRow("SELECT CAST('hello' AS VARCHAR(10)) FROM RDB$DATABASE").Scan(&vs); err != nil {
		t.Fatalf("CAST VARCHAR: %v", err)
	}
	if vs != "hello" {
		t.Errorf("CAST VARCHAR: got %q, want 'hello'", vs)
	}
}

// --- Benchmarks ---

func BenchmarkPing(b *testing.B) {
	db, _ := sql.Open("firebird", testDSN)
	defer db.Close()
	db.Ping()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		db.Ping()
	}
}

func BenchmarkQuerySingleRow(b *testing.B) {
	db, _ := sql.Open("firebird", testDSN)
	defer db.Close()
	db.Ping()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		var v int
		db.QueryRow("SELECT 1+1 FROM RDB$DATABASE").Scan(&v)
	}
}

func BenchmarkExecInsert(b *testing.B) {
	db, _ := sql.Open("firebird", testDSN)
	defer db.Close()

	db.Exec("DROP TABLE BENCH_INS")
	db.Exec(`CREATE TABLE BENCH_INS (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	defer db.Exec("DROP TABLE BENCH_INS")

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		db.Exec("INSERT INTO BENCH_INS VALUES (?, ?)", i+1, i*10)
	}
}

func BenchmarkPreparedExec(b *testing.B) {
	db, _ := sql.Open("firebird", testDSN)
	defer db.Close()

	db.Exec("DROP TABLE BENCH_PREP")
	db.Exec(`CREATE TABLE BENCH_PREP (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	defer db.Exec("DROP TABLE BENCH_PREP")

	stmt, _ := db.Prepare("INSERT INTO BENCH_PREP VALUES (?, ?)")
	defer stmt.Close()

	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		stmt.Exec(i+1, i*10)
	}
}

func BenchmarkQueryManyRows(b *testing.B) {
	db, _ := sql.Open("firebird", testDSN)
	defer db.Close()

	db.Exec("DROP TABLE BENCH_MANY")
	db.Exec(`CREATE TABLE BENCH_MANY (ID INTEGER NOT NULL PRIMARY KEY, V1 INTEGER, V2 VARCHAR(50))`)
	for i := range 1000 {
		db.Exec("INSERT INTO BENCH_MANY VALUES (?, ?, ?)", i+1, i*10, fmt.Sprintf("row_%05d", i+1))
	}
	defer db.Exec("DROP TABLE BENCH_MANY")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rows, _ := db.Query("SELECT ID, V1, V2 FROM BENCH_MANY")
		count := 0
		for rows.Next() {
			var id, v1 int
			var v2 string
			rows.Scan(&id, &v1, &v2)
			count++
		}
		rows.Close()
	}
}

// --- BLOB Tests ---

func TestBlobBinarySmall(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_BLOB_BIN")
	_, err := db.Exec(`CREATE TABLE TEST_BLOB_BIN (
		ID INTEGER NOT NULL PRIMARY KEY,
		DATA BLOB SUB_TYPE 0
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_BIN")

	// Small binary data
	data := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD, 0x80, 0x7F}
	_, err = db.Exec("INSERT INTO TEST_BLOB_BIN VALUES (1, ?)", data)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var got []byte
	if err := db.QueryRow("SELECT DATA FROM TEST_BLOB_BIN WHERE ID=1").Scan(&got); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("length: got %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Fatalf("byte %d: got 0x%02X, want 0x%02X", i, got[i], data[i])
		}
	}
}

func TestBlobBinaryLarge(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_BLOB_BIG")
	_, err := db.Exec(`CREATE TABLE TEST_BLOB_BIG (
		ID INTEGER NOT NULL PRIMARY KEY,
		DATA BLOB SUB_TYPE 0
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_BIG")

	// 100KB binary data with pattern
	data := make([]byte, 100*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	_, err = db.Exec("INSERT INTO TEST_BLOB_BIG VALUES (1, ?)", data)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var got []byte
	if err := db.QueryRow("SELECT DATA FROM TEST_BLOB_BIG WHERE ID=1").Scan(&got); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != len(data) {
		t.Fatalf("length: got %d, want %d", len(got), len(data))
	}
	for i := range data {
		if got[i] != data[i] {
			t.Fatalf("byte %d: got 0x%02X, want 0x%02X", i, got[i], data[i])
		}
	}
}

func TestBlobText(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_BLOB_TXT")
	_, err := db.Exec(`CREATE TABLE TEST_BLOB_TXT (
		ID INTEGER NOT NULL PRIMARY KEY,
		CONTENT BLOB SUB_TYPE TEXT
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_TXT")

	// Long text content
	text := strings.Repeat("Hello World! ", 1000) // ~13KB
	_, err = db.Exec("INSERT INTO TEST_BLOB_TXT VALUES (1, ?)", text)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var got string
	if err := db.QueryRow("SELECT CONTENT FROM TEST_BLOB_TXT WHERE ID=1").Scan(&got); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got != text {
		t.Errorf("text length: got %d, want %d", len(got), len(text))
	}
}

func TestBlobNull(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_BLOB_NULL")
	_, err := db.Exec(`CREATE TABLE TEST_BLOB_NULL (
		ID INTEGER NOT NULL PRIMARY KEY,
		DATA BLOB SUB_TYPE 0
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_NULL")

	_, err = db.Exec("INSERT INTO TEST_BLOB_NULL VALUES (1, NULL)")
	if err != nil {
		t.Fatalf("INSERT NULL: %v", err)
	}

	var got sql.NullString
	if err := db.QueryRow("SELECT DATA FROM TEST_BLOB_NULL WHERE ID=1").Scan(&got); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got.Valid {
		t.Error("expected NULL blob, got non-NULL")
	}
}

func TestBlobMultipleRows(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_BLOB_MULTI")
	_, err := db.Exec(`CREATE TABLE TEST_BLOB_MULTI (
		ID INTEGER NOT NULL PRIMARY KEY,
		DATA BLOB SUB_TYPE TEXT
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_MULTI")

	for i := range 10 {
		_, err := db.Exec("INSERT INTO TEST_BLOB_MULTI VALUES (?, ?)",
			i+1, fmt.Sprintf("blob_content_%d", i+1))
		if err != nil {
			t.Fatalf("INSERT %d: %v", i, err)
		}
	}

	rows, err := db.Query("SELECT ID, DATA FROM TEST_BLOB_MULTI ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int
		var data string
		if err := rows.Scan(&id, &data); err != nil {
			t.Fatalf("row %d: Scan: %v", count, err)
		}
		expected := fmt.Sprintf("blob_content_%d", id)
		if data != expected {
			t.Errorf("row %d: got %q, want %q", id, data, expected)
		}
		count++
	}
	if count != 10 {
		t.Fatalf("expected 10 rows, got %d", count)
	}
}

// --- Stored Procedure Tests ---

func TestExecProcedure(t *testing.T) {
	db := openTestDB(t)

	// Create a simple stored procedure
	db.Exec("DROP PROCEDURE TEST_ADD_PROC")
	_, err := db.Exec(`
		CREATE PROCEDURE TEST_ADD_PROC (A INTEGER, B INTEGER)
		RETURNS (RESULT INTEGER)
		AS
		BEGIN
			RESULT = A + B;
			SUSPEND;
		END
	`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE: %v", err)
	}
	defer db.Exec("DROP PROCEDURE TEST_ADD_PROC")

	var result int
	if err := db.QueryRow("SELECT RESULT FROM TEST_ADD_PROC(10, 20)").Scan(&result); err != nil {
		t.Fatalf("SELECT FROM PROC: %v", err)
	}
	if result != 30 {
		t.Errorf("got %d, want 30", result)
	}
}

func TestExecProcedureMultiRow(t *testing.T) {
	db := openTestDB(t)

	db.Exec("DROP PROCEDURE TEST_GEN_PROC")
	_, err := db.Exec(`
		CREATE PROCEDURE TEST_GEN_PROC (N INTEGER)
		RETURNS (VAL INTEGER)
		AS
		DECLARE VARIABLE I INTEGER;
		BEGIN
			I = 1;
			WHILE (I <= N) DO
			BEGIN
				VAL = I * I;
				SUSPEND;
				I = I + 1;
			END
		END
	`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE: %v", err)
	}
	defer db.Exec("DROP PROCEDURE TEST_GEN_PROC")

	rows, err := db.Query("SELECT VAL FROM TEST_GEN_PROC(5)")
	if err != nil {
		t.Fatalf("SELECT FROM PROC: %v", err)
	}
	defer rows.Close()

	expected := []int{1, 4, 9, 16, 25}
	i := 0
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("row %d: Scan: %v", i, err)
		}
		if v != expected[i] {
			t.Errorf("row %d: got %d, want %d", i, v, expected[i])
		}
		i++
	}
	if i != 5 {
		t.Fatalf("expected 5 rows, got %d", i)
	}
}

func TestExecuteProcedure(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_PROC_TBL")
	db.Exec(`CREATE TABLE TEST_PROC_TBL (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`)
	db.Exec("DROP PROCEDURE TEST_INS_PROC")

	_, err := db.Exec(`
		CREATE PROCEDURE TEST_INS_PROC (P_ID INTEGER, P_V INTEGER)
		AS
		BEGIN
			INSERT INTO TEST_PROC_TBL (ID, V) VALUES (:P_ID, :P_V);
		END
	`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE: %v", err)
	}
	defer func() {
		db.Exec("DROP PROCEDURE TEST_INS_PROC")
		db.Exec("DROP TABLE TEST_PROC_TBL")
	}()

	_, err = db.Exec("EXECUTE PROCEDURE TEST_INS_PROC(1, 100)")
	if err != nil {
		t.Fatalf("EXECUTE PROCEDURE: %v", err)
	}

	var v int
	if err := db.QueryRow("SELECT V FROM TEST_PROC_TBL WHERE ID=1").Scan(&v); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if v != 100 {
		t.Errorf("got %d, want 100", v)
	}
}

func TestExecuteProcedureReturning(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP PROCEDURE TEST_RET_PROC")
	db.Exec("DROP TABLE TEST_PROC_RET_TBL")
	if _, err := db.Exec(`CREATE TABLE TEST_PROC_RET_TBL (ID INTEGER NOT NULL PRIMARY KEY, V INTEGER)`); err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	defer func() {
		db.Exec("DROP PROCEDURE TEST_RET_PROC")
		db.Exec("DROP TABLE TEST_PROC_RET_TBL")
	}()

	_, err := db.Exec(`
		CREATE PROCEDURE TEST_RET_PROC (P_ID INTEGER, P_V INTEGER)
		RETURNS (NEW_ID INTEGER, NEW_V INTEGER)
		AS
		BEGIN
			INSERT INTO TEST_PROC_RET_TBL (ID, V) VALUES (:P_ID, :P_V);
			NEW_ID = P_ID;
			NEW_V = P_V;
		END
	`)
	if err != nil {
		t.Fatalf("CREATE PROCEDURE: %v", err)
	}

	var id, v int
	if err := db.QueryRow("EXECUTE PROCEDURE TEST_RET_PROC(?, ?)", 1, 100).Scan(&id, &v); err != nil {
		t.Fatalf("EXECUTE PROCEDURE query: %v", err)
	}
	if id != 1 || v != 100 {
		t.Fatalf("query row = (%d, %d), want (1, 100)", id, v)
	}

	stmt, err := db.Prepare("EXECUTE PROCEDURE TEST_RET_PROC(?, ?)")
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer stmt.Close()

	if err := stmt.QueryRow(2, 200).Scan(&id, &v); err != nil {
		t.Fatalf("prepared EXECUTE PROCEDURE query: %v", err)
	}
	if id != 2 || v != 200 {
		t.Fatalf("prepared row = (%d, %d), want (2, 200)", id, v)
	}

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM TEST_PROC_RET_TBL").Scan(&count); err != nil {
		t.Fatalf("SELECT COUNT: %v", err)
	}
	if count != 2 {
		t.Fatalf("inserted rows = %d, want 2", count)
	}
}

// --- Context Tests ---

func TestContextTimeout(t *testing.T) {
	db := openTestDB(t)

	// Use a very short timeout - should fail
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	// Give the context time to expire
	time.Sleep(5 * time.Millisecond)

	_, err := db.ExecContext(ctx, "SELECT 1 FROM RDB$DATABASE")
	if err == nil {
		t.Fatal("expected error from expired context")
	}
}

func TestContextCancelDuringQuery(t *testing.T) {
	db := openTestDB(t)

	// Normal query with generous timeout should succeed
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var v int
	if err := db.QueryRowContext(ctx, "SELECT 1+1 FROM RDB$DATABASE").Scan(&v); err != nil {
		t.Fatalf("QueryRowContext: %v", err)
	}
	if v != 2 {
		t.Errorf("got %d, want 2", v)
	}
}

func TestContextCancelDuringRowsScan(t *testing.T) {
	db, err := sqlOpenTestDBWithParam("fetch_size", "50")
	if err != nil {
		t.Fatalf("open fetch_size DB: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	rows, err := db.QueryContext(ctx, `
		EXECUTE BLOCK RETURNS (I BIGINT)
		AS
			DECLARE C BIGINT = 0;
		BEGIN
			WHILE (C < 9000000000) DO
			BEGIN
				C = C + 1;
				I = C;
				SUSPEND;
			END
		END`)
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	defer rows.Close()

	deadline := time.Now().Add(3 * time.Second)
	scanned := 0
	var scanErr error
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			scanErr = err
			break
		}
		scanned++
		if time.Now().After(deadline) {
			t.Fatalf("Rows.Next did not stop after context deadline; scanned %d rows", scanned)
		}
	}
	if scanned == 0 {
		t.Fatal("expected to scan at least one row before cancellation")
	}
	if err := rows.Err(); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(scanErr, context.DeadlineExceeded) {
		t.Fatalf("Rows.Err = %v, Scan err = %v, want context deadline exceeded", err, scanErr)
	}

	assertConnectionReusable(t, db, "after context-cancelled rows scan")
}

func TestPingWithContext(t *testing.T) {
	db := openTestDB(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Retry once: the previous test may leave a transient TCP state.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		db, _ = sql.Open("firebird", testDSN)
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		t.Cleanup(func() { db.Close() })
		if err2 := db.PingContext(ctx); err2 != nil {
			t.Fatalf("PingContext (retry): %v (original: %v)", err2, err)
		}
	}
}

// --- Stress Tests ---

func TestStressConcurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	db := openTestDB(t)

	// Use a single connection: multiple goroutines share it via
	// database/sql's pool, which serializes wire-level access.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	db.Exec("DROP TABLE TEST_STRESS")
	db.Exec(`CREATE TABLE TEST_STRESS (ID INTEGER NOT NULL PRIMARY KEY, V VARCHAR(100))`)
	defer db.Exec("DROP TABLE TEST_STRESS")

	const workers = 5
	const opsPerWorker = 20

	var wg sync.WaitGroup
	errCh := make(chan error, workers*opsPerWorker)

	for w := range workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for i := range opsPerWorker {
				id := workerID*opsPerWorker + i + 1

				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

				// Insert
				_, err := db.ExecContext(ctx, "INSERT INTO TEST_STRESS VALUES (?, ?)",
					id, fmt.Sprintf("worker_%d_op_%d", workerID, i))
				if err != nil {
					cancel()
					errCh <- fmt.Errorf("worker %d insert %d: %v", workerID, i, err)
					continue
				}

				// Read back
				var v string
				err = db.QueryRowContext(ctx, "SELECT V FROM TEST_STRESS WHERE ID=?", id).Scan(&v)
				cancel()
				if err != nil {
					errCh <- fmt.Errorf("worker %d select %d: %v", workerID, i, err)
				}
			}
		}(w)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}

	// Verify total count
	var count int
	db.QueryRow("SELECT COUNT(*) FROM TEST_STRESS").Scan(&count)
	if count != workers*opsPerWorker {
		t.Errorf("total rows: got %d, want %d", count, workers*opsPerWorker)
	}
}

func TestStressSequentialReconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	// Test that many sequential open/close cycles work
	for i := range 20 {
		db, err := sql.Open("firebird", testDSN)
		if err != nil {
			t.Fatalf("iter %d: Open: %v", i, err)
		}
		db.SetMaxOpenConns(1)

		// Firebird may not have finished cleaning up the previous TCP
		// connection. Retry once with a short pause.
		if err := db.Ping(); err != nil {
			db.Close()
			time.Sleep(50 * time.Millisecond)
			db, _ = sql.Open("firebird", testDSN)
			db.SetMaxOpenConns(1)
			if err2 := db.Ping(); err2 != nil {
				db.Close()
				t.Fatalf("iter %d: Ping: %v (original: %v)", i, err2, err)
			}
		}

		var v int
		if err := db.QueryRow("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE", i+1).Scan(&v); err != nil {
			db.Close()
			t.Fatalf("iter %d: Query: %v", i, err)
		}
		if v != i+1 {
			db.Close()
			t.Fatalf("iter %d: got %d, want %d", i, v, i+1)
		}

		db.Close()
	}
}

func TestStressRapidPrepareClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	db := openTestDB(t)

	for i := range 100 {
		stmt, err := db.Prepare("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE")
		if err != nil {
			t.Fatalf("iter %d: Prepare: %v", i, err)
		}
		var v int
		if err := stmt.QueryRow(i + 1).Scan(&v); err != nil {
			stmt.Close()
			t.Fatalf("iter %d: QueryRow: %v", i, err)
		}
		if v != i+1 {
			stmt.Close()
			t.Fatalf("iter %d: got %d, want %d", i, v, i+1)
		}
		stmt.Close()
	}
}

// --- Firebird 4+ DSN helpers ---

// openFB4DB returns a *sql.DB for Firebird 4 using the configured test DSN.
// Skips the test if the server is not reachable.
func openFB4DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("firebird", testDSN_FB4)
	if err != nil {
		t.Skipf("FB4 not available: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("FB4 not reachable (%s): %v", testDSN_FB4, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openFB5DB returns a *sql.DB for Firebird 5 using the configured test DSN.
// Skips the test if the server is not reachable.
func openFB5DB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("firebird", testDSN_FB5)
	if err != nil {
		t.Skipf("FB5 not available: %v", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("FB5 not reachable (%s): %v", testDSN_FB5, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// --- NamedValueChecker Tests ---

func TestNamedValueChecker(t *testing.T) {
	db := openTestDB(t)

	// int32 should be accepted and converted
	var v int
	if err := db.QueryRow("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE", int32(42)).Scan(&v); err != nil {
		t.Fatalf("int32 param: %v", err)
	}
	if v != 42 {
		t.Errorf("int32: got %d, want 42", v)
	}

	// int16 should be accepted
	if err := db.QueryRow("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE", int16(7)).Scan(&v); err != nil {
		t.Fatalf("int16 param: %v", err)
	}
	if v != 7 {
		t.Errorf("int16: got %d, want 7", v)
	}

	// uint32 should be accepted
	if err := db.QueryRow("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE", uint32(99)).Scan(&v); err != nil {
		t.Fatalf("uint32 param: %v", err)
	}
	if v != 99 {
		t.Errorf("uint32: got %d, want 99", v)
	}

	// float32 should be accepted and converted to float64
	var f float64
	if err := db.QueryRow("SELECT CAST(? AS DOUBLE PRECISION) FROM RDB$DATABASE", float32(3.14)).Scan(&f); err != nil {
		t.Fatalf("float32 param: %v", err)
	}
	if math.Abs(f-3.14) > 0.01 {
		t.Errorf("float32: got %f, want ~3.14", f)
	}
}

// --- Column Metadata Tests ---

func TestColumnMetadata(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_META")
	_, err := db.Exec(`CREATE TABLE TEST_META (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_SMALL SMALLINT,
		V_INT INTEGER,
		V_BIG BIGINT,
		V_FLOAT FLOAT,
		V_DOUBLE DOUBLE PRECISION,
		V_NUM NUMERIC(10,2),
		V_DEC DECIMAL(18,4),
		V_CHAR CHAR(20),
		V_VARCHAR VARCHAR(100),
		V_BOOL BOOLEAN,
		V_DATE DATE,
		V_TIME TIME,
		V_TS TIMESTAMP,
		V_BLOB BLOB SUB_TYPE 0,
		V_BLOB_TXT BLOB SUB_TYPE TEXT
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_META")

	_, err = db.Exec(`INSERT INTO TEST_META VALUES (
		1, 1, 1, 1, 1.0, 1.0, 1.00, 1.0000,
		'A', 'B', TRUE, '2024-01-01', '12:00:00',
		'2024-01-01 12:00:00', NULL, NULL
	)`)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rows, err := db.Query("SELECT * FROM TEST_META")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("ColumnTypes: %v", err)
	}

	expectedTypes := map[string]string{
		"ID":         "INTEGER",
		"V_SMALL":    "SMALLINT",
		"V_INT":      "INTEGER",
		"V_BIG":      "BIGINT",
		"V_FLOAT":    "FLOAT",
		"V_DOUBLE":   "DOUBLE PRECISION",
		"V_NUM":      "NUMERIC",
		"V_DEC":      "NUMERIC",
		"V_CHAR":     "CHAR",
		"V_VARCHAR":  "VARCHAR",
		"V_BOOL":     "BOOLEAN",
		"V_DATE":     "DATE",
		"V_TIME":     "TIME",
		"V_TS":       "TIMESTAMP",
		"V_BLOB":     "BLOB",
		"V_BLOB_TXT": "BLOB SUB_TYPE TEXT",
	}

	for _, ct := range colTypes {
		expected, ok := expectedTypes[ct.Name()]
		if !ok {
			continue
		}
		if ct.DatabaseTypeName() != expected {
			t.Errorf("column %s: DatabaseTypeName got %q, want %q",
				ct.Name(), ct.DatabaseTypeName(), expected)
		}
	}

	// Test ColumnTypeLength for variable-length types
	for _, ct := range colTypes {
		switch ct.Name() {
		case "V_VARCHAR":
			length, ok := ct.Length()
			if !ok {
				t.Error("V_VARCHAR: Length not reported")
			} else if length <= 0 {
				t.Errorf("V_VARCHAR: Length got %d, want > 0", length)
			}
		case "V_CHAR":
			length, ok := ct.Length()
			if !ok {
				t.Error("V_CHAR: Length not reported")
			} else if length <= 0 {
				t.Errorf("V_CHAR: Length got %d, want > 0", length)
			}
		}
	}

	// Test Nullable
	for _, ct := range colTypes {
		nullable, ok := ct.Nullable()
		if !ok {
			continue
		}
		if ct.Name() == "ID" && nullable {
			t.Error("ID should not be nullable")
		}
		if ct.Name() == "V_INT" && !nullable {
			t.Error("V_INT should be nullable")
		}
	}

	// Test PrecisionScale for NUMERIC/DECIMAL
	for _, ct := range colTypes {
		if ct.Name() == "V_NUM" {
			prec, scale, ok := ct.DecimalSize()
			if !ok {
				t.Error("V_NUM: PrecisionScale not reported")
			} else if scale != 2 {
				t.Errorf("V_NUM: scale got %d, want 2", scale)
			} else if prec <= 0 {
				t.Errorf("V_NUM: precision got %d, want > 0", prec)
			}
		}
		if ct.Name() == "V_DEC" {
			prec, scale, ok := ct.DecimalSize()
			if !ok {
				t.Error("V_DEC: PrecisionScale not reported")
			} else if scale != 4 {
				t.Errorf("V_DEC: scale got %d, want 4", scale)
			} else if prec <= 0 {
				t.Errorf("V_DEC: precision got %d, want > 0", prec)
			}
		}
	}
}

// --- Transaction Edge Case Tests ---

func TestTransactionReadOnly(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_RO")
	db.Exec(`CREATE TABLE TEST_RO (ID INTEGER NOT NULL PRIMARY KEY)`)
	defer db.Exec("DROP TABLE TEST_RO")

	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatalf("BeginTx read-only: %v", err)
	}
	defer tx.Rollback()

	// Read should succeed
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM TEST_RO").Scan(&count); err != nil {
		t.Fatalf("SELECT in read-only tx: %v", err)
	}

	// Write should fail
	_, err = tx.Exec("INSERT INTO TEST_RO VALUES (1)")
	if err == nil {
		t.Error("expected error for INSERT in read-only transaction")
	}
}

func TestTransactionUnsupportedIsolation(t *testing.T) {
	db := openTestDB(t)

	unsupported := []sql.IsolationLevel{
		sql.LevelReadUncommitted,
		sql.LevelWriteCommitted,
		sql.LevelLinearizable,
	}

	for _, iso := range unsupported {
		_, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: iso})
		if err == nil {
			t.Errorf("expected error for isolation level %d", iso)
		}
	}
}

// --- Result Tests ---

func TestLastInsertIdReturnsError(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_LID")
	db.Exec(`CREATE TABLE TEST_LID (ID INTEGER NOT NULL PRIMARY KEY)`)
	defer db.Exec("DROP TABLE TEST_LID")

	res, err := db.Exec("INSERT INTO TEST_LID VALUES (1)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	_, err = res.LastInsertId()
	if err == nil {
		t.Error("expected error from LastInsertId (Firebird has no auto-increment)")
	}

	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("RowsAffected: %v", err)
	}
	if n != 1 {
		t.Errorf("RowsAffected: got %d, want 1", n)
	}
}

// --- Validator / SessionResetter Tests ---

func TestValidatorAfterClose(t *testing.T) {
	db := openTestDB(t)

	// Normal usage should work
	if err := db.Ping(); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// After closing db, new operations should fail
	db.Close()
	err := db.Ping()
	if err == nil {
		t.Error("expected error after db.Close()")
	}
}

func TestPoolReusesConnections(t *testing.T) {
	db := openTestDB(t)
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)

	// Execute multiple sequential queries - should reuse connections
	for i := range 20 {
		var v int
		if err := db.QueryRow("SELECT CAST(? AS INTEGER) FROM RDB$DATABASE", i+1).Scan(&v); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
		if v != i+1 {
			t.Fatalf("iter %d: got %d, want %d", i, v, i+1)
		}
	}
}

// --- Resilience Tests ---

func TestConnectionToInvalidServer(t *testing.T) {
	// Connect to an invalid port - should fail quickly
	db, err := sql.Open("firebird", "firebird://sysdba:masterkey@localhost:19999/test.fdb")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err = db.PingContext(ctx)
	if err == nil {
		t.Fatal("expected error connecting to invalid server")
	}
}

func TestInvalidCredentials(t *testing.T) {
	badCredsDSN := strings.Replace(testDSN, "sysdba:masterkey@", "baduser:badpass@", 1)
	db, err := sql.Open("firebird", badCredsDSN)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	err = db.Ping()
	if err == nil {
		t.Fatal("expected error for invalid credentials")
	}
}

// --- Firebird 4/5 Type Tests (require Docker containers) ---

func TestDataTypeDecfloat_FB4(t *testing.T) {
	db := openFB4DB(t)

	db.Exec("DROP TABLE TEST_DECF")
	_, err := db.Exec(`CREATE TABLE TEST_DECF (
		ID INTEGER NOT NULL PRIMARY KEY,
		V16 DECFLOAT(16),
		V34 DECFLOAT(34)
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_DECF")

	tests := []struct {
		id      int
		v16     string
		v34     string
		wantV16 string
		wantV34 string
	}{
		{1, "123.456", "123456789012345678901234567890.1234", "123.456", "123456789012345678901234567890.1234"},
		{2, "-0.001", "-0.00000000000000000000000000000001", "-0.001", "-1E-32"},
		{3, "0", "0", "0", "0"},
		{4, "9999999999999999", "9999999999999999999999999999999999", "9999999999999999", "9999999999999999999999999999999999"},
	}

	for _, tt := range tests {
		_, err := db.Exec("INSERT INTO TEST_DECF VALUES (?, CAST(? AS DECFLOAT(16)), CAST(? AS DECFLOAT(34)))",
			tt.id, tt.v16, tt.v34)
		if err != nil {
			t.Fatalf("INSERT id=%d: %v", tt.id, err)
		}
	}

	rows, err := db.Query("SELECT ID, V16, V34 FROM TEST_DECF ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		var id int
		var v16, v34 string
		if err := rows.Scan(&id, &v16, &v34); err != nil {
			t.Fatalf("row %d: Scan: %v", i, err)
		}
		if id != tests[i].id {
			t.Errorf("row %d: id got %d, want %d", i, id, tests[i].id)
		}
		if v16 != tests[i].wantV16 {
			t.Errorf("row %d: V16 got %q, want %q", i, v16, tests[i].wantV16)
		}
		if v34 != tests[i].wantV34 {
			t.Errorf("row %d: V34 got %q, want %q", i, v34, tests[i].wantV34)
		}
		t.Logf("row %d: V16=%q V34=%q", i, v16, v34)
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if i != len(tests) {
		t.Fatalf("expected %d rows, got %d", len(tests), i)
	}
}

func TestDataTypeDecfloatSpecials_FB4(t *testing.T) {
	db := openFB4DB(t)

	// Test special DECFLOAT values via expressions
	var v string

	if err := db.QueryRow("SELECT CAST('Infinity' AS DECFLOAT(16)) FROM RDB$DATABASE").Scan(&v); err != nil {
		t.Fatalf("Infinity scan: %v", err)
	}
	if v != "Infinity" {
		t.Errorf("Infinity: got %q, want %q", v, "Infinity")
	}

	if err := db.QueryRow("SELECT CAST('-Infinity' AS DECFLOAT(16)) FROM RDB$DATABASE").Scan(&v); err != nil {
		t.Fatalf("-Infinity scan: %v", err)
	}
	if v != "-Infinity" {
		t.Errorf("-Infinity: got %q, want %q", v, "-Infinity")
	}

	if err := db.QueryRow("SELECT CAST('NaN' AS DECFLOAT(16)) FROM RDB$DATABASE").Scan(&v); err != nil {
		t.Fatalf("NaN scan: %v", err)
	}
	if v != "NaN" {
		t.Errorf("NaN: got %q, want %q", v, "NaN")
	}
}

func TestDataTypeInt128_FB4(t *testing.T) {
	db := openFB4DB(t)

	db.Exec("DROP TABLE TEST_I128")
	_, err := db.Exec(`CREATE TABLE TEST_I128 (
		ID INTEGER NOT NULL PRIMARY KEY,
		V INT128
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_I128")

	tests := []struct {
		id  int
		val string
	}{
		{1, "0"},
		{2, "170141183460469231731687303715884105727"},  // max INT128
		{3, "-170141183460469231731687303715884105728"}, // min INT128
		{4, "123456789012345678901234567890"},
	}

	for _, tt := range tests {
		_, err := db.Exec(fmt.Sprintf("INSERT INTO TEST_I128 VALUES (%d, %s)", tt.id, tt.val))
		if err != nil {
			t.Fatalf("INSERT id=%d: %v", tt.id, err)
		}
	}

	rows, err := db.Query("SELECT ID, V FROM TEST_I128 ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		var id int
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			t.Fatalf("row %d: Scan: %v", i, err)
		}
		if v == "" {
			t.Errorf("row %d: INT128 value is empty", i)
		}
		t.Logf("row %d: val=%q (expected %q)", i, v, tests[i].val)
		i++
	}
	if i != len(tests) {
		t.Fatalf("expected %d rows, got %d", len(tests), i)
	}
}

func TestDataTypeTimestampTZ_FB4(t *testing.T) {
	db := openFB4DB(t)
	if _, err := db.Exec("set bind of timestamp with time zone to native"); err != nil {
		t.Fatalf("SET BIND timestamp with time zone: %v", err)
	}

	db.Exec("DROP TABLE TEST_TSTZ")
	_, err := db.Exec(`CREATE TABLE TEST_TSTZ (
		ID INTEGER NOT NULL PRIMARY KEY,
		V TIMESTAMP WITH TIME ZONE
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_TSTZ")

	_, err = db.Exec("INSERT INTO TEST_TSTZ VALUES (1, TIMESTAMP '2024-06-15 14:30:00 America/New_York')")
	if err != nil {
		t.Fatalf("INSERT named tz: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_TSTZ VALUES (2, TIMESTAMP '2024-06-15 18:30:00 UTC')")
	if err != nil {
		t.Fatalf("INSERT UTC: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_TSTZ VALUES (3, TIMESTAMP '2024-01-15 10:00:00 +05:30')")
	if err != nil {
		t.Fatalf("INSERT offset: %v", err)
	}

	rows, err := db.Query("SELECT ID, V FROM TEST_TSTZ ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	count := 0
	expected := map[int]struct {
		location string
		hour     int
		minute   int
		offset   int
	}{
		1: {hour: 14, minute: 30, offset: -4 * 60 * 60},
		2: {location: "UTC", hour: 18, minute: 30, offset: 0},
		3: {location: "+05:30", hour: 10, minute: 0, offset: 5*60*60 + 30*60},
	}
	for rows.Next() {
		var id int
		var ts time.Time
		if err := rows.Scan(&id, &ts); err != nil {
			t.Fatalf("row %d: Scan: %v", count, err)
		}
		if ts.IsZero() {
			t.Errorf("row %d: got zero time", id)
		}
		want, ok := expected[id]
		if !ok {
			t.Fatalf("unexpected row id %d", id)
		}
		if want.location != "" && ts.Location().String() != want.location {
			t.Errorf("row %d: location got %q, want %q", id, ts.Location().String(), want.location)
		}
		if ts.Hour() != want.hour || ts.Minute() != want.minute {
			t.Errorf("row %d: clock got %02d:%02d, want %02d:%02d",
				id, ts.Hour(), ts.Minute(), want.hour, want.minute)
		}
		_, offset := ts.Zone()
		if offset != want.offset {
			t.Errorf("row %d: offset got %d, want %d", id, offset, want.offset)
		}
		t.Logf("row %d: %v (zone=%s)", id, ts, ts.Location())
		count++
	}
	if count != 3 {
		t.Fatalf("expected 3 rows, got %d", count)
	}
}

func TestDataTypeTimeTZ_FB4(t *testing.T) {
	db := openFB4DB(t)
	if _, err := db.Exec("set bind of time with time zone to native"); err != nil {
		t.Fatalf("SET BIND time with time zone: %v", err)
	}

	db.Exec("DROP TABLE TEST_TTZ")
	_, err := db.Exec(`CREATE TABLE TEST_TTZ (
		ID INTEGER NOT NULL PRIMARY KEY,
		V TIME WITH TIME ZONE
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_TTZ")

	_, err = db.Exec("INSERT INTO TEST_TTZ VALUES (1, TIME '14:30:00 America/New_York')")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_TTZ VALUES (2, TIME '00:00:00 UTC')")
	if err != nil {
		t.Fatalf("INSERT midnight UTC: %v", err)
	}
	_, err = db.Exec("INSERT INTO TEST_TTZ VALUES (3, TIME '10:15:00 +05:30')")
	if err != nil {
		t.Fatalf("INSERT offset: %v", err)
	}

	rows, err := db.Query("SELECT ID, V FROM TEST_TTZ ORDER BY ID")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	count := 0
	expected := map[int]struct {
		location string
		hour     int
		minute   int
		offset   int
	}{
		1: {hour: 14, minute: 30, offset: -5 * 60 * 60},
		2: {location: "UTC", hour: 0, minute: 0, offset: 0},
		3: {location: "+05:30", hour: 10, minute: 15, offset: 5*60*60 + 30*60},
	}
	for rows.Next() {
		var id int
		var tm time.Time
		if err := rows.Scan(&id, &tm); err != nil {
			t.Fatalf("row %d: Scan: %v", count, err)
		}
		if want, ok := expected[id]; ok {
			if want.location != "" && tm.Location().String() != want.location {
				t.Errorf("row %d: location got %q, want %q", id, tm.Location().String(), want.location)
			}
			if tm.Hour() != want.hour || tm.Minute() != want.minute {
				t.Errorf("row %d: clock got %02d:%02d, want %02d:%02d",
					id, tm.Hour(), tm.Minute(), want.hour, want.minute)
			}
			_, offset := tm.Zone()
			if offset != want.offset {
				t.Errorf("row %d: offset got %d, want %d", id, offset, want.offset)
			}
		}
		t.Logf("row %d: %v (zone=%s)", id, tm, tm.Location())
		count++
	}
	if count != 3 {
		t.Fatalf("expected 3 rows, got %d", count)
	}
}

func TestDataTypeNumericHighPrecision_FB4(t *testing.T) {
	db := openFB4DB(t)

	db.Exec("DROP TABLE TEST_NUMHP")
	_, err := db.Exec(`CREATE TABLE TEST_NUMHP (
		ID INTEGER NOT NULL PRIMARY KEY,
		V NUMERIC(34,10)
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_NUMHP")

	_, err = db.Exec("INSERT INTO TEST_NUMHP VALUES (1, 123456789012345678901234.1234567890)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var v string
	if err := db.QueryRow("SELECT V FROM TEST_NUMHP WHERE ID=1").Scan(&v); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if v == "" {
		t.Error("high-precision NUMERIC value is empty")
	}
	t.Logf("NUMERIC(34,10) = %q", v)
}

// --- FB4 Column Metadata ---

func TestColumnMetadataFB4Types(t *testing.T) {
	db := openFB4DB(t)
	runColumnMetadataFB4PlusTypes(t, db, "TEST_META4")
}

func TestColumnMetadataFB5Types(t *testing.T) {
	db := openFB5DB(t)
	runColumnMetadataFB4PlusTypes(t, db, "TEST_META5")
}

func runColumnMetadataFB4PlusTypes(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	db.Exec("DROP TABLE " + table)
	_, err := db.Exec(`CREATE TABLE ` + table + ` (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_DF16 DECFLOAT(16),
		V_DF34 DECFLOAT(34),
		V_I128 INT128,
		V_TSTZ TIMESTAMP WITH TIME ZONE,
		V_TTZ TIME WITH TIME ZONE
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE " + table)

	_, err = db.Exec("INSERT INTO " + table + " VALUES (1, 1.0, 1.0, 1, CURRENT_TIMESTAMP, CURRENT_TIME)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rows, err := db.Query("SELECT * FROM " + table)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	colTypes, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("ColumnTypes: %v", err)
	}

	expectedTypes := map[string]string{
		"V_DF16": "DECFLOAT(16)",
		"V_DF34": "DECFLOAT(34)",
		"V_I128": "INT128",
		"V_TSTZ": "TIMESTAMP WITH TIME ZONE",
		"V_TTZ":  "TIME WITH TIME ZONE",
	}
	expectedScanTypes := map[string]reflect.Type{
		"V_DF16": reflect.TypeOf(""),
		"V_DF34": reflect.TypeOf(""),
		"V_I128": reflect.TypeOf(""),
		"V_TSTZ": reflect.TypeOf(time.Time{}),
		"V_TTZ":  reflect.TypeOf(time.Time{}),
	}

	for _, ct := range colTypes {
		expected, ok := expectedTypes[ct.Name()]
		if !ok {
			continue
		}
		if ct.DatabaseTypeName() != expected {
			t.Errorf("column %s: DatabaseTypeName got %q, want %q",
				ct.Name(), ct.DatabaseTypeName(), expected)
		}
		if scanType := ct.ScanType(); scanType != expectedScanTypes[ct.Name()] {
			t.Errorf("column %s: ScanType got %v, want %v",
				ct.Name(), scanType, expectedScanTypes[ct.Name()])
		}
	}
}

// --- Firebird 5 Tests (verify protocol v18) ---

func TestConnectFB5(t *testing.T) {
	db := openFB5DB(t)
	if err := db.Ping(); err != nil {
		t.Fatalf("FB5 Ping: %v", err)
	}

	var v int
	if err := db.QueryRow("SELECT 1+1 FROM RDB$DATABASE").Scan(&v); err != nil {
		t.Fatalf("FB5 QueryRow: %v", err)
	}
	if v != 2 {
		t.Errorf("FB5: got %d, want 2", v)
	}
}

func TestDataTypesFB5(t *testing.T) {
	db := openFB5DB(t)

	// Test all FB4+ types work on FB5 as well
	db.Exec("DROP TABLE TEST_FB5_TYPES")
	_, err := db.Exec(`CREATE TABLE TEST_FB5_TYPES (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_DF16 DECFLOAT(16),
		V_DF34 DECFLOAT(34),
		V_I128 INT128,
		V_TSTZ TIMESTAMP WITH TIME ZONE,
		V_TTZ TIME WITH TIME ZONE,
		V_BOOL BOOLEAN
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_FB5_TYPES")

	_, err = db.Exec(`INSERT INTO TEST_FB5_TYPES VALUES (
		1, 42.5, 12345678901234567890.12345, 999999999999999999,
		TIMESTAMP '2025-03-15 10:00:00 UTC',
		TIME '15:30:00 Europe/Madrid',
		TRUE
	)`)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var (
		id   int
		df16 string
		df34 string
		i128 string
		tstz time.Time
		ttz  time.Time
		b    bool
	)
	if err := db.QueryRow("SELECT * FROM TEST_FB5_TYPES WHERE ID=1").Scan(
		&id, &df16, &df34, &i128, &tstz, &ttz, &b,
	); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if df16 == "" || df34 == "" || i128 == "" {
		t.Errorf("empty string values: df16=%q df34=%q i128=%q", df16, df34, i128)
	}
	if df16 != "42.5" {
		t.Errorf("DECFLOAT(16): got %q, want 42.5", df16)
	}
	if df34 != "12345678901234567890.12345" {
		t.Errorf("DECFLOAT(34): got %q", df34)
	}
	if tstz.IsZero() {
		t.Error("TIMESTAMP WITH TIME ZONE is zero")
	}
	if !b {
		t.Error("BOOLEAN: got false, want true")
	}
	t.Logf("FB5: df16=%q df34=%q i128=%q tstz=%v ttz=%v bool=%v", df16, df34, i128, tstz, ttz, b)
}

// --- NULL handling for all types ---

func TestNullAllTypes(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_NULL_ALL")
	_, err := db.Exec(`CREATE TABLE TEST_NULL_ALL (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_SMALL SMALLINT,
		V_INT INTEGER,
		V_BIG BIGINT,
		V_FLOAT FLOAT,
		V_DOUBLE DOUBLE PRECISION,
		V_NUM NUMERIC(10,2),
		V_CHAR CHAR(20),
		V_VARCHAR VARCHAR(100),
		V_BOOL BOOLEAN,
		V_DATE DATE,
		V_TIME TIME,
		V_TS TIMESTAMP,
		V_BLOB BLOB SUB_TYPE 0,
		V_BLOB_TXT BLOB SUB_TYPE TEXT
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_NULL_ALL")

	// Insert row with ALL nulls
	_, err = db.Exec(`INSERT INTO TEST_NULL_ALL VALUES (
		1, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL
	)`)
	if err != nil {
		t.Fatalf("INSERT all nulls: %v", err)
	}

	// Scan all as NullString (universal scanner)
	row := db.QueryRow("SELECT V_SMALL, V_INT, V_BIG, V_FLOAT, V_DOUBLE, V_NUM, V_CHAR, V_VARCHAR, V_BOOL, V_DATE, V_TIME, V_TS, V_BLOB, V_BLOB_TXT FROM TEST_NULL_ALL WHERE ID=1")

	vals := make([]sql.NullString, 14)
	ptrs := make([]any, 14)
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := row.Scan(ptrs...); err != nil {
		t.Fatalf("Scan all nulls: %v", err)
	}
	for i, v := range vals {
		if v.Valid {
			t.Errorf("column %d: expected NULL, got %q", i, v.String)
		}
	}
}

// --- UTF-8 multibyte Tests ---

func TestUTF8MultiByte(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_UTF8")
	_, err := db.Exec(`CREATE TABLE TEST_UTF8 (
		ID INTEGER NOT NULL PRIMARY KEY,
		V_VARCHAR VARCHAR(200),
		V_BLOB BLOB SUB_TYPE TEXT
	)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_UTF8")

	// Test various Unicode scripts
	tests := []struct {
		id  int
		txt string
	}{
		{1, "日本語テスト"},           // Japanese
		{2, "Ñoño café résumé"}, // Accented Latin
		{3, "Привет мир"},       // Cyrillic
		{4, "🔥🚀💻"},              // Emoji
		{5, "مرحبا"},            // Arabic
	}

	for _, tt := range tests {
		_, err := db.Exec("INSERT INTO TEST_UTF8 VALUES (?, ?, ?)",
			tt.id, tt.txt, tt.txt)
		if err != nil {
			t.Fatalf("INSERT id=%d: %v", tt.id, err)
		}
	}

	for _, tt := range tests {
		var vc, bl string
		if err := db.QueryRow("SELECT V_VARCHAR, V_BLOB FROM TEST_UTF8 WHERE ID=?", tt.id).Scan(&vc, &bl); err != nil {
			t.Fatalf("Scan id=%d: %v", tt.id, err)
		}
		if vc != tt.txt {
			t.Errorf("id=%d VARCHAR: got %q, want %q", tt.id, vc, tt.txt)
		}
		if bl != tt.txt {
			t.Errorf("id=%d BLOB TEXT: got %q, want %q", tt.id, bl, tt.txt)
		}
	}
}

// ---------------------------------------------------------------------------
// Wire Protocol Info Tests
// ---------------------------------------------------------------------------
//
// These tests verify wire-level info operations (opInfoDatabase,
// opInfoTransaction, opInfoBlob) by accessing the underlying wire
// connection through sql.Conn.Raw().
// Skipped when the Firebird server is not available.

func TestDatabaseInfo(t *testing.T) {
	db := openTestDB(t)
	if err := db.Ping(); err != nil {
		t.Skipf("Firebird server not available: %v", err)
	}

	ctx := context.Background()
	sqlConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer sqlConn.Close()

	err = sqlConn.Raw(func(driverConn any) error {
		c := driverConn.(*conn)
		items := []byte{
			wire.IscInfoPageSize,
			wire.IscInfoOdsVersion,
			wire.IscInfoOdsMinorVersion,
			wire.IscInfoFirebirdVersion,
			wire.IscInfoDBSQLDialect,
			wire.IscInfoDBReadOnly,
		}
		buf, infoErr := c.wc.InfoDatabase(items, 1024)
		if infoErr != nil {
			return fmt.Errorf("InfoDatabase: %w", infoErr)
		}

		parsed, truncated := wire.ParseInfoBuffer(buf)
		if truncated {
			return fmt.Errorf("InfoDatabase buffer truncated")
		}
		if len(parsed) == 0 {
			return fmt.Errorf("InfoDatabase returned no items")
		}

		foundPageSize := false
		foundODS := false
		for _, item := range parsed {
			switch item.Tag {
			case wire.IscInfoPageSize:
				ps := item.Int32LE()
				t.Logf("Page size: %d", ps)
				if ps < 4096 || ps > 32768 {
					return fmt.Errorf("unexpected page size: %d (expected 4096-32768)", ps)
				}
				foundPageSize = true
			case wire.IscInfoOdsVersion:
				ods := item.Int32LE()
				t.Logf("ODS version: %d", ods)
				if ods < 12 || ods > 20 {
					return fmt.Errorf("unexpected ODS version: %d (expected 12-20)", ods)
				}
				foundODS = true
			case wire.IscInfoOdsMinorVersion:
				t.Logf("ODS minor version: %d", item.Int32LE())
			case wire.IscInfoFirebirdVersion:
				t.Logf("Firebird version: %s", item.String())
			case wire.IscInfoDBSQLDialect:
				d := item.Int32LE()
				t.Logf("SQL dialect: %d", d)
				if d != 3 {
					return fmt.Errorf("unexpected dialect: %d (expected 3)", d)
				}
			case wire.IscInfoDBReadOnly:
				ro := item.Int32LE()
				t.Logf("Read-only: %v", ro != 0)
				if ro != 0 {
					return fmt.Errorf("expected writable database, got read-only")
				}
			}
		}
		if !foundPageSize {
			return fmt.Errorf("page size not returned")
		}
		if !foundODS {
			return fmt.Errorf("ODS version not returned")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransactionInfo(t *testing.T) {
	db := openTestDB(t)
	if err := db.Ping(); err != nil {
		t.Skipf("Firebird server not available: %v", err)
	}

	ctx := context.Background()
	sqlConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer sqlConn.Close()

	// Execute a query to ensure autoTx is initialized
	var dummy int
	if err := sqlConn.QueryRowContext(ctx, "SELECT 1 FROM RDB$DATABASE").Scan(&dummy); err != nil {
		t.Fatalf("initial query: %v", err)
	}

	err = sqlConn.Raw(func(driverConn any) error {
		c := driverConn.(*conn)
		txHandle := c.autoTx
		if txHandle == 0 {
			return fmt.Errorf("autoTx handle is zero after query")
		}

		items := []byte{
			wire.IscInfoTraID,
			wire.IscInfoTraIsolation,
			wire.IscInfoTraAccess,
		}
		buf, infoErr := c.wc.InfoTransaction(txHandle, items, 1024)
		if infoErr != nil {
			return fmt.Errorf("InfoTransaction: %w", infoErr)
		}

		parsed, truncated := wire.ParseInfoBuffer(buf)
		if truncated {
			return fmt.Errorf("InfoTransaction buffer truncated")
		}
		if len(parsed) == 0 {
			return fmt.Errorf("InfoTransaction returned no items")
		}

		foundID := false
		for _, item := range parsed {
			switch item.Tag {
			case wire.IscInfoTraID:
				txID := item.Int32LE()
				t.Logf("Transaction ID: %d", txID)
				if txID <= 0 {
					return fmt.Errorf("unexpected transaction ID: %d", txID)
				}
				foundID = true
			case wire.IscInfoTraIsolation:
				t.Logf("Isolation (raw bytes): %v", item.Data)
			case wire.IscInfoTraAccess:
				t.Logf("Access mode (raw bytes): %v", item.Data)
			}
		}
		if !foundID {
			return fmt.Errorf("transaction ID not returned")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransactionInfoExplicit(t *testing.T) {
	db := openTestDB(t)
	if err := db.Ping(); err != nil {
		t.Skipf("Firebird server not available: %v", err)
	}

	ctx := context.Background()
	sqlConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer sqlConn.Close()

	// Test each isolation level with an explicit transaction
	levels := []struct {
		name string
		iso  sql.IsolationLevel
	}{
		{"ReadCommitted", sql.LevelReadCommitted},
		{"RepeatableRead", sql.LevelRepeatableRead},
		{"Serializable", sql.LevelSerializable},
	}

	for _, lvl := range levels {
		t.Run(lvl.name, func(t *testing.T) {
			tx, txErr := sqlConn.BeginTx(ctx, &sql.TxOptions{Isolation: lvl.iso})
			if txErr != nil {
				t.Fatalf("BeginTx(%s): %v", lvl.name, txErr)
			}

			var v int
			if qErr := tx.QueryRowContext(ctx, "SELECT 1 FROM RDB$DATABASE").Scan(&v); qErr != nil {
				tx.Rollback()
				t.Fatalf("query in tx: %v", qErr)
			}
			if v != 1 {
				t.Errorf("expected 1, got %d", v)
			}
			tx.Rollback()
		})
	}
}

func TestBlobInfo(t *testing.T) {
	db := openTestDB(t)
	if err := db.Ping(); err != nil {
		t.Skipf("Firebird server not available: %v", err)
	}

	ctx := context.Background()
	sqlConn, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("db.Conn: %v", err)
	}
	defer sqlConn.Close()

	// Ensure autoTx is active
	var dummy int
	sqlConn.QueryRowContext(ctx, "SELECT 1 FROM RDB$DATABASE").Scan(&dummy)

	err = sqlConn.Raw(func(driverConn any) error {
		c := driverConn.(*conn)
		txHandle := c.autoTx
		if txHandle == 0 {
			return fmt.Errorf("autoTx handle is zero")
		}

		// Create a BLOB at wire level with known content
		testData := []byte("Hello, BLOB info test! This is test data for verifying InfoBlob operation.")

		blobHandle, _, blobErr := c.wc.CreateBlob(txHandle, nil)
		if blobErr != nil {
			return fmt.Errorf("CreateBlob: %w", blobErr)
		}

		if putErr := c.wc.PutSegment(blobHandle, testData); putErr != nil {
			c.wc.CancelBlob(blobHandle)
			return fmt.Errorf("PutSegment: %w", putErr)
		}

		// Query blob info while handle is still open
		items := []byte{
			wire.IscInfoBlobTotalLength,
			wire.IscInfoBlobNumSegments,
			wire.IscInfoBlobMaxSegment,
			wire.IscInfoBlobType,
		}
		buf, infoErr := c.wc.InfoBlob(blobHandle, items, 1024)
		if infoErr != nil {
			c.wc.CancelBlob(blobHandle)
			return fmt.Errorf("InfoBlob: %w", infoErr)
		}

		parsed, truncated := wire.ParseInfoBuffer(buf)
		if truncated {
			c.wc.CancelBlob(blobHandle)
			return fmt.Errorf("InfoBlob buffer truncated")
		}

		foundLength := false
		for _, item := range parsed {
			switch item.Tag {
			case wire.IscInfoBlobTotalLength:
				length := item.Int32LE()
				t.Logf("BLOB total length: %d", length)
				if int(length) != len(testData) {
					c.wc.CancelBlob(blobHandle)
					return fmt.Errorf("BLOB length: got %d, want %d", length, len(testData))
				}
				foundLength = true
			case wire.IscInfoBlobNumSegments:
				segs := item.Int32LE()
				t.Logf("BLOB segments: %d", segs)
				if segs != 1 {
					c.wc.CancelBlob(blobHandle)
					return fmt.Errorf("BLOB segments: got %d, want 1", segs)
				}
			case wire.IscInfoBlobMaxSegment:
				t.Logf("BLOB max segment: %d", item.Int32LE())
			case wire.IscInfoBlobType:
				t.Logf("BLOB type: %d", item.Int32LE())
			}
		}
		if !foundLength {
			c.wc.CancelBlob(blobHandle)
			return fmt.Errorf("BLOB total length not returned")
		}

		// Cancel the blob (we don't need to persist it)
		return c.wc.CancelBlob(blobHandle)
	})
	if err != nil {
		t.Fatal(err)
	}
}
