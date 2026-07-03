//go:build nak

// Variante nakagami/firebirdsql: compilar/correr con -tags nak.
package compare

import (
	_ "github.com/nakagami/firebirdsql"
)

const (
	driverName  = "firebirdsql"
	driverLabel = "nakagami/firebirdsql"
)

func dsn(target string) string {
	return "sysdba:masterkey@" + target + "?charset=UTF8"
}
