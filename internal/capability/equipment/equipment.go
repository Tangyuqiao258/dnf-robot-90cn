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
	slots := rc.AvatarSlots
	if len(slots) == 0 {
		slots = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	}
	wantedTypes := make(map[int]struct{}, len(slots))
	for _, slot := range slots {
		if slot >= 0 && slot <= 9 {
			wantedTypes[slot+20] = struct{}{}
		}
	}
	eligible := make([]shared.EquipmentCatalogItem, 0)
	for _, item := range items {
		if item.ID == 0 || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) || !AvatarRenderable(item) {
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
		selected = SelectSetItems(candidatesBySlot, rc.EquipSetMinSlots, randIntn)
	}
	FillRandomItems(selected, candidatesBySlot, randIntn)
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
func equipmentCandidates(items []shared.EquipmentCatalogItem, level, job, grow int, rc robotconfig.RuntimeConfig) (map[int][]shared.EquipmentCatalogItem, map[int]int) {
	slots := rc.EquipSlots
	if len(slots) == 0 {
		slots = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
	materials := ArmorMaterialsFor(job, grow)
	candidatesBySlot := make(map[int][]shared.EquipmentCatalogItem, len(slots))
	slotByItemType := make(map[int]int, len(slots))
	bestLevelBySlot := make(map[int]int, len(slots))
	for _, slot := range slots {
		itemType := SlotToItemType(slot)
		if itemType == 0 {
			continue
		}
		slotByItemType[itemType] = slot
		candidatesBySlot[slot] = nil
	}
	for _, item := range items {
		slot, wanted := slotByItemType[item.ItemType]
		if !wanted || item.ID == 0 || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) || item.Level > level {
			continue
		}
		if rc.EquipRarityMax > 0 && (item.Rarity < rc.EquipRarityMin || item.Rarity > rc.EquipRarityMax) {
			continue
		}
		if !UsableByJob(item.UseJob, job) {
			continue
		}
		if !armorAllowed(item, materials) {
			continue
		}
		if item.Level > bestLevelBySlot[slot] {
			bestLevelBySlot[slot] = item.Level
		}
		candidatesBySlot[slot] = append(candidatesBySlot[slot], item)
	}
	return candidatesBySlot, bestLevelBySlot
}

func SelectAvatar(items []shared.EquipmentCatalogItem, job int, rc robotconfig.RuntimeConfig, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	return PrepareAvatarCatalog(items, rc).Select(job, rc, randIntn)
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

type avatarJobCandidates struct {
	bySlot map[int][]shared.EquipmentCatalogItem
	groups map[string]*setGroup
}

// maxAvatarJob bounds the per-job avatar buckets. The game's character/character.lst
// ends at job 15 and its creation path refuses anything outside it, so every job
// the robot can create has a bucket; Select treats a job without one as having no
// avatars. Keep this in step with the job ids the PVF releases.
const maxAvatarJob = 15

func PrepareAvatarCatalog(items []shared.EquipmentCatalogItem, rc robotconfig.RuntimeConfig) *AvatarCandidateCatalog {
	slots := normalizedAvatarSlots(rc.AvatarSlots)
	slotByType := make(map[int]int, len(slots))
	for _, slot := range slots {
		slotByType[slot+20] = slot
	}
	catalog := &AvatarCandidateCatalog{items: items, slots: slots, jobs: make(map[int]*avatarJobCandidates, maxAvatarJob+1)}
	for job := 0; job <= maxAvatarJob; job++ {
		bySlot := make(map[int][]shared.EquipmentCatalogItem, len(slots))
		for _, slot := range slots {
			bySlot[slot] = nil
		}
		catalog.jobs[job] = &avatarJobCandidates{bySlot: bySlot}
	}
	for _, item := range items {
		slot, wanted := slotByType[item.ItemType]
		if !wanted || item.ID == 0 || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) || !AvatarRenderable(item) {
			continue
		}
		for job := 0; job <= maxAvatarJob; job++ {
			if AvatarUsableByJob(item, job) {
				candidates := catalog.jobs[job]
				candidates.bySlot[slot] = append(candidates.bySlot[slot], item)
			}
		}
	}
	for _, candidates := range catalog.jobs {
		candidates.groups = buildSetGroups(candidates.bySlot)
	}
	return catalog
}

func (c *AvatarCandidateCatalog) Select(job int, rc robotconfig.RuntimeConfig, randIntn func(int) int) map[int]shared.EquipmentCatalogItem {
	if c == nil {
		return map[int]shared.EquipmentCatalogItem{}
	}
	if !sameIntValues(c.slots, normalizedAvatarSlots(rc.AvatarSlots)) {
		// The caller changed the avatar slot configuration after preparation.
		// Rebuild instead of returning a selection from stale candidates. The
		// rebuild carries the current slots, so the retry settles.
		return PrepareAvatarCatalog(c.items, rc).Select(job, rc, randIntn)
	}
	candidates, ok := c.jobs[job]
	if !ok {
		// A job outside maxAvatarJob has no avatars. Rebuilding cannot change
		// that, so this must not retry: doing so recursed until the goroutine
		// stack overflowed, killing the process on the first robot of such a
		// job instead of leaving it without avatars.
		return map[int]shared.EquipmentCatalogItem{}
	}
	selected := make(map[int]shared.EquipmentCatalogItem)
	if rc.PreferAvatarSets {
		selected = selectAvatarSetItemsFromGroups(candidates.groups, rc.AvatarSetMinSlots, randIntn)
	}
	FillRandomItems(selected, candidates.bySlot, randIntn)
	return selected
}

func normalizedAvatarSlots(slots []int) []int {
	if len(slots) == 0 {
		return []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	}
	out := make([]int, 0, len(slots))
	for _, slot := range slots {
		if slot >= 0 && slot <= 9 {
			out = append(out, slot)
		}
	}
	return out
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
		if item.ID <= 0 || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) {
			continue
		}
		switch item.ItemType {
		case 30:
			creatures = append(creatures, item)
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

// PetArtifactRenderable rejects malformed item-info fallbacks and
// quest-material scripts that happen to carry an artifact equipment type.
// Those records have ErrorString names or stackable icons and are not
// loadable by the game creature manager as equipped artifacts.
func PetArtifactRenderable(item shared.EquipmentCatalogItem) bool {
	if item.ID <= 0 || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) {
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
	slots := rc.EquipSlots
	if len(slots) == 0 {
		slots = []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
	}
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
	if item.ID <= 0 || item.ItemType != slot || item.Expire || item.GrantBlocked || !shared.ClientCompatibleEquipment(item) || item.Level > level || !UsableByJob(item.UseJob, job) {
		return false
	}
	if !armorAllowed(item, ArmorMaterialsFor(job, grow)) {
		return false
	}
	return rc.EquipRarityMax <= 0 || item.Rarity >= rc.EquipRarityMin && item.Rarity <= rc.EquipRarityMax
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
	return selectAvatarSetItemsFromGroups(buildSetGroups(candidatesBySlot), minSlots, randIntn)
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
		return selectBestSetItems(groups, minSlots, randIntn)
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
	return selectSetGroup(best[safeRandIntn(randIntn, len(best))], randIntn)
}

func buildSetGroups(candidatesBySlot map[int][]shared.EquipmentCatalogItem) map[string]*setGroup {
	groups := make(map[string]*setGroup)
	for slot, candidates := range candidatesBySlot {
		for _, item := range candidates {
			for _, setKey := range itemSetKeys(item.SetKey) {
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
