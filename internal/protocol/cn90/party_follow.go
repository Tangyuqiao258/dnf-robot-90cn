package cn90

import "encoding/binary"

// UserPositionNotice is the class0/0x0016 same-area movement broadcast the
// 90CN server sends to every player sharing the mover's (town, area) key, party
// or not. The actor key is the raw character id, so a party follower can match
// its leader's id against it.
type UserPositionNotice struct {
	ActorKey  uint16
	X         uint16
	Y         uint16
	Direction byte
	Rate      uint16
}

// UserAreaNotice is the class0/0x0017 area announcement. The server sends it to
// the mover's old-area peers on an area change and to same-party peers when the
// receiver itself changes area, so it is the only leader position a follower in
// another area ever sees.
type UserAreaNotice struct {
	ActorKey  uint16
	Town      byte
	Area      byte
	X         uint16
	Y         uint16
	Direction byte
	StateBits byte
}

// ParseUserPositionNotice decodes the 0x0016 body written by
// buildCurrentTownUserPositionNotificationBody:
//
//	u16 actor_key, u16 x, u16 y, u8 direction, u16 initial_movement_rate
func ParseUserPositionNotice(body []byte) (UserPositionNotice, bool) {
	if len(body) < 9 {
		return UserPositionNotice{}, false
	}
	return UserPositionNotice{
		ActorKey:  binary.LittleEndian.Uint16(body[0:2]),
		X:         binary.LittleEndian.Uint16(body[2:4]),
		Y:         binary.LittleEndian.Uint16(body[4:6]),
		Direction: body[6],
		Rate:      binary.LittleEndian.Uint16(body[7:9]),
	}, true
}

// ParseUserAreaNotice decodes the 0x0017 body written by
// buildCurrentTownUserAreaNotificationBody:
//
//	u16 actor_key, u8 town, u8 area, u16 x, u16 y, u8 direction, u8 state_bits
func ParseUserAreaNotice(body []byte) (UserAreaNotice, bool) {
	if len(body) < 10 {
		return UserAreaNotice{}, false
	}
	return UserAreaNotice{
		ActorKey:  binary.LittleEndian.Uint16(body[0:2]),
		Town:      body[2],
		Area:      body[3],
		X:         binary.LittleEndian.Uint16(body[4:6]),
		Y:         binary.LittleEndian.Uint16(body[6:8]),
		Direction: body[8],
		StateBits: body[9],
	}, true
}
