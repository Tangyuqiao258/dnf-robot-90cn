package equipment

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

func TestBestEquipmentLevelsMirrorsSelectionFilters(t *testing.T) {
	rc := robotconfig.Default()
	rc.EquipSlots = []int{1, 2}
	// Level 60 sits in the 36..70 band, whose gear is 神器 (rarity 3).
	items := []shared.EquipmentCatalogItem{
		{ID: 1, ItemType: 1, Level: 30, Rarity: 3, UseJob: []int{1}},
		{ID: 2, ItemType: 1, Level: 70, Rarity: 3, UseJob: []int{1}},
		{ID: 3, ItemType: 1, Level: 60, Rarity: 3, UseJob: []int{2}},
		{ID: 4, ItemType: 1, Level: 55, Rarity: 3, UseJob: []int{100}, ClientIncompatible: true},
		{ID: 5, ItemType: 1, Level: 58, Rarity: 3, UseJob: []int{1}, Expire: true},
		{ID: 6, ItemType: 2, Level: 40, Rarity: 3, UseJob: []int{1}},
	}
	// Item 4 is deliberately ClientIncompatible and still counted: the flag marks
	// the item-info script shape, not a selection bar, so level 55 is what this
	// slot's band tops out at. Item 2 (past the gear level), item 3 (another job)
	// and item 5 (expired) stay out, which is what mirroring the filters means.
	best := BestEquipmentLevels(items, 60, 1, 0, rc)
	if best[1] != 55 || best[2] != 40 {
		t.Fatalf("best levels=%v", best)
	}
	// Level 30 sits in the ≤35 band, whose gear is 紫 (rarity 2), so none of the
	// 神器 the catalog holds is gear this character is owed.
	best = BestEquipmentLevels(items, 30, 1, 0, rc)
	if best[1] != 0 || best[2] != 0 {
		t.Fatalf("out-of-band best levels=%v", best)
	}
}

// TestArmorSlotsFallBackToTheArchivesOwnRarity pins the fallback the fleet's
// 重甲 and 板甲 branches depend on. The band's rarity wins wherever the archive can
// pay for it, an armor slot the band cannot fill takes the best rarity the archive
// does hold, and a slot that is not armor stays empty rather than borrowing the
// rule. 勇者 is the one rarity the fallback must never reach: it is the 异界 set.
func TestArmorSlotsFallBackToTheArchivesOwnRarity(t *testing.T) {
	rc := robotconfig.Default()
	rc.EquipSlots = []int{1, 3, 4, 8}
	rc.PreferEquipSets = false
	// Level 60 sits in the 36..70 band, whose gear is 神器 (rarity 3).
	items := []shared.EquipmentCatalogItem{
		{ID: 1, ItemType: 8, Level: 60, Rarity: 4, UseJob: []int{100}, Path: "equipment/character/common/amulet/1.equ"},
		{ID: 2, ItemType: 3, Level: 60, Rarity: 4, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/2.equ"},
		{ID: 3, ItemType: 3, Level: 60, Rarity: 6, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/3.equ"},
		{ID: 4, ItemType: 4, Level: 60, Rarity: 5, UseJob: []int{100}, Path: "equipment/character/common/shoulder/cloth/4.equ"},
	}
	worn := SelectEquipment(items, 60, 1, 0, rc, func(int) int { return 0 })
	if got := worn[3].ID; got != 2 {
		t.Fatalf("coat = %d, want the 史诗 record 2: the band's 神器 is absent and 史诗 outranks 传说", got)
	}
	if item, ok := worn[8]; ok {
		t.Fatalf("amulet = %d, want none: an accessory sits out an empty band rather than falling back", item.ID)
	}
	if item, ok := worn[4]; ok {
		t.Fatalf("shoulder = %d, want none: 勇者 is never worn, whatever is missing", item.ID)
	}
	// The band's own rarity wins wherever the archive holds it.
	items = append(items, shared.EquipmentCatalogItem{ID: 5, ItemType: 3, Level: 60, Rarity: 3, UseJob: []int{100}, Path: "equipment/character/common/jacket/cloth/5.equ"})
	worn = SelectEquipment(items, 60, 1, 0, rc, func(int) int { return 0 })
	if got := worn[3].ID; got != 5 {
		t.Fatalf("coat = %d, want the 神器 record 5", got)
	}
}

func TestBuildCreatureSlotsUsesActiveCreatureRecord(t *testing.T) {
	const itemID = 63050
	raw := buildCreatureSlots(itemID, nil)
	if len(raw) != 102*61 {
		t.Fatalf("creature raw length=%d, want %d", len(raw), 102*61)
	}
	offset := 98 * 61
	if raw[offset+1] != 5 || binary.LittleEndian.Uint32(raw[offset+2:offset+6]) != itemID || raw[offset+7] != 2 {
		t.Fatalf("active creature record=%x", raw[offset:offset+61])
	}
	for i, value := range raw {
		if i >= offset && i < offset+8 {
			continue
		}
		if value != 0 {
			t.Fatalf("unexpected nonzero creature byte at %d: %d", i, value)
		}
	}
}

func TestCompressedCreaturePreservesGameContainerHeader(t *testing.T) {
	compressed := CompressedCreatureLoadout(63050, nil)
	if len(compressed) < 4 || binary.LittleEndian.Uint32(compressed[:4]) != 102*61 {
		header := compressed
		if len(header) > 4 {
			header = header[:4]
		}
		t.Fatalf("creature header=%x", header)
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed[4:]))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	if got := binary.LittleEndian.Uint32(raw[98*61+2 : 98*61+6]); got != 63050 {
		t.Fatalf("compressed creature item id=%d", got)
	}
}

func TestBuildCreatureSlotsMapsArtifactsToFinalRecords(t *testing.T) {
	artifacts := map[int]shared.EquipmentCatalogItem{
		31: {ID: 63500, ItemType: 31, Durability: 10},
		33: {ID: 64500, ItemType: 33, Durability: 20},
	}
	raw := buildCreatureSlots(63050, artifacts)
	for slot, want := range map[int]uint32{98: 63050, 99: 63500, 101: 64500} {
		offset := slot * 61
		if got := binary.LittleEndian.Uint32(raw[offset+2 : offset+6]); got != want {
			t.Fatalf("creature record %d item id=%d, want %d", slot, got, want)
		}
	}
	if got := binary.LittleEndian.Uint32(raw[100*61+2 : 100*61+6]); got != 0 {
		t.Fatalf("unselected blue artifact item id=%d, want 0", got)
	}
}

func TestWriteEquipSlotUsesHighIntensify(t *testing.T) {
	opt := SlotOptions{IntensifyMin: 0, IntensifyMax: 10}
	for i := 0; i < 100; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		raw := make([]byte, 61)
		WriteEquipSlot(raw, shared.EquipmentCatalogItem{ID: 1000 + i, ItemType: 3}, rng, opt)
		if raw[6] < 7 || raw[6] > 10 {
			t.Fatalf("armor intensify got %d want 7..10", raw[6])
		}
	}
	for i := 0; i < 100; i++ {
		rng := rand.New(rand.NewSource(int64(i)))
		raw := make([]byte, 61)
		WriteEquipSlot(raw, shared.EquipmentCatalogItem{ID: 2000 + i, ItemType: 1}, rng, opt)
		if raw[6] < 8 || raw[6] > 15 {
			t.Fatalf("weapon intensify got %d want 8..15", raw[6])
		}
	}
}

func TestWriteEquipSlotUsesPVFDurability(t *testing.T) {
	raw := make([]byte, 61)
	WriteEquipSlot(raw, shared.EquipmentCatalogItem{ID: 1000, ItemType: 1, Durability: 18}, rand.New(rand.NewSource(1)), SlotOptions{})
	if got := int(binary.LittleEndian.Uint16(raw[11:13])); got != 18 {
		t.Fatalf("durability=%d, want 18", got)
	}
}

func TestWritePetArtifactSlotUsesUnenhancedInventoryRecord(t *testing.T) {
	raw := make([]byte, 61)
	raw[6] = 99
	WritePetArtifactSlot(raw, shared.EquipmentCatalogItem{ID: 63524, Durability: 12})
	if raw[0] != 0 || raw[1] != 1 || binary.LittleEndian.Uint32(raw[2:6]) != 63524 {
		t.Fatalf("artifact header=%x", raw[:6])
	}
	if raw[6] != 0 || binary.LittleEndian.Uint16(raw[11:13]) != 12 {
		t.Fatalf("artifact enhancement/durability=%d/%d", raw[6], binary.LittleEndian.Uint16(raw[11:13]))
	}
}

func TestEquipmentSlotsNeedRepairValidatesConfiguredSlots(t *testing.T) {
	items := map[int]shared.EquipmentCatalogItem{
		100: {ID: 100, ItemType: 1, Level: 50, Rarity: 3, Durability: 20, UseJob: []int{1}},
	}
	rc := robotconfig.RuntimeConfig{EquipSlots: []int{1}, EquipRarityMin: 0, EquipRarityMax: 5}
	raw := make([]byte, 12*61)
	binary.LittleEndian.PutUint32(raw[2:6], 100)
	binary.LittleEndian.PutUint16(raw[11:13], 20)
	if EquipmentSlotsNeedRepair(raw, items, 50, 1, 0, rc) {
		t.Fatal("valid weapon was marked for repair")
	}
	binary.LittleEndian.PutUint16(raw[11:13], 21)
	if !EquipmentSlotsNeedRepair(raw, items, 50, 1, 0, rc) {
		t.Fatal("durability different from PVF value was accepted")
	}
	binary.LittleEndian.PutUint16(raw[11:13], 20)
	if !EquipmentSlotsNeedRepair(raw, items, 50, 2, 0, rc) {
		t.Fatal("wrong-job weapon was accepted")
	}
	binary.LittleEndian.PutUint32(raw[2:6], 0)
	if !EquipmentSlotsNeedRepair(raw, items, 50, 1, 0, rc) {
		t.Fatal("missing weapon was accepted")
	}
}

func TestAvatarSetSelectionPrefersFullSetCoverage(t *testing.T) {
	candidates := testSetCandidates(9, 6)
	selected := SelectAvatarSetItems(candidates, 2, func(n int) int { return n - 1 })
	if len(selected) != 9 {
		t.Fatalf("selected slots got %d want full nine-slot set", len(selected))
	}
	for _, item := range selected {
		if item.SetKey != "quality" {
			t.Fatalf("selected set %q want quality", item.SetKey)
		}
	}
}

func TestAvatarSetSelectionFallsBackBelowSixSlots(t *testing.T) {
	candidates := testSetCandidates(5, 4)
	selected := SelectAvatarSetItems(candidates, 2, func(n int) int { return n - 1 })
	if len(selected) != 5 {
		t.Fatalf("selected slots got %d want best five-slot set", len(selected))
	}
	for _, item := range selected {
		if item.SetKey != "quality" {
			t.Fatalf("selected set %q want quality", item.SetKey)
		}
	}
}

func TestEquipmentSetSelectionKeepsHighestScore(t *testing.T) {
	candidates := testSetCandidates(9, 6)
	selected := SelectSetItems(candidates, 2, func(n int) int { return n - 1 })
	if len(selected) != 9 {
		t.Fatalf("selected slots got %d want highest-score nine-slot set", len(selected))
	}
	for _, item := range selected {
		if item.SetKey != "quality" {
			t.Fatalf("selected set %q want quality", item.SetKey)
		}
	}
}

func TestSelectEquipmentScansCatalogAcrossConfiguredSlots(t *testing.T) {
	// Level 100 is past the last band's gate, so this character is owed 史诗
	// (rarity 4) gear.
	items := []shared.EquipmentCatalogItem{
		{ID: 100, ItemType: 1, Level: 100, Rarity: 4, UseJob: []int{1}, ClientIncompatible: true},
		{ID: 101, ItemType: 1, Level: 50, Rarity: 4, UseJob: []int{1}},
		{ID: 102, ItemType: 1, Level: 90, Rarity: 4, UseJob: []int{1}},
		{ID: 103, ItemType: 1, Level: 100, Rarity: 4, UseJob: []int{1}},
		{ID: 104, ItemType: 1, Level: 100, Rarity: 4, UseJob: []int{2}},
		{ID: 201, ItemType: 2, Level: 80, Rarity: 4, UseJob: []int{100}},
		{ID: 301, ItemType: 3, Level: 80, Rarity: 4, UseJob: []int{1}},
	}

	// Item 100 is deliberately ClientIncompatible and still eligible, so the
	// slot's top band holds both level-100 weapons and the deterministic first
	// draw is the flagged record rather than the clean item 103.
	selected := SelectEquipment(items, 100, 1, 0, robotconfig.RuntimeConfig{EquipSlots: []int{1, 2}}, func(int) int { return 0 })

	if len(selected) != 2 || selected[1].ID != 100 || selected[2].ID != 201 {
		t.Fatalf("selected equipment = %+v", selected)
	}
}

func TestSelectAvatarScansCatalogAcrossConfiguredSlots(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{ID: 99, ItemType: 20, Name: "flagged hat", UseJob: []int{1}, ClientIncompatible: true},
		{ID: 100, ItemType: 20, Name: "hat", UseJob: []int{1}},
		{ID: 101, ItemType: 20, Name: "other hat", UseJob: []int{2}},
		{ID: 200, ItemType: 21, Name: "hair", UseJob: []int{2}},
		{ID: 900, ItemType: 29, Name: "aura"},
	}

	// Below RareAvatarMinLevel, so the whole fixture is in the one 时装 grade the
	// fixtures declare — rarity 0 — and the test is about slot coverage. Item 99
	// is deliberately ClientIncompatible and still selectable, so the
	// deterministic draw covers slot 0 with it; slot 1 stays empty because its
	// only candidate belongs to another job; slot 9 draws the aura.
	selected := SelectAvatar(items, 50, 1, robotconfig.RuntimeConfig{AvatarSlots: []int{0, 1, 9}}, func(int) int { return 0 })

	if len(selected) != 2 || selected[0].ID != 99 || selected[9].ID != 900 {
		t.Fatalf("selected avatar = %+v", selected)
	}
}

// TestSelectorsKeepClientIncompatibleItemsEligible states directly what the
// retired ClientIncompatible gate broke: the flag records an item-info script
// shape, not a render failure, so a slot whose pool holds one flagged and one
// clean item keeps both, and the draw may land on either.
func TestSelectorsKeepClientIncompatibleItemsEligible(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{ID: 300, ItemType: 1, Level: 50, Rarity: 3, UseJob: []int{1}, ClientIncompatible: true},
		{ID: 301, ItemType: 1, Level: 50, Rarity: 3, UseJob: []int{1}},
	}
	// Level 50 sits in the 36..70 band, whose gear is 神器 (rarity 3).
	rc := robotconfig.RuntimeConfig{EquipSlots: []int{1}}

	if got := SelectEquipment(items, 50, 1, 0, rc, func(int) int { return 0 }); len(got) != 1 || got[1].ID != 300 {
		t.Fatalf("flagged weapon = %+v, want it selectable like any other", got)
	}
	if got := SelectEquipment(items, 50, 1, 0, rc, func(n int) int { return n - 1 }); len(got) != 1 || got[1].ID != 301 {
		t.Fatalf("clean weapon = %+v, want the second draw", got)
	}
	if best := BestEquipmentLevels(items, 50, 1, 0, rc); best[1] != 50 {
		t.Fatalf("best levels=%v, want the flagged record counted", best)
	}
}

// TestSelectAvatarSettlesForJobsOutsideTheCatalog covers the avatar path for a
// job id the catalog was not prepared for. Select used to rebuild the catalog
// and retry, and because the rebuilt catalog still had no bucket for the job,
// the retry never settled: any job id above maxAvatarJob overflowed the
// goroutine stack and killed the process on the first robot of that job,
// instead of leaving it without avatars. A maxAvatarJob the PVF releases has to
// select normally, so this cannot pass by refusing every job.
func TestSelectAvatarSettlesForJobsOutsideTheCatalog(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{ID: 100, ItemType: 20, Name: "hat", Path: "equipment/character/knight/avatar/cap/100.equ", UseJob: []int{12}},
		{ID: 200, ItemType: 20, Name: "hat", Path: "equipment/character/gunner/avatar/cap/200.equ", UseJob: []int{2}},
	}
	rc := robotconfig.RuntimeConfig{AvatarSlots: []int{0}, MinAvatarSlots: 0}
	if selected := SelectAvatar(items, 50, 12, rc, func(int) int { return 0 }); len(selected) != 1 || selected[0].ID != 100 {
		t.Fatalf("job 12 selected %+v, want its own avatar", selected)
	}
	if selected := SelectAvatar(items, 50, 2, rc, func(int) int { return 0 }); len(selected) != 1 || selected[0].ID != 200 {
		t.Fatalf("job 2 selected %+v, want its own avatar", selected)
	}
	// Above the PVF's job count: no avatars, and no unbounded retry either.
	if selected := SelectAvatar(items, 50, maxAvatarJob+1, rc, func(int) int { return 0 }); len(selected) != 0 {
		t.Fatalf("job %d selected %+v, want none", maxAvatarJob+1, selected)
	}
}

func TestPrepareAvatarCatalogCoversEveryPVFJob(t *testing.T) {
	items := []shared.EquipmentCatalogItem{{ID: 100, ItemType: 20, Name: "hat", UseJob: []int{12}}}
	rc := robotconfig.RuntimeConfig{AvatarSlots: []int{0}}
	catalog := PrepareAvatarCatalog(items, rc)
	for job := 0; job <= maxAvatarJob; job++ {
		if _, ok := catalog.jobs[job]; !ok {
			t.Fatalf("job %d has no avatar bucket; maxAvatarJob must follow the PVF's job count", job)
		}
	}
}

func TestSelectPetChoosesPartialArtifactSet(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{ID: 300, ItemType: 30},
		{ID: 301, ItemType: 30},
		{ID: 310, ItemType: 31},
		{ID: 320, ItemType: 32},
		{ID: 330, ItemType: 33},
	}
	rc := robotconfig.RuntimeConfig{
		PetEnabled: true, PetArtifactEnabled: true,
		PetArtifactSlots: []int{31, 32, 33}, MinPetArtifactSlots: 1, MaxPetArtifactSlots: 2,
	}
	pet, artifacts, ok := SelectPet(items, rc, func(n int) int { return 0 })
	if !ok || pet.ItemType != 30 {
		t.Fatalf("pet = %#v, artifacts = %#v, ok=%t", pet, artifacts, ok)
	}
	if len(artifacts) != 1 {
		t.Fatalf("artifacts = %#v, want one selected artifact", artifacts)
	}
	if _, ok := artifacts[31]; !ok {
		t.Fatalf("artifacts = %#v, deterministic first artifact missing", artifacts)
	}
	_, artifacts, ok = SelectPet(items, rc, func(n int) int { return n - 1 })
	if !ok || len(artifacts) != 2 {
		t.Fatalf("artifacts = %#v, want two selected artifacts", artifacts)
	}
}

// TestSelectPetSkipsUnhatchedEggs covers the operator's crash report: the 90CN
// client dies when an unhatched 宠物蛋 reaches the creature slot, and the catalog
// files those eggs under the very same item type as the real creatures.
func TestSelectPetSkipsUnhatchedEggs(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{ID: 63006, ItemType: 30, Name: "宠物蛋 (佛拉斯)"},
		{ID: 63013, ItemType: 30, Name: "宠物蛋 (波堤斯)"},
		{ID: 63000, ItemType: 30, Name: "佛拉斯"},
	}
	rc := robotconfig.RuntimeConfig{PetEnabled: true}
	// Draw both the first and the last candidate, so an egg left in the pool is
	// picked whichever way the draw lands.
	for _, randIntn := range []func(int) int{func(int) int { return 0 }, func(n int) int { return n - 1 }} {
		pet, _, ok := SelectPet(items, rc, randIntn)
		if !ok {
			t.Fatal("no creature selected")
		}
		if pet.ID != 63000 {
			t.Fatalf("selected %#v, want the hatched creature", pet)
		}
	}
	if _, _, ok := SelectPet(items[:2], rc, func(int) int { return 0 }); ok {
		t.Fatal("a pool of nothing but eggs still produced a creature")
	}
}

func TestPetRenderableRejectsEggNames(t *testing.T) {
	if PetRenderable(shared.EquipmentCatalogItem{ID: 63006, ItemType: 30, Name: "宠物蛋 (佛拉斯)"}) {
		t.Fatal("an unhatched egg is renderable as a creature")
	}
	if PetRenderable(shared.EquipmentCatalogItem{ID: 63006, ItemType: 30, Name2: "宠物蛋 (佛拉斯)"}) {
		t.Fatal("an egg named only in name2 is renderable as a creature")
	}
	if !PetRenderable(shared.EquipmentCatalogItem{ID: 63000, ItemType: 30, Name: "佛拉斯"}) {
		t.Fatal("a hatched creature was rejected")
	}
	if PetRenderable(shared.EquipmentCatalogItem{ID: 310, ItemType: 31, Name: "artifact"}) {
		t.Fatal("an artifact is renderable as a creature")
	}
}

func TestSelectPetCanDisableArtifactsAndPets(t *testing.T) {
	items := []shared.EquipmentCatalogItem{{ID: 300, ItemType: 30}, {ID: 310, ItemType: 31}}
	if _, artifacts, ok := SelectPet(items, robotconfig.RuntimeConfig{PetEnabled: true, PetArtifactEnabled: false}, func(n int) int { return 0 }); !ok || len(artifacts) != 0 {
		t.Fatalf("disabled artifacts result ok=%t artifacts=%#v", ok, artifacts)
	}
	if _, _, ok := SelectPet(items, robotconfig.RuntimeConfig{}, func(n int) int { return 0 }); ok {
		t.Fatal("disabled pet unexpectedly selected")
	}
}

func TestPetArtifactRenderableRejectsQuestMaterialFallback(t *testing.T) {
	valid := shared.EquipmentCatalogItem{ID: 63524, ItemType: 31, Name: "artifact", Path: "equipment/creature/artifact_red/hand.equ", Icon: "Item/creature/artifact_red.img"}
	invalid := valid
	invalid.ID = 430000001
	invalid.Name2 = "ErrorString"
	invalid.NeedMaterial = true
	invalid.Icon = "Item/stackable/quest.img"
	if !PetArtifactRenderable(valid) || PetArtifactRenderable(invalid) {
		t.Fatalf("artifact renderability valid=%t invalid=%t", PetArtifactRenderable(valid), PetArtifactRenderable(invalid))
	}
}

func TestSelectAvatarFiltersPVFErrorStringItemsOnly(t *testing.T) {
	items := []shared.EquipmentCatalogItem{
		{ID: 100, ItemType: 20, Name: "ErrorString", Path: "avatar/cap/broken.equ", UseJob: []int{7}},
		{ID: 101, ItemType: 20, Name: "safe hat", Path: "avatar/cap/safe.equ", UseJob: []int{7}},
		{ID: 200, ItemType: 23, Name: "party tank top", Path: "avatar/coat/tank.equ", UseJob: []int{7}},
		{ID: 300, ItemType: 24, Name: "beach pants", Path: "avatar/pants/beach.equ", UseJob: []int{7}},
	}

	selected := SelectAvatar(items, 50, 7, robotconfig.RuntimeConfig{AvatarSlots: []int{0, 3, 4}}, func(int) int { return 0 })

	if len(selected) != 3 || selected[0].ID != 101 || selected[3].ID != 200 || selected[4].ID != 300 {
		t.Fatalf("selected safe avatar = %+v", selected)
	}
}

func TestAvatarRenderableRejectsMissingAndPlaceholderNames(t *testing.T) {
	for _, item := range []shared.EquipmentCatalogItem{
		{ID: 100, ItemType: 26},
		{ID: 101, ItemType: 26, Name: "name_101"},
	} {
		if AvatarRenderable(item) {
			t.Fatalf("invalid avatar remained available: %+v", item)
		}
	}
}

func TestFilterAvatarSupportedJobsIntersectsConfiguredJobsWithPVFSlots(t *testing.T) {
	items := make([]shared.EquipmentCatalogItem, 0)
	for slot := 0; slot < 8; slot++ {
		items = append(items, shared.EquipmentCatalogItem{ID: 1000 + slot, ItemType: 20 + slot, Name: "job one avatar", UseJob: []int{1}})
	}
	for slot := 0; slot < 7; slot++ {
		items = append(items, shared.EquipmentCatalogItem{ID: 2000 + slot, ItemType: 20 + slot, Name: "job eight avatar", UseJob: []int{8}})
	}

	got := FilterAvatarSupportedJobs([]int{1, 8, 10}, items, robotconfig.RuntimeConfig{MinAvatarSlots: 8})
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("supported jobs = %v, want [1]", got)
	}
}

func TestFilterAvatarSupportedJobsKeepsConfiguredJobsWithoutAvatarCatalog(t *testing.T) {
	got := FilterAvatarSupportedJobs([]int{1, 8}, []shared.EquipmentCatalogItem{{ID: 100, ItemType: 1}}, robotconfig.RuntimeConfig{MinAvatarSlots: 8})
	if len(got) != 2 || got[0] != 1 || got[1] != 8 {
		t.Fatalf("supported jobs = %v, want configured fallback", got)
	}
}

// TestFilterAvatarSupportedJobsCountsClientIncompatibleSlots pins the retired
// ClientIncompatible gate from the other side: the flag no longer withholds an
// avatar from the slot coverage a job is credited with, so job 1's eighth slot
// — a deliberately ClientIncompatible record — still counts and reaches the
// configured 8-slot minimum.
func TestFilterAvatarSupportedJobsCountsClientIncompatibleSlots(t *testing.T) {
	items := make([]shared.EquipmentCatalogItem, 0, 16)
	for slot := 0; slot < 8; slot++ {
		items = append(items, shared.EquipmentCatalogItem{ID: 1000 + slot, ItemType: 20 + slot, Name: "safe job avatar", UseJob: []int{8}})
	}
	for slot := 0; slot < 7; slot++ {
		items = append(items, shared.EquipmentCatalogItem{ID: 2000 + slot, ItemType: 20 + slot, Name: "partial job avatar", UseJob: []int{1}})
	}
	items = append(items, shared.EquipmentCatalogItem{ID: 2007, ItemType: 27, Name: "flagged job avatar", UseJob: []int{1}, ClientIncompatible: true})

	got := FilterAvatarSupportedJobs([]int{1, 8}, items, robotconfig.RuntimeConfig{MinAvatarSlots: 8})
	if len(got) != 2 || got[0] != 1 || got[1] != 8 {
		t.Fatalf("supported jobs = %v, want [1 8]", got)
	}
}

func TestFilterEquipmentSupportedJobsUsesGeneratedLevel(t *testing.T) {
	// Both levels sit in the 36..70 band, so both items carry 神器 (rarity 3).
	items := []shared.EquipmentCatalogItem{
		{ID: 100, ItemType: 1, Level: 50, Rarity: 3, UseJob: []int{1}},
		{ID: 200, ItemType: 1, Level: 60, Rarity: 3, UseJob: []int{2}},
	}
	rc := robotconfig.RuntimeConfig{EquipSlots: []int{1}, EquipRarityMax: 5}
	if got := FilterEquipmentSupportedJobs([]int{1, 2, 3}, items, 50, rc); len(got) != 1 || got[0] != 1 {
		t.Fatalf("level 50 jobs=%v, want [1]", got)
	}
	if got := FilterEquipmentSupportedJobs([]int{1, 2, 3}, items, 60, rc); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("level 60 jobs=%v, want [1 2]", got)
	}
}

func TestEquipmentSlotsNeedRepairAllowsUnavailableLevelSlots(t *testing.T) {
	items := map[int]shared.EquipmentCatalogItem{
		100: {ID: 100, ItemType: 1, Level: 50, Rarity: 3, Durability: 20, UseJob: []int{1}},
		111: {ID: 111, ItemType: 11, Level: 60, Rarity: 3, UseJob: []int{100}},
	}
	rc := robotconfig.RuntimeConfig{EquipSlots: []int{1, 11}, EquipRarityMax: 5}
	raw := make([]byte, 12*61)
	binary.LittleEndian.PutUint32(raw[2:6], 100)
	binary.LittleEndian.PutUint16(raw[11:13], 20)
	if EquipmentSlotsNeedRepair(raw, items, 50, 1, 0, rc) {
		t.Fatal("level-locked slot was treated as required")
	}
	if !EquipmentSlotsNeedRepair(raw, items, 60, 1, 0, rc) {
		t.Fatal("available level slot was not treated as required")
	}
}

func testSetCandidates(qualitySlots, varietySlots int) map[int][]shared.EquipmentCatalogItem {
	out := make(map[int][]shared.EquipmentCatalogItem)
	for slot := 0; slot < qualitySlots; slot++ {
		out[slot] = append(out[slot], shared.EquipmentCatalogItem{ID: 1000 + slot, SetKey: "quality", Level: 100, Rarity: 5})
	}
	for slot := 0; slot < varietySlots; slot++ {
		out[slot] = append(out[slot], shared.EquipmentCatalogItem{ID: 2000 + slot, SetKey: "variety"})
	}
	return out
}

// TestSelectEquipmentFamilySetItemsDrawsLooseAccessories covers the operator's
// fourth rule: 辅助装备和首饰可以发散件,大概40%的角色发散件,60%的角色发套装. The armor
// family is outside the draw -- a level-90 robot is owed a five-piece set
// whatever the coin says.
func TestSelectEquipmentFamilySetItemsDrawsLooseAccessories(t *testing.T) {
	candidates := map[int][]shared.EquipmentCatalogItem{}
	for _, family := range equipmentFamilies {
		key := fmt.Sprintf("family_%d_set", family[0])
		for _, slot := range family {
			candidates[slot] = append(candidates[slot],
				shared.EquipmentCatalogItem{ID: 1000 + slot, SetKey: key, Level: 90, Rarity: 4},
				shared.EquipmentCatalogItem{ID: 2000 + slot, SetKey: "", Level: 90, Rarity: 4},
			)
		}
	}
	for _, testCase := range []struct {
		draw int
		loose bool
	}{
		{EquipmentLooseAccessoryPercent - 1, true},
		{EquipmentLooseAccessoryPercent, false},
	} {
		draw := testCase.draw
		randIntn := func(n int) int {
			if n <= 0 {
				return 0
			}
			return draw % n
		}
		selected := SelectEquipmentFamilySetItems(candidates, 2, randIntn)
		for _, slot := range equipmentFamilies[0] {
			if selected[slot].SetKey == "" {
				t.Fatalf("draw %d left armor slot %d out of its set: %+v", draw, slot, selected[slot])
			}
		}
		for _, index := range []int{AccessoryFamilyIndex, SpecialFamilyIndex} {
			for _, slot := range equipmentFamilies[index] {
				item, ok := selected[slot]
				switch {
				case testCase.loose && ok:
					t.Fatalf("draw %d dressed loose slot %d from set %q", draw, slot, item.SetKey)
				case !testCase.loose && (!ok || item.SetKey == ""):
					t.Fatalf("draw %d left slot %d loose: %+v ok=%t", draw, slot, item, ok)
				}
			}
		}
	}
}

func TestBuildSetGroupsSupportsSharedSetItems(t *testing.T) {
	groups := buildSetGroups(map[int][]shared.EquipmentCatalogItem{
		0: {{ID: 100, SetKey: "set-a|set-b"}},
		1: {{ID: 101, SetKey: "set-a"}, {ID: 201, SetKey: "set-b"}},
	})
	if groups["set-a"] == nil || groups["set-a"].coverage != 2 {
		t.Fatalf("set-a group=%+v", groups["set-a"])
	}
	if groups["set-b"] == nil || groups["set-b"].coverage != 2 {
		t.Fatalf("set-b group=%+v", groups["set-b"])
	}
}
