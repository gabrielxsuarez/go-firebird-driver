package firebird

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestISO88591CharsetRoundTrip(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_ISO88591")
	_, err := db.Exec(`
		CREATE TABLE TEST_ISO88591 (
			ID INTEGER NOT NULL PRIMARY KEY,
			V_VARCHAR VARCHAR(50) CHARACTER SET ISO8859_1,
			V_CHAR CHAR(4) CHARACTER SET ISO8859_1
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_ISO88591")

	wantVarchar := "VARTA\u00a0CR"
	wantChar := "A\u00a0"
	_, err = db.Exec("INSERT INTO TEST_ISO88591 VALUES (?, ?, ?)", 1, wantVarchar, wantChar)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var gotVarchar, gotChar string
	if err := db.QueryRow("SELECT V_VARCHAR, V_CHAR FROM TEST_ISO88591 WHERE ID=1").Scan(&gotVarchar, &gotChar); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !utf8.ValidString(gotVarchar) || !utf8.ValidString(gotChar) {
		t.Fatalf("got invalid UTF-8: varchar=% x char=% x", gotVarchar, gotChar)
	}
	if gotVarchar != wantVarchar {
		t.Fatalf("varchar = %q, want %q", gotVarchar, wantVarchar)
	}
	if gotChar != wantChar {
		t.Fatalf("char = %q, want %q", gotChar, wantChar)
	}
}

func TestCharsetRoundTripMatrix(t *testing.T) {
	db := openTestDB(t)

	tests := []struct {
		name    string
		charset string
		value   string
		fixed   string
	}{
		{
			name:    "ISO88591",
			charset: "ISO8859_1",
			value:   "VARTA\u00a0CR",
			fixed:   "A\u00a0",
		},
		{
			name:    "WIN1251",
			charset: "WIN1251",
			value:   "\u041f\u0440\u0438\u0432\u0435\u0442",
			fixed:   "\u042f",
		},
		{
			name:    "WIN1252",
			charset: "WIN1252",
			value:   "precio 10\u20ac",
			fixed:   "\u20ac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table := "TEST_CH_" + tt.name
			db.Exec("DROP TABLE " + table)
			_, err := db.Exec(fmt.Sprintf(`
				CREATE TABLE %s (
					ID INTEGER NOT NULL PRIMARY KEY,
					V_VARCHAR VARCHAR(50) CHARACTER SET %s,
					V_CHAR CHAR(12) CHARACTER SET %s
				)`, table, tt.charset, tt.charset))
			if err != nil {
				t.Fatalf("CREATE: %v", err)
			}
			defer db.Exec("DROP TABLE " + table)

			if _, err := db.Exec("INSERT INTO "+table+" VALUES (?, ?, ?)", 1, tt.value, tt.fixed); err != nil {
				t.Fatalf("INSERT: %v", err)
			}

			var gotValue, gotFixed string
			if err := db.QueryRow("SELECT V_VARCHAR, V_CHAR FROM "+table+" WHERE ID=1").Scan(&gotValue, &gotFixed); err != nil {
				t.Fatalf("SELECT: %v", err)
			}
			if !utf8.ValidString(gotValue) || !utf8.ValidString(gotFixed) {
				t.Fatalf("got invalid UTF-8: varchar=% x char=% x", gotValue, gotFixed)
			}
			if gotValue != tt.value {
				t.Fatalf("varchar = %q, want %q", gotValue, tt.value)
			}
			if gotFixed != tt.fixed {
				t.Fatalf("char = %q, want %q", gotFixed, tt.fixed)
			}
		})
	}
}

func TestOctetsCharsetRoundTrip(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_OCTETS")
	_, err := db.Exec(`
		CREATE TABLE TEST_OCTETS (
			ID INTEGER NOT NULL PRIMARY KEY,
			V_VARCHAR VARCHAR(8) CHARACTER SET OCTETS,
			V_CHAR CHAR(4) CHARACTER SET OCTETS
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_OCTETS")

	wantVarchar := []byte{0x00, 0x41, 0xff, 0x20, 0x42}
	wantChar := []byte{0x41, 0x20, 0x00, 0xff}
	if _, err := db.Exec("INSERT INTO TEST_OCTETS VALUES (?, ?, ?)", 1, wantVarchar, wantChar); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rows, err := db.Query("SELECT V_VARCHAR, V_CHAR FROM TEST_OCTETS WHERE ID=1")
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer rows.Close()

	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("ColumnTypes: %v", err)
	}
	for i, ct := range columnTypes {
		if got, want := ct.ScanType(), reflect.TypeOf([]byte{}); got != want {
			t.Fatalf("column %d ScanType = %v, want %v", i, got, want)
		}
	}

	if !rows.Next() {
		t.Fatal("expected one row")
	}
	var gotVarchar, gotChar []byte
	if err := rows.Scan(&gotVarchar, &gotChar); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !reflect.DeepEqual(gotVarchar, wantVarchar) {
		t.Fatalf("varchar = % x, want % x", gotVarchar, wantVarchar)
	}
	if !reflect.DeepEqual(gotChar, wantChar) {
		t.Fatalf("char = % x, want % x", gotChar, wantChar)
	}
	if rows.Next() {
		t.Fatal("expected one row only")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
}

func TestTextBlobWIN1251ConnectionCharsetRoundTrip(t *testing.T) {
	db, err := sqlOpenTestDBWithParam("charset", "WIN1251")
	if err != nil {
		t.Fatalf("open WIN1251 DB: %v", err)
	}
	defer db.Close()

	db.Exec("DROP TABLE TEST_BLOB_WIN1251")
	_, err = db.Exec(`
		CREATE TABLE TEST_BLOB_WIN1251 (
			ID INTEGER NOT NULL PRIMARY KEY,
			V_BLOB BLOB SUB_TYPE TEXT
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_WIN1251")

	want := "\u041f\u0440\u0438\u0432\u0435\u0442"
	if _, err := db.Exec("INSERT INTO TEST_BLOB_WIN1251 VALUES (?, ?)", 1, want); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var got string
	if err := db.QueryRow("SELECT V_BLOB FROM TEST_BLOB_WIN1251 WHERE ID=1").Scan(&got); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("got invalid UTF-8: % x", got)
	}
	if got != want {
		t.Fatalf("blob = %q, want %q", got, want)
	}

	var raw []byte
	if err := db.QueryRow("SELECT CAST(V_BLOB AS BLOB SUB_TYPE 0) FROM TEST_BLOB_WIN1251 WHERE ID=1").Scan(&raw); err != nil {
		t.Fatalf("SELECT raw blob: %v", err)
	}
	wantRaw := []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2}
	if !bytes.Equal(raw, wantRaw) {
		t.Fatalf("raw blob = % x, want WIN1251 bytes % x", raw, wantRaw)
	}
}

func sqlOpenTestDBWithParam(key, value string) (*sql.DB, error) {
	dsn := testDSN
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("firebird", dsn+sep+key+"="+value)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
