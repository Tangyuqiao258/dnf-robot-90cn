package robotconfig

import (
	"fmt"
	"strings"

	foundationconfig "robot/internal/foundation/config"
)

func validateRuntimeConfig(dec *foundationconfig.Decoder, rc RuntimeConfig) error {
	checkRange := func(section, key string, value, minimum, maximum int) {
		dec.Check(section, key, value >= minimum && value <= maximum, fmt.Sprintf("must be between %d and %d", minimum, maximum))
	}
	checkPositive := func(section, key string, value int) {
		dec.Check(section, key, value > 0, "must be positive")
	}
	checkOrder := func(section, minimumKey string, minimum int, maximumKey string, maximum int) {
		dec.Check(section, maximumKey, maximum >= minimum, fmt.Sprintf("must be greater than or equal to %s", minimumKey))
	}
	checkListRange := func(section, key string, values []int, minimum, maximum int) {
		for _, value := range values {
			if value < minimum || value > maximum {
				dec.Check(section, key, false, fmt.Sprintf("values must be between %d and %d", minimum, maximum))
				return
			}
		}
	}

	checkRange("create", "level_min", rc.LevelMin, 1, 255)
	checkRange("create", "level_max", rc.LevelMax, 1, 255)
	checkOrder("create", "level_min", rc.LevelMin, "level_max", rc.LevelMax)
	if _, err := ParseLevelPlan(rc.LevelPlan); err != nil {
		dec.Check("create", "level_plan", false, err.Error())
	}
	checkRange("create", "pool_size", rc.PoolSize, 0, 1000000)
	dec.Check("create", "pool_size", rc.PoolSize == 0 || rc.PoolSize <= rc.RobotUIDEnd-rc.RobotUIDStart+1, "must fit inside the robot_uid_start..robot_uid_end range")
	checkListRange("create", "jobs", rc.Jobs, 0, 255)
	// grow_types selects the awakening stage: 0 = none, 1 = first, 2 = second.
	checkListRange("create", "grow_types", rc.GrowTypes, 0, 2)
	dec.Check("create", "robot_uid_start", rc.RobotUIDStart >= 100000 && uint64(rc.RobotUIDStart) <= uint64(^uint32(0)), "must be between 100000 and 4294967295")
	dec.Check("create", "robot_uid_end", rc.RobotUIDEnd >= rc.RobotUIDStart && uint64(rc.RobotUIDEnd) <= uint64(^uint32(0)), "must be greater than or equal to robot_uid_start and at most 4294967295")
	dec.Check("create", "robot_uid_guard", rc.RobotUIDGuard == 0 || (rc.RobotUIDGuard > rc.RobotUIDEnd && uint64(rc.RobotUIDGuard) <= uint64(^uint32(0))), "must be 0 or greater than robot_uid_end and at most 4294967295")
	dec.Check("create", "name_ascii_prefix", !rc.NameASCIIFallback || strings.TrimSpace(rc.NameASCIIPrefix) != "", "must not be empty when name_ascii_fallback is true")
	checkRange("create", "inventory_capacity", rc.InventoryCapacity, 1, 255)

	checkRange("spawn", "spawn_village", rc.SpawnVillage, 1, 255)
	checkRange("spawn", "spawn_fallback_village", rc.SpawnFallbackVillage, 1, 255)
	checkRange("spawn", "spawn_area", rc.SpawnArea, -1, 255)
	checkRange("spawn", "spawn_x_min", rc.SpawnXMin, 0, 65535)
	checkRange("spawn", "spawn_x_max", rc.SpawnXMax, 0, 65535)
	checkOrder("spawn", "spawn_x_min", rc.SpawnXMin, "spawn_x_max", rc.SpawnXMax)
	checkRange("spawn", "spawn_y_min", rc.SpawnYMin, 0, 65535)
	checkRange("spawn", "spawn_y_max", rc.SpawnYMax, 0, 65535)
	checkOrder("spawn", "spawn_y_min", rc.SpawnYMin, "spawn_y_max", rc.SpawnYMax)

	checkRange("move", "move_speed_min", rc.MoveSpeedMin, 1, 65535)
	checkRange("move", "move_speed_max", rc.MoveSpeedMax, 1, 65535)
	checkOrder("move", "move_speed_min", rc.MoveSpeedMin, "move_speed_max", rc.MoveSpeedMax)
	checkRange("move", "move_type", rc.MoveType, 0, 255)
	checkRange("move", "move_steps", rc.MoveSteps, 1, 12)
	checkRange("move", "move_step_delay_ms", rc.MoveStepDelayMS, 0, 60000)

	checkRange("online", "login_delay_ms", rc.LoginDelayMS, 1000, 600000)
	checkRange("online", "reconnect_delay_ms", rc.ReconnectDelayMS, 5000, 3600000)
	checkRange("online", "max_reconnect", rc.MaxReconnect, 0, 10)
	checkRange("online", "max_online_robots", rc.MaxOnlineRobots, 1, 10000)
	dec.Check("online", "max_online_per_command", rc.MaxOnlinePerCommand >= 1 && rc.MaxOnlinePerCommand <= rc.MaxOnlineRobots, "must be positive and no greater than max_online_robots")
	checkRange("online", "online_dispatch_interval_ms", rc.OnlineDispatchIntervalMS, 0, 60000)
	checkRange("online", "online_confirm_timeout_ms", rc.OnlineConfirmTimeoutMS, 5000, 120000)

	// Equipment type 13 is an earring slot in the current client profile;
	// 1..12 are the classic weapon/title/armor/accessory types.
	checkListRange("equipment", "equip_slots", rc.EquipSlots, 1, 13)
	checkRange("equipment", "equip_rarity_min", rc.EquipRarityMin, 0, 255)
	checkRange("equipment", "equip_rarity_max", rc.EquipRarityMax, 0, 255)
	checkOrder("equipment", "equip_rarity_min", rc.EquipRarityMin, "equip_rarity_max", rc.EquipRarityMax)
	checkRange("equipment", "equip_intensify_min", rc.EquipIntensifyMin, 0, 255)
	checkRange("equipment", "equip_intensify_max", rc.EquipIntensifyMax, 0, 255)
	checkOrder("equipment", "equip_intensify_min", rc.EquipIntensifyMin, "equip_intensify_max", rc.EquipIntensifyMax)
	checkRange("equipment", "equip_smithing_min", rc.EquipSmithingMin, 0, 255)
	checkRange("equipment", "equip_smithing_max", rc.EquipSmithingMax, 0, 255)
	checkOrder("equipment", "equip_smithing_min", rc.EquipSmithingMin, "equip_smithing_max", rc.EquipSmithingMax)
	checkRange("equipment", "equip_set_min_slots", rc.EquipSetMinSlots, 2, 12)
	// The runtime normalizer clamps the requested set threshold to the number
	// of configured slots.  Keep partial configuration files loadable here:
	// the default threshold is intentionally five, while callers may select a
	// smaller slot subset for diagnostics or specialized deployments.

	checkListRange("avatar", "avatar_slots", rc.AvatarSlots, 0, 9)
	dec.Check("avatar", "min_avatar_slots", rc.MinAvatarSlots >= 0 && rc.MinAvatarSlots <= len(rc.AvatarSlots), "must be between 0 and the number of avatar_slots")
	checkRange("avatar", "avatar_set_min_slots", rc.AvatarSetMinSlots, 2, 10)
	// See the equipment threshold note above.  Normalize applies the effective
	// threshold after decoding the complete configuration.
	checkRange("pet", "pet_probability_percent", rc.PetProbabilityPercent, 0, 100)
	checkListRange("pet", "pet_artifact_slots", rc.PetArtifactSlots, 31, 33)
	checkRange("pet", "min_pet_artifact_slots", rc.MinPetArtifactSlots, 0, 3)
	checkRange("pet", "max_pet_artifact_slots", rc.MaxPetArtifactSlots, 0, 3)
	checkOrder("pet", "min_pet_artifact_slots", rc.MinPetArtifactSlots, "max_pet_artifact_slots", rc.MaxPetArtifactSlots)

	checkRange("title", "title_item", rc.TitleItem, 1, 4294967295)
	checkRange("title", "title_top_item", rc.TitleTopItem, 1, 4294967295)
	checkRange("title", "title_top_level", rc.TitleTopLevel, 1, 255)

	// A century is the ceiling: the expiry is carried as a unix second count, and
	// a term beyond that would push the stored date past any year the client can
	// render.
	checkRange("premium", "black_diamond_days", rc.BlackDiamondDays, 1, 36500)

	checkPositive("store", "store_equipment_price_min", rc.StoreEquipmentPriceMin)
	checkPositive("store", "store_equipment_price_max", rc.StoreEquipmentPriceMax)
	checkOrder("store", "store_equipment_price_min", rc.StoreEquipmentPriceMin, "store_equipment_price_max", rc.StoreEquipmentPriceMax)
	dec.Check("store", "store_equipment_price_max", int64(rc.StoreEquipmentPriceMax) <= int64(^uint32(0)), "must fit the unsigned 32-bit store protocol")
	dec.Check("store", "store_equipment_level_weight", rc.StoreEquipmentLevelWeight >= 0, "must not be negative")
	dec.Check("store", "store_equipment_rarity_weight", rc.StoreEquipmentRarityWeight >= 0, "must not be negative")
	dec.Check("store", "store_equipment_intensify_weight", rc.StoreEquipmentIntensifyWeight >= 0, "must not be negative")
	dec.Check("store", "store_equipment_level_weight", rc.StoreEquipmentLevelWeight > 0 || rc.StoreEquipmentRarityWeight > 0 || rc.StoreEquipmentIntensifyWeight > 0, "equipment price weights must not all be zero")
	checkPositive("store", "store_material_price_min", rc.StoreMaterialPriceMin)
	checkPositive("store", "store_material_price_max", rc.StoreMaterialPriceMax)
	checkOrder("store", "store_material_price_min", rc.StoreMaterialPriceMin, "store_material_price_max", rc.StoreMaterialPriceMax)
	dec.Check("store", "store_material_price_max", int64(rc.StoreMaterialPriceMax) <= int64(^uint32(0)), "must fit the unsigned 32-bit store protocol")
	checkRange("store", "store_equipment_start_box_index", rc.StoreEquipmentStartBox, 7, 43)
	checkRange("store", "store_material_start_box_index", rc.StoreMaterialStartBox, 103, 139)
	checkRange("store", "store_equipment_intensify_min", rc.StoreEquipmentIntensifyMin, 0, 31)
	checkRange("store", "store_equipment_intensify_max", rc.StoreEquipmentIntensifyMax, 0, 31)
	checkOrder("store", "store_equipment_intensify_min", rc.StoreEquipmentIntensifyMin, "store_equipment_intensify_max", rc.StoreEquipmentIntensifyMax)
	checkRange("store", "store_confirm_timeout_sec", rc.StoreConfirmTimeoutSec, 1, 35)
	checkRange("store", "store_enchant_ratio_percent", rc.StoreEnchantRatioPercent, 0, 100)

	checkRange("follow", "follow_radius_x", rc.FollowRadiusX, 1, 65535)
	checkRange("follow", "follow_radius_y", rc.FollowRadiusY, 1, 65535)
	checkRange("shout", "shout_delay_ms", rc.ShoutDelayMS, 0, 60000)

	dec.Check("auto", "auto_target_online_count", rc.AutoTargetOnlineCount >= 0 && rc.AutoTargetOnlineCount <= rc.MaxOnlineRobots, "must be between 0 and max_online_robots")
	checkRange("auto", "auto_move_interval_min_sec", rc.AutoMoveIntervalMinSec, 1, 86400)
	checkRange("auto", "auto_move_interval_max_sec", rc.AutoMoveIntervalMaxSec, 1, 86400)
	checkOrder("auto", "auto_move_interval_min_sec", rc.AutoMoveIntervalMinSec, "auto_move_interval_max_sec", rc.AutoMoveIntervalMaxSec)
	checkRange("auto", "auto_shout_interval_min_sec", rc.AutoShoutIntervalMinSec, 1, 86400)
	checkRange("auto", "auto_shout_interval_max_sec", rc.AutoShoutIntervalMaxSec, 1, 86400)
	checkOrder("auto", "auto_shout_interval_min_sec", rc.AutoShoutIntervalMinSec, "auto_shout_interval_max_sec", rc.AutoShoutIntervalMaxSec)
	checkRange("auto", "auto_store_probability_percent", rc.AutoStoreProbabilityPercent, 0, 100)
	checkRange("auto", "auto_store_interval_min_sec", rc.AutoStoreIntervalMinSec, 1, 86400)
	checkRange("auto", "auto_store_interval_max_sec", rc.AutoStoreIntervalMaxSec, 1, 86400)
	checkOrder("auto", "auto_store_interval_min_sec", rc.AutoStoreIntervalMinSec, "auto_store_interval_max_sec", rc.AutoStoreIntervalMaxSec)
	checkRange("auto", "auto_store_duration_sec", rc.AutoStoreDurationSec, 60, 86400)
	checkRange("auto", "auto_store_tick_sec", rc.AutoStoreTickSec, 1, 300)
	checkRange("auto", "auto_store_max_position_tries", rc.AutoStoreMaxPositionTries, 1, 10000)
	checkRange("auto", "auto_store_fail_cooldown_sec", rc.AutoStoreFailCooldownSec, 1, 3600)
	checkRange("auto", "auto_game_port_stable_sec", rc.AutoGamePortStableSec, 1, 300)
	checkRange("auto", "auto_game_port_check_timeout_ms", rc.AutoGamePortCheckTimeoutMS, 1, 10000)
	checkRange("auto", "server_notice_robot_interval_min_sec", rc.ServerNoticeRobotIntervalMinSec, 60, 86400)
	checkRange("auto", "server_notice_robot_interval_max_sec", rc.ServerNoticeRobotIntervalMaxSec, 60, 86400)
	checkOrder("auto", "server_notice_robot_interval_min_sec", rc.ServerNoticeRobotIntervalMinSec, "server_notice_robot_interval_max_sec", rc.ServerNoticeRobotIntervalMaxSec)
	checkRange("auto", "server_notice_min_gap_sec", rc.ServerNoticeMinGapSec, 30, 86400)
	checkRange("auto", "server_notice_max_gap_sec", rc.ServerNoticeMaxGapSec, 30, 86400)
	checkOrder("auto", "server_notice_min_gap_sec", rc.ServerNoticeMinGapSec, "server_notice_max_gap_sec", rc.ServerNoticeMaxGapSec)
	checkRange("auto", "server_notice_max_per_hour", rc.ServerNoticeMaxPerHour, 0, 3600)
	checkRange("auto", "server_notice_lottery_percent", rc.ServerNoticeLotteryPercent, 0, 100)
	checkRange("auto", "server_notice_village", rc.ServerNoticeVillage, -1, 255)
	checkRange("auto", "server_notice_area", rc.ServerNoticeArea, -1, 255)

	checkRange("scheduler", "bad_recover_sec", rc.SchedulerBadRecoverSec, 1, 86400)
	checkRange("scheduler", "bad_failures", rc.SchedulerBadFailures, 1, 100)
	checkRange("scheduler", "metrics_interval_sec", rc.SchedulerMetricsIntervalSec, 1, 300)
	dec.Check("scheduler", "store_concurrent", rc.SchedulerStoreConcurrent >= 1 && rc.SchedulerStoreConcurrent <= rc.MaxOnlineRobots, "must be positive and no greater than max_online_robots")
	checkRange("scheduler", "online_batch_size", rc.SchedulerOnlineBatchSize, 1, 120)
	checkRange("scheduler", "online_start_rate", rc.SchedulerOnlineStartRate, 1, 60)
	checkRange("scheduler", "online_fill_timeout_sec", rc.SchedulerOnlineFillTimeout, 1, 3600)
	checkRange("scheduler", "breaker_abnormal_percent", rc.SchedulerBreakerAbnormalPct, 1, 100)
	checkRange("scheduler", "breaker_pause_sec", rc.SchedulerBreakerPauseSec, 30, 3600)
	checkRange("scheduler", "breaker_release_batch", rc.SchedulerBreakerReleaseBatch, 1, 120)
	checkRange("scheduler", "breaker_floor_percent", rc.SchedulerBreakerFloorPct, 0, 100)
	checkRange("scheduler", "port_down_release_batch", rc.SchedulerPortDownReleaseBatch, 1, 120)

	checkRange("system", "actor_poll_ms", rc.SystemActorPollMS, 100, 10000)
	checkRange("system", "manual_action_timeout_sec", rc.SystemManualActionTimeoutSec, 1, 3600)
	checkRange("system", "packet_rate_per_sec", rc.SystemPacketRatePerSec, 1, 10000)

	return dec.Validate()
}
