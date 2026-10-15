package shared

import "strings"

// BlockedItemNameFragments are the name fragments of PVF records no robot may
// ever be granted:
//   - 后续确认: release placeholders. The client renders the name, but the
//     record carries no gameplay data and looks unfinished in a live world.
//   - 活动: event-only gear and 装扮 that the live world does not run.
//   - 租: rental items, which the system reclaims.
//   - 克隆: clone 装扮, which the operator barred the fleet from wearing. A scan
//     of the live archive finds 1300 records carrying it -- the 克隆装扮 and
//     稀有克隆装扮 pieces of the eight avatar slots, the 克隆装扮礼盒 and the
//     兑换券 that open into them, and one joke title. They are all clone avatar
//     stock, so the fragment is stated by name rather than by item type, which
//     also keeps it independent of parse order: [name] can be read before
//     [equipment type].
//   - GM: the game-master copies of one 稀有装扮 set (暗黑骑士戾芒头盔 GM and its
//     seven siblings, 32 records in all). Every one of them is a staff-only
//     variant, and the fragment is unambiguous: no other record in the live
//     archive carries the letters, in any case.
//   - (旧): the superseded copies the archive keeps beside the current ones
//     ((旧) 棉布长袍 and the records like it). Only the parenthesised marker is
//     blocked, not the character: 破旧的棉布衬衫 is ordinary starter gear.
//   - [ and ]: names carrying a bracket, which is how the archive marks a record
//     belonging to a closed subsystem -- the [网吧] internet-cafe weapons, the
//     异界 skill-name armour -- and how it truncates a name it could not finish
//     reading. 20777 records carry an opening bracket and the operator asked for
//     the whole shape to stay out of the fleet.
var BlockedItemNameFragments = []string{"后续确认", "活动", "租", "克隆", "GM", "(旧)", "[", "]"}

// ItemNameBlocked reports whether a catalog item name carries a blocked
// fragment. A nameless record is not this function's business: the catalog
// projection blocks those itself, where it can tell a missing [name] apart from
// a name that was never read.
func ItemNameBlocked(name string) bool {
	if name == "" {
		return false
	}
	for _, fragment := range BlockedItemNameFragments {
		if strings.Contains(name, fragment) {
			return true
		}
	}
	return false
}

type EquipmentCatalogItem struct {
	ID            int    `json:"id"`
	Name          string `json:"name,omitempty"`
	Name2         string `json:"name2,omitempty"`
	Path          string `json:"path,omitempty"`
	Level         int    `json:"level"`
	ItemType      int    `json:"item_type"`
	Slot          string `json:"slot,omitempty"`
	SetKey        string `json:"set_key,omitempty"`
	PartSetIndex  int    `json:"part_set_index,omitempty"`
	Rarity        int    `json:"rarity,omitempty"`
	Price         int    `json:"price,omitempty"`
	Value         int    `json:"value,omitempty"`
	Durability    int    `json:"durability,omitempty"`
	Attach        string `json:"attach,omitempty"`
	Trade         bool   `json:"trade,omitempty"`
	NoTrade       bool   `json:"no_trade,omitempty"`
	TradeBlock    bool   `json:"trade_block,omitempty"`
	CanTrade      *bool  `json:"available_trade,omitempty"`
	CanAuction    *bool  `json:"available_auction,omitempty"`
	CanShop       *bool  `json:"available_shop,omitempty"`
	CanDrop       *bool  `json:"available_drop,omitempty"`
	Auction       bool   `json:"auction,omitempty"`
	Shop          bool   `json:"shop,omitempty"`
	BadName       bool   `json:"bad_name,omitempty"`
	NeedMaterial  bool   `json:"need_material,omitempty"`
	BasicMaterial bool   `json:"basic_material,omitempty"`
	Icon          string `json:"icon,omitempty"`
	FieldImage    string `json:"field_image,omitempty"`
	SubType       int    `json:"sub_type,omitempty"`
	Expire        bool   `json:"expire,omitempty"`
	StackLimit    int    `json:"stack_limit,omitempty"`
	UseJob        []int  `json:"use_job,omitempty"`

	RecipeTargetID int `json:"recipe_target_id,omitempty"`

	// ClientIncompatible marks an item whose script places an extended [add ...]
	// property before [equipment type], the form the iteminfo/market path was
	// once believed to reject. It is NOT a render gate: the client parses past
	// those properties and draws the item normally. Character 1 in the live
	// database wears [add attack speed]/[add cast speed]/[add move speed] ahead
	// of [equipment type] on its amulet, wrist and ring -- with [icon] and
	// [field image] behind them and the type line after -- and carries the
	// remaining tags in its bag; a census of the archive finds the form on
	// 49820 of 171645 .equ files, so gating worn gear on it emptied every
	// level-90 set out of the fleet's pool. Only the market consumers
	// (iteminfo, store assortment) still honour it. See ClientCompatibleEquipment.
	ClientIncompatible bool `json:"client_incompatible,omitempty"`

	// GrantBlocked marks an item that no robot may ever be granted, whatever
	// the consumer (worn gear, avatar, pet, bag stock, auction purchase).
	// The catalog projection derives it from the name once so consumers pay a
	// bool check instead of rescanning every name. See ItemNameBlocked.
	GrantBlocked bool `json:"grant_blocked,omitempty"`
}

// ClientCompatibleEquipment reports whether an item may enter the market
// surfaces that build an iteminfo table for the client. It is deliberately not
// consulted when the fleet dresses itself: the marker's stated cause does not
// hold (see ClientIncompatible), and honouring it there left the fleet unable
// to wear any complete level-90 set. Market consumers keep the guard until the
// auction symptom that motivated it has its own reproduction.
func ClientCompatibleEquipment(item EquipmentCatalogItem) bool {
	return !item.ClientIncompatible
}

type MapCatalogItem struct {
	Village        int            `json:"village"`
	VillageName    string         `json:"village_name,omitempty"`
	Area           int            `json:"area"`
	Level          int            `json:"level"`
	MinLevel       int            `json:"min_level,omitempty"`
	NeedQuests     []int          `json:"need_quests,omitempty"`
	XMin           int            `json:"x_min"`
	XMax           int            `json:"x_max"`
	YMin           int            `json:"y_min"`
	YMax           int            `json:"y_max"`
	Rectangles     []MapRectangle `json:"rectangles,omitempty"`
	Use            bool           `json:"use"`
	Gate           bool           `json:"gate,omitempty"`
	NormalEligible *bool          `json:"normal_eligible,omitempty"`
	StoreEligible  *bool          `json:"store_eligible,omitempty"`
	StoreProbe     *bool          `json:"store_probe_eligible,omitempty"`
	// Links is the map's portal table as destination areas: the town graph a
	// crowd policy walks to measure how many transitions a map sits away from
	// the player.
	Links []MapAreaKey `json:"links,omitempty"`
	// SeriaExit marks a map a character lands in when leaving the Seria room
	// (the door target plus every return portal). Crowd rules pin the
	// enchanting stalls to these maps.
	SeriaExit bool `json:"seria_exit,omitempty"`
	// DungeonGate marks a map that hosts dungeon entrances. Crowd rules pin the
	// disassembler stalls to these maps.
	DungeonGate bool `json:"dungeon_gate,omitempty"`
	// DungeonLevel is the level a character must reach before the dungeons this
	// map's entrances open, or zero when the map hosts no dungeon whose entry
	// level is known. A robot standing at a dungeon entrance is leveled to the
	// dungeon it guards.
	DungeonLevel int `json:"dungeon_level,omitempty"`
	// GateX, GateY is where a character arrives on a generic area transition;
	// zero means the area declares no arrival point.
	GateX int `json:"gate_x,omitempty"`
	GateY int `json:"gate_y,omitempty"`
}

type MapRectangle struct {
	XMin int `json:"x_min"`
	XMax int `json:"x_max"`
	YMin int `json:"y_min"`
	YMax int `json:"y_max"`
}

type MapAreaKey struct {
	Village int
	Area    int
}

type MapLocation struct {
	Village int
	Area    int
	X       int
	Y       int
}
