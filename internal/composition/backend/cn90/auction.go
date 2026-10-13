package cn90

import (
	"context"
	"fmt"
	"strconv"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	foundationlog "robot/internal/foundation/log"
	protocol "robot/internal/protocol/cn90"
	"robot/internal/shared"
)

// The auction behavior of one robot session: keep a few own listings on the
// board so players see stock, and buy player listings that sit under the price
// model. Server authority for every gate is the server tree's
// `internal/services/dnfbridge/gameplay_module_auction.go` with the durable
// owner in `internal/modules/dnf/auction`.
//
// The stock itself is not produced here: the offline stage (bag.go) writes the
// bag rows and the gold before the robot logs in, and this file lists those
// exact rows. The plan is a pure function of the character id, so the runtime
// recomputes the same (slot, item, count) triple the seeder wrote.

// auctionBuyPerRound bounds how many listings one round takes, so a page full
// of cheap stock cannot drain a wallet in a single tick.
const auctionBuyPerRound = 3

// auctionBuyScanPages is how many search pages one round reads. The server
// answers the board one fixed-size page at a time, so reading a large board
// whole would cost hundreds of round trips per robot per round; the round reads
// a window of this many pages instead.
const auctionBuyScanPages = 4

// auctionRoundTimeout bounds one whole round: a listing top-up, a browse and
// the buys it decides on.
const auctionRoundTimeout = 30 * time.Second

// auctionListingCeiling mirrors the server's per-character listing cap. The
// runtime config normalizes to the same number; repeating it here keeps an
// unnormalized config (a direct caller, a test) from driving the round past a
// gate the server would refuse.
const auctionListingCeiling = 10

// AuctionPolicy is one round's price model, derived from the runtime config.
type AuctionPolicy struct {
	StockStacks  int
	ListPerRobot int
	ListUnitPct  int
	BuyEnabled   bool
	BuyUnitPct   int
	BuyMaxTotal  int
}

// AuctionPolicyFromConfig reads the auction block of the runtime config. The
// stock count has to match the seam the offline seeder used, because the plan
// is the same pure function of the character id on both sides.
//
// A listing count of zero is honored, not defaulted: the configured value
// decides how many live listings a character keeps, and zero leaves it as a
// pure buyer.
func AuctionPolicyFromConfig(rc robotconfig.RuntimeConfig) AuctionPolicy {
	listPerRobot := rc.AuctionListPerRobot
	if listPerRobot < 0 {
		listPerRobot = 0
	}
	if listPerRobot > auctionListingCeiling {
		listPerRobot = auctionListingCeiling
	}
	stockStacks := rc.AuctionStockStacks
	if stockStacks <= 0 {
		stockStacks = cn90AuctionStockDefaultStacks
	}
	return AuctionPolicy{
		StockStacks:  stockStacks,
		ListPerRobot: listPerRobot,
		ListUnitPct:  rc.AuctionListUnitPercent,
		BuyEnabled:   rc.AuctionBuyEnabled,
		BuyUnitPct:   rc.AuctionBuyUnitPercent,
		BuyMaxTotal:  rc.AuctionBuyMaxTotal,
	}
}

// auctionReferencePrice is the catalog's own price for one item: the PVF price
// when it carries one, the value field otherwise. Zero means the item cannot be
// priced, and both listing and buying skip it.
func auctionReferencePrice(item shared.EquipmentCatalogItem) int {
	if item.Price > 0 {
		return item.Price
	}
	if item.Value > 0 {
		return item.Value
	}
	return 0
}

// auctionSession is the slice of the 90CN session the auction round uses.
type auctionSession interface {
	AuctionList(context.Context, AuctionStack, uint32) error
	AuctionMyListings(context.Context) ([]protocol.AuctionListing, error)
	AuctionSearch(context.Context, []uint32, uint32) (protocol.AuctionSearchPage, error)
	AuctionBuy(context.Context, protocol.AuctionListing, uint32) error
}

// AuctionRound runs one auction cycle for uid and reports how many listings it
// added and how many it bought. It is a no-op for a session that is not on the
// 90CN adapter, so the scheduler can offer every backend the same schedule.
func (t *ActionTransport) AuctionRound(uid int, rc robotconfig.RuntimeConfig) (int, int, error) {
	if t == nil {
		return 0, 0, fmt.Errorf("90CN auction round needs a transport")
	}
	session, err := t.session(uid)
	if err != nil {
		return 0, 0, err
	}
	auction, ok := session.(auctionSession)
	if !ok {
		return 0, 0, fmt.Errorf("90CN session does not expose the auction protocol")
	}
	t.mu.RLock()
	status := t.status[uid]
	t.mu.RUnlock()
	if status.CID <= 0 {
		return 0, 0, fmt.Errorf("90CN auction round needs a selected character for uid %d", uid)
	}
	policy := AuctionPolicyFromConfig(rc)
	ctx, cancel := context.WithTimeout(context.Background(), auctionRoundTimeout)
	defer cancel()

	own, err := auction.AuctionMyListings(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("90CN auction listings for uid %d: %w", uid, err)
	}
	ownIDs := make(map[uint64]struct{}, len(own))
	ownItems := make(map[uint32]struct{}, len(own))
	for _, listing := range own {
		ownIDs[listing.ListingID] = struct{}{}
		if itemID := protocol.AuctionItemID(listing.ItemRaw); itemID != 0 {
			ownItems[itemID] = struct{}{}
		}
	}

	listed, err := t.auctionTopUp(ctx, auction, uid, status.CID, ownItems, len(own), policy)
	if err != nil {
		return listed, 0, err
	}
	if !policy.BuyEnabled {
		return listed, 0, nil
	}
	bought, err := t.auctionBuyUnderpriced(ctx, auction, uid, ownIDs, policy)
	if err != nil {
		return listed, bought, err
	}
	return listed, bought, nil
}

// auctionTopUp lists the character's seeded stacks until it holds ListPerRobot
// live listings. A stack whose item is already listed is skipped: the server
// takes the stack out of the bag into escrow when it accepts a listing, so the
// same row cannot be listed twice.
func (t *ActionTransport) auctionTopUp(ctx context.Context, auction auctionSession, uid, cid int, listedItems map[uint32]struct{}, live int, policy AuctionPolicy) (int, error) {
	remaining := policy.ListPerRobot - live
	if remaining <= 0 {
		return 0, nil
	}
	plan := cn90AuctionStockPlan(t.auctionCatalog(), strconv.Itoa(cid), policy.StockStacks)
	added := 0
	attempts := 0
	// One attempt per missing listing: a stack the server refuses (its item was
	// sold while the robot was online, so the seeded row is stale) must not turn
	// one round into a walk over the whole plan.
	for _, stack := range plan {
		if added >= remaining || attempts >= remaining {
			break
		}
		if _, exists := listedItems[uint32(stack.ItemID)]; exists {
			continue
		}
		attempts++
		unit := auctionListUnitPrice(auctionReferencePrice(stack.Item), stack.Count, policy.ListUnitPct)
		if unit == 0 {
			continue
		}
		err := auction.AuctionList(ctx, AuctionStack{
			ListType: cn90MainInventoryList,
			Slot:     uint16(stack.Slot),
			ItemID:   uint32(stack.ItemID),
			Count:    uint32(stack.Count),
		}, uint32(unit))
		if err != nil {
			// A stack that was sold while the robot was online is still in the
			// plan but no longer in the bag, so the server refuses it. The next
			// startup refills the bag; this round moves on.
			foundationlog.Robotf("CN90_AUCTION_ROUND_LIST_FAILED uid=%d item=%d slot=%d err=%v\n", uid, stack.ItemID, stack.Slot, err)
			continue
		}
		added++
	}
	return added, nil
}

// auctionBuyUnderpriced buys the listings that sit at or under the buy gate.
//
// The server orders the board oldest first, so the listing every earlier round
// has already looked at and passed over sits at the front, and the one a player
// has just put up sits at the back. The round therefore reads a window at the
// back of the board: re-reading the front would only re-examine listings that
// no previous round found worth taking. The first request is what reveals the
// page size and the board size, and it is reused as the window's first page
// whenever the board is small enough to fit the window whole.
//
// Cheapest price per unit first is deliberately not applied: the budget and the
// per-round quota cap the round either way.
func (t *ActionTransport) auctionBuyUnderpriced(ctx context.Context, auction auctionSession, uid int, ownIDs map[uint64]struct{}, policy AuctionPolicy) (int, error) {
	head, err := auction.AuctionSearch(ctx, nil, 0)
	if err != nil {
		return 0, fmt.Errorf("90CN auction browse for uid %d: %w", uid, err)
	}
	pageSize := len(head.Listings)
	if pageSize == 0 {
		return 0, nil
	}
	offset := uint32(0)
	if window := uint32(pageSize * auctionBuyScanPages); head.Total > window {
		offset = (head.Total - window) / uint32(pageSize) * uint32(pageSize)
	}
	items := t.stackableIndex()
	spent := 0
	bought := 0
	for scanned := 0; scanned < auctionBuyScanPages && offset < head.Total; scanned++ {
		page := head
		if offset > 0 {
			fetched, err := auction.AuctionSearch(ctx, nil, offset)
			if err != nil {
				return bought, fmt.Errorf("90CN auction browse at offset %d for uid %d: %w", offset, uid, err)
			}
			page = fetched
		}
		if len(page.Listings) == 0 {
			break
		}
		for _, listing := range page.Listings {
			if bought >= auctionBuyPerRound {
				break
			}
			if _, mine := ownIDs[listing.ListingID]; mine {
				continue
			}
			if listing.BuyoutPrice == 0 {
				continue
			}
			itemID := protocol.AuctionItemID(listing.ItemRaw)
			item, known := items[int(itemID)]
			if !known {
				continue
			}
			reference := auctionReferencePrice(item)
			if reference <= 0 {
				continue
			}
			listed := protocol.AuctionItemCount(listing.ItemRaw)
			if listed == 0 {
				listed = 1
			}
			unit := int(listing.BuyoutPrice) / int(listed)
			if unit == 0 || unit > auctionBuyUnitGate(reference, policy.BuyUnitPct) {
				continue
			}
			total := int(listing.BuyoutPrice)
			if policy.BuyMaxTotal > 0 && spent+total > policy.BuyMaxTotal {
				continue
			}
			if err := auction.AuctionBuy(ctx, listing, 0); err != nil {
				foundationlog.Robotf("CN90_AUCTION_ROUND_BUY_FAILED uid=%d listing=%d item=%d err=%v\n", uid, listing.ListingID, itemID, err)
				continue
			}
			spent += total
			bought++
		}
		if bought >= auctionBuyPerRound {
			break
		}
		offset += uint32(len(page.Listings))
	}
	return bought, nil
}

// auctionCatalog is the stockable catalog the assembly handed the transport.
// It is written once before the runtime starts and only read afterwards.
func (t *ActionTransport) auctionCatalog() []shared.EquipmentCatalogItem {
	if t == nil {
		return nil
	}
	return t.stackables
}

// stackableIndex maps the stocked catalog by item id.
func (t *ActionTransport) stackableIndex() map[int]shared.EquipmentCatalogItem {
	catalog := t.auctionCatalog()
	index := make(map[int]shared.EquipmentCatalogItem, len(catalog))
	for _, item := range catalog {
		if item.ID > 0 {
			index[item.ID] = item
		}
	}
	return index
}

// auctionListUnitPrice marks the reference price up by the configured
// percentage and keeps the whole buyout under the server ceiling. A percentage
// below 100 is raised to 100: a robot never undercuts its own reference.
func auctionListUnitPrice(reference, count, percent int) int {
	if reference <= 0 || count <= 0 {
		return 0
	}
	if percent < 100 {
		percent = 100
	}
	unit := reference * percent / 100
	if unit <= 0 {
		unit = 1
	}
	if ceiling := int(protocol.AuctionMaxPrice) / count; unit > ceiling {
		unit = ceiling
	}
	return unit
}

// auctionBuyUnitGate is the highest unit price the robot still pays. A
// percentage above 100 is lowered to 100 for the same reason listings are
// floored at 100: the reference price is the anchor, not a suggestion.
func auctionBuyUnitGate(reference, percent int) int {
	if reference <= 0 {
		return 0
	}
	if percent > 100 {
		percent = 100
	}
	if percent <= 0 {
		return 0
	}
	gate := reference * percent / 100
	if gate <= 0 {
		gate = 1
	}
	return gate
}
