package robottemplate

import (
	"strings"
	"testing"
	"unicode/utf8"

	robotconfig "robot/internal/capability/robotconfig"
)

func TestSafeShoutMessage(t *testing.T) {
	if got := SafeShoutMessage(" \x01hello "); got != "hello" {
		t.Fatalf("safe shout got %q want hello", got)
	}
	if got := SafeShoutMessage(""); got != "hello" {
		t.Fatalf("empty shout got %q want hello", got)
	}
}

func TestSafeShoutMessageUsesProtocolLimitWithoutSplittingUTF8(t *testing.T) {
	if got := SafeShoutMessage(strings.Repeat("a", 300)); len(got) != 255 {
		t.Fatalf("ASCII shout length got %d want 255", len(got))
	}

	got := SafeShoutMessage(strings.Repeat("喊", 100))
	if !utf8.ValidString(got) {
		t.Fatal("Chinese shout was truncated inside a UTF-8 character")
	}
	if count := utf8.RuneCountInString(got); count != 85 {
		t.Fatalf("Chinese shout characters got %d want 85", count)
	}
	if len(got) != 255 {
		t.Fatalf("Chinese shout bytes got %d want 255", len(got))
	}
}

func TestPrepareShoutSeparatesLocalAndWorld(t *testing.T) {
	localType, localChannel, localOut := PrepareShout("hello", false)
	if localType != 3 || localChannel != "local" || localOut != "hello" {
		t.Fatalf("local shout got type=%d channel=%s out=%q", localType, localChannel, localOut)
	}

	worldType, worldChannel, worldOut := PrepareShout("hello", true)
	if worldType != 11 || worldChannel != "world" || worldOut != "hello" {
		t.Fatalf("world shout got type=%d channel=%s out=%q, want type=11 channel=world out=hello", worldType, worldChannel, worldOut)
	}
}

func TestAllocateNameUsesGrowJobAndCommonPoolsInOrder(t *testing.T) {
	tpl := NameTemplates{
		Common: []string{"Common"},
		Jobs: map[int]NamePool{1: {
			Names: []string{"Job"},
			Grows: map[int]NamePool{2: {Names: []string{"Grow"}}},
		}},
	}
	used := map[string]struct{}{}
	if got := AllocateName(123, 1, 2, used, robotconfig.RuntimeConfig{}, tpl, nil, nil); got != "Grow" {
		t.Fatalf("first name=%q want Grow", got)
	}
	if got := AllocateName(124, 1, 2, used, robotconfig.RuntimeConfig{}, tpl, nil, nil); got != "Job" {
		t.Fatalf("second name=%q want Job", got)
	}
	if got := AllocateName(125, 1, 2, used, robotconfig.RuntimeConfig{}, tpl, nil, nil); got != "Common" {
		t.Fatalf("third name=%q want Common", got)
	}
}

func TestNameEncodingRules(t *testing.T) {
	if !FitsGameSlot("RobotName") {
		t.Fatalf("ascii name should fit game slot")
	}
	if FitsGameSlot("") {
		t.Fatalf("empty name should not fit game slot")
	}
	if got := DBName("RobotName"); got == "" {
		t.Fatalf("db name should not be empty")
	}
}

// TestGameSlotMeasuresGBKBytes pins the 90CN name window: the server measures
// the name in GBK bytes and accepts 2..30 of them. Measuring UTF-8 bytes, as
// this check used to, rejected every Chinese name of seven or more characters.
func TestGameSlotMeasuresGBKBytes(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"迷你战士宠物装备", true},                // 24 UTF-8 bytes, 16 GBK bytes
		{strings.Repeat("甲", 15), true},   // exactly 30 GBK bytes
		{strings.Repeat("甲", 16), false},  // 32 GBK bytes, over the limit
		{"甲", true},                       // one character is already 2 GBK bytes
		{"甲a甲a甲a甲a甲a甲a甲a甲a甲a甲a甲a", false}, // mixed, 34 GBK bytes
		{"🎉", false},                      // not representable in GBK
	}
	for _, tc := range cases {
		if got := FitsGameSlot(tc.name); got != tc.want {
			t.Fatalf("FitsGameSlot(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := DBName("吴翰林"); got != "吴翰林" {
		t.Fatalf("DBName mangled a Chinese name: %q", got)
	}
}
