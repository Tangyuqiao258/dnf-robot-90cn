package cn90

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// 参考: reference/RobotS4A21/internal/protocol/s4a21/party_udp_test.go

func TestPartyUDPHandshakeReplies(t *testing.T) {
	peer := &partyUDPPeer{}
	codec := partyUDPCodec{key: 0x7e}
	state3 := buildPartyUDP(9, 1, 3, 1, codec)
	replies := partyUDPReplies(state3, peer, 1, true)
	if len(replies) != 1 || replies[0][0] != 2 || replies[0][7] != 1 {
		t.Fatalf("state3 replies=%X", replies)
	}
	state0 := buildPartyUDP(10, 1, 0, 1, codec)
	replies = partyUDPReplies(state0, peer, 1, true)
	if len(replies) != 1 {
		t.Fatalf("state0 replies=%X", replies)
	}
	state1 := buildPartyUDP(11, 1, 1, 1, codec)
	replies = partyUDPReplies(state1, peer, 1, true)
	if len(replies) != 1 || replies[0][0] != 1 {
		t.Fatalf("state1 replies=%X", replies)
	}
	sequence := binary.LittleEndian.Uint32(replies[0][1:5])
	if len(peer.pending) == 0 || peer.pendingSeq != sequence {
		t.Fatalf("pending state2 sequence=%d peer=%+v", sequence, peer)
	}
	partyUDPReplies(partyUDPAck(1, sequence), peer, 1, true)
	if peer.pending != nil {
		t.Fatal("state2 ACK did not clear pending reply")
	}
}

func TestPartyUDPPeerTableIsBounded(t *testing.T) {
	client := NewClient(nil)
	for i := 0; i < partyUDPMaxPeers+64; i++ {
		client.partyUDP.mu.Lock()
		client.partyUDP.partyUDPPeerLocked(&net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 40000 + i})
		client.partyUDP.mu.Unlock()
	}
	client.partyUDP.mu.Lock()
	count := len(client.partyUDP.peers)
	overflow := client.partyUDP.partyUDPPeerLocked(&net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1})
	client.partyUDP.mu.Unlock()
	if count != partyUDPMaxPeers {
		t.Fatalf("peer table size = %d, want %d", count, partyUDPMaxPeers)
	}
	if overflow != nil {
		t.Fatal("peer table accepted a peer beyond the limit")
	}
}

func TestPartyUDPEchoIsNotReturned(t *testing.T) {
	peer := &partyUDPPeer{}
	if got := partyUDPReplies([]byte("not-a-tqos-frame"), peer, 1, true); len(got) != 0 {
		t.Fatalf("invalid datagram produced replies=%X", got)
	}
}

func TestPartyRealtimeInfoSelectsSelfSlot(t *testing.T) {
	client := NewClient(nil)
	client.partyUDP.selfUID = 5784
	client.ApplyPartyRealtimeInfo([]byte{
		2,
		1, 0, 100, 0, 0,
		0x98, 0x16, 100, 0, 1,
	})
	if slot, known := client.PartyUDPSlot(); !known || slot != 1 {
		t.Fatalf("party identity slot=%d known=%t", slot, known)
	}
}

func TestPartyUDPWaitsForSelfSlot(t *testing.T) {
	peer := &partyUDPPeer{}
	payload := buildPartyUDP(9, 0, 3, 1, partyUDPCodec{key: 0x7e})
	if replies := partyUDPReplies(payload, peer, 0, false); len(replies) != 0 {
		t.Fatalf("unknown self slot produced replies=%X", replies)
	}
}

func TestPartyUDPA21TwelveByteFrame(t *testing.T) {
	peer := &partyUDPPeer{}
	frame := []byte{0x02, 0x01, 0x00, 0x00, 0x00, 0x0c, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x3a, 0xcf, 0xe7, 0xef, 0x59, 0x5a, 0x58, 0x58, 0x59}
	replies := partyUDPReplies(frame, peer, 1, true)
	if len(replies) != 1 || replies[0][0] != 2 {
		t.Fatalf("A21 12-byte frame replies=%X", replies)
	}
	body := replies[0][9:]
	state, _, _, ok := decodePartyUDPBody(body, 1, 1, &partyUDPPeer{})
	if !ok || state != 0 {
		t.Fatalf("A21 response checksum/codec invalid state=%d ok=%t body=%X", state, ok, body)
	}
}

func TestPartyUDPA21RouteZeroChecksum(t *testing.T) {
	peer := &partyUDPPeer{}
	frame := []byte{0x02, 0x03, 0x00, 0x00, 0x00, 0x0c, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xaf, 0x92, 0x1a, 0xb4, 0x59, 0x5a, 0x59, 0x58, 0x59}
	if replies := partyUDPReplies(frame, peer, 1, true); len(replies) != 1 {
		t.Fatalf("A21 route-zero frame replies=%X", replies)
	}
}

func TestPartyUDPKeepsReliableStatePerRoute(t *testing.T) {
	peer := &partyUDPPeer{}
	codec := partyUDPCodec{key: 0x7e}
	for _, route := range []byte{0, 1} {
		partyUDPReplies(buildPartyUDP(1, 1, 3, route, codec), peer, 1, true)
		partyUDPReplies(buildPartyUDP(2, 1, 0, route, codec), peer, 1, true)
	}
	route0 := partyUDPReplies(buildPartyUDP(3, 1, 1, 0, codec), peer, 1, true)
	route1 := partyUDPReplies(buildPartyUDP(3, 1, 1, 1, codec), peer, 1, true)
	if len(route0) != 1 || len(route1) != 1 || route0[0][0] != 1 || route1[0][0] != 1 {
		t.Fatalf("route replies=%X/%X", route0, route1)
	}
	if binary.LittleEndian.Uint32(route0[0][1:5]) != binary.LittleEndian.Uint32(route1[0][1:5]) {
		t.Fatalf("routes should have independent reliable sequences: %X/%X", route0[0], route1[0])
	}
}

func TestPartyUDPAcksReliableFramesInCombinedDatagram(t *testing.T) {
	peer := &partyUDPPeer{}
	ack := partyUDPAck(0, 7)
	reliable := make([]byte, 9+5)
	reliable[0] = 1
	binary.LittleEndian.PutUint32(reliable[1:5], 12)
	binary.LittleEndian.PutUint16(reliable[5:7], 5)
	reliable[7] = 0
	copy(reliable[9:], []byte{3, 0, 1, 2, 3})
	payload := append(append([]byte{}, ack...), reliable...)
	replies := partyUDPReplies(payload, peer, 1, true)
	if len(replies) != 1 || len(replies[0]) != 8 || replies[0][0] != 0 {
		t.Fatalf("combined reliable replies=%X", replies)
	}
	if got := binary.LittleEndian.Uint32(replies[0][2:6]); got != 13 {
		t.Fatalf("combined reliable ack=%d want=13", got)
	}
}

// TestSetUDPIPPortBodyMatchesCurrentServerLayout pins the 31-byte class1/op2
// body the server's parseCurrentPartyPeerEndpointRegistration accepts: byte 0
// is the character attribute, inner/outer IPv4 follow, the UDP port is
// big-endian and the MTU little-endian, then a length-prefixed token.
// 参考: reference/ServerS5A3-16ww/go-server/internal/services/dnfbridge/party_peer_endpoint.go:27
func TestSetUDPIPPortBodyMatchesCurrentServerLayout(t *testing.T) {
	body := setUDPIPPortBody(net.IPv4(192, 168, 1, 6), 28848, 1472)
	if len(body) != 31 {
		t.Fatalf("registration body length=%d want 31", len(body))
	}
	if body[0] != 5 {
		t.Fatalf("character attribute=%d want 5", body[0])
	}
	if !net.IP(body[1:5]).Equal(net.IPv4(192, 168, 1, 6)) || !net.IP(body[5:9]).Equal(net.IPv4(192, 168, 1, 6)) {
		t.Fatalf("inner/outer ipv4=%v/%v", net.IP(body[1:5]), net.IP(body[5:9]))
	}
	if port := binary.BigEndian.Uint16(body[9:11]); port != 28848 {
		t.Fatalf("port=%d want 28848", port)
	}
	if mtu := binary.LittleEndian.Uint32(body[11:15]); mtu != 1472 {
		t.Fatalf("mtu=%d want 1472", mtu)
	}
	if token := binary.LittleEndian.Uint32(body[15:19]); token != 12 || len(body)-19 != int(token) {
		t.Fatalf("token length=%d body_len=%d", token, len(body))
	}
}

func TestParsePartyPeerEndpoints(t *testing.T) {
	body := make([]byte, 1+2*partyPeerEndpointRowBytes)
	body[0] = 2
	binary.LittleEndian.PutUint16(body[1:3], 7)
	copy(body[3:7], []byte{127, 0, 0, 1})
	copy(body[7:11], []byte{10, 0, 0, 2})
	binary.BigEndian.PutUint16(body[11:13], 30001)
	row2 := body[1+partyPeerEndpointRowBytes:]
	binary.LittleEndian.PutUint16(row2[0:2], 9)
	copy(row2[2:6], []byte{0, 0, 0, 0})
	copy(row2[6:10], []byte{192, 168, 1, 6})
	binary.BigEndian.PutUint16(row2[10:12], 14512)
	endpoints, ok := parsePartyPeerEndpoints(body)
	if !ok || len(endpoints) != 2 {
		t.Fatalf("endpoints=%v ok=%t", endpoints, ok)
	}
	if endpoints[0].uid != 7 || endpoints[0].ip != [4]byte{127, 0, 0, 1} || endpoints[0].port != 30001 {
		t.Fatalf("endpoint[0]=%+v", endpoints[0])
	}
	if endpoints[1].uid != 9 || endpoints[1].ip != [4]byte{192, 168, 1, 6} || endpoints[1].port != 14512 {
		t.Fatalf("endpoint[1]=%+v", endpoints[1])
	}
	if _, ok := parsePartyPeerEndpoints(body[:len(body)-1]); ok {
		t.Fatal("truncated endpoint body accepted")
	}
}

// TestApplyPartyPeerEndpointsAnnouncesPeers drives one op11 exchange through a
// real UDP socket: every remote member (and only remote members) must receive
// the 8-byte transport announcement from the registered port.
func TestApplyPartyPeerEndpointsAnnouncesPeers(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client := NewClient(nil)
	client.partyUDP.mu.Lock()
	client.partyUDP.conn = listener
	client.partyUDP.selfUID = 7
	client.partyUDP.selfSlot = 2
	client.partyUDP.slotKnown = true
	client.partyUDP.mu.Unlock()

	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	peerPort := uint16(peer.LocalAddr().(*net.UDPAddr).Port)
	body := make([]byte, 1+2*partyPeerEndpointRowBytes)
	body[0] = 2
	binary.LittleEndian.PutUint16(body[1:3], 7)
	copy(body[3:7], []byte{127, 0, 0, 1})
	copy(body[7:11], []byte{127, 0, 0, 1})
	binary.BigEndian.PutUint16(body[11:13], 1234)
	row2 := body[1+partyPeerEndpointRowBytes:]
	binary.LittleEndian.PutUint16(row2[0:2], 9)
	copy(row2[2:6], []byte{127, 0, 0, 1})
	copy(row2[6:10], []byte{127, 0, 0, 1})
	binary.BigEndian.PutUint16(row2[10:12], peerPort)

	// The relay flush in the server is what the first announcement unblocks, so
	// the test reads it back before the second exchange.
	sent, ok := client.ApplyPartyPeerEndpoints(body)
	if !ok || sent != 1 {
		t.Fatalf("announcements=%d ok=%t", sent, ok)
	}
	_ = peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	buffer := make([]byte, 32)
	n, _, err := peer.ReadFromUDP(buffer)
	if err != nil {
		t.Fatalf("peer did not receive the announcement: %v", err)
	}
	want := partyUDPAck(2, 0)
	if n != len(want) || string(buffer[:n]) != string(want) {
		t.Fatalf("announcement=%X want=%X", buffer[:n], want)
	}
	// The same snapshot inside the refresh window must not repeat.
	if sent, _ := client.ApplyPartyPeerEndpoints(body); sent != 0 {
		t.Fatalf("repeated snapshot sent %d announcements", sent)
	}
}
