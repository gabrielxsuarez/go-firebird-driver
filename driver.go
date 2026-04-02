// Package firebird implements a database/sql/driver for Firebird 3.0+.
//
// The driver communicates using the Firebird wire protocol (v13-v18)
// and is designed for aggressive memory efficiency with zero allocations
// in hot paths. It registers itself under both "firebird" (recommended)
// and "firebirdsql" (compatibility alias for nakagami/firebirdsql users).
//
// Usage:
//
//	import (
//		"database/sql"
//		_ "github.com/gabrielxsuarez/go-firebird-driver"
//	)
//
//	db, err := sql.Open("firebird", "firebird://sysdba:masterkey@localhost:3050/path/to/database.fdb")
package firebird

import (
	"context"
	"database/sql"
	"database/sql/driver"
)

func init() {
	drv := &Driver{}
	registerDriver("firebird", drv)
	registerDriver("firebirdsql", drv)
}

func registerDriver(name string, drv driver.Driver) {
	for _, registered := range sql.Drivers() {
		if registered == name {
			return
		}
	}
	sql.Register(name, drv)
}

// Driver implements driver.Driver and driver.DriverContext.
type Driver struct{}

var _ driver.Driver = (*Driver)(nil)
var _ driver.DriverContext = (*Driver)(nil)

// Open implements driver.Driver. Parses DSN and connects.
func (d *Driver) Open(dsn string) (driver.Conn, error) {
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

// OpenConnector implements driver.DriverContext.
// Parses the DSN once; subsequent Connect calls reuse it.
func (d *Driver) OpenConnector(dsn string) (driver.Connector, error) {
	cfg, err := ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	return &Connector{driver: d, config: cfg}, nil
}

// Connector implements driver.Connector.
type Connector struct {
	driver *Driver
	config *Config
}

var _ driver.Connector = (*Connector)(nil)

// Connect implements driver.Connector.
func (c *Connector) Connect(ctx context.Context) (driver.Conn, error) {
	return newConnection(ctx, c.config)
}

// Driver implements driver.Connector.
func (c *Connector) Driver() driver.Driver {
	return c.driver
}
