package wire

// Fuzzers de inputs controlados por el servidor: un servidor malicioso o
// corrupto no debe poder hacer panic al cliente.

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func FuzzParseServerKeys(f *testing.F) {
	// Buffers reales capturados de FB4/FB5
	fb4, _ := hex.DecodeString("000953796d6d6574726963011443686143686136342043686143686120417263340311436861436861363400f063f90e0014dedd03174368614368610056953abdc0d7037534a8a94800000000")
	f.Add(fb4)
	f.Add([]byte{})
	f.Add([]byte{0x03})
	f.Add([]byte{0x01, 0xFF})
	f.Add([]byte{0x03, 0x08, 'C', 'h', 'a', 'C', 'h', 'a', 0, 1})

	f.Fuzz(func(t *testing.T, data []byte) {
		plugins, iv := parseServerKeys(data)
		if iv != nil && len(iv) != 12 && len(iv) != 16 {
			t.Fatalf("IV con longitud inválida: %d", len(iv))
		}
		_ = plugins
	})
}

func FuzzReadStatusVector(f *testing.F) {
	// Vector de éxito (fast path)
	f.Add([]byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0})
	// Error con string arg: gds + code + string + end
	f.Add([]byte{
		0, 0, 0, 1, 0x14, 0x00, 0x00, 0x01, // isc_arg_gds + código
		0, 0, 0, 2, 0, 0, 0, 3, 'a', 'b', 'c', 0, // isc_arg_string + len + data + pad
		0, 0, 0, 0, // isc_arg_end
	})
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 5, 0xFF, 0xFF, 0xFF, 0xFF})

	f.Fuzz(func(t *testing.T, data []byte) {
		r := NewReader(bytes.NewReader(data))
		sv := r.readStatusVector()
		// Formatear no debe hacer panic tampoco (usa la tabla de mensajes).
		_ = sv.Error()
		if sv.HasError() {
			e := &StatusError{SV: sv}
			_ = e.Error()
			_ = e.GDSCode()
		}
	})
}

func FuzzParseSQLDescribeChunk(f *testing.F) {
	// Continuación truncada a mitad de descriptor
	f.Add([]byte{
		IscInfoSQLSelect,
		IscInfoSQLDescribeVars, 2, 0, 10, 0,
		IscInfoSQLSQLDASeq, 2, 0, 1, 0,
		IscInfoSQLType, 2, 0, 0x80, 0x01,
		IscInfoTruncated,
	})
	f.Add([]byte{IscInfoTruncated})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		var st describeState
		// Dos chunks seguidos: el estado debe sobrevivir buffers arbitrarios.
		_ = parseSQLDescribeChunk(data, &st)
		_ = parseSQLDescribeChunk(data, &st)
	})
}
