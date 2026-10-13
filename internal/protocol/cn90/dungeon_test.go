package cn90

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestSelectDungeonBodyIsTwentyOneBytesWithAZeroTail(t *testing.T) {
	body := SelectDungeonBody(0x0001abcd, 3)
	if len(body) != selectDungeonBodySize {
		t.Fatalf("op16 body length = %d want %d", len(body), selectDungeonBodySize)
	}
	if got := binary.LittleEndian.Uint32(body[0:4]); got != 0x0001abcd {
		t.Fatalf("op16 dungeon id = %d want %d", got, 0x0001abcd)
	}
	if body[4] != 3 {
		t.Fatalf("op16 difficulty = %d want 3", body[4])
	}
	// Every field after the difficulty has to stay zero: the server rejects a
	// non-zero entry option, selection mode, runtime state, reserved word,
	// party state or special mode, and only accepts a runtime token of 0 or
	// 0xffff, so a zero tail is the only shape a solo entry can send.
	for i := 5; i < len(body); i++ {
		if body[i] != 0 {
			t.Fatalf("op16 body[%d] = %d want 0", i, body[i])
		}
	}
}

func TestSelectDungeonBodyCarriesAFullWidthDungeonID(t *testing.T) {
	body := SelectDungeonBody(0xffffffff, 0)
	if got := binary.LittleEndian.Uint32(body[0:4]); got != 0xffffffff {
		t.Fatalf("op16 dungeon id = 0x%08x want 0xffffffff", got)
	}
}

func TestDungeonSelectAckAcceptsTheUpperSuccessPrefix(t *testing.T) {
	for _, body := range [][]byte{{1}, {1, 0}, {1, 0, 0}} {
		if err := DungeonSelectAck(CmdSelectDungeon, body); err != nil {
			t.Fatalf("success body %v was rejected: %v", body, err)
		}
	}
}

func TestDungeonSelectAckNamesTheKnownFailureCodes(t *testing.T) {
	for code, want := range map[byte]string{
		0x08: "not the party leader",
		0x0e: "level too low",
		0x11: "required entry item missing",
		0x16: "fatigue exhausted",
	} {
		err := DungeonSelectAck(CmdSelectDungeon, []byte{0, code, 0})
		if err == nil {
			t.Fatalf("failure code 0x%02x was accepted as success", code)
		}
		if got := err.Error(); !strings.Contains(got, want) {
			t.Fatalf("failure code 0x%02x message = %q want it to name %q", code, got, want)
		}
	}
}

func TestDungeonSelectAckReportsAnUnknownCodeByNumber(t *testing.T) {
	err := DungeonSelectAck(CmdSelectDungeon, []byte{0, 0x7f, 0})
	if err == nil {
		t.Fatal("an unknown failure code was accepted as success")
	}
	if got := err.Error(); !strings.Contains(got, "127") {
		t.Fatalf("unknown code message = %q want it to carry the code number", got)
	}
}

func TestDungeonSelectAckRejectsAnEmptyBody(t *testing.T) {
	if err := DungeonSelectAck(CmdSelectDungeon, nil); err == nil {
		t.Fatal("an empty body was accepted as success")
	}
}

func TestDieCharacterBodyIsTheLittleEndianPosition(t *testing.T) {
	body := DieCharacterBody(-300, 1024)
	if len(body) != 4 {
		t.Fatalf("op40 body length = %d want 4", len(body))
	}
	if got := int16(binary.LittleEndian.Uint16(body[0:2])); got != -300 {
		t.Fatalf("op40 x = %d want -300", got)
	}
	if got := int16(binary.LittleEndian.Uint16(body[2:4])); got != 1024 {
		t.Fatalf("op40 y = %d want 1024", got)
	}
}

func TestUseCoinBodyIsTheLittleEndianObjectKey(t *testing.T) {
	body := UseCoinBody(0x1234)
	if len(body) != 2 {
		t.Fatalf("op41 body length = %d want 2", len(body))
	}
	if got := binary.LittleEndian.Uint16(body); got != 0x1234 {
		t.Fatalf("op41 object key = 0x%04x want 0x1234", got)
	}
}

func TestUseCoinAckAcceptsTheSuccessPair(t *testing.T) {
	if err := UseCoinAck(CmdUseCoin, []byte{1, 0}); err != nil {
		t.Fatalf("revive success was rejected: %v", err)
	}
}

func TestUseCoinAckReportsTheFailurePair(t *testing.T) {
	err := UseCoinAck(CmdUseCoin, []byte{0, 1})
	if err == nil {
		t.Fatal("the revive failure pair was accepted as success")
	}
}

func TestUseCoinAckRejectsAShortBody(t *testing.T) {
	for _, body := range [][]byte{nil, {1}} {
		if err := UseCoinAck(CmdUseCoin, body); err == nil {
			t.Fatalf("a %d-byte body was accepted as success", len(body))
		}
	}
}

func TestParseDungeonDeathStateDecodesBothNotices(t *testing.T) {
	dead, err := ParseDungeonDeathState(NotiDungeonDeathState, []byte{0x34, 0x12, 0, 0})
	if err != nil {
		t.Fatalf("death notice failed to parse: %v", err)
	}
	if dead.ObjectKey != 0x1234 || dead.Revived || dead.Flag != 0 {
		t.Fatalf("death notice = %+v want key 0x1234 dead flag 0", dead)
	}
	revived, err := ParseDungeonDeathState(NotiDungeonDeathState, []byte{0x34, 0x12, 1, 0})
	if err != nil {
		t.Fatalf("revive notice failed to parse: %v", err)
	}
	if revived.ObjectKey != 0x1234 || !revived.Revived {
		t.Fatalf("revive notice = %+v want key 0x1234 revived", revived)
	}
}

func TestParseDungeonDeathStateRejectsOtherLengths(t *testing.T) {
	for _, body := range [][]byte{nil, {0x34}, {0x34, 0x12, 0}, {0x34, 0x12, 0, 0, 0}} {
		if _, err := ParseDungeonDeathState(NotiDungeonDeathState, body); err == nil {
			t.Fatalf("a %d-byte body was accepted as a death notice", len(body))
		}
	}
}

func TestParseDungeonDeathStateRejectsAnUnknownStateByte(t *testing.T) {
	for _, state := range []byte{2, 3, 0xff} {
		if _, err := ParseDungeonDeathState(NotiDungeonDeathState, []byte{0x34, 0x12, state, 0}); err == nil {
			t.Fatalf("state byte %d was accepted as a death notice", state)
		}
	}
}
