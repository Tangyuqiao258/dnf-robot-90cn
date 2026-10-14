package cn90

import (
	"context"
	"testing"

	robotconfig "robot/internal/capability/robotconfig"
)

func TestPopulationReportMeasuresLoadouts(t *testing.T) {
	path := newEquipmentTestDatabase(t)
	db := openPurgeTestDatabase(t, path)
	defer db.Close()
	if _, err := db.Exec(`
INSERT INTO dnf_equipment_entries(character_id, entry_key, slot_index, item_id, bind_flag, raw_entry) VALUES
 ('9','12',12,101,0,NULL),
 ('9','14',14,102,0,NULL),
 ('9','0',0,201,0,NULL),
 ('9','1',1,202,0,NULL),
 ('20','12',12,103,0,NULL);
INSERT INTO dnf_characters(character_id, account_id, slot, name, job, level, delete_flag, tutorial_completed) VALUES ('20','robot17000009',1,'测试乙','2',70,0,1);`); err != nil {
		t.Fatal(err)
	}
	config := robotconfig.Default()
	config.EquipSlots = []int{1, 3}
	config.AvatarSlots = []int{0, 1}
	config.EquipSetMinSlots = 2
	config.AvatarSetMinSlots = 2
	inspector := SQLitePopulationInspector{
		DatabasePath: path, AccountPrefix: "robot", Config: config,
		EquipmentSets: map[int]string{101: "set_a", 102: "set_a"},
	}
	report, err := inspector.PopulationReport(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if report.Characters != 2 {
		t.Fatalf("characters = %d", report.Characters)
	}
	if report.Equipment.ExpectedSlots != 4 || report.Equipment.FilledSlots != 3 {
		t.Fatalf("equipment slots = %+v", report.Equipment)
	}
	if report.Equipment.FullCharacters != 1 || report.Equipment.SetCharacters != 1 {
		t.Fatalf("equipment coverage = %+v", report.Equipment)
	}
	if report.Equipment.SlotCoveragePercent != 75 {
		t.Fatalf("equipment coverage percent = %v", report.Equipment.SlotCoveragePercent)
	}
	if report.Avatars.ExpectedSlots != 4 || report.Avatars.FilledSlots != 2 || report.Avatars.FullCharacters != 1 {
		t.Fatalf("avatar coverage = %+v", report.Avatars)
	}
	if report.Avatars.SetCharacters != 0 {
		t.Fatalf("avatar set characters = %d", report.Avatars.SetCharacters)
	}
}
