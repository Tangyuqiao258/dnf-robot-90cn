package cn90

import (
	"context"
	"os"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// TestLivePartyFollow proves the follower half of town party follow against the
// real server: the leader's ordinary town walk must reach the follower as a
// class0/0x0016 broadcast, and the follower must decode it into a walk target.
//
// The open question this answers is the server's own behaviour, not the
// client's: dnfbridge/town_position.go broadcasts a mover to every player
// sharing its (town, area) key with no distance or party check, so a party
// member that is nowhere near the leader should still receive it. Nothing in
// the client can confirm that without a live broadcast to observe.
func TestLivePartyFollow(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live party follow")
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
		followerAccount = "robot17000005"
		followerName    = "跟随者丙"
		followerUID     = 17000005
		leaderAccount   = "robot17000006"
		leaderName      = "队长丁"
		leaderUID       = 17000006
		// Both sessions must share the town area: the server forwards the op10
		// prompt to same-area peers, and it broadcasts 0x0016 to the same key.
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
		{followerAccount, followerName, followerUID, 0},
		{leaderAccount, leaderName, leaderUID, 1},
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
	followerSession, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: followerAccount, CharacterSlot: slots[followerAccount],
		EnablePartyAutoAccept: true,
		EnablePartyFollow:     true,
		InitialTownKnown:      true, InitialVillage: village, InitialArea: area, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open follower session: %v", err)
	}
	follower, ok := followerSession.(*Session)
	if !ok {
		t.Fatal("unexpected follower session type")
	}
	followerClosed := false
	defer func() {
		if !followerClosed {
			_ = followerSession.Close()
		}
	}()
	t.Logf("follower online: account=%s cid=%d", followerAccount, follower.selfCharacterID)

	leaderSession, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: leaderAccount, CharacterSlot: slots[leaderAccount],
		InitialTownKnown: true, InitialVillage: village, InitialArea: area, InitialX: 480, InitialY: 260,
	})
	if err != nil {
		t.Fatalf("open leader session: %v", err)
	}
	leader, ok := leaderSession.(*Session)
	if !ok {
		t.Fatal("unexpected leader session type")
	}
	leaderClosed := false
	defer func() {
		if !leaderClosed {
			_ = leaderSession.Close()
		}
	}()
	t.Logf("leader online: account=%s cid=%d", leaderAccount, leader.selfCharacterID)

	// The leader watches every class0 notice so the follower's own walk can be
	// confirmed from the other side of the broadcast.
	leaderPackets := make(chan protocol.Packet, 128)
	cleanupLeader := leader.setPacketObserver(func(packet protocol.Packet) {
		select {
		case leaderPackets <- packet:
		default:
		}
	})
	defer cleanupLeader()

	// The leader invites; the follower's auto-accept answers op11 and the server
	// makes the inviter slot 0, which is what names it the follow leader.
	if err := leader.client.RequestPeer(ctx, follower.selfCharacterID, 13, 0, 0); err != nil {
		t.Fatalf("send party invite: %v", err)
	}

	partyFormed := false
	waitParty := time.After(20 * time.Second)
	for !partyFormed {
		select {
		case packet := <-leaderPackets:
			if packet.Class == protocol.ClassNotice && (packet.Type == notiRuntimePartyState || packet.Type == notiRuntimePartyPeers) {
				t.Logf("runtime party snapshot: type=0x%04x len=%d", packet.Type, len(packet.Body))
				partyFormed = true
			}
		case <-waitParty:
			t.Fatal("party was not formed before the follow check")
		}
	}

	if _, ok := follower.PartyFollowTarget(); ok {
		t.Fatal("follower had a walk target before the leader moved")
	}

	// The leader walks. Any ordinary op35 makes the server broadcast 0x0016 to
	// the other players in the area.
	const (
		leaderX = 612
		leaderY = 288
	)
	if err := leaderSession.(*Session).MoveTown(ctx, shared.TownMoveIntent{X: leaderX, Y: leaderY, Direction: 3, Motion: 0}); err != nil {
		t.Fatalf("leader move: %v", err)
	}

	var target shared.PartyFollowTarget
	tracked := false
	waitTrack := time.After(20 * time.Second)
	for !tracked {
		select {
		case <-waitTrack:
			t.Fatalf("follower never tracked the leader walk: target=%+v tracked=%t", target, tracked)
		default:
		}
		target, tracked = follower.PartyFollowTarget()
		if tracked {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if target.CharacterID != int(leader.selfCharacterID) {
		t.Fatalf("follower tracked cid=%d want the leader %d", target.CharacterID, leader.selfCharacterID)
	}
	if target.X != leaderX || target.Y != leaderY {
		t.Fatalf("follower tracked x=%d y=%d want x=%d y=%d", target.X, target.Y, leaderX, leaderY)
	}
	t.Logf("follow verified: follower cid=%d tracked leader cid=%d at %d,%d", follower.selfCharacterID, target.CharacterID, target.X, target.Y)

	// Finally the follower walks to the tracked spot. The leader must see the
	// follower's broadcast, which proves the server accepts the follower's op35
	// while it is in the party.
	const (
		followX = 570
		followY = 276
	)
	if err := followerSession.(*Session).MoveTown(ctx, shared.TownMoveIntent{X: followX, Y: followY, Direction: 3, Motion: 0}); err != nil {
		t.Fatalf("follower move: %v", err)
	}

	sawFollower := false
	waitFollower := time.After(20 * time.Second)
	for !sawFollower {
		select {
		case packet := <-leaderPackets:
			if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiUserPosition {
				continue
			}
			notice, ok := protocol.ParseUserPositionNotice(packet.Body)
			if ok && notice.ActorKey == follower.selfCharacterID {
				t.Logf("leader saw follower broadcast: cid=%d x=%d y=%d", notice.ActorKey, notice.X, notice.Y)
				sawFollower = true
			}
		case <-waitFollower:
			t.Fatal("leader never saw the follower's town broadcast")
		}
	}

	if err := followerSession.Close(); err != nil {
		t.Fatalf("close follower session: %v", err)
	}
	followerClosed = true
	if err := leaderSession.Close(); err != nil {
		t.Fatalf("close leader session: %v", err)
	}
	leaderClosed = true

	for _, account := range []string{followerAccount, leaderAccount} {
		if err := liveClearAccount(ctx, binder, channels, account); err != nil {
			t.Fatalf("cleanup %s: %v", account, err)
		}
	}
	t.Logf("test characters removed")
}
