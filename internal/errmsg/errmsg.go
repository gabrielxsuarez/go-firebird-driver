// Package errmsg provides the client-side Firebird error message templates.
//
// Firebird servers send only GDS codes and raw arguments over the wire; the
// human-readable text lives in a client-side table (firebird.msg). This
// package embeds that table, generated from the Firebird source tree.
package errmsg

//go:generate go run ./gen

import (
	"fmt"
	"strconv"
	"strings"
)

// Render returns the human-readable message for a GDS code, substituting the
// @1..@n placeholders with the status-vector arguments. Returns "" when the
// code has no known template; unused arguments are ignored and missing ones
// leave the placeholder in place (mirrors fb_interpret's behavior closely
// enough for diagnostics).
func Render(code int32, params []any) string {
	tmpl := Template(code)
	if tmpl == "" {
		return ""
	}
	if !strings.ContainsRune(tmpl, '@') {
		return tmpl
	}
	var b strings.Builder
	b.Grow(len(tmpl) + 16)
	for i := 0; i < len(tmpl); i++ {
		c := tmpl[i]
		if c != '@' || i+1 >= len(tmpl) {
			b.WriteByte(c)
			continue
		}
		n := tmpl[i+1]
		if n < '1' || n > '9' {
			b.WriteByte(c)
			continue
		}
		idx := int(n - '1')
		if idx < len(params) {
			b.WriteString(paramString(params[idx]))
		} else {
			b.WriteByte('@')
			b.WriteByte(n)
		}
		i++
	}
	return b.String()
}

func paramString(p any) string {
	switch v := p.(type) {
	case string:
		return v
	case int32:
		return strconv.FormatInt(int64(v), 10)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprint(v)
	}
}
