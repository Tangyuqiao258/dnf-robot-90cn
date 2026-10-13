package cn90

import (
	"context"
	"encoding/binary"
	"os"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// Class0 notices the online party bridge sends (dnfbridge party_online.go).
const (
	// notiResponsePeer is the acceptance/rejection notice delivered to the
	// inviter: u16 responderID + u8 mode + u32 value.
	notiResponsePeer uint16 = 8
	// Right after a party forms every member receives the runtime projection:
	// member state (0x0099) followed by member endpoints (0x000b).
	notiRuntimePartyState uint16 = 0x0099
	notiRuntimePartyPeers uint16 = 0x000b
)

// TestLivePartyAutoAccept proves the 90CN session answers a forwarded party
// invite without operator input: the inviter sends op10, the robot session
// (opened with EnablePartyAutoAccept) answers op11 on its own, and the
// server's acceptance notice plus the runtime party snapshots reach the
// inviter. Both test characters are provisioned and removed by the test.
func TestLivePartyAutoAccept(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live party auto-accept")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	adminAddress, adminToken, err := serverLayout.adminEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	channels, _, err := resolveChannelCatalog(serverLayout, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !liveListener(t, channels) {
		t.Skipf("no live 90CN game listener among %v", channels.ports)
	}
	const (
		robotAccount   = "robot17000003"
		robotName      = "组队机器人甲"
		robotUID       = 17000003
		inviterAccount = "robot17000004"
		inviterName    = "组队邀请者乙"
		inviterUID     = 17000004
		// Both sessions must stand in the same town area: the server forwards
		// the op10 prompt only to a target in the inviter's area
		// (onlinePlayers.PeerInSameArea).
		village = 38
		area    = 1
	)
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	slots := make(map[string]uint16, 2)
	for _, member := range []struct {
		account string
		name    string
		uid     int
		job     int
	}{
		{robotAccount, robotName, robotUID, 0},
		{inviterAccount, inviterName, inviterUID, 1},
	} {
		if err := liveClearAccount(ctx, binder, channels, member.account); err != nil {
			t.Fatalf("pre-clean %s: %v", member.account, err)
		}
		result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
			AccountName: member.account, CharacterName: member.name, Job: member.job, RobotUID: member.uid,
		})
		if err != nil {
			t.Fatalf("provision %s: %v", member.account, err)
		}
		if !result.Created || result.BackendSlot == nil {
			t.Fatalf("provision result for %s = %+v", member.account, result)
		}
		slots[member.account] = *result.BackendSlot
	}

	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	robotSession, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: robotAccount, CharacterSlot: slots[robotAccount],
		EnablePartyAutoAccept: true,
		InitialTownKnown:      true, InitialVillage: village, InitialArea: area, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open robot session: %v", err)
	}
	robot, ok := robotSession.(*Session)
	if !ok {
		t.Fatal("unexpected robot session type")
	}
	robotClosed := false
	defer func() {
		if !robotClosed {
			_ = robotSession.Close()
		}
	}()
	t.Logf("robot online: account=%s cid=%d", robotAccount, robot.selfCharacterID)

	inviterSession, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: inviterAccount, CharacterSlot: slots[inviterAccount],
		InitialTownKnown: true, InitialVillage: village, InitialArea: area, InitialX: 480, InitialY: 260,
	})
	if err != nil {
		t.Fatalf("open inviter session: %v", err)
	}
	inviter, ok := inviterSession.(*Session)
	if !ok {
		t.Fatal("unexpected inviter session type")
	}
	inviterClosed := false
	defer func() {
		if !inviterClosed {
			_ = inviterSession.Close()
		}
	}()
	t.Logf("inviter online: account=%s cid=%d", inviterAccount, inviter.selfCharacterID)

	packets := make(chan protocol.Packet, 64)
	cleanup := inviter.setPacketObserver(func(packet protocol.Packet) {
		select {
		case packets <- packet:
		default:
		}
	})
	defer cleanup()

	// The inviter sends the quick-party invite (mode 13) to the robot, the
	// same shape the server's own bridge test uses.
	if err := inviter.client.RequestPeer(ctx, robot.selfCharacterID, 13, 0, 0); err != nil {
		t.Fatalf("send party invite: %v", err)
	}

	accepted := false
	formed := false
	deadline := time.After(15 * time.Second)
	for !accepted || !formed {
		select {
		case packet := <-packets:
			switch {
			case packet.Class == protocol.ClassNotice && packet.Type == notiResponsePeer && len(packet.Body) >= 7:
				responder := binary.LittleEndian.Uint16(packet.Body[:2])
				value := binary.LittleEndian.Uint32(packet.Body[3:7])
				t.Logf("peer response notice: responder=%d mode=%d value=%d", responder, packet.Body[2], value)
				if responder == robot.selfCharacterID && packet.Body[2] == 13 && value == 1 {
					accepted = true
				}
			case packet.Type == notiRuntimePartyState || packet.Type == notiRuntimePartyPeers:
				t.Logf("runtime party snapshot: class=%d type=0x%04x len=%d", packet.Class, packet.Type, len(packet.Body))
				formed = true
			}
		case <-deadline:
			t.Fatalf("party was not formed: accepted=%t formed=%t", accepted, formed)
		}
	}
	t.Logf("party auto-accept verified: robot cid=%d accepted the mode-13 invite and the runtime snapshots arrived", robot.selfCharacterID)

	if err := robotSession.Close(); err != nil {
		t.Fatalf("close robot session: %v", err)
	}
	robotClosed = true
	if err := inviterSession.Close(); err != nil {
		t.Fatalf("close inviter session: %v", err)
	}
	inviterClosed = true

	// Remove the test characters so the operator's database stays clean.
	for _, account := range []string{robotAccount, inviterAccount} {
		if err := liveClearAccount(ctx, binder, channels, account); err != nil {
			t.Fatalf("cleanup %s: %v", account, err)
		}
	}
	t.Logf("test characters removed")
}
