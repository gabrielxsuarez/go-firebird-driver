package errmsg

import "testing"

func TestTemplateKnownCodes(t *testing.T) {
	cases := []struct {
		code int32
		want string
	}{
		{335544321, "arithmetic exception, numeric overflow, or string truncation"},
		{335544351, "unsuccessful metadata update"},
		{335544569, "Dynamic SQL Error"},
		{336397288, "DROP TABLE @1 failed"},
	}
	for _, c := range cases {
		if got := Template(c.code); got != c.want {
			t.Errorf("Template(%d) = %q, want %q", c.code, got, c.want)
		}
	}
	if got := Template(0); got != "" {
		t.Errorf("Template(0) = %q, want empty", got)
	}
	if got := Template(999999999); got != "" {
		t.Errorf("código desconocido: %q, want empty", got)
	}
}

func TestRenderSubstitution(t *testing.T) {
	// 336397288 = "DROP TABLE @1 failed"
	if got := Render(336397288, []any{"CLIENTES"}); got != "DROP TABLE CLIENTES failed" {
		t.Errorf("Render = %q", got)
	}
	// 335544436 = "SQL error code = @1" con parámetro numérico
	if got := Render(335544436, []any{int32(-607)}); got != "SQL error code = -607" {
		t.Errorf("Render numérico = %q", got)
	}
	// parámetro faltante: el placeholder queda visible, no panic
	if got := Render(336397288, nil); got != "DROP TABLE @1 failed" {
		t.Errorf("Render sin params = %q", got)
	}
	// parámetros de más se ignoran
	if got := Render(335544321, []any{"extra"}); got != "arithmetic exception, numeric overflow, or string truncation" {
		t.Errorf("Render params extra = %q", got)
	}
}
