package cn90

import (
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// auctionTestRow builds one 168-byte search row: listing id at +0, buyout at
// +13, the seller name at +21 and the compact item row at +43.
func auctionTestRow(listingID uint64, buyout uint32, seller string, itemID, count uint32) []byte {
	row := make([]byte, 168)
	binary.LittleEndian.PutUint64(row[0:8], listingID)
	binary.LittleEndian.PutUint32(row[13:17], buyout)
	copy(row[21:34], seller)
	copy(row[43:164], auctionTestItemRaw(itemID, count))
	return row
}

// auctionTestSearchAnswer builds the search response: ok/code, the total, a
// u16 row count and the rows.
func auctionTestSearchAnswer(rows ...[]byte) []byte {
	answer := []byte{1, 0}
	answer = binary.LittleEndian.AppendUint32(answer, uint32(len(rows)))
	answer = binary.LittleEndian.AppendUint16(answer, uint16(len(rows)))
	for _, row := range rows {
		answer = append(answer, row...)
	}
	return answer
}

// auctionTestListingAnswer builds the op188 answer for the session's own live
// listings: ok/code, a u8 row count and 151-byte rows.
func auctionTestListingAnswer(rows ...[]byte) []byte {
	answer := []byte{1, 0, byte(len(rows))}
	for _, row := range rows {
		answer = append(answer, row...)
	}
	return answer
}

// auctionTestMineRow builds one own-listing row: the compact item row at +30.
func auctionTestMineRow(itemID, count uint32) []byte {
	row := make([]byte, 151)
	copy(row[30:151], auctionTestItemRaw(itemID, count))
	return row
}

// auctionTestCatalog is the stock pool with a price on every entry, so the
// plan always resolves a reference price.
func auctionTestCatalog() []shared.EquipmentCatalogItem {
	catalog := make([]shared.EquipmentCatalogItem, 0, len(cn90AuctionStockPool))
	for _, id := range cn90AuctionStockPool {
		catalog = append(catalog, shared.EquipmentCatalogItem{
			ID: id, Path: fmt.Sprintf("stackable/item_%d", id), Price: 1000,
		})
	}
	return catalog
}

// auctionTestTransport attaches one piped session and the catalog, so a test
// plays the server side of a whole round.
func auctionTestTransport(t *testing.T, catalog []shared.EquipmentCatalogItem, cid int) (*ActionTransport, *Session, net.Conn) {
	t.Helper()
	session, serverConn := newAuctionTestSession(t)
	transport := NewActionTransport()
	transport.sessions[7] = session
	transport.status[7] = shared.RuntimeStatus{UID: 7, CID: cid, StateName: shared.RuntimeStateRunning}
	transport.stackables = catalog
	return transport, session, serverConn
}

// auctionTestConfig is a round configuration whose intervals are irrelevant:
// the transport is called directly, not through the actor timer.
func auctionTestConfig() robotconfig.RuntimeConfig {
	return robotconfig.RuntimeConfig{
		AuctionEnabled:         true,
		AuctionStockStacks:     cn90AuctionStockDefaultStacks,
		AuctionListPerRobot:    2,
		AuctionListUnitPercent: 130,
		AuctionBuyEnabled:      true,
		AuctionBuyUnitPercent:  100,
		AuctionBuyMaxTotal:     1000000,
	}
}

// auctionScript plays the server side of a round: it answers frames in order
// and reports mismatches to the test that owns it, so no assertion runs on the
// script goroutine.
type auctionScript struct {
	done chan struct{}
	errs chan string
}

func newAuctionScript(run func(*auctionScript)) *auctionScript {
	script := &auctionScript{done: make(chan struct{}), errs: make(chan string, 16)}
	go func() {
		defer close(script.done)
		run(script)
	}()
	return script
}

func (s *auctionScript) fail(format string, args ...any) {
	select {
	case s.errs <- fmt.Sprintf(format, args...):
	default:
	}
}

// expect reads one frame and reports whether it carries want.
func (s *auctionScript) expect(conn net.Conn, want uint16, what string) ([]byte, bool) {
	_, typ, body, ok := readTestLegacyFrame(conn, 5*time.Second)
	if !ok {
		s.fail("%s: no frame arrived", what)
		return nil, false
	}
	if typ != want {
		s.fail("%s: got op%d, want op%d", what, typ, want)
		return nil, false
	}
	return body, true
}

// expectSilence reports a frame that arrives although the round should have
// stopped sending.
func (s *auctionScript) expectSilence(conn net.Conn, what string) {
	if _, typ, _, ok := readTestLegacyFrame(conn, 500*time.Millisecond); ok {
		s.fail("%s: unexpected op%d", what, typ)
	}
}

func (s *auctionScript) finish(t *testing.T) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(10 * time.Second):
		t.Error("the scripted server did not finish")
		return
	}
	close(s.errs)
	for message := range s.errs {
		t.Error(message)
	}
}

func TestAuctionRoundListsStockThenBuysUnderpricedListings(t *testing.T) {
	transport, session, serverConn := auctionTestTransport(t, auctionTestCatalog(), 9)
	script := newAuctionScript(func(s *auctionScript) {
		// The round starts from the character's own live listings.
		if _, ok := s.expect(serverConn, protocol.CmdAuctionMyListings, "round opening"); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionMyListings, Body: auctionTestListingAnswer()})
		// One register per configured listing. The unit price is the reference
		// 1000 marked up by 130 percent, and the buyout is that times the count.
		for index := 0; index < 2; index++ {
			body, ok := s.expect(serverConn, protocol.CmdAuctionRegister, fmt.Sprintf("listing %d", index))
			if !ok {
				return
			}
			if unit := binary.LittleEndian.Uint32(body[20:24]); unit != 1300 {
				s.fail("listing %d unit price = %d, want 1300", index, unit)
			}
			if buyout, count := binary.LittleEndian.Uint32(body[16:20]), binary.LittleEndian.Uint32(body[8:12]); buyout != 1300*count {
				s.fail("listing %d buyout = %d for count %d", index, buyout, count)
			}
			session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionRegister, Body: auctionTestAck(true, 0)})
		}
		// Then the browse: one listing at the gate, one far above it.
		if _, ok := s.expect(serverConn, protocol.CmdAuctionSearchPage, "buy phase opening"); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionSearchItem, Body: auctionTestSearchAnswer(
			auctionTestRow(41, 1000, "player-one", 1072, 5),
			auctionTestRow(42, 90000, "player-two", 1298, 10),
		)})
		// Only the first is affordable, and it is taken whole.
		body, ok := s.expect(serverConn, protocol.CmdAuctionBuyPiece, "buy")
		if !ok {
			return
		}
		if price, count := binary.LittleEndian.Uint32(body[0:4]), binary.LittleEndian.Uint32(body[4:8]); price != 1000 || count != 5 {
			s.fail("buy price=%d count=%d, want the whole listing 1000/5", price, count)
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionBuyPiece, Body: auctionTestAck(true, 0)})
	})
	listed, bought, err := transport.AuctionRound(7, auctionTestConfig())
	if err != nil {
		t.Fatalf("auction round failed: %v", err)
	}
	script.finish(t)
	if listed != 2 {
		t.Errorf("listed = %d, want 2", listed)
	}
	if bought != 1 {
		t.Errorf("bought = %d, want 1", bought)
	}
}

func TestAuctionRoundStopsListingWhenTheCharacterIsFull(t *testing.T) {
	live := make([][]byte, 0, 2)
	for index := 0; index < 2; index++ {
		live = append(live, auctionTestMineRow(uint32(1072+index), 3))
	}
	transport, session, serverConn := auctionTestTransport(t, auctionTestCatalog(), 9)
	script := newAuctionScript(func(s *auctionScript) {
		if _, ok := s.expect(serverConn, protocol.CmdAuctionMyListings, "round opening"); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionMyListings, Body: auctionTestListingAnswer(live...)})
		// With the listing slots full the round goes straight to the browse
		// instead of registering a stack the server would refuse.
		if _, ok := s.expect(serverConn, protocol.CmdAuctionSearchPage, "full character"); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionSearchItem, Body: auctionTestSearchAnswer()})
	})
	listed, bought, err := transport.AuctionRound(7, auctionTestConfig())
	if err != nil {
		t.Fatalf("auction round failed: %v", err)
	}
	script.finish(t)
	if listed != 0 || bought != 0 {
		t.Fatalf("listed=%d bought=%d, want a quiet round", listed, bought)
	}
}

func TestAuctionRoundSkipsListingsAboveTheBuyGate(t *testing.T) {
	transport, session, serverConn := auctionTestTransport(t, auctionTestCatalog(), 9)
	script := newAuctionScript(func(s *auctionScript) {
		if _, ok := s.expect(serverConn, protocol.CmdAuctionMyListings, "round opening"); !ok {
			return
		}
		// The character is already at its listing count, so only the buy gate
		// is under test.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionMyListings, Body: auctionTestListingAnswer(auctionTestMineRow(1072, 3), auctionTestMineRow(1298, 3))})
		if _, ok := s.expect(serverConn, protocol.CmdAuctionSearchPage, "browse"); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionSearchItem, Body: auctionTestSearchAnswer(
			auctionTestRow(41, 1001, "over-by-one", 1072, 1),
			auctionTestRow(42, 500000, "scalper", 1072, 1),
		)})
		s.expectSilence(serverConn, "an unaffordable page")
	})
	listed, bought, err := transport.AuctionRound(7, auctionTestConfig())
	if err != nil {
		t.Fatalf("auction round failed: %v", err)
	}
	script.finish(t)
	if listed != 0 {
		t.Errorf("listed = %d, want 0 for a full character", listed)
	}
	if bought != 0 {
		t.Errorf("bought = %d, want 0 over the gate", bought)
	}
}

func TestAuctionRoundHonorsTheBuyBudget(t *testing.T) {
	transport, session, serverConn := auctionTestTransport(t, auctionTestCatalog(), 9)
	policy := auctionTestConfig()
	policy.AuctionListPerRobot = 0
	policy.AuctionBuyMaxTotal = 600
	script := newAuctionScript(func(s *auctionScript) {
		if _, ok := s.expect(serverConn, protocol.CmdAuctionMyListings, "round opening"); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionMyListings, Body: auctionTestListingAnswer()})
		// No register: this round is configured with no listings to place.
		if _, ok := s.expect(serverConn, protocol.CmdAuctionSearchPage, "browse"); !ok {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionSearchItem, Body: auctionTestSearchAnswer(
			auctionTestRow(41, 500, "first", 1072, 1),
			auctionTestRow(42, 500, "second", 1298, 1),
		)})
		body, ok := s.expect(serverConn, protocol.CmdAuctionBuyPiece, "first buy")
		if !ok {
			return
		}
		if price := binary.LittleEndian.Uint32(body[0:4]); price != 500 {
			s.fail("first buy price = %d, want 500", price)
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionBuyPiece, Body: auctionTestAck(true, 0)})
		// A second buy would take the round to 1000, over the 600 budget.
		s.expectSilence(serverConn, "a spent budget")
	})
	listed, bought, err := transport.AuctionRound(7, policy)
	if err != nil {
		t.Fatalf("auction round failed: %v", err)
	}
	script.finish(t)
	if listed != 0 {
		t.Errorf("listed = %d, want 0", listed)
	}
	if bought != 1 {
		t.Errorf("bought = %d, want the single affordable listing", bought)
	}
}

func TestAuctionRoundReportsAMissingCharacter(t *testing.T) {
	transport, _, _ := auctionTestTransport(t, auctionTestCatalog(), 0)
	if _, _, err := transport.AuctionRound(7, auctionTestConfig()); err == nil {
		t.Fatal("a round without a character was accepted")
	}
	if _, _, err := transport.AuctionRound(99, auctionTestConfig()); err == nil {
		t.Fatal("a round without a session was accepted")
	}
}

func TestAuctionRoundEndsWithTheSession(t *testing.T) {
	transport, session, serverConn := auctionTestTransport(t, auctionTestCatalog(), 9)
	script := newAuctionScript(func(s *auctionScript) {
		if _, ok := s.expect(serverConn, protocol.CmdAuctionMyListings, "round opening"); !ok {
			return
		}
		close(session.done)
	})
	if _, _, err := transport.AuctionRound(7, auctionTestConfig()); err == nil {
		t.Fatal("a round over a closed session was accepted")
	}
	script.finish(t)
}

func TestAuctionPolicyClampsTheListingCeiling(t *testing.T) {
	policy := AuctionPolicyFromConfig(robotconfig.RuntimeConfig{AuctionListPerRobot: 99, AuctionStockStacks: 0})
	if policy.ListPerRobot != auctionListingCeiling {
		t.Errorf("list per robot = %d, want the server ceiling %d", policy.ListPerRobot, auctionListingCeiling)
	}
	if policy.StockStacks != cn90AuctionStockDefaultStacks {
		t.Errorf("stock stacks = %d, want the seeder's own fallback %d", policy.StockStacks, cn90AuctionStockDefaultStacks)
	}
	if buyer := AuctionPolicyFromConfig(robotconfig.RuntimeConfig{AuctionListPerRobot: 0}); buyer.ListPerRobot != 0 {
		t.Errorf("listing count 0 = %d, want a pure buyer", buyer.ListPerRobot)
	}
	if negative := AuctionPolicyFromConfig(robotconfig.RuntimeConfig{AuctionListPerRobot: -5}); negative.ListPerRobot != 0 {
		t.Errorf("negative listing count = %d, want a pure buyer", negative.ListPerRobot)
	}
}

func TestAuctionPricesAnchorOnTheReference(t *testing.T) {
	// A listed price never undercuts the reference, and stays under the server
	// ceiling even for a large stack.
	if unit := auctionListUnitPrice(1000, 3, 130); unit != 1300 {
		t.Errorf("list unit = %d, want 1300", unit)
	}
	if unit := auctionListUnitPrice(1000, 3, 50); unit != 1000 {
		t.Errorf("list unit below the reference = %d, want 1000", unit)
	}
	if unit := auctionListUnitPrice(int(protocol.AuctionMaxPrice), 1, 130); unit != int(protocol.AuctionMaxPrice) {
		t.Errorf("list unit = %d, want the server ceiling %d", unit, protocol.AuctionMaxPrice)
	}
	if unit := auctionListUnitPrice(0, 3, 130); unit != 0 {
		t.Errorf("list unit without a reference = %d, want 0", unit)
	}
	// A buy gate never pays over the reference.
	if gate := auctionBuyUnitGate(1000, 100); gate != 1000 {
		t.Errorf("buy gate = %d, want the reference 1000", gate)
	}
	if gate := auctionBuyUnitGate(1000, 250); gate != 1000 {
		t.Errorf("buy gate over the reference = %d, want 1000", gate)
	}
	if gate := auctionBuyUnitGate(1000, 80); gate != 800 {
		t.Errorf("buy gate = %d, want 800", gate)
	}
	if gate := auctionBuyUnitGate(0, 100); gate != 0 {
		t.Errorf("buy gate without a reference = %d, want 0", gate)
	}
}

func TestAuctionReferencePriceFallsBackToTheValue(t *testing.T) {
	if got := auctionReferencePrice(shared.EquipmentCatalogItem{Price: 500, Value: 900}); got != 500 {
		t.Errorf("reference price = %d, want the PVF price 500", got)
	}
	if got := auctionReferencePrice(shared.EquipmentCatalogItem{Value: 900}); got != 900 {
		t.Errorf("reference price = %d, want the value 900", got)
	}
	if got := auctionReferencePrice(shared.EquipmentCatalogItem{}); got != 0 {
		t.Errorf("reference price = %d, want 0", got)
	}
}
