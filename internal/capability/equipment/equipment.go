package equipment

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"math/rand"
	"sort"
	"strings"
	"sync"

	robotconfig "robot/internal/capability/robotconfig"
	foundrand "robot/internal/foundation/random"
	"robot/internal/shared"
)

type SlotOptions struct {
	IntensifyMin int
	IntensifyMax int
	SmithingMin  int
	SmithingMax  int
}

const (
	creatureSlotSize          = 61
	creatureSlotCount         = 102
	activeCreatureSlot        = 98
	firstCreatureArtifactSlot = 99
)

// buildCreatureSlots builds the creature inventory slice loaded
// into creature inventory slots 140-241. Its final four records become the
// equipped creature and red, blue, and green artifact slots respectively.
func buildCreatureSlots(itemID int, artifacts map[int]shared.EquipmentCatalogItem) []byte {
	raw := make([]byte, creatureSlotSize*creatureSlotCount)
	if itemID > 0 {
		offset := activeCreatureSlot * creatureSlotSize
		raw[offset+1] = 5
		binary.LittleEndian.PutUint32(raw[offset+2:offset+6], uint32(itemID))
		raw[offset+7] = 2
	}
	for itemType, item := range artifacts {
		if itemType < 31 || itemType > 33 || item.ID <= 0 || !PetArtifactRenderable(item) {
			continue
		}
		slot := firstCreatureArtifactSlot + itemType - 31
		WritePetArtifactSlot(raw[slot*creatureSlotSize:(slot+1)*creatureSlotSize], item)
	}
	return raw
}

func CompressedCreatureLoadout(itemID int, artifacts map[int]shared.EquipmentCatalogItem) []byte {
	return CompressRaw(buildCreatureSlots(itemID, artifacts))
}

func CompressRaw(raw []byte) []byte {
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	_, _ = zw.Write(raw)
	_ = zw.Close()
	blob := append(make([]byte, 4), compressed.Bytes()...)
	binary.LittleEndian.PutUint32(blob[0:4], uint32(len(raw)))
	return blob
}

func SlotToItemType(slot int) int {
	if slot >= 1 && slot <= 12 {
		return slot
	}
	return 0
}

func UsableByJob(jobs []int, job int) bool {
	if len(jobs) == 0 {
		return true
	}
	for _, j := range jobs {
		if j == 100 || j == job {
			return true
		}
	}
	return false
}

func AvatarUsableByJob(item shared.EquipmentCatalogItem, job int) bool {
	if item.ItemType < 20 || item.ItemType > 29 {
		return false
	}
	if len(item.UseJob) == 0 {
		return item.ItemType == 29
	}
	flags := avatarFlagsFor(item)
	if job >= 0 && job <= 4 && flags.female {
		return false
	}
	if job >= 5 && job <= 8 && flags.male {
		return false
	}
	for _, j := range item.UseJob {
		if j == job {
			return true
		}
	}
	return false
}

// avatarFlags is the metadata-derived part of avatar eligibility. The values
// are cached because the item catalog is scanned for every robot; the
// lowercased metadata string was the dominant allocation in startup.
type avatarFlags struct {
	renderable bool
	female     bool
	male       bool
}

type avatarFlagKey struct {
	id                      int
	name, name2, path, icon string
}

var avatarFlagCache sync.Map // avatarFlagKey -> avatarFlags

func avatarFlagsFor(item shared.EquipmentCatalogItem) avatarFlags {
	key := avatarFlagKey{id: item.ID, name: item.Name, name2: item.Name2, path: item.Path, icon: item.Icon}
	if cached, ok := avatarFlagCache.Load(key); ok {
		return cached.(avatarFlags)
	}
	meta := strings.ToLower(strings.TrimSpace(item.Name + " " + item.Name2 + " " + item.Path + " " + item.Icon))
	renderMeta := strings.ToLower(strings.TrimSpace(item.Name + " " + item.Path + " " + item.Icon))
	name := strings.ToLower(strings.TrimSpace(item.Name))
	flags := avatarFlags{
		renderable: name != "" && name != "errorstring" && !strings.HasPrefix(name, "name_") && !strings.Contains(renderMeta, "errorstring"),
		female:     strings.Contains(meta, "female") || strings.Contains(meta, "\u5973"),
		male:       strings.Contains(meta, "male") || strings.Contains(meta, "\u7537"),
	}
	avatarFlagCache.Store(key, flags)
	return flags
}

// AvatarRenderable filters PVF records explicitly marked as broken. Do not
// infer validity from outfit style: tattoos, swimwear and beach pieces are
// legitimate avatars.
func AvatarRenderable(item shared.EquipmentCatalogItem) bool {
	if item.ID == 0 {
		return false
	}
	return avatarFlagsFor(item).renderable
}

// FilterAvatarSupportedJobs intersects the configured creation jobs with the
// jobs that can fill the configured minimum number of avatar slots from PVF.
// A missing avatar catalog keeps the configured jobs so environments without
// an exported catalog retain their existing creation behavior.
func FilterAvatarSupportedJobs(jobs []int, items []shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig) []int {
	if len(jobs) == 0 || rc.MinAvatarSlots <= 0 {
		return append([]int(nil), jobs...)
	}
	slots := AvatarSlotsInUse(rc.AvatarSlots)
	wantedTypes := make(map[int]struct{}, len(slots))
	for _, slot := range slots {
		if slot >= 0 && slot <= 9 {
			wantedTypes[slot+20] = struct{}{}
		}
	}
	eligible := make([]shared.EquipmentCatalogItem, 0)
	for _, item := range items {
		if item.ID == 0 || item.Expire || item.GrantBlocked || !AvatarRenderable(item) {
			continue
		}
		if _, ok := wantedTypes[item.ItemType]; ok {
			eligible = append(eligible, item)
		}
	}
	if len(eligible) == 0 {
		return append([]int(nil), jobs...)
	}
	out := make([]int, 0, len(jobs))
	for _, job := range jobs {
		covered := make(map[int]struct{}, len(wantedTypes))
		for _, item := range eligible {
			if AvatarUsableByJob(item, job) {
				covered[item.ItemType] = struct{}{}
			}
		}
		if len(covered) >= rc.MinAvatarSlots {
			out = append(out, job)
		}
	}
	return out
}

// FilterEquipmentSupportedJobs keeps jobs that can equip a weapon at the
// character's generated level. Older PVFs may not contain newer job weapons.
func FilterEquipmentSupportedJobs(jobs []int, items []shared.EquipmentCatalogItem, level int, rc robotconfig.RuntimeConfig) []int {
	if !configuredEquipmentSlot(rc.EquipSlots, 1) || len(items) == 0 {
		return append([]int(nil), jobs...)
	}
	hasWeaponCatalog := false
	for _, item := range items {
		if item.ID > 0 && item.ItemType == 1 {
			hasWeaponCatalog = true
			break
		}
	}
	if !hasWeaponCatalog {
		return append([]int(nil), jobs...)
	}
	out := make([]int, 0, len(jobs))
	for _, job := range jobs {
		for _, item := range items {
			if equipmentCandidate(item, 1, level, job, 0, rc) {
				out = append(out, job)
				break
			}
		}
	}
	return out
}

func SafeAvg(total, count int) int {
	if count <= 0 {
		return 0
	}
	return total / count
}

func SelectEquipment(items []shared.EquipmentCatalogItem, level int, job, grow int, rc robotconfig.RuntimeConfig, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	candidatesBySlot, bestLevelBySlot := equipmentCandidates(items, level, job, grow, rc)
	for slot, candidates := range candidatesBySlot {
		if len(candidates) == 0 {
			delete(candidatesBySlot, slot)
			continue
		}
		bestLevel := bestLevelBySlot[slot]
		if bestLevel > 0 {
			near := candidates[:0]
			for _, item := range candidates {
				if item.Level >= bestLevel-10 {
					near = append(near, item)
				}
			}
			if len(near) > 0 {
				candidates = near
			}
		}
		candidatesBySlot[slot] = candidates
	}
	selected := make(map[int]shared.EquipmentCatalogItem)
	if rc.PreferEquipSets {
		selected = SelectEquipmentFamilySetItems(candidatesBySlot, rc.EquipSetMinSlots, randIntn)
	}
	FillRandomItems(selected, candidatesBySlot, randIntn)
	// Pinned after the draw so it survives both the set pick and the fill. The
	// title is not part of the candidate pool: it is named, not chosen.
	if item, ok := pinnedTitle(level, rc, items, randIntn); ok {
		selected[titleSlot] = item
	}
	return selected
}

// BestEquipmentLevels returns the highest selectable item level per configured
// equipment slot for (level, job, config). The loadout compatibility check uses
// the same candidate filters as SelectEquipment so an existing loadout is only
// kept when a fresh selection could have produced it.
func BestEquipmentLevels(items []shared.EquipmentCatalogItem, level, job, grow int, rc robotconfig.RuntimeConfig) map[int]int {
	_, bestLevelBySlot := equipmentCandidates(items, level, job, grow, rc)
	return bestLevelBySlot
}

// equipmentCandidates collects the selectable items per configured slot and
// the highest selectable item level per slot.
//
// The level and rarity the pool is drawn from come from the character's level
// band, not from the equipment config: the operator states one rarity per band,
// and a band's rarity is a single value rather than a range, so the retired
// equip_rarity_min/max keys no longer decide what a robot wears. An armor slot
// the band's rarity has nothing for drops to armorFallbackRarities; a slot of
// any other kind simply goes unfilled.
func equipmentCandidates(items []shared.EquipmentCatalogItem, level, job, grow int, rc robotconfig.RuntimeConfig) (map[int][]shared.EquipmentCatalogItem, map[int]int) {
	slots := rc.EquipSlots
	if len(slots) == 0 {
		slots = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	// 耳环 is worn on top of the configured slots by a level-90 character, and
	// only then: below 90 the archive has no earring a character may wear, so
	// the slot is not offered at all, whether or not equip_slots lists it.
	if level >= EarringMinLevel {
		slots = appendWornSlot(slots, EarringItemType)
	} else {
		slots = withoutWornSlot(slots, EarringItemType)
	}
	materials := ArmorMaterialsFor(job, grow)
	gearLevel := EquipLevelFor(level)
	rarity := BandRarityFor(level)
	slotByItemType := make(map[int]int, len(slots))
	for _, slot := range slots {
		itemType := WornItemTypeOfSlot(slot)
		if itemType == 0 {
			continue
		}
		slotByItemType[itemType] = slot
	}
	// Every rarity the pool holds is kept apart rather than filtered down to the
	// band's own, so an armor slot that the band cannot fill falls back without a
	// second walk over the catalog. See armorFallbackRarities.
	poolBySlot := make(map[int]map[int][]shared.EquipmentCatalogItem, len(slots))
	for _, item := range items {
		slot, wanted := slotByItemType[item.ItemType]
		if !wanted || item.ID == 0 || item.Expire || item.GrantBlocked || item.Level > gearLevel {
			continue
		}
		if !UsableByJob(item.UseJob, job) {
			continue
		}
		if !armorAllowed(item, materials) {
			continue
		}
		if !weaponAllowed(item, job, grow) {
			continue
		}
		if poolBySlot[slot] == nil {
			poolBySlot[slot] = make(map[int][]shared.EquipmentCatalogItem, 2)
		}
		poolBySlot[slot][item.Rarity] = append(poolBySlot[slot][item.Rarity], item)
	}
	candidatesBySlot := make(map[int][]shared.EquipmentCatalogItem, len(slots))
	bestLevelBySlot := make(map[int]int, len(slots))
	for _, slot := range slots {
		itemType := WornItemTypeOfSlot(slot)
		if itemType == 0 {
			continue
		}
		pool := poolBySlot[slot]
		candidates := pool[rarity]
		if len(candidates) == 0 && itemType >= firstArmorItemType && itemType <= lastArmorItemType {
			for _, fallback := range armorFallbackRarities {
				if len(pool[fallback]) > 0 {
					candidates = pool[fallback]
					break
				}
			}
		}
		if len(candidates) == 0 {
			continue
		}
		candidatesBySlot[slot] = candidates
		for _, item := range candidates {
			if item.Level > bestLevelBySlot[slot] {
				bestLevelBySlot[slot] = item.Level
			}
		}
	}
	return candidatesBySlot, bestLevelBySlot
}

// SelectAvatar chooses the 时装 a robot of this level wears. The level is what
// picks the grade — see AvatarRarityFor for the rule and the band it comes from.
func SelectAvatar(items []shared.EquipmentCatalogItem, level, job int, rc robotconfig.RuntimeConfig, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	return PrepareAvatarCatalog(items, rc).Select(level, job, rc, randIntn)
}

// AvatarCandidateCatalog is a prepared, immutable per-job view of one avatar
// catalog. Preparing it once keeps repeated selections for the same catalog
// from rescanning every item; the plain SelectAvatar helper builds a throwaway
// catalog for callers that select only once.
type AvatarCandidateCatalog struct {
	items []shared.EquipmentCatalogItem
	slots []int
	jobs  map[int]*avatarJobCandidates
}

// avatarJobCandidates is one job's avatars, split the way selection asks for
// them: one pool per 时装 grade, and 光环 collected on its own.
type avatarJobCandidates struct {
	bands map[int]*avatarBandCandidates
	aura  []shared.EquipmentCatalogItem
}

// avatarBandCandidates is one 时装 grade's pool for one job. The bySlot keys are
// the configured slots except 光环, which never joins a grade or a set group.
type avatarBandCandidates struct {
	bySlot map[int][]shared.EquipmentCatalogItem
	groups map[string]*setGroup
}

// maxAvatarJob bounds the per-job avatar buckets. The game's character/character.lst
// ends at job 15 and its creation path refuses anything outside it, so every job
// the robot can create has a bucket; Select treats a job without one as having no
// avatars. Keep this in step with the job ids the PVF releases.
const maxAvatarJob = 15

func PrepareAvatarCatalog(items []shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig) *AvatarCandidateCatalog {
	slots := AvatarSlotsInUse(rc.AvatarSlots)
	slotByType := make(map[int]int, len(slots))
	auraWanted := false
	for _, slot := range slots {
		if slot == AvatarAuraSlot {
			auraWanted = true
			continue
		}
		slotByType[slot+20] = slot
	}
	catalog := &AvatarCandidateCatalog{items: items, slots: slots, jobs: make(map[int]*avatarJobCandidates, maxAvatarJob+1)}
	for job := 0; job <= maxAvatarJob; job++ {
		bands := make(map[int]*avatarBandCandidates, 2)
		for _, rarity := range []int{CommonAvatarRarity, RareAvatarRarity} {
			bySlot := make(map[int][]shared.EquipmentCatalogItem, len(slots))
			for _, slot := range slots {
				if slot != AvatarAuraSlot {
					bySlot[slot] = nil
				}
			}
			bands[rarity] = &avatarBandCandidates{bySlot: bySlot}
		}
		catalog.jobs[job] = &avatarJobCandidates{bands: bands}
	}
	for _, item := range items {
		if item.ID == 0 || item.Expire || item.GrantBlocked || !AvatarRenderable(item) {
			continue
		}
		if item.ItemType-20 == AvatarAuraSlot {
			if !auraWanted {
				continue
			}
			for job := 0; job <= maxAvatarJob; job++ {
				if AvatarUsableByJob(item, job) {
					catalog.jobs[job].aura = append(catalog.jobs[job].aura, item)
				}
			}
			continue
		}
		slot, wanted := slotByType[item.ItemType]
		if !wanted {
			continue
		}
		rarityBand := AvatarRarityBand(item.Rarity)
		for job := 0; job <= maxAvatarJob; job++ {
			if !AvatarUsableByJob(item, job) {
				continue
			}
			bySlot := catalog.jobs[job].bands[rarityBand].bySlot
			bySlot[slot] = append(bySlot[slot], item)
		}
	}
	for _, candidates := range catalog.jobs {
		for _, band := range candidates.bands {
			band.groups = buildAvatarSetGroups(band.bySlot)
		}
	}
	return catalog
}

func (c *AvatarCandidateCatalog) Select(level, job int, rc robotconfig.RuntimeConfig, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	if c == nil {
		return map[int]shared.EquipmentCatalogItem{}
	}
	if !sameIntValues(c.slots, AvatarSlotsInUse(rc.AvatarSlots)) {
		// The caller changed the avatar slot configuration after preparation.
		// Rebuild instead of returning a selection from stale candidates. The
		// rebuild carries the current slots, so the retry settles.
		return PrepareAvatarCatalog(c.items, rc).Select(level, job, rc, randIntn)
	}
	candidates, ok := c.jobs[job]
	if !ok {
		// A job outside maxAvatarJob has no avatars. Rebuilding cannot change
		// that, so this must not retry: doing so recursed until the goroutine
		// stack overflowed, killing the process on the first robot of such a
		// job instead of leaving it without avatars.
		return map[int]shared.EquipmentCatalogItem{}
	}
	if level < AvatarMinLevel {
		// Under the floor the client would refuse the 高级时装 this path hands
		// out, and the operator chose to grant nothing over splitting the low
		// levels further.
		return map[int]shared.EquipmentCatalogItem{}
	}
	selected := make(map[int]shared.EquipmentCatalogItem)
	if band, ok := candidates.bands[AvatarRarityFor(level)]; ok {
		if rc.PreferAvatarSets {
			selected = selectAvatarSetItemsFromGroups(band.groups, AvatarSetMinSlotsFor(level, rc.AvatarSetMinSlots), randIntn)
		}
		FillRandomItems(selected, band.bySlot, randIntn)
	}
	// 光环 is drawn last and on its own, from every grade the archive carries for
	// it. It is not part of the 时装 set, so the level band must not narrow it.
	if len(candidates.aura) > 0 {
		selected[AvatarAuraSlot] = candidates.aura[safeRandIntn(randIntn, len(candidates.aura))]
	}
	return selected
}

func sameIntValues(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// SelectPet chooses one usable creature and a bounded subset of its artifact
// slots. Pet artifacts are keyed by their protocol item type (31, 32, 33).
func SelectPet(items []shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig, randIntn func(int) int) (shared.EquipmentCatalogItem, map[int]shared.EquipmentCatalogItem, bool) {
	if !rc.PetEnabled {
		return shared.EquipmentCatalogItem{}, nil, false
	}
	creatures := make([]shared.EquipmentCatalogItem, 0)
	byType := make(map[int][]shared.EquipmentCatalogItem)
	artifactSlots := rc.PetArtifactSlots
	if len(artifactSlots) == 0 {
		artifactSlots = []int{31, 32, 33}
	}
	allowed := make(map[int]struct{}, len(artifactSlots))
	for _, itemType := range artifactSlots {
		if itemType >= 31 && itemType <= 33 {
			allowed[itemType] = struct{}{}
		}
	}
	for _, item := range items {
		if item.ID <= 0 || item.Expire || item.GrantBlocked {
			continue
		}
		switch item.ItemType {
		case 30:
			if PetRenderable(item) {
				creatures = append(creatures, item)
			}
		case 31, 32, 33:
			if rc.PetArtifactEnabled {
				if _, ok := allowed[item.ItemType]; ok && PetArtifactRenderable(item) {
					byType[item.ItemType] = append(byType[item.ItemType], item)
				}
			}
		}
	}
	if len(creatures) == 0 {
		return shared.EquipmentCatalogItem{}, nil, false
	}
	pet := creatures[safeRandIntn(randIntn, len(creatures))]
	if !rc.PetArtifactEnabled || len(byType) == 0 || rc.MaxPetArtifactSlots <= 0 {
		return pet, nil, true
	}
	types := make([]int, 0, len(byType))
	for itemType := range byType {
		types = append(types, itemType)
	}
	sort.Ints(types)
	maxSlots := rc.MaxPetArtifactSlots
	if maxSlots > len(types) {
		maxSlots = len(types)
	}
	minSlots := rc.MinPetArtifactSlots
	if minSlots < 0 {
		minSlots = 0
	}
	if minSlots > maxSlots {
		minSlots = maxSlots
	}
	count := minSlots
	if maxSlots > minSlots {
		count += safeRandIntn(randIntn, maxSlots-minSlots+1)
	}
	selected := make(map[int]shared.EquipmentCatalogItem, count)
	for i := 0; i < count; i++ {
		last := len(types) - i - 1
		pick := safeRandIntn(randIntn, last+1)
		itemType := types[pick]
		types[pick], types[last] = types[last], types[pick]
		candidates := byType[itemType]
		selected[itemType] = candidates[safeRandIntn(randIntn, len(candidates))]
	}
	return pet, selected, true
}

// petEggNameMarker is the word that marks an unhatched creature. The catalog
// carries 91 of them next to the real creatures and they share the creature
// equipment type, so nothing but the name tells them apart.
const petEggNameMarker = "宠物蛋"

// PetRenderable reports whether a catalog item may be worn as a creature.
//
// The egg filter is the operator's: the 90CN client crashes when an unhatched
// 宠物蛋 is placed in the creature slot, which is exactly the slot a type-30
// item is written to. A hatched creature carries the same type and the same
// table, so the name is the only thing that separates the two, and a draw that
// ignores it hands the client a crash at login.
func PetRenderable(item shared.EquipmentCatalogItem) bool {
	if item.ID <= 0 || item.Expire || item.GrantBlocked {
		return false
	}
	if item.ItemType != 30 {
		return false
	}
	if strings.Contains(item.Name, petEggNameMarker) || strings.Contains(item.Name2, petEggNameMarker) {
		return false
	}
	return true
}

// PetArtifactRenderable rejects malformed item-info fallbacks and
// quest-material scripts that happen to carry an artifact equipment type.
// Those records have ErrorString names or stackable icons and are not
// loadable by the game creature manager as equipped artifacts.
func PetArtifactRenderable(item shared.EquipmentCatalogItem) bool {
	if item.ID <= 0 || item.Expire || item.GrantBlocked {
		return false
	}
	if item.ItemType < 31 || item.ItemType > 33 {
		return false
	}
	name := strings.TrimSpace(item.Name)
	if name == "" && (item.Path != "" || item.Icon != "" || item.Name2 != "") {
		return false
	}
	if strings.EqualFold(name, "ErrorString") || strings.EqualFold(strings.TrimSpace(item.Name2), "ErrorString") {
		return false
	}
	if item.NeedMaterial || item.BasicMaterial {
		return false
	}
	path := strings.ToLower(strings.TrimSpace(item.Path))
	if path != "" && path != "etc/iteminfo.dat" && !strings.Contains(path, "equipment/creature/artifact_") {
		return false
	}
	icon := strings.ToLower(strings.TrimSpace(item.Icon))
	return icon == "" || !strings.Contains(icon, "stackable")
}

func BuildEquipmentSlots(items []shared.EquipmentCatalogItem, level int, job, grow int, rc robotconfig.RuntimeConfig, randIntn func(int) int, withRand func(func(*rand.Rand)) error) []byte {
	selected := SelectEquipment(items, level, job, grow, rc, randIntn)
	raw := make([]byte, 12*61)
	for slot, item := range selected {
		if slot < 1 || slot > 12 {
			// The earring has no place in the legacy 12-slot blob.
			continue
		}
		write := func(rng *rand.Rand) {
			WriteEquipSlot(raw[(slot-1)*61:slot*61], item, rng, SlotOptions{
				IntensifyMin: rc.EquipIntensifyMin,
				IntensifyMax: rc.EquipIntensifyMax,
				SmithingMin:  rc.EquipSmithingMin,
				SmithingMax:  rc.EquipSmithingMax,
			})
		}
		if withRand != nil {
			_ = withRand(write)
		} else {
			write(rand.New(rand.NewSource(1)))
		}
	}
	return raw
}

func EquipmentSlotsNeedRepair(raw []byte, itemsByID map[int]shared.EquipmentCatalogItem, level, job, grow int, rc robotconfig.RuntimeConfig) bool {
	slots := equipSlotsFor(level, rc)
	required := make(map[int]bool, len(slots))
	for _, item := range itemsByID {
		if equipmentCandidate(item, item.ItemType, level, job, grow, rc) && configuredEquipmentSlot(slots, item.ItemType) {
			required[item.ItemType] = true
		}
	}
	for _, slot := range slots {
		if SlotToItemType(slot) == 0 {
			continue
		}
		offset := (slot - 1) * 61
		if offset+61 > len(raw) {
			return true
		}
		slotRaw := raw[offset : offset+61]
		itemID := int(binary.LittleEndian.Uint32(slotRaw[2:6]))
		if itemID == 0 && !required[slot] {
			continue
		}
		item, ok := itemsByID[itemID]
		if !ok || !equipmentCandidate(item, slot, level, job, grow, rc) {
			return true
		}
		durability := int(binary.LittleEndian.Uint16(slotRaw[11:13]))
		expectedDurability := item.Durability
		if expectedDurability < 0 {
			expectedDurability = 0
		}
		if expectedDurability > 65535 {
			expectedDurability = 65535
		}
		if durability != expectedDurability {
			return true
		}
	}
	return false
}

func configuredEquipmentSlot(slots []int, wanted int) bool {
	if len(slots) == 0 {
		return wanted >= 1 && wanted <= 12
	}
	for _, slot := range slots {
		if slot == wanted {
			return true
		}
	}
	return false
}

func equipmentCandidate(item shared.EquipmentCatalogItem, slot, level, job, grow int, rc robotconfig.RuntimeConfig) bool {
	if item.ID <= 0 || item.ItemType != slot || item.Expire || item.GrantBlocked {
		return false
	}
	if slot == titleSlot {
		if ids := TitleTierItemIDs(level, rc); len(ids) > 0 {
			// Pinned, not drawn, so the job sweep and the rarity band must not
			// second-guess it: the operator named these items. 天选之人 is a
			// rarity-6 title and the fleet's band tops out at 5. Every id in the
			// tier counts, or a robot that drew the second rank would be repaired
			// on every pass.
			return TitleAllowed(level, rc, item.ID)
		}
		// No pin: slot 2 behaves like any other, so an operator who lists it in
		// equip_slots keeps the random draw and its band.
	}
	if item.Level > EquipLevelFor(level) || !UsableByJob(item.UseJob, job) {
		return false
	}
	if slot == EarringItemType && level < EarringMinLevel {
		return false
	}
	if !armorAllowed(item, ArmorMaterialsFor(job, grow)) {
		return false
	}
	if !weaponAllowed(item, job, grow) {
		return false
	}
	// Same band rule as equipmentCandidates, so an existing loadout is only
	// kept when a fresh selection could have produced it.
	return item.Rarity == BandRarityFor(level)
}

type setGroup struct {
	key       string
	bySlot    map[int][]shared.EquipmentCatalogItem
	coverage  int
	levelSum  int
	raritySum int
	count     int
}

func SelectSetItems(candidatesBySlot map[int][]shared.EquipmentCatalogItem, minSlots int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	return selectBestSetItems(buildSetGroups(candidatesBySlot), minSlots, randIntn)
}

func SelectAvatarSetItems(candidatesBySlot map[int][]shared.EquipmentCatalogItem, minSlots int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	return selectAvatarSetItemsFromGroups(buildAvatarSetGroups(candidatesBySlot), minSlots, randIntn)
}

func selectAvatarSetItemsFromGroups(groups map[string]*setGroup, minSlots int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	coverageFloor := 6
	if minSlots > coverageFloor {
		coverageFloor = minSlots
	}
	eligible := make([]*setGroup, 0, len(groups))
	for _, group := range groups {
		if group.coverage >= coverageFloor {
			eligible = append(eligible, group)
		}
	}
	if len(eligible) == 0 {
		return selectAvatarBestSetItems(groups, minSlots, randIntn)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].coverage != eligible[j].coverage {
			return eligible[i].coverage > eligible[j].coverage
		}
		if eligible[i].levelSum != eligible[j].levelSum {
			return eligible[i].levelSum > eligible[j].levelSum
		}
		if eligible[i].raritySum != eligible[j].raritySum {
			return eligible[i].raritySum > eligible[j].raritySum
		}
		return eligible[i].key < eligible[j].key
	})
	bestCoverage := eligible[0].coverage
	bestLevel := eligible[0].levelSum
	bestRarity := eligible[0].raritySum
	best := eligible[:0]
	for _, group := range eligible {
		if group.coverage == bestCoverage && group.levelSum == bestLevel && group.raritySum == bestRarity {
			best = append(best, group)
		}
	}
	return selectAvatarFamilySet(best[safeRandIntn(randIntn, len(best))], randIntn)
}

// selectAvatarBestSetItems is selectBestSetItems with the 装扮 materializer, so
// the fallback below the coverage floor dresses from a name family too.
func selectAvatarBestSetItems(groups map[string]*setGroup, minSlots int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	if minSlots <= 1 {
		minSlots = 2
	}
	var best []*setGroup
	bestScore := -1
	for _, group := range groups {
		if group.coverage < minSlots {
			continue
		}
		score := group.coverage*1000000 + SafeAvg(group.levelSum, group.count)*1000 + SafeAvg(group.raritySum, group.count)
		if score > bestScore {
			bestScore = score
			best = []*setGroup{group}
		} else if score == bestScore {
			best = append(best, group)
		}
	}
	if len(best) == 0 {
		return map[int]shared.EquipmentCatalogItem{}
	}
	return selectAvatarFamilySet(best[safeRandIntn(randIntn, len(best))], randIntn)
}

func buildSetGroups(candidatesBySlot map[int][]shared.EquipmentCatalogItem) map[string]*setGroup {
	return buildSetGroupsByKey(candidatesBySlot, func(item shared.EquipmentCatalogItem) []string {
		return itemSetKeys(item.SetKey)
	})
}

// buildAvatarSetGroups groups 装扮 by the name family the pieces share, falling
// back to the archive's set key for a piece whose name cannot name a family. The
// archive's key is useless for 装扮 -- every 稀有装扮 piece of a job carries
// pvf_part_3 -- so the name is what tells one 天空套 from the next.
func buildAvatarSetGroups(candidatesBySlot map[int][]shared.EquipmentCatalogItem) map[string]*setGroup {
	return buildSetGroupsByKey(candidatesBySlot, func(item shared.EquipmentCatalogItem) []string {
		if family := AvatarFamilyKey(item.Name); family != "" {
			return []string{family}
		}
		return itemSetKeys(item.SetKey)
	})
}

func buildSetGroupsByKey(candidatesBySlot map[int][]shared.EquipmentCatalogItem, keysFor func(shared.EquipmentCatalogItem) []string) map[string]*setGroup {
	groups := make(map[string]*setGroup)
	for slot, candidates := range candidatesBySlot {
		for _, item := range candidates {
			for _, setKey := range keysFor(item) {
				group := groups[setKey]
				if group == nil {
					group = &setGroup{key: setKey, bySlot: make(map[int][]shared.EquipmentCatalogItem)}
					groups[setKey] = group
				}
				if len(group.bySlot[slot]) == 0 {
					group.coverage++
				}
				group.bySlot[slot] = append(group.bySlot[slot], item)
				group.levelSum += item.Level
				group.raritySum += item.Rarity
				group.count++
			}
		}
	}
	return groups
}

func itemSetKeys(value string) []string {
	parts := strings.Split(value, "|")
	out := make([]string, 0, len(parts))
	seen := make(map[string]bool)
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return out
}

func selectBestSetItems(groups map[string]*setGroup, minSlots int, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	if minSlots <= 1 {
		minSlots = 2
	}
	var best []*setGroup
	bestScore := -1
	for _, group := range groups {
		if group.coverage < minSlots {
			continue
		}
		score := group.coverage*1000000 + SafeAvg(group.levelSum, group.count)*1000 + SafeAvg(group.raritySum, group.count)
		if score > bestScore {
			bestScore = score
			best = []*setGroup{group}
		} else if score == bestScore {
			best = append(best, group)
		}
	}
	selected := make(map[int]shared.EquipmentCatalogItem)
	if len(best) == 0 {
		return selected
	}
	return selectSetGroup(best[safeRandIntn(randIntn, len(best))], randIntn)
}

func selectSetGroup(group *setGroup, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	selected := make(map[int]shared.EquipmentCatalogItem)
	if group == nil {
		return selected
	}
	for slot, candidates := range group.bySlot {
		if len(candidates) == 0 {
			continue
		}
		selected[slot] = candidates[safeRandIntn(randIntn, len(candidates))]
	}
	return selected
}

// selectAvatarFamilySet materializes one 装扮 set from a name family: a seed
// piece is drawn, and every other slot then takes the candidate whose name
// shares the longest prefix with the seed.
//
// Picking at random from the family would mix it with the sets that share its
// four-character root, and picking at random from the archive's pvf_part_3 group
// mixes all 稀有装扮 of the job. A group whose pieces carry no usable name is
// materialized the old way, so a SetKey-grouped pool keeps behaving as it did.
func selectAvatarFamilySet(group *setGroup, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	selected := make(map[int]shared.EquipmentCatalogItem)
	if group == nil {
		return selected
	}
	slots := make([]int, 0, len(group.bySlot))
	for slot, candidates := range group.bySlot {
		if len(candidates) > 0 {
			slots = append(slots, slot)
		}
	}
	if len(slots) == 0 {
		return selected
	}
	sort.Ints(slots)
	seedSlot := slots[safeRandIntn(randIntn, len(slots))]
	seed := group.bySlot[seedSlot][safeRandIntn(randIntn, len(group.bySlot[seedSlot]))]
	if AvatarFamilyKey(seed.Name) == "" {
		return selectSetGroup(group, randIntn)
	}
	selected[seedSlot] = seed
	for _, slot := range slots {
		if slot == seedSlot {
			continue
		}
		best, bestAffinity := shared.EquipmentCatalogItem{}, -1
		for _, item := range group.bySlot[slot] {
			if affinity := AvatarNameAffinity(seed.Name, item.Name); affinity > bestAffinity {
				best, bestAffinity = item, affinity
			}
		}
		if bestAffinity < AvatarFamilyMinPrefix {
			// Nothing in this slot belongs to the seed's own set, so the slot is
			// left to the random fill rather than dressed from a stranger's set.
			continue
		}
		selected[slot] = best
	}
	return selected
}

func FillRandomItems(selected map[int]shared.EquipmentCatalogItem, candidatesBySlot map[int][]shared.EquipmentCatalogItem, randIntn func(int) int) {
	for slot, candidates := range candidatesBySlot {
		if _, ok := selected[slot]; ok || len(candidates) == 0 {
			continue
		}
		selected[slot] = candidates[safeRandIntn(randIntn, len(candidates))]
	}
}

func WriteEquipSlot(dst []byte, item shared.EquipmentCatalogItem, rng *rand.Rand, opt SlotOptions) {
	if len(dst) < 61 {
		return
	}
	dst[0] = 0x00
	dst[1] = 0x01
	binary.LittleEndian.PutUint32(dst[2:6], uint32(item.ID))
	intensifyMin := maxInt(opt.IntensifyMin, 7)
	intensifyMax := maxInt(opt.IntensifyMax, intensifyMin)
	intensify := foundrand.BetweenAtLeast(rng, intensifyMin, intensifyMax)
	if item.ItemType == 1 {
		intensify = foundrand.BetweenAtLeast(rng, 8, 15)
	}
	if item.ItemType == 2 {
		intensify = 0
	}
	dst[6] = byte(intensify)
	binary.LittleEndian.PutUint32(dst[7:11], uint32(foundrand.BetweenAtLeast(rng, 0, 400000)))
	durability := item.Durability
	if durability < 0 {
		durability = 0
	}
	if durability > 65535 {
		durability = 65535
	}
	binary.LittleEndian.PutUint16(dst[11:13], uint16(durability))
	if item.ItemType == 1 {
		dst[51] = byte(foundrand.BetweenAtLeast(rng, opt.SmithingMin, opt.SmithingMax))
	}
}

// WritePetArtifactSlot builds the compact inventory record consumed by the
// creature manager for an equipped artifact. Artifacts occupy the last three
// records of the creature inventory block and do not use weapon-style
// enhancement or random durability values.
func WritePetArtifactSlot(dst []byte, item shared.EquipmentCatalogItem) {
	if len(dst) < 61 || item.ID <= 0 {
		return
	}
	clear(dst)
	dst[1] = 0x01
	binary.LittleEndian.PutUint32(dst[2:6], uint32(item.ID))
	durability := item.Durability
	if durability < 0 {
		durability = 0
	}
	if durability > 65535 {
		durability = 65535
	}
	binary.LittleEndian.PutUint16(dst[11:13], uint16(durability))
}

// WriteStoreEquipSlot builds a complete inventory equipment record once for
// the private-store pool, then applies the explicitly configured enhancement.
func WriteStoreEquipSlot(dst []byte, item shared.EquipmentCatalogItem, rng *rand.Rand, intensify int) {
	WriteEquipSlot(dst, item, rng, SlotOptions{IntensifyMin: intensify, IntensifyMax: intensify})
	if len(dst) < 61 {
		return
	}
	// Inventory byte 0 is the sealed-instance flag. PVF "sealing"
	// equipment must enter the bag sealed or the server treats the instance as
	// trade-restricted when CPrivateStore::AddItem validates it.
	dst[0] = 1
	clear(dst[7:11])
	durability := item.Durability
	if durability <= 0 {
		// Catalogs exported before durability was included remain usable. One is
		// valid for durable equipment and avoids generating a value above the PVF
		// maximum, which the tested store validator rejects with 0x11.
		durability = 1
	}
	if durability > 65535 {
		durability = 65535
	}
	binary.LittleEndian.PutUint16(dst[11:13], uint16(durability))
	if intensify < 0 {
		intensify = 0
	}
	if intensify > 255 {
		intensify = 255
	}
	if isTitleEquipment(item) {
		intensify = 0
	}
	dst[6] = byte(intensify)
}

func isTitleEquipment(item shared.EquipmentCatalogItem) bool {
	if item.ItemType == 2 {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(item.Slot)) {
	case "titlename", "title", "title name":
		return true
	default:
		return false
	}
}

func safeRandIntn(randIntn func(int) int, n int) int {
	if n <= 0 || randIntn == nil {
		return 0
	}
	v := randIntn(n)
	if v < 0 || v >= n {
		return 0
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
