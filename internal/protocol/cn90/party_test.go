package cn90

import (
	"bytes"
	"testing"
)

// The byte vectors mirror the server's own bridge tests
// (dnfbridge/game_aligned_test.go): a mode-13 invite from character 1001 to
// character 1002, its forwarded prompt and the answering op11 body.

func TestParseRequestPeerNoticeReadsForwardedHead(t *testing.T) {
	notice, ok := ParseRequestPeerNotice([]byte{0xe9, 0x03, 13, 0, 0, 0, 0})
	if !ok || notice.SourceID != 1001 || notice.Mode != 13 || notice.Value != 0 {
		t.Fatalf("notice = %+v ok=%t", notice, ok)
	}
	// Ordinary mode-0 prompts carry a longer tail; only the head is consumed.
	long := append([]byte{0xe9, 0x03, 0, 0x0a, 0x82, 0x01, 0}, make([]byte, 10)...)
	notice, ok = ParseRequestPeerNotice(long)
	if !ok || notice.SourceID != 1001 || notice.Mode != 0 || notice.Value != 0x0001820a {
		t.Fatalf("long notice = %+v ok=%t", notice, ok)
	}
	if _, ok := ParseRequestPeerNotice([]byte{0xe9, 0x03, 13, 0, 0, 0}); ok {
		t.Fatal("short prompt was accepted")
	}
}

func TestPartyInviteAcceptValueMatchesServerSemantics(t *testing.T) {
	for mode, want := range map[byte]uint32{0: 0, 12: 0, 4: 1, 13: 1} {
		value, ok := PartyInviteAcceptValue(mode)
		if !ok || value != want {
			t.Fatalf("mode %d accept value = %d ok=%t want %d", mode, value, ok, want)
		}
	}
	// Mode 1 is the trade prompt, mode 15 the town-menu marker; neither is a
	// party invite response.
	for _, mode := range []byte{1, 10, 15, 7} {
		if _, ok := PartyInviteAcceptValue(mode); ok {
			t.Fatalf("mode %d must not be a party invite", mode)
		}
	}
}

func TestRequestPeerBodyMatchesPerModeLengths(t *testing.T) {
	body, err := RequestPeerBody(1002, 13, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(body, []byte{0xea, 0x03, 13, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("mode 13 body = % X", body)
	}
	body, err = RequestPeerBody(1002, 0, 0, 0x11223344)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 13 || !bytes.Equal(body[:7], []byte{0xea, 0x03, 0, 0, 0, 0, 0}) ||
		!bytes.Equal(body[9:13], []byte{0x44, 0x33, 0x22, 0x11}) {
		t.Fatalf("mode 0 body = % X", body)
	}
	// Mode 1 is the trade request. It is not a party invite, but the server
	// forwards it on the same route and decodes the same eleven-byte shape, so
	// the encoder must build it.
	body, err = RequestPeerBody(1002, TradeRequestMode, 0, 0)
	if err != nil {
		t.Fatalf("trade request body: %v", err)
	}
	if len(body) != 11 || !bytes.Equal(body[:7], []byte{0xea, 0x03, 1, 0, 0, 0, 0}) {
		t.Fatalf("mode 1 body = % X", body)
	}
	// Mode 2 is the nearest mode the server neither decodes nor forwards; the
	// encoder must keep refusing it rather than inventing a shape.
	if _, err := RequestPeerBody(1002, 2, 0, 0); err == nil {
		t.Fatal("mode 2 was accepted as a forwarded peer request mode")
	}
}

func TestResponsePeerBodyAndResult(t *testing.T) {
	if body := ResponsePeerBody(1001, 13, 1); !bytes.Equal(body, []byte{0xe9, 0x03, 13, 1, 0, 0, 0}) {
		t.Fatalf("accept body = % X", body)
	}
	if ok, _ := ResponsePeerResult([]byte{1, 0xe9, 0x03, 13}); !ok {
		t.Fatal("success ack was not recognized")
	}
	if ok, code := ResponsePeerResult([]byte{0, 5}); ok || code != 5 {
		t.Fatalf("failure ack = ok=%t code=%d", ok, code)
	}
	if ok, _ := ResponsePeerResult(nil); ok {
		t.Fatal("empty ack was treated as success")
	}
}
