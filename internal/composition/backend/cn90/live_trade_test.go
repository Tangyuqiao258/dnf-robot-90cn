package cn90

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// liveTradeEvent is one trade notice a live session saw, flattened so the test
// can assert on the round without re-deriving anything from the session state.
type liveTradeEvent struct {
	kind string
	peer uint16
	rows int
}

// liveTradeEvents registers an observer that decodes the whole trade notice
// family. The session's own tracker keeps only the window state, so the
// individual rows have to be read here.
func liveTradeEvents(session *Session) (<-chan liveTradeEvent, func()) {
	events := make(chan liveTradeEvent, 64)
	cleanup := session.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassNotice {
			return
		}
		var event liveTradeEvent
		switch packet.Type {
		case protocol.NotiTradeState:
			notice, ok := protocol.ParseTradeStateNotice(packet.Body)
			if !ok {
				return
			}
			event = liveTradeEvent{kind: "state", peer: notice.CharacterID}
		case protocol.NotiTradeFinish:
			notice, ok := protocol.ParseTradeFinishNotice(packet.Body)
			if !ok {
				return
			}
			event = liveTradeEvent{kind: "finish", rows: len(notice.Transfers)}
		case protocol.NotiTradeCancel:
			notice, ok := protocol.ParseTradeCancelNotice(packet.Body)
			if !ok {
				return
			}
			event = liveTradeEvent{kind: "cancel", rows: len(notice.Entries)}
		case protocol.NotiTradeChange:
			notice, ok := protocol.ParseTradeChangeNotice(packet.Body)
			if !ok {
				return
			}
			event = liveTradeEvent{kind: "change", peer: notice.TradeSlot}
		default:
			return
		}
		select {
		case events <- event:
		default:
		}
	})
	return events, cleanup
}

// waitLiveTradeEvent waits for one event of the named kind.
func waitLiveTradeEvent(t *testing.T, events <-chan liveTradeEvent, kind string, timeout time.Duration) liveTradeEvent {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			if event.kind == kind {
				return event
			}
		case <-deadline:
			t.Fatalf("no %s notice arrived within %s", kind, timeout)
		}
	}
}

// liveBagRows reads one character's main-bag rows as slot to item id. Rows are
// written by the server on save, so this is only meaningful while the character
// is offline.
func liveBagRows(t *testing.T, databasePath, characterID string) map[int]int {
	t.Helper()
	db := openPurgeTestDatabase(t, databasePath)
	defer db.Close()
	rows, err := db.Query(
		`SELECT entry_key, item_id FROM `+dnfInventoryItemsTable+` WHERE character_id=? AND collection_name=? AND item_count>0`,
		characterID, cn90InventorySlotCollection)
	if err != nil {
		t.Fatalf("read bag character=%s: %v", characterID, err)
	}
	defer rows.Close()
	held := make(map[int]int)
	for rows.Next() {
		var key string
		var itemID int
		if err := rows.Scan(&key, &itemID); err != nil {
			t.Fatalf("scan bag character=%s: %v", characterID, err)
		}
		// The row key is "list:slot"; only the main inventory is the bag the
		// trade moves through.
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		list, listErr := strconv.Atoi(parts[0])
		slot, slotErr := strconv.Atoi(parts[1])
		if listErr != nil || slotErr != nil || list != cn90MainInventoryList {
			continue
		}
		held[slot] = itemID
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate bag character=%s: %v", characterID, err)
	}
	return held
}

// liveBagCounts reads one character's main-bag totals as item id to the number
// of units held across every row. The trade moves units, not rows: a stack that
// gave one unit away is still there, and a unit received for an item the
// character already holds merges into the row it holds it in, so a comparison
// of row ids alone would report neither side as having changed.
func liveBagCounts(t *testing.T, databasePath, characterID string) map[int]int {
	t.Helper()
	db := openPurgeTestDatabase(t, databasePath)
	defer db.Close()
	rows, err := db.Query(
		`SELECT entry_key, item_id, item_count FROM `+dnfInventoryItemsTable+` WHERE character_id=? AND collection_name=? AND item_count>0`,
		characterID, cn90InventorySlotCollection)
	if err != nil {
		t.Fatalf("read bag character=%s: %v", characterID, err)
	}
	defer rows.Close()
	held := make(map[int]int)
	for rows.Next() {
		var key string
		var itemID, count int
		if err := rows.Scan(&key, &itemID, &count); err != nil {
			t.Fatalf("scan bag character=%s: %v", characterID, err)
		}
		// The row key is "list:slot"; only the main inventory is the bag the
		// trade moves through.
		parts := strings.SplitN(key, ":", 2)
		if len(parts) != 2 {
			continue
		}
		list, listErr := strconv.Atoi(parts[0])
		if listErr != nil || list != cn90MainInventoryList {
			continue
		}
		held[itemID] += count
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate bag character=%s: %v", characterID, err)
	}
	return held
}

// liveCharacterID resolves the account's active character id while it is
// offline, which is the key the bag rows are stored under.
func liveCharacterID(t *testing.T, databasePath, account string) string {
	t.Helper()
	db := openPurgeTestDatabase(t, databasePath)
	defer db.Close()
	row, err := loadCharacterProfileRow(context.Background(), db, account)
	if err != nil {
		t.Fatalf("resolve character %s: %v", account, err)
	}
	return row.characterID
}

// TestLiveItemTrade proves player-to-player item trade against the real server
// end to end: two sessions stand in the same town area, one asks with op10 mode
// 1, the other's session answers on its own, both put a stack of their seeded
// stock into the window, both consent, and the exchange is read back out of the
// database after the characters are offline again.
//
// The database read is the point of the test. Every notice on the wire is
// something the server tells the participants, and the completion notice alone
// would still be true if the rows had been projected without being written. The
// offline bag read is the only evidence that the items actually changed hands.
func TestLiveItemTrade(t *testing.T) {
	if os.Getenv("CN90_LIVE_PROTOCOL") != "1" {
		t.Skip("set CN90_LIVE_PROTOCOL=1 to run the live item trade")
	}
	root := liveRuntimeRoot(t)
	serverLayout, err := resolveRuntimeLayout(root)
	if err != nil {
		t.Fatal(err)
	}
	databasePath, _, err := serverLayout.databasePath()
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
	pvfPath, _, err := serverLayout.pvfPath()
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := ReadCatalogs(pvfPath)
	if err != nil {
		t.Fatal(err)
	}
	const (
		initiatorAccount = "robot17000021"
		initiatorName    = "交易甲"
		initiatorUID     = 17000021
		acceptorAccount  = "robot17000022"
		acceptorName     = "交易乙"
		acceptorUID      = 17000022
		// The server only opens a window while both characters share a
		// (town, area) key, so both sessions are placed in the same one.
		village = 38
		area    = 1
		// How many of the seeded stock rows each session may try to give away.
		offerStacks = 4
	)
	binder := NewAccountBinder(adminAddress, adminToken)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	provisioner := Provisioner{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	members := []struct {
		account string
		name    string
		uid     int
	}{
		{initiatorAccount, initiatorName, initiatorUID},
		{acceptorAccount, acceptorName, acceptorUID},
	}
	slots := make(map[string]uint16, len(members))
	for index, member := range members {
		if err := liveClearAccount(ctx, binder, channels, member.account); err != nil {
			t.Fatalf("pre-clean %s: %v", member.account, err)
		}
		result, err := provisioner.ProvisionCharacter(ctx, shared.ProvisionCharacterRequest{
			AccountName: member.account, CharacterName: member.name, Job: index, RobotUID: member.uid,
		})
		if err != nil {
			t.Fatalf("provision %s: %v", member.account, err)
		}
		if !result.Created || result.BackendSlot == nil {
			t.Fatalf("provision result for %s = %+v", member.account, result)
		}
		slots[member.account] = *result.BackendSlot
	}

	// The trade has nothing to move unless the characters hold stock, and the
	// stock stage writes the bag offline, so it runs before either session
	// connects.
	applier, err := NewSQLiteLoadoutApplier(ctx, databasePath, robotconfig.Default(), catalogs.Equipment, pvfPath, nil)
	if err != nil {
		t.Fatalf("loadout applier: %v", err)
	}
	defer applier.Close()
	// The stock pool is drawn from the projected stackable catalog, which the
	// loadout applier does not read on its own.
	applier.Stackables = catalogs.Stackable
	applier.LevelThresholds = catalogs.LevelThresholds
	for _, member := range members {
		if err := applier.reconcileRobotAuctionStock(ctx, member.account); err != nil {
			t.Fatalf("seed stock for %s: %v", member.account, err)
		}
	}

	characterIDs := make(map[string]string, len(members))
	slotsBefore := make(map[string]map[int]int, len(members))
	countsBefore := make(map[string]map[int]int, len(members))
	for _, member := range members {
		characterIDs[member.account] = liveCharacterID(t, databasePath, member.account)
		slotsBefore[member.account] = liveBagRows(t, databasePath, characterIDs[member.account])
		countsBefore[member.account] = liveBagCounts(t, databasePath, characterIDs[member.account])
		if len(slotsBefore[member.account]) == 0 {
			t.Fatalf("%s has no bag rows after the stock stage", member.account)
		}
		t.Logf("bag before: account=%s cid=%s rows=%d items=%v",
			member.account, characterIDs[member.account], len(slotsBefore[member.account]), countsBefore[member.account])
	}
	// The automatic offer names the first seeded stock row, and both sessions
	// give one unit away, so the two characters must be seeded with different
	// items there or the exchange would cancel out and prove nothing.
	initiatorOffered, ok := slotsBefore[initiatorAccount][cn90AuctionStockBaseSlot]
	if !ok {
		t.Fatalf("%s has no seeded stock at bag row %d", initiatorAccount, cn90AuctionStockBaseSlot)
	}
	acceptorOffered, ok := slotsBefore[acceptorAccount][cn90AuctionStockBaseSlot]
	if !ok {
		t.Fatalf("%s has no seeded stock at bag row %d", acceptorAccount, cn90AuctionStockBaseSlot)
	}
	if initiatorOffered == acceptorOffered {
		t.Fatalf("both characters were seeded with item %d at bag row %d, so the exchange cannot be told apart",
			initiatorOffered, cn90AuctionStockBaseSlot)
	}
	t.Logf("offer rows: initiator gives item %d, acceptor gives item %d", initiatorOffered, acceptorOffered)

	factory := SessionFactory{ConnectHost: "127.0.0.1", Channels: channels, Timeout: 30 * time.Second, Binder: binder}
	openSession := func(account, name string, x, y int) *Session {
		t.Helper()
		session, err := factory.OpenSession(ctx, shared.OpenSessionRequest{
			AccountName: account, CharacterSlot: slots[account],
			EnableTrade: true, TradeOfferStacks: offerStacks, TradeOfferCount: 1,
			InitialTownKnown: true, InitialVillage: village, InitialArea: area, InitialX: x, InitialY: y,
		})
		if err != nil {
			t.Fatalf("open %s session: %v", name, err)
		}
		live, ok := session.(*Session)
		if !ok {
			t.Fatalf("unexpected %s session type", name)
		}
		t.Logf("%s online: account=%s cid=%d", name, account, live.selfCharacterID)
		return live
	}
	initiator := openSession(initiatorAccount, "initiator", 450, 234)
	initiatorClosed := false
	defer func() {
		if !initiatorClosed {
			_ = initiator.Close()
		}
	}()
	// The acceptor is opened only so the two are online together; nothing drives
	// it from the test, which is the point: its window runs on its own.
	acceptor := openSession(acceptorAccount, "acceptor", 480, 260)
	acceptorClosed := false
	defer func() {
		if !acceptorClosed {
			_ = acceptor.Close()
		}
	}()
	if initiator.selfCharacterID == acceptor.selfCharacterID {
		t.Fatal("both sessions resolved to the same character")
	}

	initiatorEvents, cleanupInitiator := liveTradeEvents(initiator)
	defer cleanupInitiator()
	acceptorEvents, cleanupAcceptor := liveTradeEvents(acceptor)
	defer cleanupAcceptor()

	// The request: op10 mode 1, acknowledged only by the class0/0x0008 notice
	// the server sends once the acceptor's session answers.
	beginCtx, beginCancel := context.WithTimeout(ctx, 30*time.Second)
	err = initiator.BeginTrade(beginCtx, acceptor.selfCharacterID)
	beginCancel()
	if err != nil {
		t.Fatalf("begin trade: %v", err)
	}
	if peer, open := initiator.TradeOpen(); !open || peer != acceptor.selfCharacterID {
		t.Fatalf("initiator window = (%d, %v) want (%d, true)", peer, open, acceptor.selfCharacterID)
	}
	t.Logf("initiator window open on %d", acceptor.selfCharacterID)

	// The acceptor's session answered and offered without being told to; the
	// offer is what tells the initiator the window is live on both sides.
	waitLiveTradeEvent(t, initiatorEvents, "change", 30*time.Second)
	if peer, open := acceptor.TradeOpen(); !open || peer != initiator.selfCharacterID {
		t.Fatalf("acceptor window = (%d, %v) want (%d, true)", peer, open, initiator.selfCharacterID)
	}
	t.Logf("acceptor window open on %d", initiator.selfCharacterID)

	// The atomic commit. Both consent on their own once both offers are final,
	// and the completion notice is what proves the exchange was written.
	finish := waitLiveTradeEvent(t, initiatorEvents, "finish", 60*time.Second)
	if finish.rows == 0 {
		t.Fatalf("the initiator completion carried no transfer: %+v", finish)
	}
	t.Logf("initiator received %d traded row(s)", finish.rows)
	peerFinish := waitLiveTradeEvent(t, acceptorEvents, "finish", 60*time.Second)
	if peerFinish.rows == 0 {
		t.Fatalf("the acceptor completion carried no transfer: %+v", peerFinish)
	}
	t.Logf("acceptor received %d traded row(s)", peerFinish.rows)
	if peer, open := initiator.TradeOpen(); open {
		t.Fatalf("initiator window stayed open on %d after the commit", peer)
	}

	// Take both characters offline so the server writes their bags out.
	for _, closed := range []struct {
		name    string
		session *Session
		flag    *bool
	}{
		{"initiator", initiator, &initiatorClosed},
		{"acceptor", acceptor, &acceptorClosed},
	} {
		if err := closed.session.Close(); err != nil {
			t.Fatalf("close %s session: %v", closed.name, err)
		}
		*closed.flag = true
	}
	// The channel save is not instantaneous; poll until the exchange shows up.
	var afterInitiator, afterAcceptor map[int]int
	for attempt := 0; attempt < 20; attempt++ {
		afterInitiator = liveBagCounts(t, databasePath, characterIDs[initiatorAccount])
		afterAcceptor = liveBagCounts(t, databasePath, characterIDs[acceptorAccount])
		if afterInitiator[acceptorOffered] > countsBefore[initiatorAccount][acceptorOffered] &&
			afterAcceptor[initiatorOffered] > countsBefore[acceptorAccount][initiatorOffered] {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("bag after: initiator items=%v acceptor items=%v", afterInitiator, afterAcceptor)

	// The exchange itself: each side lost exactly one unit of what it offered
	// and gained exactly one unit of what the other offered.
	for _, side := range []struct {
		name            string
		before, after   map[int]int
		offered, wanted int
	}{
		{"initiator", countsBefore[initiatorAccount], afterInitiator, initiatorOffered, acceptorOffered},
		{"acceptor", countsBefore[acceptorAccount], afterAcceptor, acceptorOffered, initiatorOffered},
	} {
		if lost := side.before[side.offered] - side.after[side.offered]; lost != 1 {
			t.Fatalf("%s gave away %d units of item %d, want 1 (before=%d after=%d)",
				side.name, lost, side.offered, side.before[side.offered], side.after[side.offered])
		}
		if gained := side.after[side.wanted] - side.before[side.wanted]; gained != 1 {
			t.Fatalf("%s received %d units of item %d, want 1 (before=%d after=%d)",
				side.name, gained, side.wanted, side.before[side.wanted], side.after[side.wanted])
		}
		t.Logf("%s gave one unit of item %d and received one unit of item %d", side.name, side.offered, side.wanted)
	}

	for _, member := range members {
		if err := liveClearAccount(ctx, binder, channels, member.account); err != nil {
			t.Fatalf("cleanup %s: %v", member.account, err)
		}
	}
	t.Logf("test characters removed")
}
