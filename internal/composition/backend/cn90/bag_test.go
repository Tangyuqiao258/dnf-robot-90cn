package cn90

import (
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// newBagTestDatabase adds the wallet rows the auction stock stage has to work
// around to the shared native schema: the character already carries a
// revive-coin wallet at slot 1 and no gold.
func newBagTestDatabase(t *testing.T) string {
	t.Helper()
	path := newEquipmentTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`
INSERT INTO dnf_inventories(character_id,updated_at) VALUES ('9','2026-10-14 00:00:00');
INSERT INTO dnf_inventory_items(character_id,collection_name,entry_key,item_id,item_count,bind_flag,raw_entry) VALUES ('9','slots','0:1',1,0,0,NULL);`); err != nil {
		t.Fatal(err)
	}
	return path
}

func newBagTestApplier(t *testing.T, path string, config robotconfig.RuntimeConfig) *SQLiteLoadoutApplier {
	t.Helper()
	applier, err := NewSQLiteLoadoutApplier(context.Background(), path, config, nil, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { applier.Close() })
	return applier
}

// bagTestCatalog carries every pool id plus one pool id whose attach type
// binds the item, so the tradeability filter is actually exercised.
func bagTestCatalog(boundID int) []shared.EquipmentCatalogItem {
	items := make([]shared.EquipmentCatalogItem, 0, len(cn90AuctionStockPool))
	for _, id := range cn90AuctionStockPool {
		item := shared.EquipmentCatalogItem{
			ID: id, Name: fmt.Sprintf("item-%d", id), Path: fmt.Sprintf("stackable/material/item_%d.stk", id),
			Attach: "free", StackLimit: 100,
		}
		if id == boundID {
			item.Attach = "account"
		}
		items = append(items, item)
	}
	return items
}

func TestAuctionStockPlanIsDeterministicAndTradeable(t *testing.T) {
	boundID := cn90AuctionStockPool[0]
	catalog := bagTestCatalog(boundID)
	first := cn90AuctionStockPlan(catalog, "9", 6)
	second := cn90AuctionStockPlan(catalog, "9", 6)
	if len(first) != 6 {
		t.Fatalf("plan length = %d, want 6", len(first))
	}
	for index := range first {
		if first[index].ItemID != second[index].ItemID || first[index].Slot != second[index].Slot || first[index].Count != second[index].Count {
			t.Fatalf("plan is not stable: [%d] %+v != %+v", index, first[index], second[index])
		}
		stack := first[index]
		if stack.ItemID == boundID {
			t.Fatalf("plan listed account-bound item %d", boundID)
		}
		if want := cn90AuctionStockBaseSlot + index; stack.Slot != want {
			t.Fatalf("slot[%d] = %d, want %d", index, stack.Slot, want)
		}
		if stack.Count < cn90AuctionStockMinCount || stack.Count > cn90AuctionStockMaxCount {
			t.Fatalf("count[%d] = %d outside %d..%d", index, stack.Count, cn90AuctionStockMinCount, cn90AuctionStockMaxCount)
		}
		if stack.Item.StackLimit > 0 && stack.Count > stack.Item.StackLimit {
			t.Fatalf("count[%d] = %d exceeds the item stack limit %d", index, stack.Count, stack.Item.StackLimit)
		}
	}
	// The plan is seeded per character, so a fleet must not all open with the
	// same item.
	openers := make(map[int]struct{})
	for id := 1; id <= 12; id++ {
		plan := cn90AuctionStockPlan(catalog, fmt.Sprintf("robot%08d", id), 6)
		if len(plan) != 6 {
			t.Fatalf("character %d plan length = %d, want 6", id, len(plan))
		}
		openers[plan[0].ItemID] = struct{}{}
	}
	if len(openers) < 3 {
		t.Fatalf("12 characters opened with only %d distinct items", len(openers))
	}
}

func TestAuctionStockPlanSkipsUntradeablePoolEntries(t *testing.T) {
	catalog := bagTestCatalog(-1)
	for index := range catalog {
		if catalog[index].ID == cn90AuctionStockPool[1] {
			catalog[index].NoTrade = true
		}
	}
	plan := cn90AuctionStockPlan(catalog, "9", len(cn90AuctionStockPool))
	if len(plan) != len(cn90AuctionStockPool)-1 {
		t.Fatalf("plan length = %d, want %d with one blocked pool entry", len(plan), len(cn90AuctionStockPool)-1)
	}
	for _, stack := range plan {
		if stack.ItemID == cn90AuctionStockPool[1] {
			t.Fatalf("plan listed the no-trade item %d", stack.ItemID)
		}
	}
	// A missing catalog entry must drop out the same way.
	if plan := cn90AuctionStockPlan(nil, "9", 6); len(plan) != 0 {
		t.Fatalf("empty catalog produced %d stacks", len(plan))
	}
}

func TestAuctionStockCountHonorsTheStackLimit(t *testing.T) {
	item := shared.EquipmentCatalogItem{ID: 1072, Path: "stackable/water_wicked.stk", Attach: "free", StackLimit: 5}
	got := cn90AuctionStockCount(item, 12345, 0)
	if got != 5 {
		t.Fatalf("count = %d, want the 5-item stack limit", got)
	}
	if unbounded := cn90AuctionStockCount(shared.EquipmentCatalogItem{ID: 1072}, 12345, 0); unbounded < cn90AuctionStockMinCount || unbounded > cn90AuctionStockMaxCount {
		t.Fatalf("count = %d outside %d..%d", unbounded, cn90AuctionStockMinCount, cn90AuctionStockMaxCount)
	}
}

func TestRobotAuctionStockSeedsBagAndGold(t *testing.T) {
	path := newBagTestDatabase(t)
	config := robotconfig.Default()
	config.AuctionStockStacks = 4
	config.AuctionStockGold = 2000000
	applier := newBagTestApplier(t, path, config)
	applier.Stackables = bagTestCatalog(-1)
	if err := applier.reconcileRobotAuctionStock(context.Background(), "robot17000009"); err != nil {
		t.Fatal(err)
	}

	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	// The pool is limited to one connection, so the rows and the marker lookup
	// cannot be open at the same time.
	type stockRow struct {
		key    string
		itemID int
		count  int
		bind   int
		expire any
		raw    []byte
		marker string
	}
	var rows []stockRow
	func() {
		cursor, err := db.Query(`SELECT entry_key, item_id, item_count, bind_flag, expire_at, raw_entry FROM dnf_inventory_items WHERE character_id='9' AND collection_name='slots' AND item_count>0 ORDER BY entry_key`)
		if err != nil {
			t.Fatal(err)
		}
		defer cursor.Close()
		for cursor.Next() {
			var row stockRow
			if err := cursor.Scan(&row.key, &row.itemID, &row.count, &row.bind, &row.expire, &row.raw); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, row)
		}
		if err := cursor.Err(); err != nil {
			t.Fatal(err)
		}
	}()
	if len(rows) != 4 {
		t.Fatalf("seeded %d stock rows, want 4", len(rows))
	}
	for index, row := range rows {
		if row.bind != 0 || row.expire != nil {
			t.Fatalf("stock row %s bind=%d expire=%v", row.key, row.bind, row.expire)
		}
		if len(row.raw) != cn90AuctionItemStackRawSize {
			t.Fatalf("stock row %s raw_entry length = %d, want %d", row.key, len(row.raw), cn90AuctionItemStackRawSize)
		}
		if slot := int(binary.LittleEndian.Uint16(row.raw[0:2])); slot != cn90AuctionStockBaseSlot+index {
			t.Fatalf("stock row %s carries slot %d", row.key, slot)
		}
		if got := int(binary.LittleEndian.Uint32(row.raw[2:6])); got != row.itemID {
			t.Fatalf("stock row %s raw item id = %d, column = %d", row.key, got, row.itemID)
		}
		if got := int(binary.LittleEndian.Uint32(row.raw[6:10])); got != row.count {
			t.Fatalf("stock row %s raw count = %d, column = %d", row.key, got, row.count)
		}
		if row.count <= 0 {
			t.Fatalf("stock row %s has count %d", row.key, row.count)
		}
		if err := db.QueryRow(`SELECT extra_value FROM dnf_inventory_item_extra WHERE character_id='9' AND collection_name='slots' AND entry_key=? AND extra_key=?`, row.key, cn90AuctionStockMarker).Scan(&row.marker); err != nil {
			t.Fatalf("stock row %s is missing the %s marker: %v", row.key, cn90AuctionStockMarker, err)
		}
		if row.marker != "1" {
			t.Fatalf("stock row %s marker = %q", row.key, row.marker)
		}
	}
	var gold int
	if err := db.QueryRow(`SELECT gold FROM dnf_characters WHERE character_id='9'`).Scan(&gold); err != nil {
		t.Fatal(err)
	}
	if gold != 2000000 {
		t.Fatalf("gold = %d, want the 2000000 floor", gold)
	}
	// The second pass must be a no-op, not a second set of rows.
	if err := applier.reconcileRobotAuctionStock(context.Background(), "robot17000009"); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := db.QueryRow(`SELECT count(*) FROM dnf_inventory_items WHERE character_id='9' AND collection_name='slots' AND item_count>0`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 4 {
		t.Fatalf("second reconcile left %d stock rows, want 4", after)
	}
}

func TestRobotAuctionStockRefillsOnlyMissingStacks(t *testing.T) {
	path := newBagTestDatabase(t)
	config := robotconfig.Default()
	config.AuctionStockStacks = 3
	config.AuctionStockGold = 500000
	applier := newBagTestApplier(t, path, config)
	applier.Stackables = bagTestCatalog(-1)
	ctx := context.Background()
	if err := applier.reconcileRobotAuctionStock(ctx, "robot17000009"); err != nil {
		t.Fatal(err)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()

	// The first stack is sold (the server removes the row) and a surviving stack
	// loses a few pieces to a piece purchase.
	var soldID int
	if err := db.QueryRow(`SELECT item_id FROM dnf_inventory_items WHERE character_id='9' AND collection_name='slots' AND entry_key=?`, "0:113").Scan(&soldID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM dnf_inventory_items WHERE character_id='9' AND collection_name='slots' AND entry_key='0:113'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE dnf_inventory_items SET item_count=7 WHERE character_id='9' AND collection_name='slots' AND entry_key='0:114'`); err != nil {
		t.Fatal(err)
	}
	// A purchase drained the wallet below the floor.
	if _, err := db.Exec(`UPDATE dnf_characters SET gold=1000 WHERE character_id='9'`); err != nil {
		t.Fatal(err)
	}

	if err := applier.reconcileRobotAuctionStock(ctx, "robot17000009"); err != nil {
		t.Fatal(err)
	}
	var refilled int
	if err := db.QueryRow(`SELECT count(*) FROM dnf_inventory_items WHERE character_id='9' AND collection_name='slots' AND item_id=?`, soldID).Scan(&refilled); err != nil {
		t.Fatal(err)
	}
	if refilled != 1 {
		t.Fatalf("sold stack %d was refilled %d times, want 1", soldID, refilled)
	}
	var kept int
	if err := db.QueryRow(`SELECT item_count FROM dnf_inventory_items WHERE character_id='9' AND collection_name='slots' AND entry_key='0:114'`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 7 {
		t.Fatalf("surviving stack count = %d, want the untouched 7", kept)
	}
	var gold int
	if err := db.QueryRow(`SELECT gold FROM dnf_characters WHERE character_id='9'`).Scan(&gold); err != nil {
		t.Fatal(err)
	}
	if gold != 500000 {
		t.Fatalf("gold = %d, want the 500000 floor", gold)
	}
	// The refilled row must carry a clean extras set, not the sold row's.
	var marker string
	if err := db.QueryRow(`SELECT extra_value FROM dnf_inventory_item_extra WHERE character_id='9' AND collection_name='slots' AND entry_key='0:113' AND extra_key=?`, cn90AuctionStockMarker).Scan(&marker); err != nil {
		t.Fatal(err)
	}
	if marker != "1" {
		t.Fatalf("refilled row marker = %q", marker)
	}
}

func TestRobotAuctionStockKeepsEarnedGold(t *testing.T) {
	path := newBagTestDatabase(t)
	applier := newBagTestApplier(t, path, robotconfig.Default())
	_ = applier
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`UPDATE dnf_characters SET gold=9000000 WHERE character_id='9'`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := applyCN90GoldFloor(context.Background(), tx, "9", 100000); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var gold int
	if err := db.QueryRow(`SELECT gold FROM dnf_characters WHERE character_id='9'`).Scan(&gold); err != nil {
		t.Fatal(err)
	}
	if gold != 9000000 {
		t.Fatalf("gold = %d, want the earned 9000000 kept", gold)
	}
}

func TestRobotAuctionStockDisabledWritesNothing(t *testing.T) {
	path := newBagTestDatabase(t)
	config := robotconfig.Default()
	config.AuctionEnabled = false
	applier := newBagTestApplier(t, path, config)
	applier.Stackables = bagTestCatalog(-1)
	if err := applier.reconcileRobotAuctionStock(context.Background(), "robot17000009"); err != nil {
		t.Fatal(err)
	}
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	var stock int
	if err := db.QueryRow(`SELECT count(*) FROM dnf_inventory_items WHERE character_id='9' AND collection_name='slots' AND item_count>0`).Scan(&stock); err != nil {
		t.Fatal(err)
	}
	if stock != 0 {
		t.Fatalf("disabled auction stage seeded %d stacks", stock)
	}
	var gold int
	if err := db.QueryRow(`SELECT gold FROM dnf_characters WHERE character_id='9'`).Scan(&gold); err != nil {
		t.Fatal(err)
	}
	if gold != 0 {
		t.Fatalf("disabled auction stage raised gold to %d", gold)
	}
}
