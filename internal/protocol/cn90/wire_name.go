package cn90

import (
	"bytes"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// DecodeWireName decodes an online game-server character name. The server stores
// names and protocol strings as GBK (code page 936); legacy databases may still
// carry UTF-8 wire bytes, which are detected by strict UTF-8 validity. Unlike
// charset.DecodePVFBytes this never falls back to Big5, because a GBK name whose
// bytes also decode as Big5 text must not be reinterpreted into different
// characters (which would then not resolve against the database).
func DecodeWireName(raw []byte) string {
	trimmed := bytes.TrimRight(raw, "\x00")
	if len(trimmed) == 0 {
		return ""
	}
	if isASCIIBytes(trimmed) {
		return string(trimmed)
	}
	if utf8.Valid(trimmed) {
		return string(trimmed)
	}
	decoded, _, err := transform.String(simplifiedchinese.GBK.NewDecoder(), string(trimmed))
	if err != nil {
		return string(trimmed)
	}
	return decoded
}

func isASCIIBytes(raw []byte) bool {
	for _, value := range raw {
		if value > 0x7F {
			return false
		}
	}
	return true
}
