package cn90

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The request vectors below are pinned to the offsets the server's decoders
// read (dnfbridge/auction_protocol.go decodeAuctionRegister / decodeAuctionBid
// / decodeAuctionPiece / decodeAuctionSearchByItem), so a field moved here
// fails before it can reach the live server.

func TestAuctionRegisterBodyMatchesServerOffsets(t *testing.T) {
	got, err := AuctionRegisterBody(0, 65, 1001, 3, 1500, 3000)
	if err != nil {
		t.Fatalf("AuctionRegisterBody error = %v", err)
	}
	if len(got) != 37 {
		t.Fatalf("op183 body is %d bytes, want 37", len(got))
	}
	if got[0] != 0 || got[1] != 0 {
		t.Fatalf("op183 mode/listType = %d/%d, want 0/0", got[0], got[1])
	}
	if slot := binary.LittleEndian.Uint16(got[2:4]); slot != 65 {
		t.Fatalf("slot = %d, want 65", slot)
	}
	if v := binary.LittleEndian.Uint32(got[4:8]); v != 1001 {
		t.Fatalf("item id = %d, want 1001", v)
	}
	if v := binary.LittleEndian.Uint32(got[8:12]); v != 3 {
		t.Fatalf("count = %d, want 3", v)
	}
	if v := binary.LittleEndian.Uint32(got[12:16]); v != 1500 {
		t.Fatalf("start price = %d, want 1500", v)
	}
	if v := binary.LittleEndian.Uint32(got[16:20]); v != 3000 {
		t.Fatalf("buyout = %d, want 3000", v)
	}
	if v := binary.LittleEndian.Uint32(got[20:24]); v != 1000 {
		t.Fatalf("unit price = %d, want 1000 (buyout/count)", v)
	}
	if v := binary.LittleEndian.Uint32(got[24:28]); v != 48 {
		t.Fatalf("hours = %d, want 48", v)
	}
	for i, value := range got[28:] {
		if value != 0 {
			t.Fatalf("native trailer byte %d = %d, want 0", i, value)
		}
	}
}

func TestAuctionRegisterBodyEncodesNoStartPriceSentinel(t *testing.T) {
	got, err := AuctionRegisterBody(0, 9, 2002, 1, 0, 5000)
	if err != nil {
		t.Fatalf("AuctionRegisterBody error = %v", err)
	}
	if v := binary.LittleEndian.Uint32(got[12:16]); v != AuctionNoStartPrice {
		t.Fatalf("start price = %#x, want %#x", v, AuctionNoStartPrice)
	}
	if v := binary.LittleEndian.Uint32(got[20:24]); v != 5000 {
		t.Fatalf("unit price = %d, want 5000", v)
	}
}

func TestAuctionRegisterBodyRejectsInvalidPrices(t *testing.T) {
	if _, err := AuctionRegisterBody(0, 0, 0, 1, 1, 0); err == nil {
		t.Fatal("a zero item id was accepted")
	}
	if _, err := AuctionRegisterBody(0, 0, 1, 1, 0, 0); err == nil {
		t.Fatal("a listing with no price at all was accepted")
	}
	if _, err := AuctionRegisterBody(0, 0, 1, 1, uint32(AuctionMaxPrice)+1, 0); err == nil {
		t.Fatal("a start price above the ceiling was accepted")
	}
	if _, err := AuctionRegisterBody(0, 0, 1, 10, 0, 5); err == nil {
		t.Fatal("a buyout below the stack count was accepted")
	}
}

func TestAuctionSetRegisterHours(t *testing.T) {
	body, err := AuctionRegisterBody(0, 1, 1001, 1, 100, 0)
	if err != nil {
		t.Fatalf("AuctionRegisterBody error = %v", err)
	}
	updated, err := AuctionSetRegisterHours(body, AuctionHours24)
	if err != nil {
		t.Fatalf("AuctionSetRegisterHours error = %v", err)
	}
	if v := binary.LittleEndian.Uint32(updated[24:28]); v != 24 {
		t.Fatalf("hours = %d, want 24", v)
	}
	if v := binary.LittleEndian.Uint32(body[24:28]); v != 48 {
		t.Fatalf("the source body was mutated: hours = %d", v)
	}
}

func TestAuctionCancelBodyIsNineBytes(t *testing.T) {
	got, err := AuctionCancelBody(0x1122334455667788)
	if err != nil {
		t.Fatalf("AuctionCancelBody error = %v", err)
	}
	want := []byte{0, 0x88, 0x77, 0x66, 0x55, 0x44, 0x33, 0x22, 0x11}
	if !bytes.Equal(got, want) {
		t.Fatalf("op184 body = % x, want % x", got, want)
	}
	if _, err := AuctionCancelBody(0); err == nil {
		t.Fatal("a zero listing id was accepted")
	}
}

func TestAuctionBidBodyMatchesServerOffsets(t *testing.T) {
	seller := []byte("abcdefghijklm")
	got, err := AuctionBidBody(2500, 0x0807060504030201, seller, 4321)
	if err != nil {
		t.Fatalf("AuctionBidBody error = %v", err)
	}
	if len(got) != 30 {
		t.Fatalf("op185 body is %d bytes, want 30", len(got))
	}
	if got[0] != 0 {
		t.Fatalf("mode = %d, want 0", got[0])
	}
	if v := binary.LittleEndian.Uint32(got[1:5]); v != 2500 {
		t.Fatalf("price = %d, want 2500", v)
	}
	if v := binary.LittleEndian.Uint64(got[5:13]); v != 0x0807060504030201 {
		t.Fatalf("listing id = %#x", v)
	}
	if !bytes.Equal(got[13:26], seller) {
		t.Fatalf("seller = % x, want % x", got[13:26], seller)
	}
	if v := binary.LittleEndian.Uint32(got[26:30]); v != 4321 {
		t.Fatalf("item id = %d, want 4321", v)
	}
}

func TestAuctionPieceBodyMatchesServerOffsets(t *testing.T) {
	seller := []byte("seller-name-x")
	got, err := AuctionPieceBody(700, 2, 0x0102030405060708, seller, 999)
	if err != nil {
		t.Fatalf("AuctionPieceBody error = %v", err)
	}
	if len(got) != 33 {
		t.Fatalf("op334 body is %d bytes, want 33", len(got))
	}
	if v := binary.LittleEndian.Uint32(got[0:4]); v != 700 {
		t.Fatalf("unit price = %d, want 700", v)
	}
	if v := binary.LittleEndian.Uint32(got[4:8]); v != 2 {
		t.Fatalf("count = %d, want 2", v)
	}
	if v := binary.LittleEndian.Uint64(got[8:16]); v != 0x0102030405060708 {
		t.Fatalf("listing id = %#x", v)
	}
	if !bytes.Equal(got[16:29], seller) {
		t.Fatalf("seller = % x, want % x", got[16:29], seller)
	}
	if v := binary.LittleEndian.Uint32(got[29:33]); v != 999 {
		t.Fatalf("item id = %d, want 999", v)
	}
}

func TestAuctionSearchBodies(t *testing.T) {
	item, err := AuctionSearchByItemBody(10, []uint32{1001, 1002})
	if err != nil {
		t.Fatalf("AuctionSearchByItemBody error = %v", err)
	}
	if len(item) != 20 {
		t.Fatalf("op186 body is %d bytes, want 12+4*2", len(item))
	}
	if item[0] != 0 {
		t.Fatalf("mode = %d, want 0", item[0])
	}
	if v := binary.LittleEndian.Uint32(item[1:5]); v != 10 {
		t.Fatalf("offset = %d, want 10", v)
	}
	if item[7] != 2 {
		t.Fatalf("key count = %d, want 2", item[7])
	}
	if v := binary.LittleEndian.Uint32(item[12:16]); v != 1001 {
		t.Fatalf("first key = %d, want 1001", v)
	}
	if v := binary.LittleEndian.Uint32(item[16:20]); v != 1002 {
		t.Fatalf("second key = %d, want 1002", v)
	}
	if _, err := AuctionSearchByItemBody(0, nil); err == nil {
		t.Fatal("an empty item key list was accepted")
	}
	if _, err := AuctionSearchByItemBody(0, []uint32{0}); err == nil {
		t.Fatal("a zero item key was accepted")
	}

	category := AuctionSearchCategoryBody(30)
	if len(category) != 20 {
		t.Fatalf("op187 body is %d bytes, want 20", len(category))
	}
	if category[0] != 0 {
		t.Fatalf("op187 mode = %d, want 0", category[0])
	}
	if v := binary.LittleEndian.Uint32(category[1:5]); v != 30 {
		t.Fatalf("op187 offset = %d, want 30", v)
	}
}

func TestAuctionAverageAndMineBodies(t *testing.T) {
	average, err := AuctionAveragePriceBody(7777)
	if err != nil {
		t.Fatalf("AuctionAveragePriceBody error = %v", err)
	}
	want := []byte{0, 0x61, 0x1E, 0, 0, 0, 0, 0}
	if !bytes.Equal(average, want) {
		t.Fatalf("op182 body = % x, want % x", average, want)
	}
	if _, err := AuctionAveragePriceBody(0); err == nil {
		t.Fatal("a zero average-price item id was accepted")
	}
	if got := AuctionMineBody(); !bytes.Equal(got, []byte{0}) {
		t.Fatalf("op188/189/190 body = % x, want 00", got)
	}
}

func TestAuctionAckResultAndServiceNotice(t *testing.T) {
	if ok, _ := AuctionAckResult([]byte{0x01, 0x00}); !ok {
		t.Fatal("op183 success envelope was rejected")
	}
	if ok, code := AuctionAckResult([]byte{0x00, 0x01}); ok || code != 1 {
		t.Fatalf("failure envelope = (%t,%d), want (false,1)", ok, code)
	}
	for _, body := range [][]byte{{0, 1}, {1, 1}} {
		if !AuctionServiceNotice(body) {
			t.Fatalf("class0/op183 body %v was not recognised as the service notice", body)
		}
	}
	if AuctionServiceNotice([]byte{1, 0}) || AuctionServiceNotice([]byte{0, 1, 0}) {
		t.Fatal("a non-notice body was accepted as the service-open notice")
	}
}

// auctionTestSearchRow mirrors the server's auctionSearchRow offsets so the
// page vector is a faithful re-encoding.
func auctionTestSearchRow(listingID uint64, bid, buyout, remaining uint32, seller []byte, itemID, count uint32) []byte {
	row := make([]byte, auctionSearchRowBytes)
	binary.LittleEndian.PutUint64(row[0:8], listingID)
	binary.LittleEndian.PutUint32(row[9:13], bid)
	binary.LittleEndian.PutUint32(row[13:17], buyout)
	binary.LittleEndian.PutUint32(row[17:21], remaining)
	copy(row[21:34], seller)
	// auctionItemPayload: the compact row shifts the inventory row one byte, so
	// the item id lands at +1 and the amount dword at +6.
	binary.LittleEndian.PutUint32(row[43+1:43+5], itemID)
	binary.LittleEndian.PutUint32(row[43+6:43+10], count)
	return row
}

func TestParseAuctionSearchPage(t *testing.T) {
	body := []byte{0x01, 0x00}
	body = appendU32(body, 42)
	body = append(body, 2, 0)
	body = append(body, auctionTestSearchRow(0xAA, 1500, 3000, 7200, []byte("seller"), 1001, 3)...)
	body = append(body, auctionTestSearchRow(0xBB, 0, 900, 3600, nil, 2002, 1)...)

	page, err := ParseAuctionSearchPage(body)
	if err != nil {
		t.Fatalf("ParseAuctionSearchPage error = %v", err)
	}
	if page.Total != 42 || len(page.Listings) != 2 {
		t.Fatalf("page = %+v, want total 42 and two rows", page)
	}
	first := page.Listings[0]
	if first.ListingID != 0xAA || first.CurrentBid != 1500 || first.BuyoutPrice != 3000 || first.RemainingSecs != 7200 {
		t.Fatalf("first row = %+v", first)
	}
	if first.SellerName != "seller" {
		t.Fatalf("seller name = %q, want seller", first.SellerName)
	}
	if AuctionItemID(first.ItemRaw) != 1001 || AuctionItemCount(first.ItemRaw) != 3 {
		t.Fatalf("first item = id %d count %d, want 1001/3", AuctionItemID(first.ItemRaw), AuctionItemCount(first.ItemRaw))
	}
	if page.Listings[1].ListingID != 0xBB || AuctionItemID(page.Listings[1].ItemRaw) != 2002 {
		t.Fatalf("second row = %+v", page.Listings[1])
	}
	if page.PageBytes != len(body) {
		t.Fatalf("page bytes = %d, want %d", page.PageBytes, len(body))
	}

	if _, err := ParseAuctionSearchPage([]byte{0x00, 0x01}); err == nil {
		t.Fatal("a search failure envelope was accepted as a page")
	}
	if _, err := ParseAuctionSearchPage(body[:len(body)-1]); err == nil {
		t.Fatal("a truncated search page was accepted")
	}
}

func TestParseAuctionMyListingsAndBids(t *testing.T) {
	listingRow := make([]byte, auctionMyListingBytes)
	binary.LittleEndian.PutUint64(listingRow[0:8], 0xCC)
	binary.LittleEndian.PutUint32(listingRow[8:12], 0)
	binary.LittleEndian.PutUint32(listingRow[12:16], 5000)
	binary.LittleEndian.PutUint32(listingRow[30+1:30+5], 3003)
	binary.LittleEndian.PutUint32(listingRow[30+6:30+10], 7)

	body := []byte{0x01, 0x00, 0x01}
	body = append(body, listingRow...)
	listings, err := ParseAuctionMyListings(body)
	if err != nil {
		t.Fatalf("ParseAuctionMyListings error = %v", err)
	}
	if len(listings) != 1 || listings[0].ListingID != 0xCC || listings[0].BuyoutPrice != 5000 {
		t.Fatalf("my listings = %+v", listings)
	}
	if AuctionItemID(listings[0].ItemRaw) != 3003 || AuctionItemCount(listings[0].ItemRaw) != 7 {
		t.Fatalf("my listing item = id %d count %d", AuctionItemID(listings[0].ItemRaw), AuctionItemCount(listings[0].ItemRaw))
	}

	bidRow := make([]byte, auctionMyBidRowBytes)
	binary.LittleEndian.PutUint64(bidRow[0:8], 0xDD)
	binary.LittleEndian.PutUint32(bidRow[8:12], 2500)
	binary.LittleEndian.PutUint32(bidRow[12:16], 4000)
	copy(bidRow[24:37], []byte("someone"))
	binary.LittleEndian.PutUint32(bidRow[38+1:38+5], 4004)

	bidBody := []byte{0x01, 0x00, 0x01, 0x00}
	bidBody = append(bidBody, bidRow...)
	bids, err := ParseAuctionMyBids(bidBody)
	if err != nil {
		t.Fatalf("ParseAuctionMyBids error = %v", err)
	}
	if len(bids) != 1 || bids[0].ListingID != 0xDD || bids[0].CurrentBid != 2500 {
		t.Fatalf("my bids = %+v", bids)
	}
	if bids[0].SellerName != "someone" {
		t.Fatalf("my bid seller = %q, want someone", bids[0].SellerName)
	}
	if AuctionItemID(bids[0].ItemRaw) != 4004 {
		t.Fatalf("my bid item id = %d, want 4004", AuctionItemID(bids[0].ItemRaw))
	}

	if _, err := ParseAuctionMyListings([]byte{0x00, 0x01}); err == nil {
		t.Fatal("a listing failure envelope was accepted")
	}
}

func TestParseAuctionAveragePrice(t *testing.T) {
	body := []byte{0x01, 0x00}
	body = appendU32(body, 12345)
	body = append(body, make([]byte, 13*4)...)
	price, err := ParseAuctionAveragePrice(body)
	if err != nil {
		t.Fatalf("ParseAuctionAveragePrice error = %v", err)
	}
	if price != 12345 {
		t.Fatalf("average price = %d, want 12345", price)
	}
	if _, err := ParseAuctionAveragePrice([]byte{0x00, 0x01}); err == nil {
		t.Fatal("an average-price failure envelope was accepted")
	}
}
