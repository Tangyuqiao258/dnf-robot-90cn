package robotconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadFileMapsRuntimeSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot_config.ini")
	raw := `[create]
level_min = 61
level_max = 72
jobs = 1,2,3
grow_types = 0,2
reconcile_awakening = false
robot_uid_start = 18000000
robot_uid_end = 18000999
robot_uid_guard = 18999999
name_ascii_fallback = true
name_ascii_prefix = testbot
inventory_capacity = 24

[spawn]
spawn_fixed = true
spawn_village = 2
spawn_fallback_village = 3
spawn_area = 4
spawn_x_min = 10
spawn_x_max = 20
spawn_y_min = 30
spawn_y_max = 40

[move]
move_speed_min = 101
move_speed_max = 202
move_type = 6
move_steps = 7
move_step_delay_ms = 800

[online]
login_delay_ms = 1500
reconnect_delay_ms = 6000
max_reconnect = 4
max_online_robots = 321
max_online_per_command = 123
online_dispatch_interval_ms = 250
online_confirm_timeout_ms = 12000

[equipment]
equip_slots = 1,3,5
equip_rarity_min = 1
equip_rarity_max = 4
prefer_equip_sets = false
equip_set_min_slots = 3

[avatar]
avatar_slots = 0,2,4
min_avatar_slots = 3
prefer_avatar_sets = false
avatar_set_min_slots = 3

[pet]
pet_enabled = false
pet_probability_percent = 37
pet_artifact_enabled = true
pet_artifact_slots = 31,33
min_pet_artifact_slots = 1
max_pet_artifact_slots = 1

[title]
title_enabled = true
title_item = 400330105
title_top_item = 400330106
title_top_level = 85

[premium]
black_diamond_enabled = false
black_diamond_days = 999

[store]
store_equipment_price_min = 1000
store_equipment_price_max = 2000
store_equipment_level_weight = 7
store_equipment_rarity_weight = 8
store_equipment_intensify_weight = 5
store_material_price_min = 30
store_material_price_max = 40
store_equipment_start_box_index = 9
store_material_start_box_index = 107
store_equipment_intensify_min = 8
store_equipment_intensify_max = 12
store_confirm_timeout_sec = 31

[follow]
follow_account = leader
follow_radius_x = 88
follow_radius_y = 44

[shout]
shout_delay_ms = 333
shout_send_enabled = false

[auto]
auto_actions = false
auto_target_online_count = 200
auto_move_interval_min_sec = 7
auto_move_interval_max_sec = 14
auto_game_port_stable_sec = 16
auto_game_port_check_timeout_ms = 900

[scheduler]
bad_recover_sec = 70
bad_failures = 4
metrics_interval_sec = 12
store_concurrent = 15
online_batch_size = 42
online_start_rate = 17
online_fill_timeout_sec = 99
breaker_abnormal_percent = 25
breaker_pause_sec = 80
breaker_release_batch = 13
breaker_floor_percent = 60
port_down_release_batch = 11

[system]
actor_poll_ms = 777
manual_action_timeout_sec = 90
packet_rate_per_sec = 30
`
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rc.LevelMin != 61 || rc.LevelMax != 72 || rc.RobotUIDStart != 18000000 || rc.RobotUIDEnd != 18000999 || rc.RobotUIDGuard != 18999999 {
		t.Fatalf("create config not loaded: %+v", rc)
	}
	if !reflect.DeepEqual(rc.Jobs, []int{1, 2, 3}) || !reflect.DeepEqual(rc.GrowTypes, []int{0, 2}) {
		t.Fatalf("integer lists not loaded: jobs=%v grow_types=%v", rc.Jobs, rc.GrowTypes)
	}
	if !rc.NameASCIIFallback || !rc.SpawnFixed || rc.PreferEquipSets || rc.PreferAvatarSets || rc.ShoutSendEnabled || rc.AutoActions || rc.ReconcileAwakening {
		t.Fatalf("boolean settings not loaded: %+v", rc)
	}
	if rc.SpawnVillage != 2 || rc.MoveSteps != 7 || rc.MaxOnlineRobots != 321 || rc.MaxOnlinePerCommand != 123 {
		t.Fatalf("runtime sections not loaded: %+v", rc)
	}
	if !reflect.DeepEqual(rc.EquipSlots, []int{1, 3, 5}) || !reflect.DeepEqual(rc.AvatarSlots, []int{0, 2, 4}) {
		t.Fatalf("slot lists not loaded: equipment=%v avatar=%v", rc.EquipSlots, rc.AvatarSlots)
	}
	if rc.PetEnabled || rc.PetProbabilityPercent != 37 || !rc.PetArtifactEnabled || !reflect.DeepEqual(rc.PetArtifactSlots, []int{31, 33}) || rc.MinPetArtifactSlots != 1 || rc.MaxPetArtifactSlots != 1 {
		t.Fatalf("pet settings not loaded: enabled=%t probability=%d artifacts=%t slots=%v range=%d..%d", rc.PetEnabled, rc.PetProbabilityPercent, rc.PetArtifactEnabled, rc.PetArtifactSlots, rc.MinPetArtifactSlots, rc.MaxPetArtifactSlots)
	}
	if !rc.TitleEnabled || rc.TitleItem != 400330105 || rc.TitleTopItem != 400330106 || rc.TitleTopLevel != 85 {
		t.Fatalf("title settings not loaded: enabled=%t item=%d top=%d level=%d", rc.TitleEnabled, rc.TitleItem, rc.TitleTopItem, rc.TitleTopLevel)
	}
	if rc.BlackDiamondEnabled || rc.BlackDiamondDays != 999 {
		t.Fatalf("premium settings not loaded: enabled=%t days=%d", rc.BlackDiamondEnabled, rc.BlackDiamondDays)
	}
	if rc.StoreEquipmentStartBox != 9 || rc.StoreMaterialStartBox != 107 || rc.StoreEquipmentIntensifyMin != 8 || rc.StoreEquipmentIntensifyMax != 12 {
		t.Fatalf("store pool config not loaded: %+v", rc)
	}
	if rc.StoreEquipmentPriceMin != 1000 || rc.StoreEquipmentPriceMax != 2000 || rc.StoreMaterialPriceMin != 30 || rc.StoreMaterialPriceMax != 40 {
		t.Fatalf("store prices not loaded: %+v", rc)
	}
	if rc.StoreEquipmentLevelWeight != 7 || rc.StoreEquipmentRarityWeight != 8 || rc.StoreEquipmentIntensifyWeight != 5 {
		t.Fatalf("store price weights not loaded: %+v", rc)
	}
	if rc.FollowAccount != "leader" || rc.AutoTargetOnlineCount != 200 || rc.SchedulerOnlineBatchSize != 42 || rc.SystemActorPollMS != 777 {
		t.Fatalf("follow/auto/scheduler/system config not loaded: %+v", rc)
	}
}

func TestLoadFileIgnoresRemovedNativeSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot_config.ini")
	if err := os.WriteFile(path, []byte("[create]\ndefault_money = 222\ndefault_coin = 7\nlevel_min = 60\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rc, err := LoadFile(path)
	if err != nil {
		t.Fatalf("legacy native settings rejected: %v", err)
	}
	if rc.LevelMin != 60 {
		t.Fatalf("level_min=%d want 60", rc.LevelMin)
	}
}

func TestLoadFileRejectsInvalidOrUnknownSettings(t *testing.T) {
	for _, raw := range []string{
		"[auto]\nauto_target_online_count = many\n",
		"[auto]\nauto_actions = enabled\n",
		"[auto]\nauto_actions = TRUE\n",
		"[create]\njobs = 1,broken,2\n",
		"[create]\njobs = 1 2\n",
		"[create]\njobs = 1,2,1\n",
		"[create]\njobs = \n",
		"[auto]\nauto_action = true\n",
		"[unknown]\nvalue = 1\n",
		"[unknown]\n",
		"[create]\nlevel_min = 86\nlevel_max = 85\n",
		"[create]\nrobot_uid_start = 17000000\nrobot_uid_end = 17000999\nrobot_uid_guard = 17000999\n",
		"[spawn]\nspawn_x_min = 500\nspawn_x_max = 100\n",
		"[move]\nmove_type = 256\n",
		"[online]\nmax_online_robots = 20\nmax_online_per_command = 21\n",
		"[equipment]\nequip_slots = 0,1\n",
		"[avatar]\navatar_slots = 0,1\nmin_avatar_slots = 3\n",
		"[pet]\npet_probability_percent = 101\n",
		"[store]\nstore_item_allow_ids = 3037\n",
		"[store]\nstore_item_slots = 7\n",
		"[store]\nstore_equipment_intensify_min = 14\nstore_equipment_intensify_max = 13\n",
		"[store]\nstore_equipment_level_weight = 0\nstore_equipment_rarity_weight = 0\nstore_equipment_intensify_weight = 0\n",
		"[store]\nstore_equipment_level_weight = -1\n",
		"[auto]\nauto_store_probability_percent = 101\n",
		"[scheduler]\nonline_batch_size = 121\n",
		"[system]\nactor_poll_ms = 99\n",
	} {
		path := filepath.Join(t.TempDir(), "robot_config.ini")
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadFile(path); err == nil {
			t.Fatalf("invalid robot config unexpectedly loaded: %q", raw)
		}
	}
}

func TestParseEmptyConfigUsesValidDefaults(t *testing.T) {
	rc, err := Parse("")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rc, Default()) {
		t.Fatalf("empty config = %+v, want defaults %+v", rc, Default())
	}
}

func TestParseKeepsDisabledSetThresholdsWithoutRepair(t *testing.T) {
	raw := `[equipment]
equip_slots = 1
prefer_equip_sets = false
equip_set_min_slots = 2

[avatar]
avatar_slots = 0
min_avatar_slots = 1
prefer_avatar_sets = false
avatar_set_min_slots = 2
`
	rc, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	normalized := Clone(rc)
	Normalize(&normalized)
	if !reflect.DeepEqual(normalized, rc) {
		t.Fatalf("valid parsed config required normalization: parsed=%+v normalized=%+v", rc, normalized)
	}
}

func TestLoadFileKeepsDefaultsForMissingValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "robot_config.ini")
	if err := os.WriteFile(path, []byte("[create]\nlevel_min = 60\n"), 0644); err != nil {
		t.Fatal(err)
	}

	rc, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Default()
	if rc.LevelMin != 60 || rc.LevelMax != want.LevelMax || rc.RobotUIDStart != want.RobotUIDStart || rc.RobotUIDEnd != 17000999 || rc.RobotUIDGuard != 17999999 || rc.MinAvatarSlots != 8 || rc.StoreEquipmentIntensifyMin != 7 || rc.StoreEquipmentIntensifyMax != 13 {
		t.Fatalf("defaults not preserved: %+v", rc)
	}
}

func TestLoadFileReturnsReadError(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "missing.ini")); err == nil {
		t.Fatal("LoadFile() error = nil, want read error")
	}
}
