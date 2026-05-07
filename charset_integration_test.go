package firebird

import (
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
