package dialect

import (
	"github.com/piprim/pgcrud/internal"
)

func AppendNull(b []byte) []byte {
	return append(b, "NULL"...)
}

//------------------------------------------------------------------------------

func AppendName(b []byte, ident string, quote byte) []byte {
	return appendName(b, internal.Bytes(ident), quote)
}

func appendName(b, ident []byte, quote byte) []byte {
	b = append(b, quote)
	for _, c := range ident {
		if c == quote {
			b = append(b, quote, quote)
		} else {
			b = append(b, c)
		}
	}
	b = append(b, quote)
	return b
}

func AppendIdent(b []byte, name string, quote byte) []byte {
	return appendIdent(b, internal.Bytes(name), quote)
}

func appendIdent(b, name []byte, quote byte) []byte {
	var quoted bool
loop:
	for _, c := range name {
		switch c {
		case '*':
			if !quoted {
				b = append(b, '*')
				continue loop
			}
		case '.':
			if quoted {
				b = append(b, quote)
				quoted = false
			}
			b = append(b, '.')
			continue loop
		}

		if !quoted {
			b = append(b, quote)
			quoted = true
		}
		if c == quote {
			b = append(b, quote, quote)
		} else {
			b = append(b, c)
		}
	}
	if quoted {
		b = append(b, quote)
	}
	return b
}
