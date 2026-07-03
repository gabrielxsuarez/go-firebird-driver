//go:build !nak

// Variante go-firebird-driver: compilar/correr sin tags.
package compare

import (
	_ "github.com/gabrielxsuarez/go-firebird-driver"
)

const (
	driverName  = "firebird"
	driverLabel = "go-firebird-driver"
)

func dsn(target string) string {
	return "firebird://sysdba:masterkey@" + target + "?charset=UTF8"
}
