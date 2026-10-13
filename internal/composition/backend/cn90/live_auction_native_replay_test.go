package cn90

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// TestLiveNativeAuctionReplay replays the exact wire shapes the current 90CN
// client sends to the auction house against the running lab server, using a
// real robot account with real inventory:
//
//   - op186 typed item search with the captured client body
//     (CN90_REPLAY_SEARCH_HEX, 98 bytes, keys at offset 14);
//   - op183 registration that repeats the typed per-unit price in both the
//     buyout and the unit-price field (the shape that used to be rejected);
//   - op186 search for the registered item, which must now show the listing
//     with the derived lot total;
//   - op184 cancel to hand the stack back.
func TestLiveNativeAuctionReplay(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live auction replay")
	}
	searchHex := strings.TrimSpace(os.Getenv("CN90_REPLAY_SEARCH_HEX"))
	if searchHex == "" {
		t.Skip("set CN90_REPLAY_SEARCH_HEX to the captured op186 request body")
	}
	searchBody, err := hex.DecodeString(searchHex)
	if err != nil {
		t.Fatalf("decode captured search body: %v", err)
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
	if len(channels.ports) == 0 {
		t.Fatal("no live channel port resolved")
	}

	// Account robot17001013 -> 旅人青凌夜朝 (roster slot 0). Its main
	// inventory holds item 3027 in slot 114, the same item the player tried to
	// list in the captured request.
	const (
		account       = "robot17001013"
		characterSlot = 0
		invSlot       = 114
		itemID        = 3027
		count         = 3
		unitPrice     = 9
		hours         = 24
		townVillage   = 38
		townArea      = 0
		townX         = 1782
		townY         = 200
	)

	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := binder.Register(ctx, account); err != nil {
		t.Fatalf("bind %s: %v", account, err)
	}
	address := fmt.Sprintf("127.0.0.1:%d", channels.ports[0])
	conn, err := (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}
	defer func() { _ = conn.Close() }()

	client := protocol.NewClient(conn)
	first, err := client.Read(ctx)
	if err != nil {
		t.Fatalf("first packet: %v", err)
	}
	if _, err := client.CompleteHandshakeFrom(ctx, &first); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	t.Logf("connected %s as %s", address, account)

	sequence := uint16(120)
	send := func(op uint16, body []byte, label string) {
		t.Helper()
		sequence++
		if _, err := conn.Write(protocol.EncodeLegacy(op, body, sequence)); err != nil {
			t.Fatalf("send %s: %v", label, err)
		}
		t.Logf("sent %s op=%d len=%d", label, op, len(body))
	}

	// Enter the character and the town its row records, the way the client
	// does before any auction interaction.
	if err := client.RequestRoster(ctx); err != nil {
		t.Fatalf("roster: %v", err)
	}
	replayDrain(ctx, client, 2*time.Second)
	if err := client.SelectCharacter(ctx, characterSlot); err != nil {
		t.Fatalf("select character: %v", err)
	}
	replayDrain(ctx, client, 2*time.Second)
	if err := client.ProgressInitialTown(ctx); err != nil {
		t.Fatalf("tutorial progress: %v", err)
	}
	replayDrain(ctx, client, 2*time.Second)
	if err := client.SetUserArea(ctx, townVillage, townArea, townX, townY, 5); err != nil {
		t.Fatalf("enter town: %v", err)
	}
	replayDrain(ctx, client, 2*time.Second)

	// 1) The player's captured typed search, byte for byte.
	send(186, searchBody, "captured-search-by-itemkey")
	searchReply, ok := replayWait(ctx, client, 186, 8*time.Second)
	if !ok {
		t.Fatalf("no op186 reply to the captured search body")
	}
	if len(searchReply.Body) == 0 || searchReply.Body[0] != 1 {
		t.Fatalf("captured search rejected: %x", headBytes(searchReply.Body, 24))
	}
	total := binary.LittleEndian.Uint32(searchReply.Body[2:6])
	rows := int(binary.LittleEndian.Uint16(searchReply.Body[6:8]))
	t.Logf("captured search accepted: total=%d pageRows=%d bodyLen=%d", total, rows, len(searchReply.Body))

	// 2) Native registration: the typed per-unit price in both slots.
	registerBody := make([]byte, 37)
	registerBody[0] = 0
	registerBody[1] = 0
	binary.LittleEndian.PutUint16(registerBody[2:4], invSlot)
	binary.LittleEndian.PutUint32(registerBody[4:8], itemID)
	binary.LittleEndian.PutUint32(registerBody[8:12], count)
	binary.LittleEndian.PutUint32(registerBody[12:16], 0xFFFFFFFF)
	binary.LittleEndian.PutUint32(registerBody[16:20], unitPrice)
	binary.LittleEndian.PutUint32(registerBody[20:24], unitPrice)
	binary.LittleEndian.PutUint32(registerBody[24:28], hours)
	send(183, registerBody, "native-register-per-unit")
	registerReply, ok := replayWait(ctx, client, 183, 8*time.Second)
	if !ok {
		t.Fatalf("no op183 reply to the native registration")
	}
	if len(registerReply.Body) == 0 || registerReply.Body[0] != 1 {
		t.Fatalf("native registration rejected: %x", headBytes(registerReply.Body, 24))
	}
	t.Logf("native registration accepted: reply=%x", headBytes(registerReply.Body, 8))

	// 3) Search the item back: the listing must carry the derived lot total.
	if err := client.AuctionSearchByItem(ctx, 0, []uint32{itemID}); err != nil {
		t.Fatalf("search by item: %v", err)
	}
	listingID, lotTotal, lotCount, found := replayFindListing(ctx, client, itemID)
	if !found {
		t.Fatalf("registered listing for item %d is not visible to search", itemID)
	}
	t.Logf("search shows listing=%d item=%d count=%d buyout=%d", listingID, itemID, lotCount, lotTotal)
	if lotTotal != unitPrice*count || lotCount != count {
		t.Fatalf("registered lot = count %d buyout %d, want count %d buyout %d", lotCount, lotTotal, count, unitPrice*count)
	}

	// 4) Hand the stack back.
	if err := client.AuctionCancel(ctx, listingID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	cancelReply, ok := replayWait(ctx, client, 184, 8*time.Second)
	if !ok || len(cancelReply.Body) == 0 || cancelReply.Body[0] != 1 {
		t.Fatalf("cancel rejected: %x", headBytes(cancelReply.Body, 24))
	}
	t.Logf("cancel accepted for listing %d", listingID)
	replayDrain(ctx, client, time.Second)
}

// replayDrain reads and discards packets for the given window.
func replayDrain(ctx context.Context, client *protocol.Client, window time.Duration) {
	deadline := time.Now().Add(window)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		readCtx, cancel := context.WithTimeout(ctx, remaining)
		_, err := client.Read(readCtx)
		cancel()
		if err != nil {
			return
		}
	}
}

// replayWait reads until one packet carries the opcode, or the window expires.
func replayWait(ctx context.Context, client *protocol.Client, op uint16, window time.Duration) (protocol.Packet, bool) {
	deadline := time.Now().Add(window)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return protocol.Packet{}, false
		}
		readCtx, cancel := context.WithTimeout(ctx, remaining)
		packet, err := client.Read(readCtx)
		cancel()
		if err != nil {
			return protocol.Packet{}, false
		}
		if packet.Type == op {
			return packet, true
		}
	}
}

// replayFindListing searches the last op186 reply page for the item and returns
// the newest matching listing row.
func replayFindListing(ctx context.Context, client *protocol.Client, itemID uint32) (uint64, uint32, uint32, bool) {
	reply, ok := replayWait(ctx, client, 186, 8*time.Second)
	if !ok || len(reply.Body) < 8 || reply.Body[0] != 1 {
		return 0, 0, 0, false
	}
	rows := int(binary.LittleEndian.Uint16(reply.Body[6:8]))
	var (
		lastID    uint64
		lastTotal uint32
		lastCount uint32
		found     bool
	)
	// The server orders search rows by creation time ascending, so the newest
	// listing for the item is the last matching row.
	for i := 0; i < rows; i++ {
		start := 8 + i*168
		if start+168 > len(reply.Body) {
			break
		}
		row := reply.Body[start : start+168]
		if binary.LittleEndian.Uint32(row[44:48]) != itemID {
			continue
		}
		lastID = binary.LittleEndian.Uint64(row[0:8])
		lastTotal = binary.LittleEndian.Uint32(row[13:17])
		lastCount = binary.LittleEndian.Uint32(row[49:53])
		found = true
	}
	return lastID, lastTotal, lastCount, found
}

func headBytes(body []byte, n int) []byte {
	if len(body) < n {
		return body
	}
	return body[:n]
}
