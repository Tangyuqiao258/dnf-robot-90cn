package cn90

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	equipmentcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

// SQLitePopulationInspector performs an operator-requested, read-only audit:
// account/character identity, job, gender, town coverage and the worn
// equipment/avatar coverage the Web statistics panel renders.
type SQLitePopulationInspector struct {
	DatabasePath  string
	AccountPrefix string
	Config        robotconfig.RuntimeConfig
	Maps          []shared.MapCatalogItem
	// EquipmentSets maps an item id to its PVF set key so the report can tell
	// which characters wear a matching set. Empty keys are ignored.
	EquipmentSets map[int]string
	AvatarSets    map[int]string
}

func (p SQLitePopulationInspector) PopulationReport(ctx context.Context) (robotcap.PopulationReport, error) {
	ctx, cancel := context.WithTimeout(ctx, cn90PersistenceTimeout)
	defer cancel()
	if strings.TrimSpace(p.DatabasePath) == "" || strings.TrimSpace(p.AccountPrefix) == "" {
		return robotcap.PopulationReport{}, fmt.Errorf("90CN population database path and account prefix are required")
	}
	db, err := sql.Open("sqlite", sqliteReadOnlyDSN(p.DatabasePath))
	if err != nil {
		return robotcap.PopulationReport{}, fmt.Errorf("open 90CN population database: %w", err)
	}
	defer db.Close()
	configureSQLitePool(db)
	if _, err := db.ExecContext(ctx, `PRAGMA busy_timeout=5000;`); err != nil {
		return robotcap.PopulationReport{}, fmt.Errorf("configure 90CN population database: %w", err)
	}
	characters, err := p.readCharacters(ctx, db)
	if err != nil {
		return robotcap.PopulationReport{}, err
	}
	accounts, err := p.readAccountCount(ctx, db)
	if err != nil {
		return robotcap.PopulationReport{}, err
	}
	report := p.summarize(accounts, characters)
	if err := p.measureLoadouts(ctx, db, &report); err != nil {
		return robotcap.PopulationReport{}, err
	}
	return report, nil
}

type populationCharacter struct {
	job     int
	level   int
	grow    int
	village int
	area    int
}

func (p SQLitePopulationInspector) readAccountCount(ctx context.Context, db *sql.DB) (int, error) {
	var accounts int
	query := `SELECT COUNT(*) FROM ` + dnfCharactersTable +
		` WHERE account_id LIKE ? AND delete_flag=0`
	if err := db.QueryRowContext(ctx, query, p.AccountPrefix+"%").Scan(&accounts); err != nil {
		return 0, fmt.Errorf("count 90CN population accounts: %w", err)
	}
	return accounts, nil
}

func (p SQLitePopulationInspector) readCharacters(ctx context.Context, db *sql.DB) ([]populationCharacter, error) {
	query := `SELECT job, level, grow_type, town_id, area_id FROM ` + dnfCharactersTable +
		` WHERE account_id LIKE ? AND delete_flag=0`
	rows, err := db.QueryContext(ctx, query, p.AccountPrefix+"%")
	if err != nil {
		return nil, fmt.Errorf("read 90CN population characters: %w", err)
	}
	defer rows.Close()
	characters := make([]populationCharacter, 0, 64)
	for rows.Next() {
		var jobText string
		var character populationCharacter
		if err := rows.Scan(&jobText, &character.level, &character.grow, &character.village, &character.area); err != nil {
			return nil, fmt.Errorf("scan 90CN population character: %w", err)
		}
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(jobText)); parseErr == nil {
			character.job = parsed
		}
		characters = append(characters, character)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate 90CN population characters: %w", err)
	}
	return characters, nil
}

func (p SQLitePopulationInspector) summarize(accounts int, characters []populationCharacter) robotcap.PopulationReport {
	report := robotcap.PopulationReport{Accounts: accounts, GeneratedAt: time.Now().UTC()}
	jobs := make(map[int]int)
	genders := make(map[string]int)
	areas := make(map[shared.MapAreaKey]int)
	for _, character := range characters {
		report.Characters++
		jobs[character.job]++
		genders[cn90JobGender(character.job)]++
		areas[shared.MapAreaKey{Village: character.village, Area: character.area}]++
	}
	report.Genders = genderBuckets(genders, report.Characters)
	report.Jobs = jobBuckets(jobs, report.Characters)
	p.measureAreas(&report, areas)
	return report
}

func cn90JobGender(job int) string {
	switch job {
	case 0, 1, 2, 3, 4, 9:
		return "male"
	case 5, 6, 7, 8, 10:
		return "female"
	default:
		return "unknown"
	}
}

func genderBuckets(counts map[string]int, total int) []robotcap.PopulationBucket {
	result := make([]robotcap.PopulationBucket, 0, 3)
	for _, key := range []string{"male", "female", "unknown"} {
		if count := counts[key]; count > 0 {
			result = append(result, robotcap.PopulationBucket{Key: key, Count: count, Percent: percent(count, total)})
		}
	}
	return result
}

func jobBuckets(counts map[int]int, total int) []robotcap.PopulationBucket {
	keys := make([]int, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Ints(keys)
	result := make([]robotcap.PopulationBucket, 0, len(keys))
	for _, key := range keys {
		result = append(result, robotcap.PopulationBucket{Key: strconv.Itoa(key), Count: counts[key], Percent: percent(counts[key], total)})
	}
	return result
}

func (p SQLitePopulationInspector) measureAreas(report *robotcap.PopulationReport, counts map[shared.MapAreaKey]int) {
	available := make(map[shared.MapAreaKey]shared.MapCatalogItem)
	for _, item := range p.Maps {
		if item.Use {
			available[shared.MapAreaKey{Village: item.Village, Area: item.Area}] = item
		}
	}
	report.AvailableAreas = len(available)
	for key, count := range counts {
		item, known := available[key]
		if known {
			report.OccupiedAreas++
		} else {
			report.UnknownAreaCharacters += count
		}
		report.Areas = append(report.Areas, robotcap.PopulationArea{
			Village: key.Village, VillageName: item.VillageName, Area: key.Area,
			Count: count, Percent: percent(count, report.Characters),
		})
	}
	report.AreaCoveragePercent = percent(report.OccupiedAreas, report.AvailableAreas)
	sort.Slice(report.Areas, func(i, j int) bool {
		if report.Areas[i].Count != report.Areas[j].Count {
			return report.Areas[i].Count > report.Areas[j].Count
		}
		if report.Areas[i].Village != report.Areas[j].Village {
			return report.Areas[i].Village < report.Areas[j].Village
		}
		return report.Areas[i].Area < report.Areas[j].Area
	})
}

func percent(value, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(value) * 100 / float64(total)
}

// measureLoadouts fills the equipment/avatar coverage the Web statistics panel
// renders: how many configured slots exist, how many worn rows fill them, how
// many characters are complete and how many wear a matching PVF set.
func (p SQLitePopulationInspector) measureLoadouts(ctx context.Context, db *sql.DB, report *robotcap.PopulationReport) error {
	expectedWorn := p.expectedWornSlots()
	expectedAvatars := p.expectedAvatarSlots()
	if len(expectedWorn) == 0 && len(expectedAvatars) == 0 {
		return nil
	}
	query := `SELECT e.character_id, e.slot_index, e.item_id FROM ` + dnfEquipmentEntriesTable + ` e JOIN ` + dnfCharactersTable +
		` c ON c.character_id = e.character_id WHERE c.account_id LIKE ? AND c.delete_flag=0`
	rows, err := db.QueryContext(ctx, query, p.AccountPrefix+"%")
	if err != nil {
		return fmt.Errorf("read 90CN population loadout: %w", err)
	}
	defer rows.Close()
	type loadout struct {
		worn    map[int]int
		avatars map[int]int
	}
	byCharacter := make(map[string]*loadout, 64)
	for rows.Next() {
		var characterID string
		var slot, itemID int
		if err := rows.Scan(&characterID, &slot, &itemID); err != nil {
			return fmt.Errorf("scan 90CN population loadout: %w", err)
		}
		entry, ok := byCharacter[characterID]
		if !ok {
			entry = &loadout{worn: make(map[int]int, 16), avatars: make(map[int]int, 10)}
			byCharacter[characterID] = entry
		}
		if _, wanted := expectedWorn[slot]; wanted {
			entry.worn[slot] = itemID
			continue
		}
		if _, wanted := expectedAvatars[slot]; wanted {
			entry.avatars[slot] = itemID
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate 90CN population loadout: %w", err)
	}
	equipment := robotcap.PopulationLoadout{ExpectedSlots: len(expectedWorn) * report.Characters}
	avatars := robotcap.PopulationLoadout{ExpectedSlots: len(expectedAvatars) * report.Characters}
	for _, entry := range byCharacter {
		equipment.FilledSlots += len(entry.worn)
		avatars.FilledSlots += len(entry.avatars)
		if len(entry.worn) == len(expectedWorn) {
			equipment.FullCharacters++
		}
		if len(entry.avatars) == len(expectedAvatars) {
			avatars.FullCharacters++
		}
		if hasMatchingSet(entry.worn, p.EquipmentSets, p.Config.EquipSetMinSlots) {
			equipment.SetCharacters++
		}
		if hasMatchingSet(entry.avatars, p.AvatarSets, p.Config.AvatarSetMinSlots) {
			avatars.SetCharacters++
		}
	}
	equipment.SlotCoveragePercent = percent(equipment.FilledSlots, equipment.ExpectedSlots)
	equipment.FullPercent = percent(equipment.FullCharacters, report.Characters)
	equipment.SetPercent = percent(equipment.SetCharacters, report.Characters)
	avatars.SlotCoveragePercent = percent(avatars.FilledSlots, avatars.ExpectedSlots)
	avatars.FullPercent = percent(avatars.FullCharacters, report.Characters)
	avatars.SetPercent = percent(avatars.SetCharacters, report.Characters)
	report.Equipment = equipment
	report.Avatars = avatars
	return nil
}

func (p SQLitePopulationInspector) expectedWornSlots() map[int]struct{} {
	expected := make(map[int]struct{}, 12)
	for _, itemType := range p.Config.EquipSlots {
		if slot, ok := cn90WornSlotForItemType[itemType]; ok {
			expected[slot] = struct{}{}
		}
	}
	if len(expected) == 0 {
		for _, slot := range cn90WornSlotForItemType {
			expected[slot] = struct{}{}
		}
	}
	return expected
}

func (p SQLitePopulationInspector) expectedAvatarSlots() map[int]struct{} {
	// The same slot list selection uses, so the panel cannot report a robot as
	// incomplete over a slot — 皮肤 — that is deliberately never filled.
	slots := equipmentcap.AvatarSlotsInUse(p.Config.AvatarSlots)
	expected := make(map[int]struct{}, len(slots))
	for _, slot := range slots {
		expected[slot] = struct{}{}
	}
	return expected
}

// hasMatchingSet reports whether one set key covers at least minSlots of the
// worn item ids.
func hasMatchingSet(items map[int]int, sets map[int]string, minSlots int) bool {
	if len(sets) == 0 || len(items) == 0 {
		return false
	}
	if minSlots <= 0 {
		minSlots = 2
	}
	counts := make(map[string]int, 4)
	for _, itemID := range items {
		key := strings.TrimSpace(sets[itemID])
		if key == "" {
			continue
		}
		counts[key]++
	}
	for _, count := range counts {
		if count >= minSlots {
			return true
		}
	}
	return false
}
