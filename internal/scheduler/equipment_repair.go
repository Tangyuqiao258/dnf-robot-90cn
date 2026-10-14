package scheduler

import (
	"errors"
	"fmt"

	equipcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
)

type robotEquipmentRepairRepository interface {
	RobotEquipmentRecords() ([]robotcap.EquipmentRecord, error)
	SaveEquipmentSlots(cid int, raw []byte) error
}

type EquipmentRepairResult struct {
	Scanned     int
	Repaired    int
	Invalidated int
}

func (m *RobotManager) RepairRobotEquipment() (EquipmentRepairResult, error) {
	repository, ok := m.database.(robotEquipmentRepairRepository)
	if !ok {
		return EquipmentRepairResult{}, nil
	}
	rc := m.loadRobotConfig()
	items := m.loadItemCatalogs().Equipment
	if len(items) == 0 {
		return EquipmentRepairResult{}, fmt.Errorf("equipment catalog is empty")
	}
	itemsByID := equipmentCatalogByID(items)
	records, err := repository.RobotEquipmentRecords()
	if err != nil {
		return EquipmentRepairResult{}, err
	}
	result := EquipmentRepairResult{Scanned: len(records)}
	var repairErr error
	for _, record := range records {
		if !equipcap.EquipmentSlotsNeedRepair(record.Raw, itemsByID, record.Info.Level, record.Info.Job, record.Info.Grow, rc) {
			continue
		}
		raw := equipcap.BuildEquipmentSlots(items, record.Info.Level, record.Info.Job, record.Info.Grow, rc, m.randIntn, m.withRand)
		if equipcap.EquipmentSlotsNeedRepair(raw, itemsByID, record.Info.Level, record.Info.Job, record.Info.Grow, rc) {
			repairErr = errors.Join(repairErr, fmt.Errorf("cannot build valid equipment for uid=%d cid=%d level=%d job=%d", record.Info.UID, record.Info.CID, record.Info.Level, record.Info.Job))
			continue
		}
		if err := repository.SaveEquipmentSlots(record.Info.CID, raw); err != nil {
			repairErr = errors.Join(repairErr, fmt.Errorf("repair equipment uid=%d cid=%d: %w", record.Info.UID, record.Info.CID, err))
			continue
		}
		result.Repaired++
		if err := m.invalidateCharacterCache(record.Info.UID); err != nil {
			repairErr = errors.Join(repairErr, fmt.Errorf("invalidate repaired equipment uid=%d: %w", record.Info.UID, err))
			continue
		}
		result.Invalidated++
	}
	robotLogf("[EquipmentRepair] scanned=%d repaired=%d invalidated=%d\n", result.Scanned, result.Repaired, result.Invalidated)
	return result, repairErr
}
