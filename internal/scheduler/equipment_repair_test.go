package scheduler

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"

	equipcap "robot/internal/capability/equipment"
	robotcap "robot/internal/capability/robot"
	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
	"robot/internal/shared"
)

type equipmentRepairTestDatabase struct {
	records []robotcap.EquipmentRecord
	saved   map[int][]byte
}

func (d *equipmentRepairTestDatabase) RobotEquipmentRecords() ([]robotcap.EquipmentRecord, error) {
	return d.records, nil
}
func (d *equipmentRepairTestDatabase) SaveEquipmentSlots(cid int, raw []byte) error {
	d.saved[cid] = append([]byte(nil), raw...)
	return nil
}

func TestRepairRobotEquipmentWritesOnlyInvalidRecords(t *testing.T) {
	configDir := t.TempDir()
	paths := layout.New(configDir)
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	items := make([]shared.EquipmentCatalogItem, 0, 12)
	for slot := 1; slot <= 12; slot++ {
		items = append(items, shared.EquipmentCatalogItem{ID: 1000 + slot, ItemType: slot, Level: 50, Rarity: 3, Durability: 20, UseJob: []int{1}})
	}
	data, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.PVFEquipment(), data, 0644); err != nil {
		t.Fatal(err)
	}
	rc := robotconfig.Default()
	raw := equipcap.BuildEquipmentSlots(items, 50, 1, 0, rc, func(int) int { return 0 }, nil)
	invalid := append([]byte(nil), raw...)
	binary.LittleEndian.PutUint16(invalid[11:13], 21)
	db := &equipmentRepairTestDatabase{
		records: []robotcap.EquipmentRecord{
			{Info: robotcap.Info{UID: 1, CID: 101, Level: 50, Job: 1}, Raw: raw},
			{Info: robotcap.Info{UID: 2, CID: 102, Level: 50, Job: 1}, Raw: invalid},
		},
		saved: make(map[int][]byte),
	}
	manager := NewRobotManager(db, &config.SysConfig{ConfigDir: configDir}, nil)
	manager.characterCacheInvalidate = func(int) error { return nil }
	result, err := manager.RepairRobotEquipment()
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 2 || result.Repaired != 1 || result.Invalidated != 1 {
		t.Fatalf("repair result=%+v, want scanned=2 repaired=1 invalidated=1", result)
	}
	if len(db.saved) != 1 || len(db.saved[102]) == 0 {
		t.Fatalf("saved records=%v, want cid 102 only", db.saved)
	}
	if equipcap.EquipmentSlotsNeedRepair(db.saved[102], equipmentCatalogByID(items), 50, 1, 0, rc) {
		t.Fatal("repaired equipment remains invalid")
	}
}
