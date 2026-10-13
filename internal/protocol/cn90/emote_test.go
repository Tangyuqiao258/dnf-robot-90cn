package cn90

import (
	"encoding/binary"
	"testing"
)

func TestChangeEmotionBodyIsTheLittleEndianIndex(t *testing.T) {
	body := ChangeEmotionBody(0x1234)
	if len(body) != 2 {
		t.Fatalf("op254 body length = %d want 2", len(body))
	}
	if got := binary.LittleEndian.Uint16(body); got != 0x1234 {
		t.Fatalf("op254 body value = 0x%04x want 0x1234", got)
	}
}

func TestChangeEmotionAckReturnsTheStoredIndex(t *testing.T) {
	// The success body is the server's upper-success prefix followed by the
	// index it persisted, so the ack is three bytes rather than two.
	stored, err := ChangeEmotionAck(CmdChangeEmotion, []byte{1, 9, 0})
	if err != nil {
		t.Fatalf("success ack failed: %v", err)
	}
	if stored != 9 {
		t.Fatalf("stored index = %d want 9", stored)
	}
}

func TestChangeEmotionAckReportsARejection(t *testing.T) {
	if _, err := ChangeEmotionAck(CmdChangeEmotion, []byte{0, 7}); err == nil {
		t.Fatal("a rejection code was accepted as success")
	}
}

func TestChangeEmotionAckRejectsMalformedBodies(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []byte
	}{
		{"empty", nil},
		{"success with no index", []byte{1}},
		{"success with a half index", []byte{1, 9}},
		{"bare rejection", []byte{0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ChangeEmotionAck(CmdChangeEmotion, tc.body); err == nil {
				t.Fatalf("body %v was accepted", tc.body)
			}
		})
	}
}
