package cn90

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	foundationlog "robot/internal/foundation/log"
	"robot/internal/shared"
)

// The auction stock stage gives a robot character what the server's auction
// house requires before the robot can trade at all: a tradeable stack in a
// known bag slot and a gold balance. Both live in the server's own tables, so
// they are written offline while the character is away from the channel --
// the server replaces the whole "slots" collection on every inventory save,
// which would discard a row written during a live session.
const (
	// cn90AuctionStockBaseSlot starts the stock above the two reserved main-bag
	// rows: slot 0 is the client's gold wallet and slot 1 the revive-coin
	// wallet. The range itself is the material/consumable page the shipped
	// character keeps its own stacks in.
	cn90AuctionStockBaseSlot = 113
	// cn90AuctionStockMarker is the extra key that marks a generated stock row
	// and the reconcile gate's probe: a character that already carries one is
	// left alone so operator edits survive, and a character that lost its stock
	// to a sale is refilled from the pool.
	cn90AuctionStockMarker = "robot_auction_stock"
	// cn90AuctionItemStackRawSize is the 0x77 inventory row the current client
	// and the server's item-list projection both read.
	cn90AuctionItemStackRawSize = 0x77
	// cn90AuctionStockMaxCount bounds a seeded stack when the item's own stack
	// limit is unknown, and cn90AuctionStockMinCount keeps a stack large enough
	// for a piece purchase to divide evenly.
	cn90AuctionStockMaxCount = 200
	cn90AuctionStockMinCount = 20
	// cn90AuctionStockDefaultStacks is the stock depth used when the runtime
	// config carries none. The seeder and the live round must agree on it,
	// because the plan is a pure function of the character id and this count.
	cn90AuctionStockDefaultStacks = 6
	// cn90MainInventoryList is the server's MainInventoryListType: the only
	// personal list the auction register gate accepts beside the avatar list.
	cn90MainInventoryList = 0
)

// cn90AuctionStockPool is the fixed material/consumable pool the robots stock
// the auction house with. Every id is a plain tradeable stackable: the
// server's register gate rejects an AttachType containing "account" or "bind",
// and the shipped character already carries each of these items, so the client
// renders them without a PVF surprise.
var cn90AuctionStockPool = []int{
	1072, 1298, 2600021, 2600656, 3027, 3030, 3151, 3019, 3050, 3167, 3330, 7163,
}

// cn90AuctionStockStack is one planned bag row.
type cn90AuctionStockStack struct {
	ItemID int
	Slot   int
	Count  int
	Item   shared.EquipmentCatalogItem
}

// cn90AuctionStockPlan chooses a character's stock from the pool. The choice is
// derived from the character id so one robot keeps its stock across restarts
// while the fleet spreads over the whole pool.
func cn90AuctionStockPlan(catalog []shared.EquipmentCatalogItem, characterID string, stacks int) []cn90AuctionStockStack {
	if stacks <= 0 || len(cn90AuctionStockPool) == 0 {
		return nil
	}
	if stacks > len(cn90AuctionStockPool) {
		stacks = len(cn90AuctionStockPool)
	}
	byID := make(map[int]shared.EquipmentCatalogItem, len(catalog))
	for _, item := range catalog {
		if item.ID > 0 {
			byID[item.ID] = item
		}
	}
	seed := cn90DeterministicValue(characterID)
	plan := make([]cn90AuctionStockStack, 0, stacks)
	for index := 0; index < len(cn90AuctionStockPool) && len(plan) < stacks; index++ {
		position := (int(seed) + index) % len(cn90AuctionStockPool)
		item, ok := byID[cn90AuctionStockPool[position]]
		if !ok || !cn90AuctionStockTradeable(item) {
			continue
		}
		count := cn90AuctionStockCount(item, seed, uint32(index))
		plan = append(plan, cn90AuctionStockStack{
			ItemID: item.ID,
			Slot:   cn90AuctionStockBaseSlot + len(plan),
			Count:  count,
			Item:   item,
		})
	}
	return plan
}

// cn90AuctionStockTradeable mirrors the server's own register validator: an
// item whose attach type mentions an account or character binding can never be
// listed, so it never enters the pool.
func cn90AuctionStockTradeable(item shared.EquipmentCatalogItem) bool {
	if item.ID <= 0 || item.GrantBlocked || strings.TrimSpace(item.Path) == "" {
		return false
	}
	attach := strings.ToLower(strings.TrimSpace(item.Attach))
	if strings.Contains(attach, "account") || strings.Contains(attach, "bind") {
		return false
	}
	return !item.NoTrade && !item.TradeBlock
}

// cn90AuctionStockCount derives a stable stack size, bounded by the item's own
// stack limit when the catalog carries one.
func cn90AuctionStockCount(item shared.EquipmentCatalogItem, seed uint32, index uint32) int {
	span := cn90AuctionStockMaxCount - cn90AuctionStockMinCount + 1
	count := cn90AuctionStockMinCount + int((seed>>(index%16)+uint32(index*7))%uint32(span))
	if item.StackLimit > 0 && count > item.StackLimit {
		count = item.StackLimit
	}
	if count < 1 {
		count = 1
	}
	return count
}

// cn90DeterministicValue hashes a key into a stable 32-bit value.
func cn90DeterministicValue(seed string) uint32 {
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(seed))
	return hasher.Sum32()
}

// buildCN90ItemStackRawEntryCount builds the 0x77 bag row for one stack: slot,
// item id, the live count at the create-value offset the item-list projection
// patches, and the item id repeated at +14 the way the server's own rows carry
// it.
func buildCN90ItemStackRawEntryCount(slot, itemID, count int) []byte {
	raw := make([]byte, cn90AuctionItemStackRawSize)
	binary.LittleEndian.PutUint16(raw[0x00:0x02], uint16(slot))
	binary.LittleEndian.PutUint32(raw[0x02:0x06], uint32(itemID))
	binary.LittleEndian.PutUint32(raw[0x06:0x0A], uint32(count))
	binary.LittleEndian.PutUint32(raw[0x0E:0x12], uint32(itemID))
	return raw
}

// reconcileRobotAuctionStock refills the account's auction stock when the
// character is missing pool items or sits below the gold floor. It is its own
// stage rather than part of the equipment loadout: the equipment stage re-rolls
// its gear when it runs, so a partially sold bag must not pull it in.
func (a *SQLiteLoadoutApplier) reconcileRobotAuctionStock(ctx context.Context, account string) error {
	if !a.Config.AuctionEnabled {
		return nil
	}
	account = strings.TrimSpace(account)
	if account == "" {
		return fmt.Errorf("90CN auction stock account is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	seeded := false
	err := a.persistenceDo(ctx, func(jobCtx context.Context) error {
		db, release, err := a.database(jobCtx)
		if err != nil {
			return fmt.Errorf("open 90CN auction stock database: %w", err)
		}
		defer release()
		row, err := loadCharacterProfileRow(jobCtx, db, account)
		if err != nil {
			return err
		}
		needs, err := a.auctionStockNeedsRefill(jobCtx, db, row.characterID)
		if err != nil {
			return err
		}
		if !needs {
			return nil
		}
		if err := a.applyRobotAuctionStock(jobCtx, db, row.characterID); err != nil {
			return err
		}
		seeded = true
		return nil
	})
	if err != nil {
		return err
	}
	if seeded {
		foundationlog.Robotf("CN90_AUCTION_STOCK_SEEDED account=%s\n", account)
	}
	return nil
}

// auctionStockNeedsRefill reports whether any planned pool item is missing from
// the character's bag or the gold balance is below the configured floor.
func (a *SQLiteLoadoutApplier) auctionStockNeedsRefill(ctx context.Context, db *sql.DB, characterID string) (bool, error) {
	plan := cn90AuctionStockPlan(a.Stackables, characterID, a.Config.AuctionStockStacks)
	if len(plan) == 0 {
		return false, nil
	}
	held, err := a.currentAuctionStock(ctx, db, characterID)
	if err != nil {
		return false, err
	}
	for _, stack := range plan {
		if _, ok := held[stack.ItemID]; !ok {
			return true, nil
		}
	}
	return cn90GoldBelowFloor(ctx, db, characterID, a.Config.AuctionStockGold)
}

// cn90GoldBelowFloor reads the character's gold against the configured floor.
func cn90GoldBelowFloor(ctx context.Context, db *sql.DB, characterID string, floor int) (bool, error) {
	if floor <= 0 {
		return false, nil
	}
	var gold int
	if err := db.QueryRowContext(ctx,
		`SELECT gold FROM `+dnfCharactersTable+` WHERE character_id=?`, characterID).Scan(&gold); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("read 90CN gold character=%s: %w", characterID, err)
	}
	return gold < floor, nil
}

// applyRobotAuctionStock seeds the character's auction stock and gold floor.
// An item already in the bag is left at its recorded count, so the stage is
// idempotent for a robot that still holds its stock and a refill for one whose
// stacks were sold.
func (a *SQLiteLoadoutApplier) applyRobotAuctionStock(ctx context.Context, db *sql.DB, characterID string) error {
	if !a.Config.AuctionEnabled {
		return nil
	}
	stacks := a.Config.AuctionStockStacks
	if stacks <= 0 {
		stacks = cn90AuctionStockDefaultStacks
	}
	plan := cn90AuctionStockPlan(a.Stackables, characterID, stacks)
	held, err := a.currentAuctionStock(ctx, db, characterID)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+dnfInventoriesTable+` (character_id, updated_at) VALUES (?, ?) ON CONFLICT(character_id) DO UPDATE SET updated_at=excluded.updated_at`,
		characterID, now); err != nil {
		return fmt.Errorf("upsert 90CN inventory parent character=%s: %w", characterID, err)
	}
	for _, stack := range plan {
		// A stack the robot still holds keeps whatever count the server left it
		// at; only a missing row is (re)created.
		if _, ok := held[stack.ItemID]; ok {
			continue
		}
		if err := insertCN90AuctionStockRows(ctx, tx, characterID, stack); err != nil {
			return err
		}
	}
	if err := applyCN90GoldFloor(ctx, tx, characterID, a.Config.AuctionStockGold); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit 90CN auction stock character=%s: %w", characterID, err)
	}
	return nil
}

// currentAuctionStock reports which pool items the character's bag already
// holds, so a restart refills only what was sold.
func (a *SQLiteLoadoutApplier) currentAuctionStock(ctx context.Context, db *sql.DB, characterID string) (map[int]struct{}, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT item_id FROM `+dnfInventoryItemsTable+` WHERE character_id=? AND collection_name=? AND item_count>0`,
		characterID, cn90InventorySlotCollection)
	if err != nil {
		return nil, fmt.Errorf("read 90CN auction stock character=%s: %w", characterID, err)
	}
	defer rows.Close()
	held := make(map[int]struct{})
	for rows.Next() {
		var itemID int
		if err := rows.Scan(&itemID); err != nil {
			return nil, fmt.Errorf("scan 90CN auction stock character=%s: %w", characterID, err)
		}
		held[itemID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate 90CN auction stock character=%s: %w", characterID, err)
	}
	return held, nil
}

// insertCN90AuctionStockRows writes one stack: the slot row, its extras and the
// explicit clear of any previous stack at the same slot, because the item ids
// a character holds can change between restarts.
func insertCN90AuctionStockRows(ctx context.Context, tx *sql.Tx, characterID string, stack cn90AuctionStockStack) error {
	key := strconv.Itoa(cn90MainInventoryList) + ":" + strconv.Itoa(stack.Slot)
	raw := buildCN90ItemStackRawEntryCount(stack.Slot, stack.ItemID, stack.Count)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`DELETE FROM ` + dnfInventoryItemsTable + ` WHERE character_id=? AND collection_name=? AND entry_key=?`, []any{characterID, cn90InventorySlotCollection, key}},
		{`DELETE FROM ` + dnfInventoryExtraTable + ` WHERE character_id=? AND collection_name=? AND entry_key=?`, []any{characterID, cn90InventorySlotCollection, key}},
	} {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			return fmt.Errorf("clear 90CN auction stock slot=%s character=%s: %w", key, characterID, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+dnfInventoryItemsTable+` (character_id, collection_name, entry_key, item_id, item_count, bind_flag, expire_at, raw_entry) VALUES (?, ?, ?, ?, ?, 0, NULL, ?)`,
		characterID, cn90InventorySlotCollection, key, stack.ItemID, stack.Count, raw); err != nil {
		return fmt.Errorf("insert 90CN auction stock slot=%s character=%s: %w", key, characterID, err)
	}
	extras := map[string]string{
		"item_kind":            "stackable",
		"pvf_path":             stack.Item.Path,
		"source":               cn90AuctionStockMarker,
		"amount_or_count":      strconv.Itoa(stack.Count),
		"count":                strconv.Itoa(stack.Count),
		"value_a":              strconv.Itoa(stack.Count),
		"raw_entry_hex":        hex.EncodeToString(raw),
		cn90AuctionStockMarker: strconv.Itoa(1),
	}
	return insertCN90Extras(ctx, tx, dnfInventoryExtraTable,
		`INSERT INTO `+dnfInventoryExtraTable+` (character_id, collection_name, entry_key, extra_key, extra_value) VALUES (?, ?, ?, ?, ?)`,
		[]any{characterID, cn90InventorySlotCollection, key}, extras)
}

// applyCN90GoldFloor raises the character's gold to the configured floor and
// never lowers it: the balance also carries what the robot earned from its own
// auction sales, which arrives as a system mail and is claimed in game.
func applyCN90GoldFloor(ctx context.Context, tx *sql.Tx, characterID string, floor int) error {
	if floor <= 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE `+dnfCharactersTable+` SET gold=CASE WHEN gold<? THEN ? ELSE gold END WHERE character_id=?`,
		floor, floor, characterID); err != nil {
		return fmt.Errorf("apply 90CN gold floor character=%s: %w", characterID, err)
	}
	return nil
}
