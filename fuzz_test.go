package firebird

import "testing"

func FuzzParseDSN(f *testing.F) {
	// Seed corpus
	f.Add("firebird://sysdba:masterkey@localhost:3050/test.fdb")
	f.Add("sysdba:masterkey@localhost/test.fdb")
	f.Add("firebird://user:pass@host:1234/C:\\data\\db.fdb?charset=UTF8&dialect=3")
	f.Add("firebird://u:p@h/db?wire_crypt=disabled&fetch_size=500&role=ADMIN")
	f.Add("")
	f.Add("://")
	f.Add("firebird://")
	f.Add("firebird://:@:/")

	f.Fuzz(func(t *testing.T, dsn string) {
		// Must not panic on any input.
		cfg, err := ParseDSN(dsn)
		if err != nil {
			return
		}
		if cfg == nil {
			t.Fatal("nil config with nil error")
		}
	})
}
