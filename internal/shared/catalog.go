package shared

import "strings"

// BlockedItemNameFragment is the name fragment of PVF records that exist only
// as release placeholders: the client renders the name, but the record carries
// no gameplay data and looks unfinished in a live world. No robot may ever be
// granted one.
const BlockedItemNameFragment = "后续确认"

// ItemNameBlocked reports whether a catalog item name is a release placeholder.
func ItemNameBlocked(name string) bool {
	return name != "" && strings.Contains(name, BlockedItemNameFragment)
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

	// ClientIncompatible marks an item whose script uses fields that this DP2
	// client cannot parse safely. Every generated-item consumer must reject it.
	ClientIncompatible bool `json:"client_incompatible,omitempty"`

	// GrantBlocked marks an item that no robot may ever be granted, whatever
	// the consumer (worn gear, avatar, pet, bag stock, auction purchase).
	// The catalog projection derives it from the name once so consumers pay a
	// bool check instead of rescanning every name. See ItemNameBlocked.
	GrantBlocked bool `json:"grant_blocked,omitempty"`
}

// ClientCompatibleEquipment reports whether generated equipment may be sent
// to this DP2 client. Keep compatibility policy separate from consumer-specific
// eligibility rules so every equipment path honors the same PVF marker.
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
