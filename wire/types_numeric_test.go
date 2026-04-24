package wire

import (
	"database/sql/driver"
	"testing"
)

func TestNumericInt64AplicaEscala(t *testing.T) {
	tests := []struct {
		name  string
		valor any
		scale int32
		want  int64
	}{
		{"string con escala", "14280.050", -3, 14280050},
		{"float64 con escala", 14280.050, -3, 14280050},
		{"int con escala", 52592, -3, 52592000},
		{"redondeo hacia abajo", "10.2574", -3, 10257},
		{"redondeo medio positivo", "10.2575", -3, 10258},
		{"redondeo medio negativo", "-10.2575", -3, -10258},
		{"notacion cientifica", "1.2e-2", -3, 12},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := numericInt64(tt.valor, tt.scale)
			if err != nil {
				t.Fatalf("numericInt64(%v, %d) error = %v", tt.valor, tt.scale, err)
			}
			if got != tt.want {
				t.Fatalf("numericInt64(%v, %d) = %d, want %d", tt.valor, tt.scale, got, tt.want)
			}
		})
	}
}

func TestNumericInt64RechazaValoresInvalidos(t *testing.T) {
	tests := []any{"", "abc", "1.2.3", "9223372036854775.808"}

	for _, valor := range tests {
		t.Run(valor.(string), func(t *testing.T) {
			if got, err := numericInt64(valor, -3); err == nil {
				t.Fatalf("numericInt64(%q, -3) = %d, want error", valor, got)
			}
		})
	}
}

func TestEncodeParamsOptimalErrReportaNumericInvalido(t *testing.T) {
	descs := []ColumnDescriptor{{SQLType: SQLInt64, Scale: -3}}
	values := []driver.NamedValue{{Ordinal: 1, Value: "abc"}}

	var sw StackWriter
	if _, err := EncodeParamsOptimalErr(&sw, descs, values); err == nil {
		t.Fatal("EncodeParamsOptimalErr() error = nil, want conversion error")
	}
}
