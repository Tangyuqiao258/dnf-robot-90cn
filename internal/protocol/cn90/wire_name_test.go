package cn90

import "testing"

func TestDecodeWireNamePrefersGBKOverBig5(t *testing.T) {
	// "幽城旅客" in GBK. The same bytes also decode as Big5 ("蚅傑藏諦"),
	// which must never be used for 90CN online names.
	raw := []byte{0xD3, 0xC4, 0xB3, 0xC7, 0xC2, 0xC3, 0xBF, 0xCD}
	if got := DecodeWireName(raw); got != "幽城旅客" {
		t.Fatalf("decoded=%q", got)
	}
}

func TestDecodeWireNameAcceptsLegacyUTF8AndASCII(t *testing.T) {
	if got := DecodeWireName([]byte("幽城旅客")); got != "幽城旅客" {
		t.Fatalf("legacy utf8 decoded=%q", got)
	}
	if got := DecodeWireName([]byte("robot17000050\x00\x00")); got != "robot17000050" {
		t.Fatalf("ascii decoded=%q", got)
	}
	if got := DecodeWireName(nil); got != "" {
		t.Fatalf("empty decoded=%q", got)
	}
}
