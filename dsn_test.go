package firebird

import (
	"testing"

	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

func TestParseDSNDatabasePathCompatibility(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		database string
	}{
		{
			name:     "database alias",
			dsn:      "user:password@localhost/dbname",
			database: "dbname",
		},
		{
			name:     "nakagami style unix absolute path",
			dsn:      "user:password@localhost/dir/dbname",
			database: "/dir/dbname",
		},
		{
			name:     "double slash unix absolute path",
			dsn:      "firebird://user:password@localhost:3050//var/lib/firebird/data/db.fdb",
			database: "/var/lib/firebird/data/db.fdb",
		},
		{
			name:     "windows drive path with slash separators",
			dsn:      "user:password@localhost/c:/fbdata/database.fdb",
			database: "c:/fbdata/database.fdb",
		},
		{
			name:     "windows drive path with backslash separators",
			dsn:      `user:password@localhost/c:\fbdata\database.fdb`,
			database: `c:\fbdata\database.fdb`,
		},
		{
			name:     "scheme with nakagami style unix absolute path",
			dsn:      "firebird://user:password@localhost:3050/alfabeta/firebird3/data/prospectosCL.fdb",
			database: "/alfabeta/firebird3/data/prospectosCL.fdb",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseDSN(tt.dsn)
			if err != nil {
				t.Fatalf("ParseDSN() error = %v", err)
			}
			if cfg.Database != tt.database {
				t.Fatalf("Database = %q, want %q", cfg.Database, tt.database)
			}
		})
	}
}

func TestParseDSNDefaultsAndParams(t *testing.T) {
	cfg, err := ParseDSN("user:password@localhost/dbname?charset=WIN1251&dialect=1&role=ADMIN&wire_crypt=disabled&fetch_size=500&data_type_bind=TIME+ZONE+TO+EXTENDED&session_time_zone=America%2FNew_York")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}

	if cfg.Host != "localhost" {
		t.Fatalf("Host = %q, want localhost", cfg.Host)
	}
	if cfg.Port != "3050" {
		t.Fatalf("Port = %q, want 3050", cfg.Port)
	}
	if cfg.User != "user" || cfg.Password != "password" {
		t.Fatalf("credentials = %q/%q, want user/password", cfg.User, cfg.Password)
	}
	if cfg.Charset != "WIN1251" {
		t.Fatalf("Charset = %q, want WIN1251", cfg.Charset)
	}
	if cfg.Dialect != 1 {
		t.Fatalf("Dialect = %d, want 1", cfg.Dialect)
	}
	if cfg.Role != "ADMIN" {
		t.Fatalf("Role = %q, want ADMIN", cfg.Role)
	}
	if cfg.WireCrypt != wire.WireCryptDisabled {
		t.Fatalf("WireCrypt = %d, want %d", cfg.WireCrypt, wire.WireCryptDisabled)
	}
	if cfg.FetchSize != 500 {
		t.Fatalf("FetchSize = %d, want 500", cfg.FetchSize)
	}
	if cfg.DataTypeBind != "TIME ZONE TO EXTENDED" {
		t.Fatalf("DataTypeBind = %q, want TIME ZONE TO EXTENDED", cfg.DataTypeBind)
	}
	if cfg.SessionTimeZone != "America/New_York" {
		t.Fatalf("SessionTimeZone = %q, want America/New_York", cfg.SessionTimeZone)
	}
}

func TestParseDSNDataTypeBindAliases(t *testing.T) {
	tests := []string{
		"dataTypeBind",
		"data_type_bind",
		"set_bind",
		"isc_dpb_set_bind",
	}
	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			cfg, err := ParseDSN("user:password@localhost/dbname?" + key + "=timestamp+with+time+zone+to+legacy")
			if err != nil {
				t.Fatalf("ParseDSN() error = %v", err)
			}
			if cfg.DataTypeBind != "timestamp with time zone to legacy" {
				t.Fatalf("DataTypeBind = %q", cfg.DataTypeBind)
			}
		})
	}
}

func TestParseDSNSessionTimeZoneAliases(t *testing.T) {
	tests := []string{
		"sessionTimeZone",
		"session_time_zone",
		"isc_dpb_session_time_zone",
		"timezone",
	}
	for _, key := range tests {
		t.Run(key, func(t *testing.T) {
			cfg, err := ParseDSN("user:password@localhost/dbname?" + key + "=Europe%2FMadrid")
			if err != nil {
				t.Fatalf("ParseDSN() error = %v", err)
			}
			if cfg.SessionTimeZone != "Europe/Madrid" {
				t.Fatalf("SessionTimeZone = %q", cfg.SessionTimeZone)
			}
		})
	}
}

func TestParseDSNCanonicalizesKnownCharsetAliases(t *testing.T) {
	cfg, err := ParseDSN("user:password@localhost/dbname?charset=windows-1252")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.Charset != "WIN1252" {
		t.Fatalf("Charset = %q, want WIN1252", cfg.Charset)
	}
}

func TestParseDSNPreservesUnknownCharset(t *testing.T) {
	cfg, err := ParseDSN("user:password@localhost/dbname?charset=CUSTOM_CHARSET")
	if err != nil {
		t.Fatalf("ParseDSN() error = %v", err)
	}
	if cfg.Charset != "CUSTOM_CHARSET" {
		t.Fatalf("Charset = %q, want CUSTOM_CHARSET", cfg.Charset)
	}
}
