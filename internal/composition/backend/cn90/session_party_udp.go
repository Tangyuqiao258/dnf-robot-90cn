package cn90

import (
	"context"

	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// Party P2P UDP transport.
//
// The party HUD keeps a member on "connecting" until the client's TQOS
// handshake to that member's UDP endpoint completes, and the endpoints travel
// in class0/op11 PARTY_IP_INFO. The protocol client owns the socket and the
// TQOS answering; this adapter only feeds it the two notices it needs: op153
// carries the session's own party slot (the sender byte of every control
// frame) and op11 carries the peer endpoints to announce ourselves to.
//
// 参考: reference/RobotS4A21/internal/protocol/s4a21/party_udp.go
// 参考: reference/ServerS5A3-16ww/go-server/internal/services/dnfbridge/party_online.go:792
// (op153 -> op11 -> op9 snapshot order)

// startPartyUDP installs the observer that feeds the P2P transport. It runs for
// the life of the session; the transport itself is bound during session open.
func (s *Session) startPartyUDP(ctx context.Context) {
	if s == nil || s.client == nil || ctx == nil {
		return
	}
	cleanup := s.setPacketObserver(s.observePartyUDP)
	go func() {
		defer cleanup()
		<-ctx.Done()
	}()
}

func (s *Session) observePartyUDP(packet protocol.Packet) {
	if s == nil || s.client == nil || packet.Class != protocol.ClassNotice {
		return
	}
	switch packet.Type {
	case protocol.NotiPartyMemberRealtime:
		s.client.ApplyPartyRealtimeInfo(packet.Body)
	case protocol.NotiPartyPeerEndpoints:
		sent, ok := s.client.ApplyPartyPeerEndpoints(packet.Body)
		if !ok {
			foundationlog.Robotf("CN90_PARTY_UDP_ENDPOINTS_REJECTED cid=%d body_len=%d\n",
				s.selfCharacterID, len(packet.Body))
			return
		}
		slot, slotKnown := s.client.PartyUDPSlot()
		foundationlog.Robotf("CN90_PARTY_UDP_ENDPOINTS cid=%d local_port=%d announced=%d slot=%d slot_known=%t\n",
			s.selfCharacterID, s.client.PartyUDPPort(), sent, slot, slotKnown)
	}
}
