package cn90

import (
	"encoding/binary"
	"fmt"
)

// The 90CN auction house. Server authority: the server tree's
// `internal/services/dnfbridge/gameplay_module_auction.go` (dispatch, gates and
// success envelopes) and `auction_protocol.go` (request decoders and response
// row layouts), with the durable owner in `internal/modules/dnf/auction`.
//
// Wire summary:
//
//	op182 AskAveragePrice   body exactly 8, mode 0   -> class1/op182 {01, 00, u32 price, 52 zero}
//	op183 RegistItem        body 28 or 37, mode 0    -> class1/op183 {01, 00}
//	op184 RegistCancel      body exactly 9, mode 0   -> class1/op184 {01, 00}
//	op185 Bidding           body exactly 30, mode 0  -> class1/op185 {01, 00}
//	op186 SearchByItemkey   body 12+4n, mode 0       -> class1/op186 page rows
//	op187 SearchByNoitemkey body 14 or 20, mode 0    -> class1/op186 page rows
//	op188 MyRegistedItemInfo body exactly 1 byte 0   -> class1/op188 {01, 00, u8 count, rows}
//	op189 MyBiddingInfo     body exactly 1 byte 0    -> class1/op189 {01, 00, u16 count, rows}
//	op190 MyAuctionHistory  body exactly 1 byte 0    -> class1/op190 {01, 00, u16 count, rows}
//	op334 BuyItemApiece     body exactly 33, no mode -> class1/op334 {01, 00}
//
// Every failure reply is the two-byte envelope {0x00, code}; the server always
// sends code 1 and logs the reason. Op187 answers under 186 because the current
// client registers its search reader there, so a category search response must
// be matched on op186.
//
// The server also pushes the auction service-open notice once per session: two
// class0/op183 bodies {0,1} and {1,1}, both meaning "service open". They are
// notifications, not command acks.

// Auction service-open notice bodies pushed as class0/op183 at the first
// auction request of a session.
const (
	auctionServiceNoticeBytes = 2
)

// Auction listing durations the server normalizes to; anything else is rounded
// to one of these.
const (
	AuctionHours24 uint32 = 24
	AuctionHours48 uint32 = 48
)

// AuctionNoStartPrice is the start-price sentinel that disables bidding and
// leaves the listing buyout-only.
const AuctionNoStartPrice uint32 = 0xFFFFFFFF

// AuctionMaxPrice is the durable price ceiling; the server clamps both bid and
// buyout to it and refuses the request above it.
const AuctionMaxPrice int64 = 400_000_000

// Auction row sizes on the wire. The offsets the robot reads are the same ones
// the server writes in auctionSearchRow / auctionMyListingRow / auctionMyBidRow.
const (
	auctionSearchRowBytes   = 168
	auctionMyListingBytes   = 151
	auctionMyBidRowBytes    = 159
	auctionItemPayloadBytes = 121
	auctionSellerNameBytes  = 13
)

// AuctionRegisterBody builds the op183 registration body. The unit price is
// derived the way the client derives it: the buyout divided by the stack count
// when a buyout is set, zero otherwise. The server rejects a listing whose unit
// price is zero, so a buyout below the stack count fails here instead. Pass
// AuctionNoStartPrice as startPrice for a buyout-only listing.
func AuctionRegisterBody(listType byte, slot uint16, itemID, count, startPrice, buyoutPrice uint32) ([]byte, error) {
	if itemID == 0 || count == 0 {
		return nil, fmt.Errorf("cn90 auction register needs an item id and a count")
	}
	if startPrice == 0 && buyoutPrice == 0 {
		return nil, fmt.Errorf("cn90 auction register needs a start price or a buyout price")
	}
	if startPrice != AuctionNoStartPrice && startPrice > uint32(AuctionMaxPrice) {
		return nil, fmt.Errorf("cn90 auction start price %d exceeds %d", startPrice, AuctionMaxPrice)
	}
	if buyoutPrice > uint32(AuctionMaxPrice) {
		return nil, fmt.Errorf("cn90 auction buyout %d exceeds %d", buyoutPrice, AuctionMaxPrice)
	}
	unitPrice := uint32(0)
	if buyoutPrice > 0 {
		unitPrice = buyoutPrice / count
		if unitPrice == 0 {
			return nil, fmt.Errorf("cn90 auction buyout %d is below the stack count %d", buyoutPrice, count)
		}
	}
	// 28 common bytes plus the nine-byte native trailer the current client
	// appends; the server accepts both lengths.
	body := make([]byte, 37)
	body[1] = listType
	binary.LittleEndian.PutUint16(body[2:4], slot)
	binary.LittleEndian.PutUint32(body[4:8], itemID)
	binary.LittleEndian.PutUint32(body[8:12], count)
	if startPrice == 0 {
		binary.LittleEndian.PutUint32(body[12:16], AuctionNoStartPrice)
	} else {
		binary.LittleEndian.PutUint32(body[12:16], startPrice)
	}
	binary.LittleEndian.PutUint32(body[16:20], buyoutPrice)
	binary.LittleEndian.PutUint32(body[20:24], unitPrice)
	binary.LittleEndian.PutUint32(body[24:28], AuctionHours48)
	return body, nil
}

// AuctionSetRegisterHours replaces the listing duration. The server normalizes
// anything outside 24 and 48 hours.
func AuctionSetRegisterHours(body []byte, hours uint32) ([]byte, error) {
	if len(body) != 37 {
		return nil, fmt.Errorf("cn90 auction register body is %d bytes, want 37", len(body))
	}
	out := append([]byte(nil), body...)
	binary.LittleEndian.PutUint32(out[24:28], hours)
	return out, nil
}

// AuctionCancelBody builds the exact nine-byte op184 body.
func AuctionCancelBody(listingID uint64) ([]byte, error) {
	if listingID == 0 {
		return nil, fmt.Errorf("cn90 auction cancel needs a listing id")
	}
	body := make([]byte, 9)
	binary.LittleEndian.PutUint64(body[1:9], listingID)
	return body, nil
}

// AuctionBidBody builds the exact 30-byte op185 body. The seller name is the
// 13-byte GB18030 copy carried in the search row, echoed back as the server's
// own client does.
func AuctionBidBody(price uint32, listingID uint64, sellerName []byte, itemID uint32) ([]byte, error) {
	if listingID == 0 {
		return nil, fmt.Errorf("cn90 auction bid needs a listing id")
	}
	if itemID == 0 {
		return nil, fmt.Errorf("cn90 auction bid needs an item id")
	}
	if price == 0 || int64(price) > AuctionMaxPrice {
		return nil, fmt.Errorf("cn90 auction bid price %d is outside 1..%d", price, AuctionMaxPrice)
	}
	body := make([]byte, 30)
	binary.LittleEndian.PutUint32(body[1:5], price)
	binary.LittleEndian.PutUint64(body[5:13], listingID)
	copy(body[13:26], sellerName)
	binary.LittleEndian.PutUint32(body[26:30], itemID)
	return body, nil
}

// AuctionPieceBody builds the exact 33-byte op334 unit-price buyout body. It
// carries no leading mode byte. Price is the unit price and count the number of
// units to take; the server recomputes the total itself and only logs the
// client's copy.
func AuctionPieceBody(price, count uint32, listingID uint64, sellerName []byte, itemID uint32) ([]byte, error) {
	if listingID == 0 || itemID == 0 {
		return nil, fmt.Errorf("cn90 auction piece needs a listing id and an item id")
	}
	if price == 0 || count == 0 {
		return nil, fmt.Errorf("cn90 auction piece needs a unit price and a count")
	}
	body := make([]byte, 33)
	binary.LittleEndian.PutUint32(body[0:4], price)
	binary.LittleEndian.PutUint32(body[4:8], count)
	binary.LittleEndian.PutUint64(body[8:16], listingID)
	copy(body[16:29], sellerName)
	binary.LittleEndian.PutUint32(body[29:33], itemID)
	return body, nil
}

// AuctionSearchByItemBody builds the op186 item-key search: mode, page offset,
// the key count at [7] and the keys from offset 12. The server derives the
// expected length from the count byte, so the two must agree.
func AuctionSearchByItemBody(offset uint32, itemIDs []uint32) ([]byte, error) {
	if len(itemIDs) == 0 || len(itemIDs) > 255 {
		return nil, fmt.Errorf("cn90 auction item search needs 1..255 item keys, got %d", len(itemIDs))
	}
	body := make([]byte, 12+4*len(itemIDs))
	binary.LittleEndian.PutUint32(body[1:5], offset)
	body[7] = byte(len(itemIDs))
	for i, itemID := range itemIDs {
		if itemID == 0 {
			return nil, fmt.Errorf("cn90 auction item key %d is zero", i)
		}
		binary.LittleEndian.PutUint32(body[12+i*4:], itemID)
	}
	return body, nil
}

// AuctionSearchCategoryBody builds the op187 browse body. The current client
// sends the 20-byte shape; the server reads only the mode and the page offset
// and ignores the category, level and quality filters the client fills in.
func AuctionSearchCategoryBody(offset uint32) []byte {
	body := make([]byte, 20)
	binary.LittleEndian.PutUint32(body[1:5], offset)
	return body
}

// AuctionAveragePriceBody builds the exact eight-byte op182 body: mode, the
// item key immediately after it, then four reserved bytes.
func AuctionAveragePriceBody(itemID uint32) ([]byte, error) {
	if itemID == 0 {
		return nil, fmt.Errorf("cn90 auction average price needs an item id")
	}
	body := make([]byte, 8)
	binary.LittleEndian.PutUint32(body[1:5], itemID)
	return body, nil
}

// AuctionMineBody is the op188/op189/op190 request: exactly one zero byte.
func AuctionMineBody() []byte {
	return []byte{0}
}

// AuctionAckResult reads the {success, code} envelope shared by op183/op184/
// op185/op334. Success is the two-byte {0x01, 0x00} mode body.
func AuctionAckResult(body []byte) (bool, byte) {
	if len(body) >= 1 && body[0] == 1 {
		return true, 0
	}
	if len(body) >= 2 {
		return false, body[1]
	}
	return false, 0
}

// AuctionServiceNotice reports whether a class0/op183 body is the service-open
// notice pair the server pushes once per session.
func AuctionServiceNotice(body []byte) bool {
	if len(body) != auctionServiceNoticeBytes {
		return false
	}
	return (body[0] == 0 || body[0] == 1) && body[1] == 1
}

// AuctionListing is one search row. The seller name is the 13-byte GB18030 copy
// the bid and buyout bodies echo back; ItemRaw is the 121-byte compact item
// row, which is the inventory row shifted one byte (see AuctionItemID).
type AuctionListing struct {
	ListingID     uint64
	CurrentBid    uint32
	BuyoutPrice   uint32
	RemainingSecs uint32
	SellerName    string
	SellerRaw     []byte
	ItemRaw       []byte
}

// AuctionItemID reads the item key from a compact auction item row. The row's
// id starts at +1 rather than the inventory row's +2, so the two are not
// interchangeable.
func AuctionItemID(itemRaw []byte) uint32 {
	if len(itemRaw) < 5 {
		return 0
	}
	return binary.LittleEndian.Uint32(itemRaw[1:5])
}

// AuctionItemCount reads the amount/quality dword from a compact auction item
// row at +6.
func AuctionItemCount(itemRaw []byte) uint32 {
	if len(itemRaw) < 10 {
		return 0
	}
	return binary.LittleEndian.Uint32(itemRaw[6:10])
}

// AuctionSearchPage is a parsed op186 search response.
type AuctionSearchPage struct {
	Total     uint32
	Listings  []AuctionListing
	PageBytes int
}

// ParseAuctionSearchPage parses the op186 body
// `success, mode, u32 total, u16 page count, rows[168]`.
func ParseAuctionSearchPage(body []byte) (AuctionSearchPage, error) {
	r := wireReader{body: body}
	if r.byte() != 1 {
		code := byte(0)
		if len(body) >= 2 {
			code = body[1]
		}
		return AuctionSearchPage{}, fmt.Errorf("cn90 auction search failed with code %d", code)
	}
	_ = r.byte() // mode
	page := AuctionSearchPage{Total: r.u32()}
	count := int(r.u16())
	if r.err != nil {
		return AuctionSearchPage{}, fmt.Errorf("cn90 auction search response: %v", r.err)
	}
	page.Listings = make([]AuctionListing, 0, count)
	for i := 0; i < count; i++ {
		row := r.raw(auctionSearchRowBytes)
		if row == nil {
			return AuctionSearchPage{}, fmt.Errorf("cn90 auction search row %d is truncated", i)
		}
		listing := AuctionListing{
			ListingID:     binary.LittleEndian.Uint64(row[0:8]),
			CurrentBid:    binary.LittleEndian.Uint32(row[9:13]),
			BuyoutPrice:   binary.LittleEndian.Uint32(row[13:17]),
			RemainingSecs: binary.LittleEndian.Uint32(row[17:21]),
			SellerRaw:     append([]byte(nil), row[21:34]...),
			ItemRaw:       append([]byte(nil), row[43:164]...),
		}
		listing.SellerName = DecodeWireName(listing.SellerRaw)
		page.Listings = append(page.Listings, listing)
	}
	if r.err != nil {
		return AuctionSearchPage{}, fmt.Errorf("cn90 auction search response: %v", r.err)
	}
	page.PageBytes = r.offset
	return page, nil
}

// ParseAuctionMyListings parses the op188 body
// `success, mode, u8 count, rows[151]`.
func ParseAuctionMyListings(body []byte) ([]AuctionListing, error) {
	rows, err := parseAuctionMineRows(body, auctionMyListingBytes, false)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// ParseAuctionMyBids parses the op189 and op190 bodies
// `success, mode, u16 count, rows[159]`.
func ParseAuctionMyBids(body []byte) ([]AuctionListing, error) {
	return parseAuctionMineRows(body, auctionMyBidRowBytes, true)
}

func parseAuctionMineRows(body []byte, rowBytes int, wideCount bool) ([]AuctionListing, error) {
	if len(body) < 1 || body[0] != 1 {
		code := byte(0)
		if len(body) >= 2 {
			code = body[1]
		}
		return nil, fmt.Errorf("cn90 auction listing request failed with code %d", code)
	}
	// success, the mode byte the server writes, then the count in one word: a
	// u8 for op188 and a u16 for op189/op190 (sendAuctionMine).
	r := wireReader{body: body, offset: 2}
	count := 0
	if wideCount {
		count = int(r.u16())
	} else {
		count = int(r.byte())
	}
	if r.err != nil {
		return nil, fmt.Errorf("cn90 auction listing response: %v", r.err)
	}
	rows := make([]AuctionListing, 0, count)
	for i := 0; i < count; i++ {
		row := r.raw(rowBytes)
		if row == nil {
			return nil, fmt.Errorf("cn90 auction listing row %d is truncated", i)
		}
		listing := AuctionListing{
			ListingID:   binary.LittleEndian.Uint64(row[0:8]),
			CurrentBid:  binary.LittleEndian.Uint32(row[8:12]),
			BuyoutPrice: binary.LittleEndian.Uint32(row[12:16]),
		}
		switch rowBytes {
		case auctionMyListingBytes:
			listing.ItemRaw = append([]byte(nil), row[30:151]...)
		case auctionMyBidRowBytes:
			listing.SellerRaw = append([]byte(nil), row[24:37]...)
			listing.SellerName = DecodeWireName(listing.SellerRaw)
			listing.ItemRaw = append([]byte(nil), row[38:159]...)
		}
		rows = append(rows, listing)
	}
	if r.err != nil {
		return nil, fmt.Errorf("cn90 auction listing response: %v", r.err)
	}
	return rows, nil
}

// ParseAuctionAveragePrice parses the op182 body
// `success, mode, u32 price, 13 reserved u32`. Only the first value is a price
// in this client build; the rest are zero-compatible reserve fields.
func ParseAuctionAveragePrice(body []byte) (uint32, error) {
	r := wireReader{body: body}
	if r.byte() != 1 {
		code := byte(0)
		if len(body) >= 2 {
			code = body[1]
		}
		return 0, fmt.Errorf("cn90 auction average price failed with code %d", code)
	}
	_ = r.byte() // mode
	price := r.u32()
	if r.err != nil {
		return 0, fmt.Errorf("cn90 auction average price response is truncated")
	}
	return price, nil
}
