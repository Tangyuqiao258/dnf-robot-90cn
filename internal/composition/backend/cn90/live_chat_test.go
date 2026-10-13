package cn90

import (
	"context"
	"os"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// liveChatNoticeChannel buffers the delivery notices a test session observes.
const liveChatNoticeBuffer = 16

// liveChatNotices registers an observer that decodes every class0/op118 notice
// the session receives. The session's own chat tracker only keeps the last
// time it answered each sender, so the message itself has to be read here.
func liveChatNotices(session *Session) (<-chan protocol.PeerChatNotice, func()) {
	notices := make(chan protocol.PeerChatNotice, liveChatNoticeBuffer)
	cleanup := session.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassNotice || packet.Type != protocol.NotiPeerChatMessage {
			return
		}
		notice, ok := protocol.ParsePeerChatNotice(packet.Body)
		if !ok {
			return
		}
		select {
		case notices <- notice:
		default:
		}
	})
	return notices, cleanup
}

// waitLiveChatNotice waits for one delivery notice from the named sender.
func waitLiveChatNotice(t *testing.T, notices <-chan protocol.PeerChatNotice, senderID uint16, timeout time.Duration) protocol.PeerChatNotice {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case notice := <-notices:
			if notice.SenderID == senderID {
				return notice
			}
		case <-deadline:
			t.Fatalf("no chat arrived from character %d within %s", senderID, timeout)
		}
	}
}

// TestLiveSameAreaChat proves directed chat against the real server in both
// directions: one session sends an op17 request to a character that shares its
// town area and the server delivers it as a class0/op118 notice, then the
// receiving session's own auto-reply travels back the same way.
//
// The receiving session is the one that proves delivery. A class1/op17
// acknowledgement only says the server accepted the request; it is also what a
// request gets when the target is silently dropped, so the notice on the far
// side is the only evidence the message actually arrived.
func TestLiveSameAreaChat(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live same-area chat")
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
		senderAccount = "robot17000011"
		senderName    = "聊天甲"
		senderUID     = 17000011
		peerAccount   = "robot17000012"
		peerName      = "聊天乙"
		peerUID       = 17000012
		// The server forwards only between characters sharing a (town, area)
		// key, so both sessions are placed in the same one.
		village = 38
		area    = 1
	)
	// The canned line the peer answers with, so the reply can be told apart from
	// anything else on the wire.
	const peerReply = "你好呀"

	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	defer cancel()

	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	slots := make(map[string]uint16, 2)
	for _, member := range []struct {
		account string
		name    string
		uid     int
		job     int
	}{
		{senderAccount, senderName, senderUID, 0},
		{peerAccount, peerName, peerUID, 1},
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
	// The sender never answers anything: this test is one exchange, and a
	// replying sender would hide which side produced which line.
	senderSession, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: senderAccount, CharacterSlot: slots[senderAccount],
		InitialTownKnown: true, InitialVillage: village, InitialArea: area, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open sender session: %v", err)
	}
	sender, ok := senderSession.(*Session)
	if !ok {
		t.Fatal("unexpected sender session type")
	}
	senderClosed := false
	defer func() {
		if !senderClosed {
			_ = senderSession.Close()
		}
	}()
	t.Logf("sender online: account=%s cid=%d", senderAccount, sender.selfCharacterID)

	// The peer answers a message from its own template pool.
	peerSession, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: peerAccount, CharacterSlot: slots[peerAccount],
		EnableChatReply: true, ChatReplies: []string{peerReply},
		InitialTownKnown: true, InitialVillage: village, InitialArea: area, InitialX: 480, InitialY: 260,
	})
	if err != nil {
		t.Fatalf("open peer session: %v", err)
	}
	peer, ok := peerSession.(*Session)
	if !ok {
		t.Fatal("unexpected peer session type")
	}
	peerClosed := false
	defer func() {
		if !peerClosed {
			_ = peerSession.Close()
		}
	}()
	t.Logf("peer online: account=%s cid=%d", peerAccount, peer.selfCharacterID)

	senderNotices, cleanupSender := liveChatNotices(sender)
	defer cleanupSender()
	peerNotices, cleanupPeer := liveChatNotices(peer)
	defer cleanupPeer()

	// Direction one: the peer receives what the sender wrote, attributed to the
	// sender's real name.
	if err := sender.SendChat(ctx, peer.selfCharacterID, peerName, "你好"); err != nil {
		t.Fatalf("directed chat to the peer: %v", err)
	}
	received := waitLiveChatNotice(t, peerNotices, sender.selfCharacterID, 15*time.Second)
	if received.Message != "你好" {
		t.Fatalf("peer received %q want %q", received.Message, "你好")
	}
	if received.SenderName != senderName {
		t.Fatalf("peer saw sender name %q want %q", received.SenderName, senderName)
	}
	t.Logf("peer received cid=%d name=%s text=%q", received.SenderID, received.SenderName, received.Message)

	// Direction two: the peer's session answers on its own, and the answer
	// arrives at the sender through the same route.
	reply := waitLiveChatNotice(t, senderNotices, peer.selfCharacterID, 20*time.Second)
	if reply.Message != peerReply {
		t.Fatalf("sender received %q want %q", reply.Message, peerReply)
	}
	if reply.SenderName != peerName {
		t.Fatalf("sender saw replier name %q want %q", reply.SenderName, peerName)
	}
	t.Logf("sender received cid=%d name=%s text=%q", reply.SenderID, reply.SenderName, reply.Message)

	for _, closed := range []struct {
		name    string
		session shared.RobotSession
		flag    *bool
	}{
		{"sender", senderSession, &senderClosed},
		{"peer", peerSession, &peerClosed},
	} {
		if err := closed.session.Close(); err != nil {
			t.Fatalf("close %s session: %v", closed.name, err)
		}
		*closed.flag = true
	}
	for _, account := range []string{senderAccount, peerAccount} {
		if err := liveClearAccount(ctx, binder, channels, account); err != nil {
			t.Fatalf("cleanup %s: %v", account, err)
		}
	}
	t.Logf("test characters removed")
}

// TestLiveChatToAnAbsentCharacterIsDropped pins the other half of the
// acknowledgement contract: a request the server will not deliver is answered
// with nothing at all, not with a failure. A caller can only tell the two apart
// by its own deadline, which is why SendChat has one.
func TestLiveChatToAnAbsentCharacterIsDropped(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live absent-target chat")
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
		account = "robot17000013"
		name    = "聊天丙"
		uid     = 17000013
	)
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("pre-clean %s: %v", account, err)
	}
	result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
		AccountName: account, CharacterName: name, Job: 0, RobotUID: uid,
	})
	if err != nil {
		t.Fatalf("provision %s: %v", account, err)
	}
	if !result.Created || result.BackendSlot == nil {
		t.Fatalf("provision result for %s = %+v", account, result)
	}

	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	session, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
		AccountName: account, CharacterSlot: *result.BackendSlot,
		InitialTownKnown: true, InitialVillage: 38, InitialArea: 1, InitialX: 450, InitialY: 234,
	})
	if err != nil {
		t.Fatalf("open session: %v", err)
	}
	live, ok := session.(*Session)
	if !ok {
		t.Fatal("unexpected session type")
	}
	closed := false
	defer func() {
		if !closed {
			_ = session.Close()
		}
	}()

	// No such character is online, so the server's area check fails and the
	// request is dropped in silence.
	absentTarget := uint16(live.selfCharacterID + 1)
	if absentTarget == live.selfCharacterID {
		t.Fatal("the absent target id collided with the session's own")
	}
	dropCtx, dropCancel := context.WithTimeout(ctx, 3*time.Second)
	defer dropCancel()
	if err := live.SendChat(dropCtx, absentTarget, "不存在", "你好"); err == nil {
		t.Fatal("a chat to an absent character was acknowledged")
	} else {
		t.Logf("absent-target chat was dropped: %v", err)
	}

	if err := session.Close(); err != nil {
		t.Fatalf("close session: %v", err)
	}
	closed = true
	if err := liveClearAccount(ctx, binder, channels, account); err != nil {
		t.Fatalf("cleanup %s: %v", account, err)
	}
}
