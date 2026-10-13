package cn90

import (
	"encoding/binary"
	"fmt"
)

// The 90CN mood/emotion command. The client's mood window sends op254 with a
// single little-endian emotion index; the server stores it in the character's
// "emotion_index" stat and never validates the range, so any u16 round-trips.
// The stored value is what the scene object tail hands to the client's actor
// state, which is how the chosen mood becomes visible to everyone in the area.

// ChangeEmotionBody builds the op254 body: the emotion index to adopt.
func ChangeEmotionBody(emotionIndex uint16) []byte {
	body := make([]byte, 2)
	binary.LittleEndian.PutUint16(body, emotionIndex)
	return body
}

// ChangeEmotionAck parses the op254 reply. A success carries the index the
// server stored, which is the value a caller should treat as authoritative:
// the server echoes what it persisted rather than what was requested.
func ChangeEmotionAck(opcode uint16, body []byte) (uint16, error) {
	if len(body) == 0 {
		return 0, fmt.Errorf("cn90 emotion op %d returned an empty body", opcode)
	}
	if body[0] == 1 {
		if len(body) < 3 {
			return 0, fmt.Errorf("cn90 emotion op %d success body is truncated: %d bytes", opcode, len(body))
		}
		return binary.LittleEndian.Uint16(body[1:3]), nil
	}
	if len(body) >= 2 {
		return 0, fmt.Errorf("cn90 emotion op %d rejected with code %d", opcode, body[1])
	}
	return 0, fmt.Errorf("cn90 emotion op %d was rejected", opcode)
}
