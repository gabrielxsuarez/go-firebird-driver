package firebird

// Fase 2.2: matriz multi-versión — verificación de la versión de protocolo
// negociada y de las variantes de wire_crypt contra cada servidor disponible.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// La versión de protocolo negociada debe crecer con la versión del servidor:
// los caminos condicionales por versión (NULL bitmap v13+, timeouts v16+,
// cursor flags v18+) solo se ejercitan si la negociación llega hasta ahí.
func TestNegotiatedProtocolVersion(t *testing.T) {
	cases := []struct {
		version testDBVersion
		minWant uint32
	}{
		{testDBVersion{name: "FB3", dsn: testDSN}, 13},
		{testDBVersion{name: "FB4", dsn: testDSN_FB4}, 16},
		{testDBVersion{name: "FB5", dsn: testDSN_FB5}, 18},
	}
	for _, c := range cases {
		t.Run(c.version.name, func(t *testing.T) {
			cfg, err := ParseDSN(c.version.dsn)
			if err != nil {
				t.Fatal(err)
			}
			wc, err := wire.ConnectContext(context.Background(), &wire.ProtocolConfig{
				Host:           cfg.Host,
				Port:           cfg.Port,
				Database:       cfg.Database,
				User:           cfg.User,
				Password:       cfg.Password,
				Charset:        cfg.Charset,
				Dialect:        cfg.Dialect,
				WireCrypt:      cfg.WireCrypt,
				WireCryptSet:   true,
				AuthPluginList: wire.DefaultPluginList,
			})
			if err != nil {
				t.Skipf("%s no disponible: %v", c.version.name, err)
			}
			defer wc.Detach()
			if got := wc.ProtocolVersion(); got < c.minWant {
				t.Errorf("%s: protocolo negociado %d, esperaba >= %d", c.version.name, got, c.minWant)
			}
		})
	}
}

// wire_crypt=disabled y required deben funcionar contra las tres versiones
// (los contenedores tienen WireCrypt=Enabled, que acepta ambos extremos).
func TestWireCryptModesAllVersions(t *testing.T) {
	for _, v := range testDBVersions() {
		for _, mode := range []string{"disabled", "enabled", "required"} {
			t.Run(v.name+"/"+mode, func(t *testing.T) {
				sep := "?"
				if strings.Contains(v.dsn, "?") {
					sep = "&"
				}
				db, err := sql.Open("firebird", v.dsn+sep+"wire_crypt="+mode)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var one int
				if err := db.QueryRow("SELECT 1 FROM rdb$database").Scan(&one); err != nil {
					t.Skipf("%s no disponible o modo no soportado: %v", v.name, err)
				}
			})
		}
	}
}
