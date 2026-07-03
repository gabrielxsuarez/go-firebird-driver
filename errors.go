package firebird

import "github.com/gabrielxsuarez/go-firebird-driver/wire"

// Error is the type carried by every server-reported error. It is an alias of
// wire.StatusError so callers can inspect Firebird errors without importing
// the wire package:
//
//	var fbErr *firebird.Error
//	if errors.As(err, &fbErr) {
//		log.Println(fbErr.GDSCode(), fbErr.SV.Errors[0].SQLState)
//	}
type Error = wire.StatusError
