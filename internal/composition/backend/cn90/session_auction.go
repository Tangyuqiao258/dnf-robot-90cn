package cn90

import (
	"context"
	"fmt"
	"time"

	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
)

// The 90CN auction house at session level. Server authority: the server tree's
// `internal/services/dnfbridge/gameplay_module_auction.go` (dispatch and gates)
// with the durable owner in `internal/modules/dnf/auction`.
//
// Two properties of the server shape every method here:
//
//   - op183 answers twice. A rejected or accepted registration arrives as a
//     class1/op183 {ok, code} envelope, while the auction service-open notice
//     arrives as class0/op183 {0|1, 1} at the first auction request of a
//     session. Only the class byte separates them, so every waiter filters on
//     ClassCommand.
//   - A registration is validated against the character's own bound inventory
//     row at the exact (listType, slot) it names, and a purchase is charged to
//     the character's gold, so the caller has to know which bag row it lists.
//     That knowledge stays with the caller (the offline stock stage writes
//     those rows) and arrives here as AuctionStack.

// auctionRequestTimeout bounds one auction request/response exchange. The
// server answers from memory, so this only guards a wedged session.
const auctionRequestTimeout = 10 * time.Second

// AuctionStack names one bag row the caller wants to list. ListType is the
// server's inventory list id (main bag 0 or the current avatar list) and Slot
// is the row's slot inside it.
type AuctionStack struct {
	ListType byte
	Slot     uint16
	ItemID   uint32
	Count    uint32
}

// AuctionList lists one bag stack as a buyout-only listing and waits for the
// class1/op183 acknowledgement. Bidding stays disabled by sending the
// no-start-price sentinel, which is what a robot should do: a bid-only listing
// can sit unsold for its whole duration, while a buyout is what a player can
// act on immediately.
func (s *Session) AuctionList(ctx context.Context, stack AuctionStack, unitPrice uint32) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN session is closed")
	}
	if stack.ItemID == 0 || stack.Count == 0 {
		return fmt.Errorf("90CN auction listing needs an item id and a count")
	}
	if unitPrice == 0 {
		return fmt.Errorf("90CN auction listing needs a unit price")
	}
	// The server refuses a buyout above its durable ceiling and requires the
	// buyout to divide evenly by the count, which a unit price times count
	// always does.
	buyout := uint64(unitPrice) * uint64(stack.Count)
	if buyout > uint64(protocol.AuctionMaxPrice) {
		return fmt.Errorf("90CN auction buyout %d for item %d exceeds the server ceiling %d", buyout, stack.ItemID, protocol.AuctionMaxPrice)
	}
	ctx, cancel := auctionRequestContext(ctx)
	defer cancel()
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdAuctionRegister {
			return
		}
		ok, code := protocol.AuctionAckResult(packet.Body)
		if ok {
			deliverAck(ack, nil)
			return
		}
		deliverAck(ack, fmt.Errorf("rejected with code %d", code))
	})
	defer cleanup()
	if err := s.client.AuctionRegister(ctx, stack.ListType, stack.Slot, stack.ItemID, stack.Count, protocol.AuctionNoStartPrice, uint32(buyout)); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			foundationlog.Robotf("CN90_AUCTION_LIST_REJECTED cid=%d item=%d count=%d err=%v\n", s.selfCharacterID, stack.ItemID, stack.Count, err)
			return fmt.Errorf("90CN auction register item=%d: %w", stack.ItemID, err)
		}
		foundationlog.Robotf("CN90_AUCTION_LISTED cid=%d item=%d count=%d unit=%d buyout=%d\n", s.selfCharacterID, stack.ItemID, stack.Count, unitPrice, buyout)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("90CN auction register confirmation: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during auction register")
	}
}

// AuctionCancel withdraws one of the session's own listings and waits for the
// class1/op184 acknowledgement. The server returns the stack to the seller's
// bag, so nothing has to be claimed afterwards.
func (s *Session) AuctionCancel(ctx context.Context, listingID uint64) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN session is closed")
	}
	if listingID == 0 {
		return fmt.Errorf("90CN auction cancel needs a listing id")
	}
	ctx, cancel := auctionRequestContext(ctx)
	defer cancel()
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdAuctionCancel {
			return
		}
		ok, code := protocol.AuctionAckResult(packet.Body)
		if ok {
			deliverAck(ack, nil)
			return
		}
		deliverAck(ack, fmt.Errorf("rejected with code %d", code))
	})
	defer cleanup()
	if err := s.client.AuctionCancel(ctx, listingID); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			foundationlog.Robotf("CN90_AUCTION_CANCEL_REJECTED cid=%d listing=%d err=%v\n", s.selfCharacterID, listingID, err)
			return fmt.Errorf("90CN auction cancel listing=%d: %w", listingID, err)
		}
		foundationlog.Robotf("CN90_AUCTION_CANCELLED cid=%d listing=%d\n", s.selfCharacterID, listingID)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("90CN auction cancel confirmation: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during auction cancel")
	}
}

// AuctionBuy buys count pieces of one listing and waits for the class1/op334
// acknowledgement. Pass count 0 to take the whole listing. The unit price is
// derived from the listing itself, because the server charges
// `unit price * count` and refuses anything else: the search row carries the
// listed stack size, so the buyout divides back into its unit price exactly.
//
// Both the item and the gold move by system mail (the seller's proceeds as
// mail to the seller, the purchased stack as mail to the buyer), so a
// successful buy leaves a claim waiting in the session's mailbox.
func (s *Session) AuctionBuy(ctx context.Context, listing protocol.AuctionListing, count uint32) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("90CN session is closed")
	}
	if listing.ListingID == 0 {
		return fmt.Errorf("90CN auction buy needs a listing id")
	}
	if listing.BuyoutPrice == 0 {
		return fmt.Errorf("90CN auction listing %d has no buyout price", listing.ListingID)
	}
	itemID := protocol.AuctionItemID(listing.ItemRaw)
	if itemID == 0 {
		return fmt.Errorf("90CN auction listing %d carries no readable item", listing.ListingID)
	}
	listed := protocol.AuctionItemCount(listing.ItemRaw)
	if listed == 0 {
		listed = 1
	}
	if count == 0 || count > listed {
		count = listed
	}
	unitPrice := listing.BuyoutPrice / listed
	if unitPrice == 0 {
		return fmt.Errorf("90CN auction listing %d has a buyout below its stack count %d", listing.ListingID, listed)
	}
	price := unitPrice * count
	ctx, cancel := auctionRequestContext(ctx)
	defer cancel()
	ack := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdAuctionBuyPiece {
			return
		}
		ok, code := protocol.AuctionAckResult(packet.Body)
		if ok {
			deliverAck(ack, nil)
			return
		}
		deliverAck(ack, fmt.Errorf("rejected with code %d", code))
	})
	defer cleanup()
	if err := s.client.AuctionBuyPiece(ctx, price, count, listing.ListingID, listing.SellerRaw, itemID); err != nil {
		return err
	}
	select {
	case err := <-ack:
		if err != nil {
			foundationlog.Robotf("CN90_AUCTION_BUY_REJECTED cid=%d listing=%d item=%d count=%d price=%d err=%v\n", s.selfCharacterID, listing.ListingID, itemID, count, price, err)
			return fmt.Errorf("90CN auction buy listing=%d: %w", listing.ListingID, err)
		}
		foundationlog.Robotf("CN90_AUCTION_BOUGHT cid=%d listing=%d item=%d count=%d price=%d\n", s.selfCharacterID, listing.ListingID, itemID, count, price)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("90CN auction buy confirmation: %w", ctx.Err())
	case <-s.Done():
		return fmt.Errorf("90CN session ended during auction buy")
	}
}

// AuctionSearch queries the auction page. An empty id list browses the page
// without an item filter (op187), which is what a buyer with a price model
// wants; a non-empty list asks for those item keys only (op186). The server
// answers both on CmdAuctionSearchItem.
func (s *Session) AuctionSearch(ctx context.Context, itemIDs []uint32, offset uint32) (protocol.AuctionSearchPage, error) {
	if s == nil || s.client == nil {
		return protocol.AuctionSearchPage{}, fmt.Errorf("90CN session is closed")
	}
	ctx, cancel := auctionRequestContext(ctx)
	defer cancel()
	page := make(chan protocol.AuctionSearchPage, 1)
	failure := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdAuctionSearchItem {
			return
		}
		parsed, err := protocol.ParseAuctionSearchPage(packet.Body)
		if err != nil {
			deliverErr(failure, err)
			return
		}
		select {
		case page <- parsed:
		default:
		}
	})
	defer cleanup()
	if len(itemIDs) == 0 {
		if err := s.client.AuctionSearchCategory(ctx, offset); err != nil {
			return protocol.AuctionSearchPage{}, err
		}
	} else if err := s.client.AuctionSearchByItem(ctx, offset, itemIDs); err != nil {
		return protocol.AuctionSearchPage{}, err
	}
	select {
	case parsed := <-page:
		return parsed, nil
	case err := <-failure:
		return protocol.AuctionSearchPage{}, err
	case <-ctx.Done():
		return protocol.AuctionSearchPage{}, fmt.Errorf("90CN auction search response: %w", ctx.Err())
	case <-s.Done():
		return protocol.AuctionSearchPage{}, fmt.Errorf("90CN session ended during auction search")
	}
}

// AuctionMyListings reads the character's own live listings (op188). The rows
// carry no seller name: they are all the session's own.
func (s *Session) AuctionMyListings(ctx context.Context) ([]protocol.AuctionListing, error) {
	if s == nil || s.client == nil {
		return nil, fmt.Errorf("90CN session is closed")
	}
	ctx, cancel := auctionRequestContext(ctx)
	defer cancel()
	rows := make(chan []protocol.AuctionListing, 1)
	failure := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdAuctionMyListings {
			return
		}
		parsed, err := protocol.ParseAuctionMyListings(packet.Body)
		if err != nil {
			deliverErr(failure, err)
			return
		}
		select {
		case rows <- parsed:
		default:
		}
	})
	defer cleanup()
	if err := s.client.AuctionMyListings(ctx); err != nil {
		return nil, err
	}
	select {
	case parsed := <-rows:
		return parsed, nil
	case err := <-failure:
		return nil, err
	case <-ctx.Done():
		return nil, fmt.Errorf("90CN auction listing response: %w", ctx.Err())
	case <-s.Done():
		return nil, fmt.Errorf("90CN session ended during auction listing query")
	}
}

// AuctionAveragePrice asks the server for an item's recent average price
// (op182), the only price reference the server itself keeps.
func (s *Session) AuctionAveragePrice(ctx context.Context, itemID uint32) (uint32, error) {
	if s == nil || s.client == nil {
		return 0, fmt.Errorf("90CN session is closed")
	}
	if itemID == 0 {
		return 0, fmt.Errorf("90CN auction average price needs an item id")
	}
	ctx, cancel := auctionRequestContext(ctx)
	defer cancel()
	prices := make(chan uint32, 1)
	failure := make(chan error, 1)
	cleanup := s.setPacketObserver(func(packet protocol.Packet) {
		if packet.Class != protocol.ClassCommand || packet.Type != protocol.CmdAuctionAverage {
			return
		}
		parsed, err := protocol.ParseAuctionAveragePrice(packet.Body)
		if err != nil {
			deliverErr(failure, err)
			return
		}
		select {
		case prices <- parsed:
		default:
		}
	})
	defer cleanup()
	if err := s.client.AuctionAveragePrice(ctx, itemID); err != nil {
		return 0, err
	}
	select {
	case price := <-prices:
		return price, nil
	case err := <-failure:
		return 0, err
	case <-ctx.Done():
		return 0, fmt.Errorf("90CN auction average price response: %w", ctx.Err())
	case <-s.Done():
		return 0, fmt.Errorf("90CN session ended during auction average price query")
	}
}

// auctionRequestContext bounds one exchange without shortening a caller's own
// deadline.
func auctionRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, auctionRequestTimeout)
}

// deliverAck hands one outcome to a waiter that may already have given up.
func deliverAck(ack chan error, err error) {
	select {
	case ack <- err:
	default:
	}
}

// deliverErr hands one parse failure to a waiter that may already have given
// up.
func deliverErr(failure chan error, err error) {
	select {
	case failure <- err:
	default:
	}
}
