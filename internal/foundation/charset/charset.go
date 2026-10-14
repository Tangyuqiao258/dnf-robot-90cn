package charset

import (
	"fmt"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
)

func EncodeGBKString(s string) ([]byte, error) {
	encoded, _, err := transform.Bytes(simplifiedchinese.GBK.NewEncoder(), []byte(s))
	if err != nil {
		return nil, fmt.Errorf("encode GBK: %w", err)
	}
	return encoded, nil
}

type textCodec struct {
	decode *encoding.Decoder
}

// pvfCodecs are the codecs a PVF body may be written in, outermost first. The
// installed game is the mainland-China client, so GBK is what its archive holds
// and it comes first for a reason: a GBK name whose bytes are also valid Big5
// text decodes to different Han characters under each codec and scores the same
// under readableScore, so the tie is broken by order. Listing Big5 first
// reinterpreted every such name (克隆装扮帽子 decoded as 親癒蚾啁簽赽), which
// silently defeated the catalog's name rules. Big5 stays as a last resort for
// the Japanese-lineage scripts this robot was adapted from.
var pvfCodecs = []textCodec{
	{decode: simplifiedchinese.GBK.NewDecoder()},
	{decode: traditionalchinese.Big5.NewDecoder()},
}

func DecodePVFBytes(raw []byte) string {
	best := string(raw)
	bestScore := readableScore(best)
	for _, codec := range pvfCodecs {
		s, _, err := transform.String(codec.decode, string(raw))
		if err != nil {
			continue
		}
		score := readableScore(s)
		if score > bestScore {
			best = s
			bestScore = score
		}
	}
	return best
}

func UTF8AsWindows1252String(s string) string {
	b := []byte(s)
	runes := make([]rune, len(b))
	for i, v := range b {
		runes[i] = windows1252ByteToRune(v)
	}
	return string(runes)
}

func Windows1252StringBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if b, ok := windows1252RuneToByte(r); ok {
			out = append(out, b)
			continue
		}
		out = append(out, []byte(string(r))...)
	}
	return out
}

func windows1252ByteToRune(b byte) rune {
	switch b {
	case 0x80:
		return 0x20AC
	case 0x82:
		return 0x201A
	case 0x83:
		return 0x0192
	case 0x84:
		return 0x201E
	case 0x85:
		return 0x2026
	case 0x86:
		return 0x2020
	case 0x87:
		return 0x2021
	case 0x88:
		return 0x02C6
	case 0x89:
		return 0x2030
	case 0x8A:
		return 0x0160
	case 0x8B:
		return 0x2039
	case 0x8C:
		return 0x0152
	case 0x8E:
		return 0x017D
	case 0x91:
		return 0x2018
	case 0x92:
		return 0x2019
	case 0x93:
		return 0x201C
	case 0x94:
		return 0x201D
	case 0x95:
		return 0x2022
	case 0x96:
		return 0x2013
	case 0x97:
		return 0x2014
	case 0x98:
		return 0x02DC
	case 0x99:
		return 0x2122
	case 0x9A:
		return 0x0161
	case 0x9B:
		return 0x203A
	case 0x9C:
		return 0x0153
	case 0x9E:
		return 0x017E
	case 0x9F:
		return 0x0178
	default:
		return rune(b)
	}
}

func windows1252RuneToByte(r rune) (byte, bool) {
	if r >= 0 && r <= 0xff {
		return byte(r), true
	}
	switch r {
	case 0x20AC:
		return 0x80, true
	case 0x201A:
		return 0x82, true
	case 0x0192:
		return 0x83, true
	case 0x201E:
		return 0x84, true
	case 0x2026:
		return 0x85, true
	case 0x2020:
		return 0x86, true
	case 0x2021:
		return 0x87, true
	case 0x02C6:
		return 0x88, true
	case 0x2030:
		return 0x89, true
	case 0x0160:
		return 0x8A, true
	case 0x2039:
		return 0x8B, true
	case 0x0152:
		return 0x8C, true
	case 0x017D:
		return 0x8E, true
	case 0x2018:
		return 0x91, true
	case 0x2019:
		return 0x92, true
	case 0x201C:
		return 0x93, true
	case 0x201D:
		return 0x94, true
	case 0x2022:
		return 0x95, true
	case 0x2013:
		return 0x96, true
	case 0x2014:
		return 0x97, true
	case 0x02DC:
		return 0x98, true
	case 0x2122:
		return 0x99, true
	case 0x0161:
		return 0x9A, true
	case 0x203A:
		return 0x9B, true
	case 0x0153:
		return 0x9C, true
	case 0x017E:
		return 0x9E, true
	case 0x0178:
		return 0x9F, true
	default:
		return 0, false
	}
}

func readableScore(s string) int {
	score := 0
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			score -= 20
		case unicode.Is(unicode.Han, r):
			score += 6
		case r >= 32 && r < 127:
			score += 1
		case unicode.IsLetter(r), unicode.IsNumber(r):
			score += 2
		case unicode.IsControl(r):
			score -= 2
		}
	}
	return score
}
