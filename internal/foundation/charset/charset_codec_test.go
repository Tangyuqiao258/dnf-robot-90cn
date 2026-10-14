package charset

import (
	"testing"

	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

// TestDecodePVFBytesPrefersGBK pins the one thing that decides which characters
// a PVF name comes out as: the archive is the Chinese mainland client's, so its
// bytes are GBK, and a name whose bytes also decode as Big5 must not be
// reinterpreted. Most GBK byte pairs are valid Big5 pairs too, and the two
// decodes score identically under readableScore, so only the codec order
// separates them -- with Big5 first, 克隆装扮帽子 came back as 親癒蚾啁簽赽.
// The bytes below are what the archive holds for a 克隆装扮 record, checked
// against the live Script.pvf.
func TestDecodePVFBytesPrefersGBK(t *testing.T) {
	const name = "克隆装扮帽子 (鬼剑士)"
	raw, err := EncodeGBKString(name)
	if err != nil {
		t.Fatal(err)
	}
	big5, _, err := transform.String(traditionalchinese.Big5.NewDecoder(), string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if readableScore(big5) != readableScore(name) {
		t.Fatalf("the fixture no longer ties: big5 score=%d gbk score=%d, so it no longer pins the order",
			readableScore(big5), readableScore(name))
	}
	if got := DecodePVFBytes(raw); got != name {
		t.Fatalf("DecodePVFBytes(% x) = %q, want %q", raw, got, name)
	}
}
