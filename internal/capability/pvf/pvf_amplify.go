package pvf

import (
	"regexp"
	"strconv"
	"strings"
)

// AmplifyItemPVFPath is the archive file that carries the amplification tables.
// The game server reads the same path into its own config, and the robot reads
// it so an amplified item's value is the number the server would have computed
// rather than a constant that drifts when the archive changes.
const AmplifyItemPVFPath = "etc/amplifyitem.etc"

// AmplifyConfig is the part of the amplification tables an item's initial value
// needs: the base value of each attribute and the weight of each rarity. The
// value the client shows is the product of the two, truncated, which is exactly
// what the game server computes when a player invests an option.
type AmplifyConfig struct {
	// BaseByAttribute is keyed by the client amplification attribute 1..4.
	BaseByAttribute map[int]float64
	// RarityWeight is keyed by the PVF rarity index 0..6.
	RarityWeight map[int]float64
}

// pvfRarityIndex names the rarity tokens the archive writes in [rarity weight].
// It is the same vocabulary the game server's own reader uses.
var pvfRarityIndex = map[string]int{
	"common":    0,
	"uncommon":  1,
	"rare":      2,
	"unique":    3,
	"epic":      4,
	"chronicle": 5,
	"legendary": 6,
}

// Empty reports whether the archive carried no amplification tables at all.
func (c AmplifyConfig) Empty() bool {
	return len(c.BaseByAttribute) == 0 || len(c.RarityWeight) == 0
}

// InitialValue returns the amplification value written for one attribute on an
// item of one rarity, or 0 when the tables do not cover the pair. The caller
// treats 0 as "do not amplify": a missing table must leave the item alone
// rather than stamp a zero the client would draw as a real option.
func (c AmplifyConfig) InitialValue(attribute, rarity int) int {
	base, ok := c.BaseByAttribute[attribute]
	if !ok || base <= 0 {
		return 0
	}
	weight, ok := c.RarityWeight[rarity]
	if !ok || weight <= 0 {
		return 0
	}
	return int(int64(weight * base))
}

// ReadAmplifyConfig reads the amplification tables out of an already opened
// archive.
func ReadAmplifyConfig(archive TownTextArchive) AmplifyConfig {
	config := AmplifyConfig{BaseByAttribute: map[int]float64{}, RarityWeight: map[int]float64{}}
	if archive == nil {
		return config
	}
	text, _ := archive.ReadText(AmplifyItemPVFPath)
	if strings.TrimSpace(text) == "" {
		return config
	}
	// [option mapping table] pairs each attribute's name with its client
	// number; [option data] triples a name with two numbers, the second of
	// which is the base value.
	mapping := map[string]int{}
	parseAmplifyTokens(pvfSectionBody(text, "option mapping table"), func(tokens []amplifyToken) {
		for index := 0; index+1 < len(tokens); index += 2 {
			value, ok := tokens[index+1].integer()
			if !ok || value < 0 || value > 5 {
				continue
			}
			mapping[normalizeAmplifyName(tokens[index].text)] = value
		}
	})
	parseAmplifyTokens(pvfSectionBody(text, "option data"), func(tokens []amplifyToken) {
		for index := 0; index+2 < len(tokens); index += 3 {
			attribute := mapping[normalizeAmplifyName(tokens[index].text)]
			base, ok := tokens[index+2].number()
			if attribute >= 1 && attribute <= 4 && ok && base > 0 {
				config.BaseByAttribute[attribute] = base
			}
		}
	})
	parseAmplifyTokens(pvfSectionBody(text, "rarity weight"), func(tokens []amplifyToken) {
		for index := 0; index+1 < len(tokens); index += 2 {
			weight, ok := tokens[index+1].number()
			rarity, known := pvfRarityIndex[normalizeAmplifyName(tokens[index].text)]
			if known && ok && weight > 0 {
				config.RarityWeight[rarity] = weight
			}
		}
	})
	return config
}

func parseAmplifyTokens(body string, visit func([]amplifyToken)) {
	if tokens := amplifyTokens(body); len(tokens) > 0 {
		visit(tokens)
	}
}

func normalizeAmplifyName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

// amplifyTokenPattern reads a section as the archive writes it: backticked
// names and bare numbers, in the order the section declares them. Everything
// else in the body is a comment or a bracket and is skipped.
var amplifyTokenPattern = regexp.MustCompile("`([^`]*)`|(-?[0-9]+(?:\\.[0-9]+)?)")

type amplifyToken struct {
	text     string
	value    float64
	isNumber bool
}

func (t amplifyToken) number() (float64, bool) {
	return t.value, t.isNumber
}

func (t amplifyToken) integer() (int, bool) {
	if !t.isNumber {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(t.text))
	if err != nil {
		return 0, false
	}
	return value, true
}

func amplifyTokens(body string) []amplifyToken {
	matches := amplifyTokenPattern.FindAllStringSubmatch(body, -1)
	out := make([]amplifyToken, 0, len(matches))
	for _, match := range matches {
		if match[2] != "" {
			value, err := strconv.ParseFloat(match[2], 64)
			if err != nil {
				continue
			}
			out = append(out, amplifyToken{text: match[2], value: value, isNumber: true})
			continue
		}
		name := strings.TrimSpace(match[1])
		if name == "" {
			continue
		}
		out = append(out, amplifyToken{text: name})
	}
	return out
}

// pvfSectionBody returns the body of the first [name]...[/name] block. The
// amplification sections all close explicitly, so the closing tag is required:
// a section that repeats without one ([purify material], [invest option]) is
// never asked for here.
func pvfSectionBody(text, name string) string {
	lower := strings.ToLower(text)
	open := "[" + strings.ToLower(name) + "]"
	start := strings.Index(lower, open)
	if start < 0 {
		return ""
	}
	start += len(open)
	end := strings.Index(lower[start:], "[/"+strings.ToLower(name)+"]")
	if end < 0 {
		return ""
	}
	return text[start : start+end]
}
