package cn90

// Party P2P UDP transport.
//
// The party HUD keeps every peer row on "connecting" until the client's TQOS
// handshake to that peer's UDP endpoint completes. The server exchanges the
// endpoints in class0/op11 PARTY_IP_INFO, and the current client reports its
// own dynamic endpoint in the 31-byte class1/op2 SET_UDP_IP_PORT body. A
// server-side directed relay may stand between the endpoints: it forwards the
// peer's queued handshake only after it has seen a datagram from this side on
// the opposite leg, so this transport also sends one 8-byte transport ack to
// every advertised peer as soon as it learns the endpoints.
//
// This file keeps the endpoint registered and answers the peer's TQOS control
// frames. Application payloads (the in-dungeon position plane) are
// intentionally not parsed or sent.
//
// 参考: reference/RobotS4A21/internal/protocol/s4a21/party_udp.go
// 参考: reference/ServerS5A3-16ww/go-server/internal/services/dnfbridge/party_peer_endpoint.go
// (31-byte current-EXE registration layout)

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math/bits"
	"net"
	"time"

	"robot/internal/foundation/lockhub"
)

type partyUDPCodec struct {
	key, rotate byte
	extra       [2]byte
}

type partyUDPPeer struct {
	codecRoute       [2]partyUDPCodec
	codecKnown       [2]bool
	nextSeqRoute     [2]uint32
	reliableSeqRoute [2]uint32
	pending          []byte
	pendingSeq       uint32
	pendingRoute     [2][]byte
	pendingSeqRoute  [2]uint32
	epochRoute       [2]bool
}

// partyUDPMaxPeers bounds the per-session peer table. Party UDP endpoints are
// announced by the server, but any host on the network can send a datagram to
// the registered port, so the table must not grow without limit.
const partyUDPMaxPeers = 256

// partyUDPMaxSlots bounds the party slot the transport will stamp as its own
// sender byte. The current party holds at most four members.
const partyUDPMaxSlots = 4

// partyUDPRegistrationBodySize is the exact body length the server requires for
// the current-EXE class1/op2 endpoint report.
const partyUDPRegistrationBodySize = 31

// partyUDPAdvertisedMTU is the MTU the live client reports (server accepts
// 576..9000 and ignores anything outside that range).
const partyUDPAdvertisedMTU = 1472

// partyUDPCharacterAttribute is the first byte of the live client's
// registration body.
const partyUDPCharacterAttribute = 5

// partyPeerEndpointRowBytes is one op11 row: u16 character id, inner IPv4,
// outer IPv4, big-endian UDP port, u32 account id, u8 NAT type, u32 MTU, u8
// character attribute.
const partyPeerEndpointRowBytes = 22

// partyUDPBootstrapRefresh re-sends the endpoint announcement when the same
// op11 snapshot repeats after this interval, so a recreated relay leg can learn
// this side again without waiting for a membership change.
const partyUDPBootstrapRefresh = 5 * time.Second

var partyUDPCRCTable = crc32.MakeTable(0x4db89129)

// partyUDPEndpoint is one parsed op11 row.
type partyUDPEndpoint struct {
	uid  uint16
	ip   [4]byte
	port uint16
}

type partyUDPBootstrap struct {
	endpoint partyUDPEndpoint
	at       time.Time
}

// partyUDPState is the per-client UDP transport state. It is created with the
// client and its mutex guards every field below: the UDP serve goroutine writes
// the peer table while the session drain writes the self slot.
type partyUDPState struct {
	mu         lockhub.Locker
	conn       *net.UDPConn
	selfUID    uint16
	selfSlot   byte
	slotKnown  bool
	peers      map[string]*partyUDPPeer
	bootstraps map[uint16]partyUDPBootstrap
	peerDrops  int
}

func newPartyUDPState() *partyUDPState {
	return &partyUDPState{
		peers:      make(map[string]*partyUDPPeer),
		bootstraps: make(map[uint16]partyUDPBootstrap),
	}
}

func (st *partyUDPState) close() {
	if st == nil {
		return
	}
	st.mu.Lock()
	conn := st.conn
	st.conn = nil
	st.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// setUDPIPPortBody builds the 31-byte class1/op2 body of the current client:
// character attribute, inner IPv4, outer IPv4, big-endian UDP port,
// little-endian MTU and a length-prefixed machine token (the server ignores the
// token value).
// 参考: reference/ServerS5A3-16ww/go-server/internal/services/dnfbridge/party_peer_endpoint.go:27
func setUDPIPPortBody(ip net.IP, port uint16, mtu uint32) []byte {
	body := make([]byte, partyUDPRegistrationBodySize)
	body[0] = partyUDPCharacterAttribute
	ipv4 := ip.To4()
	copy(body[1:5], ipv4)
	copy(body[5:9], ipv4)
	binary.BigEndian.PutUint16(body[9:11], port)
	binary.LittleEndian.PutUint32(body[11:15], mtu)
	binary.LittleEndian.PutUint32(body[15:19], partyUDPRegistrationBodySize-19)
	return body
}

// RegisterPartyUDPEndpoint binds a UDP socket next to the TCP session, reports
// its endpoint on class1/op2 and serves the party handshake from it. The TCP
// local address is bound explicitly because the server relay authenticates the
// UDP source against the address it observed on the TCP connection.
func (c *Client) RegisterPartyUDPEndpoint(ctx context.Context, selfUID uint16) (uint16, error) {
	if c == nil || c.conn == nil {
		return 0, fmt.Errorf("cn90 client is closed")
	}
	tcpLocal, ok := c.conn.LocalAddr().(*net.TCPAddr)
	if !ok || tcpLocal.IP == nil || tcpLocal.IP.To4() == nil {
		return 0, fmt.Errorf("cn90 party UDP needs an IPv4 TCP local address")
	}
	ipv4 := tcpLocal.IP.To4()
	udpConn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ipv4, Port: 0})
	if err != nil {
		return 0, err
	}
	port := uint16(udpConn.LocalAddr().(*net.UDPAddr).Port)
	st := c.partyUDP
	st.mu.Lock()
	if st.conn != nil {
		st.mu.Unlock()
		_ = udpConn.Close()
		return 0, fmt.Errorf("cn90 party UDP endpoint is already registered")
	}
	st.conn = udpConn
	st.selfUID = selfUID
	st.peers = make(map[string]*partyUDPPeer)
	st.bootstraps = make(map[uint16]partyUDPBootstrap)
	st.mu.Unlock()
	if err := c.send(ctx, CmdSetUDPIPPort, setUDPIPPortBody(ipv4, port, partyUDPAdvertisedMTU)); err != nil {
		st.mu.Lock()
		if st.conn == udpConn {
			st.conn = nil
		}
		st.mu.Unlock()
		_ = udpConn.Close()
		return 0, err
	}
	go c.servePartyUDP(udpConn)
	return port, nil
}

// PartyUDPPort reports the registered local UDP port, or zero while the
// transport is not registered.
func (c *Client) PartyUDPPort() uint16 {
	if c == nil || c.partyUDP == nil {
		return 0
	}
	c.partyUDP.mu.Lock()
	defer c.partyUDP.mu.Unlock()
	if c.partyUDP.conn == nil {
		return 0
	}
	return uint16(c.partyUDP.conn.LocalAddr().(*net.UDPAddr).Port)
}

// PartyUDPSlot reports the party slot learned from the class0/op153 rows.
func (c *Client) PartyUDPSlot() (byte, bool) {
	if c == nil || c.partyUDP == nil {
		return 0, false
	}
	c.partyUDP.mu.Lock()
	defer c.partyUDP.mu.Unlock()
	return c.partyUDP.selfSlot, c.partyUDP.slotKnown
}

// ApplyPartyRealtimeInfo records the roster slots announced by class0/op153.
// The transport needs the session's own slot to stamp ACK sender bytes.
func (c *Client) ApplyPartyRealtimeInfo(body []byte) {
	if c == nil || c.partyUDP == nil {
		return
	}
	if len(body) < 1 || len(body) != 1+int(body[0])*partyMemberRealtimeRowBytes {
		return
	}
	st := c.partyUDP
	st.mu.Lock()
	defer st.mu.Unlock()
	for offset := 1; offset+partyMemberRealtimeRowBytes <= len(body); offset += partyMemberRealtimeRowBytes {
		uid := binary.LittleEndian.Uint16(body[offset : offset+2])
		if uid != st.selfUID {
			continue
		}
		st.selfSlot = body[offset+4]
		st.slotKnown = st.selfSlot < partyUDPMaxSlots
		return
	}
}

// parsePartyPeerEndpoints reads one class0/op11 PARTY_IP_INFO body. The inner
// IPv4 is preferred and the outer one is the fallback for a host that only
// knows its NAT address.
func parsePartyPeerEndpoints(body []byte) ([]partyUDPEndpoint, bool) {
	if len(body) < 1 {
		return nil, false
	}
	count := int(body[0])
	if len(body) != 1+count*partyPeerEndpointRowBytes {
		return nil, false
	}
	endpoints := make([]partyUDPEndpoint, 0, count)
	for index := 0; index < count; index++ {
		row := body[1+index*partyPeerEndpointRowBytes:]
		endpoint := partyUDPEndpoint{
			uid:  binary.LittleEndian.Uint16(row[:2]),
			port: binary.BigEndian.Uint16(row[10:12]),
		}
		copy(endpoint.ip[:], row[2:6])
		if endpoint.ip == [4]byte{} {
			copy(endpoint.ip[:], row[6:10])
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, true
}

// ApplyPartyPeerEndpoints handles one op11 exchange: every remote member's
// advertised endpoint receives the transport announcement described at the top
// of this file. It returns how many announcements were sent and whether the
// body parsed.
func (c *Client) ApplyPartyPeerEndpoints(body []byte) (int, bool) {
	if c == nil || c.partyUDP == nil {
		return 0, false
	}
	endpoints, ok := parsePartyPeerEndpoints(body)
	if !ok {
		return 0, false
	}
	st := c.partyUDP
	st.mu.Lock()
	conn, selfSlot, slotKnown, selfUID := st.conn, st.selfSlot, st.slotKnown, st.selfUID
	sends := make([]partyUDPEndpoint, 0, len(endpoints))
	now := time.Now()
	if conn != nil && slotKnown {
		for _, endpoint := range endpoints {
			if endpoint.uid == 0 || endpoint.uid == selfUID || endpoint.port == 0 {
				continue
			}
			if previous, seen := st.bootstraps[endpoint.uid]; seen &&
				previous.endpoint == endpoint && now.Sub(previous.at) < partyUDPBootstrapRefresh {
				continue
			}
			st.bootstraps[endpoint.uid] = partyUDPBootstrap{endpoint: endpoint, at: now}
			sends = append(sends, endpoint)
		}
	}
	st.mu.Unlock()
	for _, target := range sends {
		address := &net.UDPAddr{
			IP:   net.IPv4(target.ip[0], target.ip[1], target.ip[2], target.ip[3]),
			Port: int(target.port),
		}
		_, _ = conn.WriteToUDP(partyUDPAck(selfSlot, 0), address)
	}
	return len(sends), true
}

// partyUDPPeerLocked returns the tracked peer for remote, creating one while
// the peer budget allows. Callers must hold st.mu.
func (st *partyUDPState) partyUDPPeerLocked(remote *net.UDPAddr) *partyUDPPeer {
	key := remote.String()
	if peer := st.peers[key]; peer != nil {
		return peer
	}
	if len(st.peers) >= partyUDPMaxPeers {
		return nil
	}
	peer := &partyUDPPeer{}
	st.peers[key] = peer
	return peer
}

func (c *Client) servePartyUDP(conn *net.UDPConn) {
	buffer := make([]byte, 2048)
	for {
		n, remote, err := conn.ReadFromUDP(buffer)
		if err != nil {
			return
		}
		if n == 0 || remote == nil {
			continue
		}
		st := c.partyUDP
		st.mu.Lock()
		peer := st.partyUDPPeerLocked(remote)
		if peer == nil {
			st.peerDrops++
			st.mu.Unlock()
			continue
		}
		selfSlot, slotKnown := st.selfSlot, st.slotKnown
		replies := partyUDPReplies(buffer[:n], peer, selfSlot, slotKnown)
		st.mu.Unlock()
		for _, reply := range replies {
			_, _ = conn.WriteToUDP(reply, remote)
		}
	}
}

func partyUDPReplies(payload []byte, peer *partyUDPPeer, selfSlot byte, slotKnown bool) [][]byte {
	if !slotKnown {
		return nil
	}
	frames, ok := splitPartyUDPFrames(payload)
	if !ok {
		return nil
	}
	replies := make([][]byte, 0, len(frames))
	for _, frame := range frames {
		replies = append(replies, partyUDPFrameReplies(frame, peer, selfSlot)...)
	}
	return replies
}

func splitPartyUDPFrames(payload []byte) ([][]byte, bool) {
	frames := make([][]byte, 0, 2)
	for len(payload) > 0 {
		frameSize := 0
		switch payload[0] {
		case 0:
			frameSize = 8
		case 1, 2:
			if len(payload) < 9 {
				return nil, false
			}
			frameSize = 9 + int(binary.LittleEndian.Uint16(payload[5:7]))
		default:
			return nil, false
		}
		if frameSize > len(payload) {
			return nil, false
		}
		frames = append(frames, payload[:frameSize])
		payload = payload[frameSize:]
	}
	return frames, len(frames) > 0
}

func partyUDPFrameReplies(payload []byte, peer *partyUDPPeer, selfSlot byte) [][]byte {
	if len(payload) == 8 && payload[0] == 0 {
		for route := 0; route < 2; route++ {
			if peer.pendingRoute[route] != nil && binary.LittleEndian.Uint32(payload[2:6]) == peer.pendingSeqRoute[route]+1 {
				peer.pendingRoute[route] = nil
				if route == 0 || peer.pending == nil || peer.pendingSeq == peer.pendingSeqRoute[route] {
					peer.pending = nil
				}
			}
		}
		return nil
	}
	if len(payload) < 9 || (payload[0] != 1 && payload[0] != 2) {
		return nil
	}
	bodyLen := int(binary.LittleEndian.Uint16(payload[5:7]))
	if len(payload) != 9+bodyLen {
		return nil
	}
	body := payload[9:]
	sequence := binary.LittleEndian.Uint32(payload[1:5])
	replies := make([][]byte, 0, 2)
	if payload[0] == 1 {
		replies = append(replies, partyUDPAck(selfSlot, sequence))
		if len(body) < 2 {
			return replies
		}
		inner := int(binary.LittleEndian.Uint16(body[:2]))
		if inner != 12 || len(body) < 2+inner {
			return replies
		}
		body = body[2 : 2+inner]
	}
	if len(body) != 12 || body[0] != 0 || body[1] != 0 || body[2] != 0 {
		return replies
	}
	sender := payload[7]
	state, codec, wireRoute, ok := decodePartyUDPBody(body, sender, 1, peer)
	if !ok {
		return replies
	}
	routeIndex := int(wireRoute)
	if routeIndex > 1 {
		routeIndex = 1
	}
	peer.codecRoute[routeIndex], peer.codecKnown[routeIndex] = codec, true
	switch state {
	case 3:
		if !peer.epochRoute[routeIndex] {
			peer.nextSeqRoute[routeIndex] = 0
			peer.epochRoute[routeIndex] = true
		}
		sequence := peer.nextSeqRoute[routeIndex]
		replies = append(replies, partyUDPReplyCandidates(sequence, selfSlot, 0, wireRoute, codec)...)
		peer.nextSeqRoute[routeIndex]++
	case 0:
		replies = append(replies, partyUDPReplyCandidates(peer.nextSeqRoute[routeIndex], selfSlot, 1, wireRoute, codec)...)
		peer.nextSeqRoute[routeIndex]++
	case 1:
		if peer.pendingRoute[routeIndex] == nil {
			peer.pendingSeqRoute[routeIndex] = peer.reliableSeqRoute[routeIndex]
			peer.reliableSeqRoute[routeIndex]++
			peer.pendingRoute[routeIndex] = buildPartyUDP(peer.pendingSeqRoute[routeIndex], selfSlot, 2, wireRoute, codec)
			peer.pending, peer.pendingSeq = peer.pendingRoute[routeIndex], peer.pendingSeqRoute[routeIndex]
		}
		replies = append(replies, peer.pendingRoute[routeIndex])
		// state2 uses the same directional tail as the unreliable states.
	}
	return replies
}

// A21 keeps a 4-byte session value in the request. Some clients expect that
// value echoed, while patched clients validate the state checksum. Emit both
// candidates with the same transport sequence; an invalid candidate is ignored
// before it can advance the receive window.
func partyUDPReplyCandidates(sequence uint32, sender, state, route byte, codec partyUDPCodec) [][]byte {
	return [][]byte{buildPartyUDP(sequence, sender, state, route, codec)}
}

func decodePartyUDPBody(body []byte, sender, route byte, peer *partyUDPPeer) (byte, partyUDPCodec, byte, bool) {
	routes := []byte{route}
	if route != 0 {
		routes = append(routes, 0)
	}
	for _, expectedRoute := range routes {
		routeIndex := int(expectedRoute)
		if routeIndex > 1 {
			routeIndex = 1
		}
		if peer.codecKnown[routeIndex] {
			codec := peer.codecRoute[routeIndex]
			if state, ok := decodePartyUDPBodyWithCodec(body, sender, expectedRoute, codec); ok {
				codec.extra = decodePartyUDPExtra(body, codec)
				return state, codec, expectedRoute, true
			}
		}
	}
	for rotate := 0; rotate < 8; rotate++ {
		codec := partyUDPCodec{key: bits.RotateLeft8(body[7], -rotate) ^ sender, rotate: byte(rotate)}
		for _, expectedRoute := range routes {
			if state, ok := decodePartyUDPBodyWithCodec(body, sender, expectedRoute, codec); ok {
				codec.extra = decodePartyUDPExtra(body, codec)
				return state, codec, expectedRoute, true
			}
		}
	}
	return 0, partyUDPCodec{}, 0, false
}

func decodePartyUDPBodyWithCodec(body []byte, sender, route byte, codec partyUDPCodec) (byte, bool) {
	decoded := bits.RotateLeft8(body[7], -int(codec.rotate)) ^ codec.key
	state := bits.RotateLeft8(body[8], -int(codec.rotate)) ^ codec.key
	gotRoute := bits.RotateLeft8(body[9], -int(codec.rotate)) ^ codec.key
	if decoded != sender || state > 3 || gotRoute != route {
		return 0, false
	}
	extra := decodePartyUDPExtra(body, codec)
	checksum := partyUDPChecksum(sender, state, route, extra)
	return state, bytes.Equal(body[3:7], checksum[:])
}

func decodePartyUDPExtra(body []byte, codec partyUDPCodec) [2]byte {
	return [2]byte{
		bits.RotateLeft8(body[10], -int(codec.rotate)) ^ codec.key,
		bits.RotateLeft8(body[11], -int(codec.rotate)) ^ codec.key,
	}
}

func buildPartyUDP(sequence uint32, sender, state, route byte, codec partyUDPCodec) []byte {
	typ, bodyLen, offset := byte(2), 12, 9
	if state == 2 {
		typ, bodyLen, offset = 1, 14, 11
	}
	out := make([]byte, 9+bodyLen)
	out[0] = typ
	binary.LittleEndian.PutUint32(out[1:5], sequence)
	binary.LittleEndian.PutUint16(out[5:7], uint16(bodyLen))
	out[7] = sender
	if typ == 1 {
		binary.LittleEndian.PutUint16(out[9:11], 12)
	}
	body := out[offset:]
	checksum := partyUDPChecksum(sender, state, route, codec.extra)
	copy(body[3:7], checksum[:])
	for i, value := range []byte{sender, state, route, codec.extra[0], codec.extra[1]} {
		body[7+i] = bits.RotateLeft8(value^codec.key, int(codec.rotate))
	}
	return out
}

func partyUDPAck(sender byte, sequence uint32) []byte {
	out := make([]byte, 8)
	out[1] = sender
	binary.LittleEndian.PutUint32(out[2:6], sequence+1)
	return out
}

func partyUDPChecksum(sender, state, route byte, extra [2]byte) [4]byte {
	value := crc32.Checksum([]byte{sender, state, route, extra[0], extra[1]}, partyUDPCRCTable)
	var out [4]byte
	binary.LittleEndian.PutUint32(out[:], value)
	return out
}
