package firebird

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	fbcharset "github.com/gabrielxsuarez/go-firebird-driver/internal/charset"
	"github.com/gabrielxsuarez/go-firebird-driver/wire"
)

// Config holds parsed DSN parameters.
type Config struct {
	Host            string
	Port            string
	Database        string
	User            string
	Password        string
	Charset         string
	Dialect         uint32
	Role            string
	WireCrypt       uint32
	FetchSize       int
	DataTypeBind    string
	SessionTimeZone string
}

// ParseDSN parses a Firebird DSN string.
// Format: firebird://user:password@host:port/path/to/database?param=value
// Also supports: user:password@host:port/path/to/database?param=value
func ParseDSN(dsn string) (*Config, error) {
	cfg := &Config{
		Port:      "3050",
		Charset:   "UTF8",
		Dialect:   wire.SQLDialectCurrent,
		WireCrypt: wire.WireCryptEnabled,
		FetchSize: 200,
	}

	// Add scheme if missing
	if !strings.Contains(dsn, "://") {
		dsn = "firebird://" + dsn
	}

	u, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("firebird: invalid DSN: %w", err)
	}

	if u.User != nil {
		cfg.User = u.User.Username()
		cfg.Password, _ = u.User.Password()
	}

	if u.Hostname() != "" {
		cfg.Host = u.Hostname()
	} else {
		cfg.Host = "localhost"
	}

	if u.Port() != "" {
		cfg.Port = u.Port()
	}

	cfg.Database = parseDatabasePath(u.Path)
	if cfg.Database == "" {
		return nil, fmt.Errorf("firebird: database path required in DSN")
	}

	// Parse query parameters
	for key, values := range u.Query() {
		if len(values) == 0 {
			continue
		}
		val := values[0]
		switch strings.ToLower(key) {
		case "charset":
			if canonical, ok := fbcharset.CanonicalName(val); ok {
				cfg.Charset = canonical
			} else {
				cfg.Charset = val
			}
		case "dialect":
			d, err := strconv.ParseUint(val, 10, 32)
			if err != nil {
				return nil, fmt.Errorf("firebird: invalid dialect %q: %w", val, err)
			}
			cfg.Dialect = uint32(d)
		case "role":
			cfg.Role = val
		case "data_type_bind", "datatypebind", "set_bind", "isc_dpb_set_bind":
			cfg.DataTypeBind = val
		case "session_time_zone", "sessiontimezone", "isc_dpb_session_time_zone", "timezone":
			cfg.SessionTimeZone = val
		case "wire_crypt":
			switch strings.ToLower(val) {
			case "disabled", "false", "0":
				cfg.WireCrypt = wire.WireCryptDisabled
			case "enabled", "true", "1":
				cfg.WireCrypt = wire.WireCryptEnabled
			case "required":
				cfg.WireCrypt = wire.WireCryptRequired
			}
		case "fetch_size":
			n, err := strconv.Atoi(val)
			if err == nil && n > 0 {
				cfg.FetchSize = n
			}
		}
	}

	return cfg, nil
}

func parseDatabasePath(path string) string {
	if strings.HasPrefix(path, "//") {
		return path[1:]
	}
	if !strings.HasPrefix(path, "/") {
		return path
	}

	withoutLeadingSlash := path[1:]
	if isWindowsDrivePath(withoutLeadingSlash) || !strings.ContainsRune(withoutLeadingSlash, '/') {
		return withoutLeadingSlash
	}
	return path
}

func isWindowsDrivePath(path string) bool {
	return len(path) >= 2 && path[1] == ':'
}
