package wire

import (
	"encoding/hex"
	"testing"
)

// Buffers p_acpt_keys reales capturados de contenedores oficiales.
func TestParseServerKeysReal(t *testing.T) {
	cases := []struct {
		name        string
		hexKeys     string
		wantPlugins []string
		wantIVLen   int
	}{
		{
			name:        "FB4",
			hexKeys:     "000953796d6d6574726963011443686143686136342043686143686120417263340311436861436861363400f063f90e0014dedd03174368614368610056953abdc0d7037534a8a94800000000",
			wantPlugins: []string{"ChaCha64", "ChaCha", "Arc4"},
			wantIVLen:   16,
		},
		{
			name:        "FB5",
			hexKeys:     "000953796d6d65747269630114436861436861363420436861436861204172633403114368614368613634009ec53b4dce649f170317436861436861006299b19d53b7f5b1e2286c7100000000",
			wantPlugins: []string{"ChaCha64", "ChaCha", "Arc4"},
			wantIVLen:   16,
		},
		{
			name:        "vacío (FB3)",
			hexKeys:     "",
			wantPlugins: nil,
			wantIVLen:   0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			keys, err := hex.DecodeString(c.hexKeys)
			if err != nil {
				t.Fatal(err)
			}
			plugins, iv := parseServerKeys(keys)
			if len(plugins) != len(c.wantPlugins) {
				t.Fatalf("plugins = %v, want %v", plugins, c.wantPlugins)
			}
			for i := range plugins {
				if plugins[i] != c.wantPlugins[i] {
					t.Errorf("plugin[%d] = %q, want %q", i, plugins[i], c.wantPlugins[i])
				}
			}
			if len(iv) != c.wantIVLen {
				t.Errorf("len(iv) = %d, want %d", len(iv), c.wantIVLen)
			}
		})
	}
}

// El buffer truncado o basura no debe hacer panic ni devolver IV inválido.
func TestParseServerKeysMalformed(t *testing.T) {
	for _, raw := range [][]byte{
		{0x03},
		{0x03, 0xFF, 0x01},
		{0x01, 0x05, 'C', 'h'},
		{0x00, 0x00, 0x01, 0x00, 0x03, 0x02, 'X', 0x00},
	} {
		plugins, iv := parseServerKeys(raw)
		if iv != nil && len(iv) != 12 && len(iv) != 16 {
			t.Errorf("iv inválido para %x: %x", raw, iv)
		}
		_ = plugins
	}
}
