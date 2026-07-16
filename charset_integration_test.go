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
	runCharsetRoundTripMatrix(t, db)
}

func TestCharsetRoundTripMatrixFirebirdVersions(t *testing.T) {
	for _, version := range testDBVersions() {
		t.Run(version.name, func(t *testing.T) {
			db := openVersionedTestDB(t, version)
			runCharsetRoundTripMatrix(t, db)
		})
	}
}

func runCharsetRoundTripMatrix(t *testing.T, db *sql.DB) {
	t.Helper()
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
			name:    "ISO88592",
			charset: "ISO8859_2",
			value:   "Za\u017c\u00f3\u0142\u0107",
			fixed:   "\u0141",
		},
		{
			name:    "WIN1250",
			charset: "WIN1250",
			value:   "Za\u017c\u00f3\u0142\u0107",
			fixed:   "\u0141",
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
		{
			name:    "WIN1257",
			charset: "WIN1257",
			value:   "\u0104\u017duol\u0173",
			fixed:   "\u010c",
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

func TestNoneCharsetPassThrough(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_NONE_CHARSET")
	_, err := db.Exec(`
		CREATE TABLE TEST_NONE_CHARSET (
			ID INTEGER NOT NULL PRIMARY KEY,
			V_TEXT VARCHAR(20) CHARACTER SET NONE,
			V_RAW VARCHAR(8) CHARACTER SET NONE
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_NONE_CHARSET")

	wantText := "utf8 \u20ac"
	wantRaw := []byte{0xff, 0x00, 0x41, 0x20}
	if _, err := db.Exec("INSERT INTO TEST_NONE_CHARSET VALUES (?, ?, ?)", 1, wantText, wantRaw); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var gotText string
	var gotRaw []byte
	if err := db.QueryRow("SELECT V_TEXT, V_RAW FROM TEST_NONE_CHARSET WHERE ID=1").Scan(&gotText, &gotRaw); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if gotText != wantText {
		t.Fatalf("text = %q, want %q", gotText, wantText)
	}
	if !bytes.Equal(gotRaw, wantRaw) {
		t.Fatalf("raw = % x, want % x", gotRaw, wantRaw)
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

func TestTextBlobExplicitColumnCharsetRoundTrip(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_BLOB_COL_CH")
	_, err := db.Exec(`
		CREATE TABLE TEST_BLOB_COL_CH (
			ID INTEGER NOT NULL PRIMARY KEY,
			V_WIN1251 BLOB SUB_TYPE TEXT CHARACTER SET WIN1251,
			V_WIN1252 BLOB SUB_TYPE TEXT CHARACTER SET WIN1252
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_BLOB_COL_CH")

	wantWin1251 := "\u041f\u0440\u0438\u0432\u0435\u0442"
	wantWin1252 := "precio 10\u20ac"
	if _, err := db.Exec("INSERT INTO TEST_BLOB_COL_CH VALUES (?, ?, ?)", 1, wantWin1251, wantWin1252); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	var gotWin1251, gotWin1252 string
	if err := db.QueryRow("SELECT V_WIN1251, V_WIN1252 FROM TEST_BLOB_COL_CH WHERE ID=1").Scan(&gotWin1251, &gotWin1252); err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	if !utf8.ValidString(gotWin1251) || !utf8.ValidString(gotWin1252) {
		t.Fatalf("got invalid UTF-8: win1251=% x win1252=% x", gotWin1251, gotWin1252)
	}
	if gotWin1251 != wantWin1251 {
		t.Fatalf("win1251 blob = %q, want %q", gotWin1251, wantWin1251)
	}
	if gotWin1252 != wantWin1252 {
		t.Fatalf("win1252 blob = %q, want %q", gotWin1252, wantWin1252)
	}
}

func TestTextBlobExplicitColumnCharsetRawBytes(t *testing.T) {
	tests := []struct {
		name    string
		charset string
		value   string
		raw     []byte
	}{
		{
			name:    "WIN1251",
			charset: "WIN1251",
			value:   "\u041f\u0440\u0438\u0432\u0435\u0442",
			raw:     []byte{0xcf, 0xf0, 0xe8, 0xe2, 0xe5, 0xf2},
		},
		{
			name:    "WIN1252",
			charset: "WIN1252",
			value:   "precio 10\u20ac",
			raw:     []byte{'p', 'r', 'e', 'c', 'i', 'o', ' ', '1', '0', 0x80},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := sqlOpenTestDBWithParam("charset", tt.charset)
			if err != nil {
				t.Fatalf("open %s DB: %v", tt.charset, err)
			}
			defer db.Close()

			table := "TEST_BLOB_RAW_" + tt.name
			db.Exec("DROP TABLE " + table)
			_, err = db.Exec(fmt.Sprintf(`
				CREATE TABLE %s (
					ID INTEGER NOT NULL PRIMARY KEY,
					V_BLOB BLOB SUB_TYPE TEXT CHARACTER SET %s
				)`, table, tt.charset))
			if err != nil {
				t.Fatalf("CREATE: %v", err)
			}
			defer db.Exec("DROP TABLE " + table)

			if _, err := db.Exec("INSERT INTO "+table+" VALUES (?, ?)", 1, tt.value); err != nil {
				t.Fatalf("INSERT: %v", err)
			}

			var got string
			if err := db.QueryRow("SELECT V_BLOB FROM " + table + " WHERE ID=1").Scan(&got); err != nil {
				t.Fatalf("SELECT: %v", err)
			}
			if got != tt.value {
				t.Fatalf("blob = %q, want %q", got, tt.value)
			}

			var raw []byte
			if err := db.QueryRow("SELECT CAST(V_BLOB AS BLOB SUB_TYPE 0) FROM " + table + " WHERE ID=1").Scan(&raw); err != nil {
				t.Fatalf("SELECT raw blob: %v", err)
			}
			if !bytes.Equal(raw, tt.raw) {
				t.Fatalf("raw blob = % x, want % x", raw, tt.raw)
			}
		})
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

// TestNoneCharsetParam cubre el parámetro DSN none_charset: el charset con el
// que se interpretan las columnas declaradas CHARACTER SET NONE. La columna
// guarda 0xD1 (Ñ en Latin-1), que no es UTF-8 válido: es justo el byte que
// distingue una interpretación de otra.
func TestNoneCharsetParam(t *testing.T) {
	db := openTestDB(t)
	db.Exec("DROP TABLE TEST_NONE_CS")
	_, err := db.Exec(`
		CREATE TABLE TEST_NONE_CS (
			ID INTEGER NOT NULL PRIMARY KEY,
			V VARCHAR(32) CHARACTER SET NONE
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	defer db.Exec("DROP TABLE TEST_NONE_CS")

	// Bytes crudos Latin-1: "AÑO".
	raw := []byte{0x41, 0xD1, 0x4F}
	if _, err := db.Exec("INSERT INTO TEST_NONE_CS VALUES (?, ?)", 1, raw); err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	t.Run("explicito decodifica con ese charset", func(t *testing.T) {
		db, err := sqlOpenTestDBWithParam("none_charset", "ISO8859_1")
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()

		var got string
		if err := db.QueryRow("SELECT V FROM TEST_NONE_CS WHERE ID=1").Scan(&got); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		if got != "AÑO" {
			t.Fatalf("V = %q, want %q", got, "AÑO")
		}
		if !utf8.ValidString(got) {
			t.Fatalf("V = % x, no es UTF-8 válido", got)
		}
	})

	t.Run("explicito codifica los parametros con ese charset", func(t *testing.T) {
		db, err := sqlOpenTestDBWithParam("none_charset", "ISO8859_1")
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()

		// El string UTF-8 de Go tiene que viajar como Latin-1 para matchear los
		// bytes guardados: si no, lectura y escritura serían asimétricas.
		var n int
		if err := db.QueryRow("SELECT count(*) FROM TEST_NONE_CS WHERE V = ?", "AÑO").Scan(&n); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		if n != 1 {
			t.Fatalf("filas = %d, want 1 (el parámetro no se codificó con none_charset)", n)
		}
	})

	t.Run("NONE explicito devuelve bytes crudos", func(t *testing.T) {
		db, err := sqlOpenTestDBWithParam("none_charset", "NONE")
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()

		rows, err := db.Query("SELECT V FROM TEST_NONE_CS WHERE ID=1")
		if err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		defer rows.Close()

		// Sin charset con qué interpretarla, la columna se decodifica como
		// bytes crudos: el ScanType tiene que decir lo mismo que el decode.
		columnTypes, err := rows.ColumnTypes()
		if err != nil {
			t.Fatalf("ColumnTypes: %v", err)
		}
		if got, want := columnTypes[0].ScanType(), reflect.TypeOf([]byte{}); got != want {
			t.Fatalf("ScanType = %v, want %v", got, want)
		}

		if !rows.Next() {
			t.Fatal("se esperaba una fila")
		}
		var got []byte
		if err := rows.Scan(&got); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if !bytes.Equal(got, raw) {
			t.Fatalf("V = % x, want % x", got, raw)
		}
	})

	t.Run("default: reinterpreta con el charset de la conexion", func(t *testing.T) {
		// Conexión UTF8 (default): none_charset vale lo mismo, así que la
		// columna se decodifica como UTF8. Decodificar UTF8 es pass-through, o
		// sea que devuelve los bytes intactos dentro de un string (no es UTF-8
		// válido, pero es la semántica de nakagami).
		//
		// Lo que se prueba acá es el TIPO NATIVO: escanear a []byte pasaría
		// tanto con string como con []byte y no probaría nada.
		var got any
		if err := db.QueryRow("SELECT V FROM TEST_NONE_CS WHERE ID=1").Scan(&got); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		s, ok := got.(string)
		if !ok {
			t.Fatalf("V = %T, want string (el default debe reinterpretar la columna con el charset de la conexión)", got)
		}
		// Y que el servidor no transliteró: el BLR sigue pidiendo NONE, así que
		// el fetch no aborta con "Malformed string" y los bytes llegan crudos.
		if !bytes.Equal([]byte(s), raw) {
			t.Fatalf("V = % x, want % x", s, raw)
		}
	})

	t.Run("ColumnTypeLength reporta el largo declarado, no el reinterpretado", func(t *testing.T) {
		// V es VARCHAR(32) CHARACTER SET NONE. Con el default (UTF8),
		// none_charset la reinterpreta, pero el largo declarado en la base no
		// cambia: NONE es un byte por carácter, así que son 32 caracteres.
		// Dividir por los bytes del charset reinterpretado daría 8.
		rows, err := db.Query("SELECT V FROM TEST_NONE_CS WHERE ID=1")
		if err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		defer rows.Close()

		columnTypes, err := rows.ColumnTypes()
		if err != nil {
			t.Fatalf("ColumnTypes: %v", err)
		}
		got, ok := columnTypes[0].Length()
		if !ok {
			t.Fatal("Length() no reportó largo para VARCHAR")
		}
		if got != 32 {
			t.Fatalf("Length = %d, want 32", got)
		}
	})
}

// TestCollatedCharsetRoundTrip cubre D3: una columna con COLLATE lleva la
// collation en el byte alto del ttype que reporta el describe. Decode/Encode
// deben usar solo el byte bajo (el charset): sin la máscara, la columna
// colacionada no matchea ningún charset conocido y cae al passthrough — la
// lectura entrega bytes crudos dentro del string y el parámetro se escribe
// como UTF-8 crudo en la base.
//
// El caso solo se dispara cuando el descriptor conserva el ttype original de
// la columna (conexión ISO8859_1 o NONE); con conexión UTF8 el servidor
// translitera y describe con el charset de la conexión, sin collation.
func TestCollatedCharsetRoundTrip(t *testing.T) {
	admin := openTestDB(t)
	admin.Exec("DROP TABLE TEST_COLLATED")
	_, err := admin.Exec(`
		CREATE TABLE TEST_COLLATED (
			ID INTEGER NOT NULL PRIMARY KEY,
			V_VARCHAR VARCHAR(50) CHARACTER SET ISO8859_1 COLLATE ES_ES,
			V_CHAR CHAR(12) CHARACTER SET ISO8859_1 COLLATE ES_ES
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	t.Cleanup(func() { admin.Exec("DROP TABLE TEST_COLLATED") })

	const want = "Ñandú ácido"
	const wantChar = "Ñá"
	// los mismos textos en Latin-1, como deben quedar guardados
	rawVarchar := []byte("\xd1and\xfa \xe1cido")
	rawChar := []byte("\xd1\xe1")

	iso, err := sqlOpenTestDBWithParam("charset", "ISO8859_1")
	if err != nil {
		t.Fatalf("open ISO8859_1: %v", err)
	}
	defer iso.Close()

	t.Run("escritura: el parametro se encodea a Latin-1", func(t *testing.T) {
		if _, err := iso.Exec("INSERT INTO TEST_COLLATED VALUES (?, ?, ?)", 1, want, wantChar); err != nil {
			t.Fatalf("INSERT: %v", err)
		}

		// Bytes reales en la base: CAST a OCTETS (byte a byte en el server).
		// Una conexión charset=NONE ya no sirve de testigo: con el fix también
		// decodifica la columna por su charset, y escanear ese string a []byte
		// devolvería el UTF-8 del valor decodificado, no lo guardado.
		var gotVarchar, gotChar []byte
		err := admin.QueryRow(`SELECT
				CAST(V_VARCHAR AS VARCHAR(50) CHARACTER SET OCTETS),
				CAST(V_CHAR AS CHAR(12) CHARACTER SET OCTETS)
			FROM TEST_COLLATED WHERE ID=1`).Scan(&gotVarchar, &gotChar)
		if err != nil {
			t.Fatalf("SELECT crudo: %v", err)
		}
		if !bytes.Equal(gotVarchar, rawVarchar) {
			t.Fatalf("V_VARCHAR en base = % x, want % x (Latin-1)", gotVarchar, rawVarchar)
		}
		// CHAR vía OCTETS: el padding puede llegar como espacios o como 0x00
		if !bytes.Equal(bytes.TrimRight(gotChar, " \x00"), rawChar) {
			t.Fatalf("V_CHAR en base = % x, want % x (Latin-1)", gotChar, rawChar)
		}
	})

	t.Run("lectura: conexion ISO8859_1 decodifica", func(t *testing.T) {
		var gotVarchar, gotChar string
		if err := iso.QueryRow("SELECT V_VARCHAR, V_CHAR FROM TEST_COLLATED WHERE ID=1").Scan(&gotVarchar, &gotChar); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		if !utf8.ValidString(gotVarchar) {
			t.Fatalf("V_VARCHAR no es UTF-8 válido: % x", gotVarchar)
		}
		if gotVarchar != want {
			t.Fatalf("V_VARCHAR = %q, want %q", gotVarchar, want)
		}
		if got := strings.TrimRight(gotChar, " "); got != wantChar {
			t.Fatalf("V_CHAR = %q, want %q", got, wantChar)
		}
	})

	t.Run("lectura: conexion NONE decodifica por charset de columna", func(t *testing.T) {
		// lc_ctype=NONE no translitera y el descriptor conserva el ttype
		// colacionado; el BLR pide el charset de la columna (byte bajo) y el
		// decode tiene que usar ese mismo charset.
		none, err := sqlOpenTestDBWithParam("charset", "NONE")
		if err != nil {
			t.Fatalf("open NONE: %v", err)
		}
		defer none.Close()

		var got string
		if err := none.QueryRow("SELECT V_VARCHAR FROM TEST_COLLATED WHERE ID=1").Scan(&got); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		if got != want {
			t.Fatalf("V_VARCHAR = %q, want %q", got, want)
		}
	})

	t.Run("lectura: conexion UTF8 sigue bien (server translitera)", func(t *testing.T) {
		var got string
		if err := admin.QueryRow("SELECT V_VARCHAR FROM TEST_COLLATED WHERE ID=1").Scan(&got); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		if got != want {
			t.Fatalf("V_VARCHAR = %q, want %q", got, want)
		}
	})
}

// sqlOpenTestDBWithQuery abre la base de test con un query string completo
// (para DSNs con más de un parámetro, p.ej. charset=NONE&none_charset=...).
func sqlOpenTestDBWithQuery(query string) (*sql.DB, error) {
	dsn := testDSN
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	db, err := sql.Open("firebird", dsn+sep+query)
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

// TestBlobTextCharsetRoundTrip cubre D3b: los blobs de texto se decodificaban
// y encodeaban con el charset de CONEXIÓN. El describe reporta el charset
// efectivo del blob en sqlscale (desc.Scale) y coincide siempre con los bytes
// que viajan: transliterados a lc_ctype si la conexión tiene charset, o
// crudos en el charset declarado de la columna con charset=NONE. Con
// charset=NONE un blob ISO8859_1 llegaba como string UTF-8 inválido y un
// parámetro string se escribía como UTF-8 crudo en la base.
//
// Scale=0 (blob CHARACTER SET NONE real, sin transliteración posible) sigue
// la reinterpretación none_charset, como CHAR/VARCHAR NONE.
func TestBlobTextCharsetRoundTrip(t *testing.T) {
	admin := openTestDB(t) // UTF8 default
	admin.Exec("DROP TABLE TEST_BLOB_CS")
	_, err := admin.Exec(`
		CREATE TABLE TEST_BLOB_CS (
			ID INTEGER NOT NULL PRIMARY KEY,
			B_ISO BLOB SUB_TYPE TEXT CHARACTER SET ISO8859_1,
			B_UTF BLOB SUB_TYPE TEXT CHARACTER SET UTF8,
			B_NONE BLOB SUB_TYPE TEXT CHARACTER SET NONE
		)`)
	if err != nil {
		t.Fatalf("CREATE: %v", err)
	}
	t.Cleanup(func() { admin.Exec("DROP TABLE TEST_BLOB_CS") })

	const want = "Ñandú ácido"
	rawISO := []byte("\xd1and\xfa \xe1cido") // 11 bytes en Latin-1
	const lenISO, lenUTF = 11, 14            // "Ñandú ácido" en Latin-1 / UTF-8

	// Escritura: el parámetro string se encodea al charset efectivo del blob,
	// venga la conexión que venga. Testigo: OCTET_LENGTH en el server.
	for _, cs := range []string{"UTF8", "ISO8859_1", "NONE"} {
		t.Run("escritura via conexion "+cs, func(t *testing.T) {
			db, err := sqlOpenTestDBWithQuery("charset=" + cs)
			if err != nil {
				t.Fatalf("open %s: %v", cs, err)
			}
			defer db.Close()

			if _, err := db.Exec("DELETE FROM TEST_BLOB_CS"); err != nil {
				t.Fatalf("DELETE: %v", err)
			}
			if _, err := db.Exec("INSERT INTO TEST_BLOB_CS (ID, B_ISO, B_UTF) VALUES (1, ?, ?)", want, want); err != nil {
				t.Fatalf("INSERT: %v", err)
			}

			var gotISO, gotUTF int
			if err := admin.QueryRow("SELECT OCTET_LENGTH(B_ISO), OCTET_LENGTH(B_UTF) FROM TEST_BLOB_CS WHERE ID=1").Scan(&gotISO, &gotUTF); err != nil {
				t.Fatalf("OCTET_LENGTH: %v", err)
			}
			if gotISO != lenISO || gotUTF != lenUTF {
				t.Fatalf("bytes en base: B_ISO=%d B_UTF=%d, want %d y %d (el parámetro no se encodeó al charset del blob)", gotISO, gotUTF, lenISO, lenUTF)
			}

			var rtISO, rtUTF string
			if err := admin.QueryRow("SELECT B_ISO, B_UTF FROM TEST_BLOB_CS WHERE ID=1").Scan(&rtISO, &rtUTF); err != nil {
				t.Fatalf("SELECT: %v", err)
			}
			if rtISO != want || rtUTF != want {
				t.Fatalf("round-trip: B_ISO=%q B_UTF=%q, want %q", rtISO, rtUTF, want)
			}
		})
	}

	// Lectura: fila canónica escrita vía UTF8; cada conexión tiene que
	// devolver el mismo string. Con charset=NONE los bytes llegan crudos
	// (Latin-1 y UTF-8 respectivamente) y el decode debe ir por columna.
	if _, err := admin.Exec("DELETE FROM TEST_BLOB_CS"); err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if _, err := admin.Exec("INSERT INTO TEST_BLOB_CS (ID, B_ISO, B_UTF) VALUES (1, ?, ?)", want, want); err != nil {
		t.Fatalf("INSERT canónico: %v", err)
	}

	for _, cs := range []string{"UTF8", "ISO8859_1", "NONE"} {
		t.Run("lectura via conexion "+cs, func(t *testing.T) {
			db, err := sqlOpenTestDBWithQuery("charset=" + cs)
			if err != nil {
				t.Fatalf("open %s: %v", cs, err)
			}
			defer db.Close()

			var gotISO, gotUTF string
			if err := db.QueryRow("SELECT B_ISO, B_UTF FROM TEST_BLOB_CS WHERE ID=1").Scan(&gotISO, &gotUTF); err != nil {
				t.Fatalf("SELECT: %v", err)
			}
			if !utf8.ValidString(gotISO) {
				t.Fatalf("B_ISO no es UTF-8 válido: % x", gotISO)
			}
			if gotISO != want || gotUTF != want {
				t.Fatalf("B_ISO=%q B_UTF=%q, want %q", gotISO, gotUTF, want)
			}
		})
	}

	t.Run("blob NONE con none_charset", func(t *testing.T) {
		db, err := sqlOpenTestDBWithQuery("charset=NONE&none_charset=ISO8859_1")
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()

		if _, err := db.Exec("INSERT INTO TEST_BLOB_CS (ID, B_NONE) VALUES (2, ?)", want); err != nil {
			t.Fatalf("INSERT: %v", err)
		}

		// El parámetro se encodeó con none_charset (Latin-1 crudo en la base).
		var n int
		if err := admin.QueryRow("SELECT OCTET_LENGTH(B_NONE) FROM TEST_BLOB_CS WHERE ID=2").Scan(&n); err != nil {
			t.Fatalf("OCTET_LENGTH: %v", err)
		}
		if n != lenISO {
			t.Fatalf("bytes en base = %d, want %d (Latin-1)", n, lenISO)
		}

		// La lectura reinterpreta con none_charset.
		var got string
		if err := db.QueryRow("SELECT B_NONE FROM TEST_BLOB_CS WHERE ID=2").Scan(&got); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		if got != want {
			t.Fatalf("B_NONE = %q, want %q", got, want)
		}
	})

	t.Run("blob NONE sin none_charset: bytes crudos", func(t *testing.T) {
		// none_charset resuelve a NONE: sin charset con qué interpretarlo,
		// el blob de texto se entrega como []byte (mismo trato que las
		// columnas CHAR/VARCHAR NONE — D1).
		db, err := sqlOpenTestDBWithQuery("charset=NONE")
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer db.Close()

		var got any
		if err := db.QueryRow("SELECT B_NONE FROM TEST_BLOB_CS WHERE ID=2").Scan(&got); err != nil {
			t.Fatalf("SELECT: %v", err)
		}
		bs, ok := got.([]byte)
		if !ok {
			t.Fatalf("B_NONE = %T, want []byte", got)
		}
		if !bytes.Equal(bs, rawISO) {
			t.Fatalf("B_NONE = % x, want % x", bs, rawISO)
		}
	})
}
