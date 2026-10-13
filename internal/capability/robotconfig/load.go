package robotconfig

import (
	"robot/internal/foundation/config"
	foundationlog "robot/internal/foundation/log"
)

// LoadFile loads the runtime robot configuration from an INI file.
func LoadFile(path string) (RuntimeConfig, error) {
	ini, err := config.Load(path)
	if err != nil {
		return RuntimeConfig{}, err
	}
	return decodeConfig(ini)
}

// Parse validates and decodes a complete runtime configuration before it is
// published to disk or memory.
func Parse(text string) (RuntimeConfig, error) {
	ini, err := config.LoadFromString(text)
	if err != nil {
		return RuntimeConfig{}, err
	}
	return decodeConfig(ini)
}

func decodeConfig(ini *config.INIConfig) (RuntimeConfig, error) {
	dec := config.NewDecoder(ini, "robot config")

	rc := Default()
	rc.LevelMin = dec.Int("create", "level_min", rc.LevelMin)
	rc.LevelMax = dec.Int("create", "level_max", rc.LevelMax)
	rc.Jobs = dec.IntList("create", "jobs", rc.Jobs)
	rc.GrowTypes = dec.IntList("create", "grow_types", rc.GrowTypes)
	rc.ReconcileAwakening = dec.Bool("create", "reconcile_awakening", rc.ReconcileAwakening)
	rc.RobotUIDStart = dec.Int("create", "robot_uid_start", rc.RobotUIDStart)
	rc.RobotUIDEnd = dec.Int("create", "robot_uid_end", rc.RobotUIDEnd)
	rc.RobotUIDGuard = dec.Int("create", "robot_uid_guard", rc.RobotUIDGuard)
	rc.NameASCIIFallback = dec.Bool("create", "name_ascii_fallback", rc.NameASCIIFallback)
	rc.NameASCIIPrefix = dec.String("create", "name_ascii_prefix", rc.NameASCIIPrefix)
	rc.InventoryCapacity = dec.Int("create", "inventory_capacity", rc.InventoryCapacity)

	// Native-backend leftovers removed from the schema. Configuration files
	// written before the removal still carry them; consume the keys so startup
	// keeps working and report them instead of failing as unknown settings.
	for _, key := range []string{"default_money", "default_coin"} {
		if dec.Obsolete("create", key) {
			foundationlog.Robotf("ROBOT_CONFIG_OBSOLETE [create] %s was removed with the native backend and is ignored\n", key)
		}
	}

	rc.SpawnFixed = dec.Bool("spawn", "spawn_fixed", rc.SpawnFixed)
	rc.SpawnVillage = dec.Int("spawn", "spawn_village", rc.SpawnVillage)
	rc.SpawnFallbackVillage = dec.Int("spawn", "spawn_fallback_village", rc.SpawnFallbackVillage)
	rc.SpawnArea = dec.Int("spawn", "spawn_area", rc.SpawnArea)
	rc.SpawnXMin = dec.Int("spawn", "spawn_x_min", rc.SpawnXMin)
	rc.SpawnXMax = dec.Int("spawn", "spawn_x_max", rc.SpawnXMax)
	rc.SpawnYMin = dec.Int("spawn", "spawn_y_min", rc.SpawnYMin)
	rc.SpawnYMax = dec.Int("spawn", "spawn_y_max", rc.SpawnYMax)

	rc.MoveSpeedMin = dec.Int("move", "move_speed_min", rc.MoveSpeedMin)
	rc.MoveSpeedMax = dec.Int("move", "move_speed_max", rc.MoveSpeedMax)
	rc.MoveType = dec.Int("move", "move_type", rc.MoveType)
	rc.MoveSteps = dec.Int("move", "move_steps", rc.MoveSteps)
	rc.MoveStepDelayMS = dec.Int("move", "move_step_delay_ms", rc.MoveStepDelayMS)

	rc.LoginDelayMS = dec.Int("online", "login_delay_ms", rc.LoginDelayMS)
	rc.ReconnectDelayMS = dec.Int("online", "reconnect_delay_ms", rc.ReconnectDelayMS)
	rc.MaxReconnect = dec.Int("online", "max_reconnect", rc.MaxReconnect)
	rc.MaxOnlineRobots = dec.Int("online", "max_online_robots", rc.MaxOnlineRobots)
	rc.MaxOnlinePerCommand = dec.Int("online", "max_online_per_command", rc.MaxOnlinePerCommand)
	rc.OnlineDispatchIntervalMS = dec.Int("online", "online_dispatch_interval_ms", rc.OnlineDispatchIntervalMS)
	rc.OnlineConfirmTimeoutMS = dec.Int("online", "online_confirm_timeout_ms", rc.OnlineConfirmTimeoutMS)

	rc.EquipSlots = dec.IntList("equipment", "equip_slots", rc.EquipSlots)
	rc.EquipRarityMin = dec.Int("equipment", "equip_rarity_min", rc.EquipRarityMin)
	rc.EquipRarityMax = dec.Int("equipment", "equip_rarity_max", rc.EquipRarityMax)
	rc.EquipIntensifyMin = dec.Int("equipment", "equip_intensify_min", rc.EquipIntensifyMin)
	rc.EquipIntensifyMax = dec.Int("equipment", "equip_intensify_max", rc.EquipIntensifyMax)
	rc.EquipSmithingMin = dec.Int("equipment", "equip_smithing_min", rc.EquipSmithingMin)
	rc.EquipSmithingMax = dec.Int("equipment", "equip_smithing_max", rc.EquipSmithingMax)
	rc.PreferEquipSets = dec.Bool("equipment", "prefer_equip_sets", rc.PreferEquipSets)
	rc.EquipSetMinSlots = dec.Int("equipment", "equip_set_min_slots", rc.EquipSetMinSlots)

	rc.AvatarSlots = dec.IntList("avatar", "avatar_slots", rc.AvatarSlots)
	rc.MinAvatarSlots = dec.Int("avatar", "min_avatar_slots", rc.MinAvatarSlots)
	rc.PreferAvatarSets = dec.Bool("avatar", "prefer_avatar_sets", rc.PreferAvatarSets)
	rc.AvatarSetMinSlots = dec.Int("avatar", "avatar_set_min_slots", rc.AvatarSetMinSlots)

	rc.PetEnabled = dec.Bool("pet", "pet_enabled", rc.PetEnabled)
	rc.PetProbabilityPercent = dec.Int("pet", "pet_probability_percent", rc.PetProbabilityPercent)
	rc.PetArtifactEnabled = dec.Bool("pet", "pet_artifact_enabled", rc.PetArtifactEnabled)
	rc.PetArtifactSlots = dec.IntList("pet", "pet_artifact_slots", rc.PetArtifactSlots)
	rc.MinPetArtifactSlots = dec.Int("pet", "min_pet_artifact_slots", rc.MinPetArtifactSlots)
	rc.MaxPetArtifactSlots = dec.Int("pet", "max_pet_artifact_slots", rc.MaxPetArtifactSlots)

	rc.AuctionEnabled = dec.Bool("auction", "auction_enabled", rc.AuctionEnabled)
	rc.AuctionStockStacks = dec.Int("auction", "auction_stock_stacks", rc.AuctionStockStacks)
	rc.AuctionStockGold = dec.Int("auction", "auction_stock_gold", rc.AuctionStockGold)
	rc.AuctionListPerRobot = dec.Int("auction", "auction_list_per_robot", rc.AuctionListPerRobot)
	rc.AuctionListUnitPercent = dec.Int("auction", "auction_list_unit_percent", rc.AuctionListUnitPercent)
	rc.AuctionBuyEnabled = dec.Bool("auction", "auction_buy_enabled", rc.AuctionBuyEnabled)
	rc.AuctionBuyUnitPercent = dec.Int("auction", "auction_buy_unit_percent", rc.AuctionBuyUnitPercent)
	rc.AuctionBuyMaxTotal = dec.Int("auction", "auction_buy_max_total", rc.AuctionBuyMaxTotal)
	rc.AuctionIntervalMinSec = dec.Int("auction", "auction_interval_min_sec", rc.AuctionIntervalMinSec)
	rc.AuctionIntervalMaxSec = dec.Int("auction", "auction_interval_max_sec", rc.AuctionIntervalMaxSec)

	rc.MailEnabled = dec.Bool("mail", "mail_enabled", rc.MailEnabled)
	rc.MailIntervalMinSec = dec.Int("mail", "mail_interval_min_sec", rc.MailIntervalMinSec)
	rc.MailIntervalMaxSec = dec.Int("mail", "mail_interval_max_sec", rc.MailIntervalMaxSec)

	rc.StoreEquipmentPriceMin = dec.Int("store", "store_equipment_price_min", rc.StoreEquipmentPriceMin)
	rc.StoreEquipmentPriceMax = dec.Int("store", "store_equipment_price_max", rc.StoreEquipmentPriceMax)
	rc.StoreEquipmentLevelWeight = dec.Int("store", "store_equipment_level_weight", rc.StoreEquipmentLevelWeight)
	rc.StoreEquipmentRarityWeight = dec.Int("store", "store_equipment_rarity_weight", rc.StoreEquipmentRarityWeight)
	rc.StoreEquipmentIntensifyWeight = dec.Int("store", "store_equipment_intensify_weight", rc.StoreEquipmentIntensifyWeight)
	rc.StoreMaterialPriceMin = dec.Int("store", "store_material_price_min", rc.StoreMaterialPriceMin)
	rc.StoreMaterialPriceMax = dec.Int("store", "store_material_price_max", rc.StoreMaterialPriceMax)
	rc.StoreEquipmentStartBox = dec.Int("store", "store_equipment_start_box_index", rc.StoreEquipmentStartBox)
	rc.StoreMaterialStartBox = dec.Int("store", "store_material_start_box_index", rc.StoreMaterialStartBox)
	rc.StoreEquipmentIntensifyMin = dec.Int("store", "store_equipment_intensify_min", rc.StoreEquipmentIntensifyMin)
	rc.StoreEquipmentIntensifyMax = dec.Int("store", "store_equipment_intensify_max", rc.StoreEquipmentIntensifyMax)
	rc.StoreConfirmTimeoutSec = dec.Int("store", "store_confirm_timeout_sec", rc.StoreConfirmTimeoutSec)
	rc.StoreEnchantRatioPercent = dec.Int("store", "store_enchant_ratio_percent", rc.StoreEnchantRatioPercent)

	rc.FollowAccount = dec.String("follow", "follow_account", rc.FollowAccount)
	rc.FollowRadiusX = dec.Int("follow", "follow_radius_x", rc.FollowRadiusX)
	rc.FollowRadiusY = dec.Int("follow", "follow_radius_y", rc.FollowRadiusY)

	rc.ShoutDelayMS = dec.Int("shout", "shout_delay_ms", rc.ShoutDelayMS)
	rc.ShoutSendEnabled = dec.Bool("shout", "shout_send_enabled", rc.ShoutSendEnabled)

	rc.AutoActions = dec.Bool("auto", "auto_actions", rc.AutoActions)
	rc.AutoMailNotify = dec.Bool("auto", "auto_mail_notify", rc.AutoMailNotify)
	rc.AutoPartyAccept = dec.Bool("auto", "auto_party_accept", rc.AutoPartyAccept)
	rc.AutoSystemAnnouncement = dec.Bool("auto", "auto_system_announcement", rc.AutoSystemAnnouncement)
	rc.AutoTargetOnlineCount = dec.Int("auto", "auto_target_online_count", rc.AutoTargetOnlineCount)
	rc.AutoMoveIntervalMinSec = dec.Int("auto", "auto_move_interval_min_sec", rc.AutoMoveIntervalMinSec)
	rc.AutoMoveIntervalMaxSec = dec.Int("auto", "auto_move_interval_max_sec", rc.AutoMoveIntervalMaxSec)
	rc.AutoShoutIntervalMinSec = dec.Int("auto", "auto_shout_interval_min_sec", rc.AutoShoutIntervalMinSec)
	rc.AutoShoutIntervalMaxSec = dec.Int("auto", "auto_shout_interval_max_sec", rc.AutoShoutIntervalMaxSec)
	rc.AutoStoreProbabilityPercent = dec.Int("auto", "auto_store_probability_percent", rc.AutoStoreProbabilityPercent)
	rc.AutoStoreIntervalMinSec = dec.Int("auto", "auto_store_interval_min_sec", rc.AutoStoreIntervalMinSec)
	rc.AutoStoreIntervalMaxSec = dec.Int("auto", "auto_store_interval_max_sec", rc.AutoStoreIntervalMaxSec)
	rc.AutoStoreDurationSec = dec.Int("auto", "auto_store_duration_sec", rc.AutoStoreDurationSec)
	rc.AutoStoreTickSec = dec.Int("auto", "auto_store_tick_sec", rc.AutoStoreTickSec)
	rc.AutoStoreMaxPositionTries = dec.Int("auto", "auto_store_max_position_tries", rc.AutoStoreMaxPositionTries)
	rc.AutoStoreFailCooldownSec = dec.Int("auto", "auto_store_fail_cooldown_sec", rc.AutoStoreFailCooldownSec)
	rc.AutoGamePortStableSec = dec.Int("auto", "auto_game_port_stable_sec", rc.AutoGamePortStableSec)
	rc.AutoGamePortCheckTimeoutMS = dec.Int("auto", "auto_game_port_check_timeout_ms", rc.AutoGamePortCheckTimeoutMS)
	rc.AutoServerNotice = dec.Bool("auto", "auto_server_notice", rc.AutoServerNotice)
	rc.ServerNoticeRobotIntervalMinSec = dec.Int("auto", "server_notice_robot_interval_min_sec", rc.ServerNoticeRobotIntervalMinSec)
	rc.ServerNoticeRobotIntervalMaxSec = dec.Int("auto", "server_notice_robot_interval_max_sec", rc.ServerNoticeRobotIntervalMaxSec)
	rc.ServerNoticeMinGapSec = dec.Int("auto", "server_notice_min_gap_sec", rc.ServerNoticeMinGapSec)
	rc.ServerNoticeMaxGapSec = dec.Int("auto", "server_notice_max_gap_sec", rc.ServerNoticeMaxGapSec)
	rc.ServerNoticeMaxPerHour = dec.Int("auto", "server_notice_max_per_hour", rc.ServerNoticeMaxPerHour)
	rc.ServerNoticeLotteryPercent = dec.Int("auto", "server_notice_lottery_percent", rc.ServerNoticeLotteryPercent)
	rc.ServerNoticeVillage = dec.Int("auto", "server_notice_village", rc.ServerNoticeVillage)
	rc.ServerNoticeArea = dec.Int("auto", "server_notice_area", rc.ServerNoticeArea)

	rc.SchedulerBadRecoverSec = dec.Int("scheduler", "bad_recover_sec", rc.SchedulerBadRecoverSec)
	rc.SchedulerBadFailures = dec.Int("scheduler", "bad_failures", rc.SchedulerBadFailures)
	rc.SchedulerMetricsIntervalSec = dec.Int("scheduler", "metrics_interval_sec", rc.SchedulerMetricsIntervalSec)
	rc.SchedulerStoreConcurrent = dec.Int("scheduler", "store_concurrent", rc.SchedulerStoreConcurrent)
	rc.SchedulerOnlineBatchSize = dec.Int("scheduler", "online_batch_size", rc.SchedulerOnlineBatchSize)
	rc.SchedulerOnlineStartRate = dec.Int("scheduler", "online_start_rate", rc.SchedulerOnlineStartRate)
	rc.SchedulerOnlineFillTimeout = dec.Int("scheduler", "online_fill_timeout_sec", rc.SchedulerOnlineFillTimeout)
	rc.SchedulerBreakerAbnormalPct = dec.Int("scheduler", "breaker_abnormal_percent", rc.SchedulerBreakerAbnormalPct)
	rc.SchedulerBreakerPauseSec = dec.Int("scheduler", "breaker_pause_sec", rc.SchedulerBreakerPauseSec)
	rc.SchedulerBreakerReleaseBatch = dec.Int("scheduler", "breaker_release_batch", rc.SchedulerBreakerReleaseBatch)
	rc.SchedulerBreakerFloorPct = dec.Int("scheduler", "breaker_floor_percent", rc.SchedulerBreakerFloorPct)
	rc.SchedulerPortDownReleaseBatch = dec.Int("scheduler", "port_down_release_batch", rc.SchedulerPortDownReleaseBatch)
	rc.SchedulerOnlineRetryBaseMS = dec.Int("scheduler", "online_retry_base_ms", rc.SchedulerOnlineRetryBaseMS)
	rc.SchedulerOnlineRetryMaxMS = dec.Int("scheduler", "online_retry_max_ms", rc.SchedulerOnlineRetryMaxMS)
	rc.SchedulerOnlineRetryJitterPct = dec.Int("scheduler", "online_retry_jitter_percent", rc.SchedulerOnlineRetryJitterPct)
	rc.SchedulerOnlineInFlight = dec.Int("scheduler", "online_in_flight", rc.SchedulerOnlineInFlight)
	rc.SchedulerRecycleCooldownSec = dec.Int("scheduler", "recycle_cooldown_sec", rc.SchedulerRecycleCooldownSec)
	rc.SchedulerOnlineBreakerPauseSec = dec.Int("scheduler", "online_breaker_pause_sec", rc.SchedulerOnlineBreakerPauseSec)
	rc.SchedulerCreateBatchSize = dec.Int("scheduler", "create_batch_size", rc.SchedulerCreateBatchSize)
	rc.SchedulerScaleDownBatch = dec.Int("scheduler", "scale_down_batch", rc.SchedulerScaleDownBatch)

	rc.SystemActorPollMS = dec.Int("system", "actor_poll_ms", rc.SystemActorPollMS)
	rc.SystemManualActionTimeoutSec = dec.Int("system", "manual_action_timeout_sec", rc.SystemManualActionTimeoutSec)
	rc.SystemPacketRatePerSec = dec.Int("system", "packet_rate_per_sec", rc.SystemPacketRatePerSec)

	if err := validateRuntimeConfig(dec, rc); err != nil {
		return RuntimeConfig{}, err
	}
	return rc, nil
}
