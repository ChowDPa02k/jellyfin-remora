package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

func systemdPathValue(value string) string {
	var escaped strings.Builder
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		switch {
		case r == utf8.RuneError && size == 1:
			writeSystemdHexByte(&escaped, value[0])
		case unicode.IsControl(r):
			for i := 0; i < size; i++ {
				writeSystemdHexByte(&escaped, value[i])
			}
		case r == '\\':
			escaped.WriteString(`\x5c`)
		case r == ' ':
			escaped.WriteString(`\x20`)
		case r == '%':
			escaped.WriteString("%%")
		default:
			escaped.WriteString(value[:size])
		}
		value = value[size:]
	}
	return escaped.String()
}

func writeSystemdHexByte(output *strings.Builder, value byte) {
	fmt.Fprintf(output, `\x%02x`, value)
}
