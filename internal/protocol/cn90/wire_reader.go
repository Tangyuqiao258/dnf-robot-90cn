package cn90

import (
	"encoding/binary"
	"fmt"
)

// wireReader is the shared little-endian cursor for the 90CN upper bodies. It
// latches the first error so a parser can read a whole structure and check once.
type wireReader struct {
	body   []byte
	offset int
	err    error
}

func (r *wireReader) skip(n int) error {
	if r.err != nil {
		return r.err
	}
	if n < 0 || r.offset+n > len(r.body) {
		r.err = fmt.Errorf("short body at offset %d", r.offset)
		return r.err
	}
	r.offset += n
	return nil
}

func (r *wireReader) byte() byte {
	if r.skip(1) != nil {
		return 0
	}
	return r.body[r.offset-1]
}

func (r *wireReader) u16() uint16 {
	if r.skip(2) != nil {
		return 0
	}
	return binary.LittleEndian.Uint16(r.body[r.offset-2 : r.offset])
}

func (r *wireReader) u32() uint32 {
	if r.skip(4) != nil {
		return 0
	}
	return binary.LittleEndian.Uint32(r.body[r.offset-4 : r.offset])
}

func (r *wireReader) u64() uint64 {
	if r.skip(8) != nil {
		return 0
	}
	return binary.LittleEndian.Uint64(r.body[r.offset-8 : r.offset])
}

// raw returns the next n bytes as a sub-slice of the body; the caller must copy
// it when it outlives the packet.
func (r *wireReader) raw(n int) []byte {
	if n < 0 || r.skip(n) != nil {
		return nil
	}
	return r.body[r.offset-n : r.offset]
}

// dstr reads the server's DSTR: u32 byte length followed by that many raw bytes.
func (r *wireReader) dstr() []byte {
	length := r.u32()
	if r.err != nil {
		return nil
	}
	return r.raw(int(length))
}

func (r *wireReader) remaining() int {
	return len(r.body) - r.offset
}
