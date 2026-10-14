package cn90

import (
	"context"
	"fmt"

	capabilitypvf "robot/internal/capability/pvf"
	"robot/internal/shared"
)

type TownMapCatalogProvider struct {
	PVFPath string
}

type Catalogs struct {
	TownMaps         []shared.MapCatalogItem
	Equipment        []shared.EquipmentCatalogItem
	Stackable        []shared.EquipmentCatalogItem
	JobGrows         map[int][]int
	QuestGates       capabilitypvf.QuestGates
	StatTables       map[int]capabilitypvf.CharacterStatTables
	StatFallbackJobs []int
	LevelThresholds  []int
	// Amplify holds the archive's amplification tables, read so an amplified
	// item's value is the number the game server would compute rather than a
	// constant that drifts when the archive changes.
	Amplify capabilitypvf.AmplifyConfig
}

func ReadCatalogs(pvfPath string) (Catalogs, error) {
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		return Catalogs{}, err
	}
	maps, err := capabilitypvf.ProjectTownMapCatalog(archive)
	if err != nil {
		return Catalogs{}, fmt.Errorf("project 90CN town maps: %w", err)
	}
	equipment, stackable, err := capabilitypvf.ProjectItemCatalogs(archive)
	if err != nil {
		return Catalogs{}, fmt.Errorf("project 90CN item catalogs: %w", err)
	}
	statTables, statFallbacks := capabilitypvf.ProjectCharacterStatCatalog(archive)
	return Catalogs{
		TownMaps: ApplyChannelSpawnPolicy(maps), Equipment: equipment, Stackable: stackable,
		JobGrows:         capabilitypvf.ProjectJobGrowCatalog(archive),
		QuestGates:       capabilitypvf.ProjectQuestGates(archive),
		StatTables:       statTables,
		StatFallbackJobs: statFallbacks,
		LevelThresholds:  capabilitypvf.ProjectLevelThresholds(archive),
		Amplify:          capabilitypvf.ReadAmplifyConfig(archive),
	}, nil
}

// 90CN channel policy: a normal listener refuses generic area transitions into
// the PvP town (10) and the channel-100 town (17), so robots must never spawn
// or be repaired there. Mark those catalog entries spawn-ineligible; movement
// and reporting still see the raw Use flag.
const (
	channelPolicyPvpTownID        = 10
	channelPolicyChannel100TownID = 17
)

func ApplyChannelSpawnPolicy(maps []shared.MapCatalogItem) []shared.MapCatalogItem {
	filtered := make([]shared.MapCatalogItem, len(maps))
	copy(filtered, maps)
	for index := range filtered {
		if filtered[index].Village != channelPolicyPvpTownID && filtered[index].Village != channelPolicyChannel100TownID {
			continue
		}
		ineligible := false
		filtered[index].NormalEligible = &ineligible
	}
	return filtered
}

func (p TownMapCatalogProvider) TownMapCatalog(ctx context.Context) ([]shared.MapCatalogItem, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	return ReadTownMapCatalog(p.PVFPath)
}

func ReadTownMapCatalog(pvfPath string) ([]shared.MapCatalogItem, error) {
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		return nil, err
	}
	maps, err := capabilitypvf.ProjectTownMapCatalog(archive)
	if err != nil {
		return nil, fmt.Errorf("project 90CN town maps: %w", err)
	}
	return ApplyChannelSpawnPolicy(maps), nil
}

func ReadItemCatalogs(pvfPath string) ([]shared.EquipmentCatalogItem, []shared.EquipmentCatalogItem, error) {
	archive, err := openCN90PVF(pvfPath)
	if err != nil {
		return nil, nil, err
	}
	equipment, stackable, err := capabilitypvf.ProjectItemCatalogs(archive)
	if err != nil {
		return nil, nil, fmt.Errorf("project 90CN item catalogs: %w", err)
	}
	return equipment, stackable, nil
}

var _ shared.TownMapCatalogProvider = TownMapCatalogProvider{}
