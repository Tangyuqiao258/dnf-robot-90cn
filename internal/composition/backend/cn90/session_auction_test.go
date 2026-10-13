package cn90

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"testing"
	"time"

	protocol "robot/internal/protocol/cn90"
)

// newAuctionTestSession builds one session whose client speaks over an
// in-memory pipe, so a test plays the server side of the exchange.
func newAuctionTestSession(t *testing.T) (*Session, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	session := &Session{client: protocol.NewClient(clientConn), done: make(chan struct{}), selfCharacterID: 42}
	t.Cleanup(func() {
		_ = clientConn.Close()
		_ = serverConn.Close()
	})
	return session, serverConn
}

// auctionTestAck builds the two-byte server envelope.
func auctionTestAck(ok bool, code byte) []byte {
	if ok {
		return []byte{1, 0}
	}
	return []byte{0, code}
}

// auctionTestItemRaw builds the compact auction item row the server writes
// into a search row: item id at +1, amount at +6.
func auctionTestItemRaw(itemID, count uint32) []byte {
	raw := make([]byte, 121)
	binary.LittleEndian.PutUint32(raw[1:5], itemID)
	binary.LittleEndian.PutUint32(raw[6:10], count)
	return raw
}

func TestSessionAuctionListAcceptsClassOneAck(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		cmd, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || cmd != 1 || typ != protocol.CmdAuctionRegister {
			return
		}
		// The server answers registration on class1/op183.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionRegister, Body: auctionTestAck(true, 0)})
		if len(body) != 37 {
			t.Errorf("register body length = %d, want the 37-byte native shape", len(body))
		}
		if start := binary.LittleEndian.Uint32(body[12:16]); start != protocol.AuctionNoStartPrice {
			t.Errorf("register start price = %#x, want the no-start sentinel", start)
		}
		if buyout := binary.LittleEndian.Uint32(body[16:20]); buyout != 1300*3 {
			t.Errorf("register buyout = %d, want unit*count", buyout)
		}
		if unit := binary.LittleEndian.Uint32(body[20:24]); unit != 1300 {
			t.Errorf("register unit price = %d, want 1300", unit)
		}
	}()
	err := session.AuctionList(context.Background(), AuctionStack{ListType: 0, Slot: 113, ItemID: 2600021, Count: 3}, 1300)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
}

func TestSessionAuctionListIgnoresTheServiceNotice(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		if _, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok || typ != protocol.CmdAuctionRegister {
			return
		}
		// The first auction request of a session also draws the class0 service
		// open notice on the same opcode. It must not be read as the result.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassNotice, Type: protocol.NotiAuctionService, Body: []byte{1, 1}})
		time.Sleep(50 * time.Millisecond)
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionRegister, Body: auctionTestAck(true, 0)})
	}()
	if err := session.AuctionList(context.Background(), AuctionStack{ListType: 0, Slot: 113, ItemID: 2600021, Count: 3}, 700); err != nil {
		t.Fatalf("list failed: %v", err)
	}
}

func TestSessionAuctionListReportsTheRejectionCode(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		if _, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok || typ != protocol.CmdAuctionRegister {
			return
		}
		// A failed registration answers class1/op183 {0, code}.
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionRegister, Body: auctionTestAck(false, 1)})
	}()
	err := session.AuctionList(context.Background(), AuctionStack{ListType: 0, Slot: 113, ItemID: 2600021, Count: 3}, 700)
	if err == nil || !strings.Contains(err.Error(), "code 1") {
		t.Fatalf("list error = %v, want the rejection code", err)
	}
}

func TestSessionAuctionListRejectsPricesOverTheCeiling(t *testing.T) {
	session, _ := newAuctionTestSession(t)
	stack := AuctionStack{ListType: 0, Slot: 113, ItemID: 2600021, Count: 3}
	if err := session.AuctionList(context.Background(), stack, 0); err == nil {
		t.Fatal("zero unit price was accepted")
	}
	over := uint32(protocol.AuctionMaxPrice/3) + 1
	if err := session.AuctionList(context.Background(), stack, over); err == nil {
		t.Fatal("a buyout over the server ceiling was accepted")
	}
}

func TestSessionAuctionBuyDerivesUnitPriceFromTheListing(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	listing := protocol.AuctionListing{
		ListingID: 77, BuyoutPrice: 5000,
		ItemRaw: auctionTestItemRaw(2600021, 5), SellerRaw: []byte("seller"),
	}
	go func() {
		_, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || typ != protocol.CmdAuctionBuyPiece {
			return
		}
		// A partial buy charges the listing's unit price times the piece count.
		if price := binary.LittleEndian.Uint32(body[0:4]); price != 2000 {
			t.Errorf("piece price = %d, want 2000", price)
		}
		if count := binary.LittleEndian.Uint32(body[4:8]); count != 2 {
			t.Errorf("piece count = %d, want 2", count)
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionBuyPiece, Body: auctionTestAck(true, 0)})
	}()
	if err := session.AuctionBuy(context.Background(), listing, 2); err != nil {
		t.Fatalf("piece buy failed: %v", err)
	}
}

func TestSessionAuctionBuyTakesTheWholeListingWithoutACount(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	listing := protocol.AuctionListing{
		ListingID: 77, BuyoutPrice: 5000,
		ItemRaw: auctionTestItemRaw(2600021, 5), SellerRaw: []byte("seller"),
	}
	go func() {
		_, _, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok {
			return
		}
		if price := binary.LittleEndian.Uint32(body[0:4]); price != 5000 {
			t.Errorf("whole-listing price = %d, want the buyout 5000", price)
		}
		if count := binary.LittleEndian.Uint32(body[4:8]); count != 5 {
			t.Errorf("whole-listing count = %d, want the listed 5", count)
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionBuyPiece, Body: auctionTestAck(true, 0)})
	}()
	if err := session.AuctionBuy(context.Background(), listing, 0); err != nil {
		t.Fatalf("whole-listing buy failed: %v", err)
	}
}

func TestSessionAuctionBuyRefusesAListingWithoutBuyout(t *testing.T) {
	session, _ := newAuctionTestSession(t)
	listing := protocol.AuctionListing{ListingID: 77, ItemRaw: auctionTestItemRaw(2600021, 5)}
	if err := session.AuctionBuy(context.Background(), listing, 0); err == nil {
		t.Fatal("a bid-only listing was bought")
	}
}

func TestSessionAuctionSearchBrowsesWithoutItemKeys(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		// An empty id list browses with op187, whose answer still arrives on
		// op186.
		_, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || typ != protocol.CmdAuctionSearchPage {
			t.Errorf("browse request type = %d, want op%d", typ, protocol.CmdAuctionSearchPage)
			return
		}
		if len(body) != 20 {
			t.Errorf("browse body length = %d, want the 20-byte client shape", len(body))
		}
		row := make([]byte, 168)
		binary.LittleEndian.PutUint64(row[0:8], 99)
		binary.LittleEndian.PutUint32(row[13:17], 4200)
		copy(row[43:164], auctionTestItemRaw(2600021, 3))
		answer := make([]byte, 0, 8+168)
		answer = append(answer, 1, 0)
		answer = binary.LittleEndian.AppendUint32(answer, 1)
		answer = binary.LittleEndian.AppendUint16(answer, 1)
		answer = append(answer, row...)
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionSearchItem, Body: answer})
	}()
	page, err := session.AuctionSearch(context.Background(), nil, 0)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if page.Total != 1 || len(page.Listings) != 1 {
		t.Fatalf("page = %+v, want one listing", page)
	}
	if got := protocol.AuctionItemID(page.Listings[0].ItemRaw); got != 2600021 {
		t.Fatalf("listing item = %d, want 2600021", got)
	}
}

func TestSessionAuctionSearchAsksForSpecificItemKeys(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		_, typ, body, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || typ != protocol.CmdAuctionSearchItem {
			t.Errorf("item search type = %d, want op%d", typ, protocol.CmdAuctionSearchItem)
			return
		}
		if len(body) != 16 || body[7] != 1 {
			t.Errorf("item search body length = %d count = %d, want 16/1", len(body), body[7])
		}
		answer := append([]byte{1, 0}, binary.LittleEndian.AppendUint32(nil, 0)...)
		answer = binary.LittleEndian.AppendUint16(answer, 0)
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionSearchItem, Body: answer})
	}()
	page, err := session.AuctionSearch(context.Background(), []uint32{2600021}, 0)
	if err != nil {
		t.Fatalf("item search failed: %v", err)
	}
	if len(page.Listings) != 0 {
		t.Fatalf("empty page carried %d rows", len(page.Listings))
	}
}

func TestSessionAuctionMyListingsReadsTheNarrowCount(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		_, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || typ != protocol.CmdAuctionMyListings {
			return
		}
		// op188 carries a u8 row count where op189/op190 carry a u16.
		answer := []byte{1, 0, 1}
		answer = append(answer, make([]byte, 151)...)
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionMyListings, Body: answer})
	}()
	rows, err := session.AuctionMyListings(context.Background())
	if err != nil {
		t.Fatalf("my listings failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
}

func TestSessionAuctionAveragePriceReadsTheValue(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		_, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second)
		if !ok || typ != protocol.CmdAuctionAverage {
			return
		}
		answer := []byte{1, 0}
		answer = binary.LittleEndian.AppendUint32(answer, 1234)
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionAverage, Body: answer})
	}()
	price, err := session.AuctionAveragePrice(context.Background(), 2600021)
	if err != nil {
		t.Fatalf("average price failed: %v", err)
	}
	if price != 1234 {
		t.Fatalf("price = %d, want 1234", price)
	}
}

func TestSessionAuctionCancelReportsTheRejection(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		if _, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok || typ != protocol.CmdAuctionCancel {
			return
		}
		session.dispatchPacket(protocol.Packet{Class: protocol.ClassCommand, Type: protocol.CmdAuctionCancel, Body: auctionTestAck(false, 1)})
	}()
	if err := session.AuctionCancel(context.Background(), 12); err == nil || !strings.Contains(err.Error(), "code 1") {
		t.Fatalf("cancel error = %v, want the rejection code", err)
	}
}

func TestSessionAuctionRequestEndsWithTheSession(t *testing.T) {
	session, serverConn := newAuctionTestSession(t)
	go func() {
		if _, typ, _, ok := readTestLegacyFrame(serverConn, 5*time.Second); !ok || typ != protocol.CmdAuctionMyListings {
			return
		}
		close(session.done)
	}()
	_, err := session.AuctionMyListings(context.Background())
	if err == nil || !strings.Contains(err.Error(), "session ended") {
		t.Fatalf("my listings error = %v, want the session-ended error", err)
	}
}
