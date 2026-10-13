package robotconfig

type RuntimeConfig struct {
	LevelMin                 int    `json:"level_min"`
	LevelMax                 int    `json:"level_max"`
	Jobs                     []int  `json:"jobs"`
	GrowTypes                []int  `json:"grow_types"`
	ReconcileAwakening       bool   `json:"reconcile_awakening"`
	RobotUIDStart            int    `json:"robot_uid_start"`
	RobotUIDEnd              int    `json:"robot_uid_end"`
	RobotUIDGuard            int    `json:"robot_uid_guard"`
	NameASCIIFallback        bool   `json:"name_ascii_fallback"`
	NameASCIIPrefix          string `json:"name_ascii_prefix"`
	SpawnFixed               bool   `json:"spawn_fixed"`
	SpawnVillage             int    `json:"spawn_village"`
	SpawnFallbackVillage     int    `json:"spawn_fallback_village"`
	SpawnArea                int    `json:"spawn_area"`
	SpawnXMin                int    `json:"spawn_x_min"`
	SpawnXMax                int    `json:"spawn_x_max"`
	SpawnYMin                int    `json:"spawn_y_min"`
	SpawnYMax                int    `json:"spawn_y_max"`
	MoveSpeedMin             int    `json:"move_speed_min"`
	MoveSpeedMax             int    `json:"move_speed_max"`
	MoveType                 int    `json:"move_type"`
	MoveSteps                int    `json:"move_steps"`
	MoveStepDelayMS          int    `json:"move_step_delay_ms"`
	LoginDelayMS             int    `json:"login_delay_ms"`
	ReconnectDelayMS         int    `json:"reconnect_delay_ms"`
	MaxReconnect             int    `json:"max_reconnect"`
	MaxOnlineRobots          int    `json:"max_online_robots"`
	MaxOnlinePerCommand      int    `json:"max_online_per_command"`
	OnlineDispatchIntervalMS int    `json:"online_dispatch_interval_ms"`
	OnlineConfirmTimeoutMS   int    `json:"online_confirm_timeout_ms"`
	InventoryCapacity        int    `json:"inventory_capacity"`
	EquipSlots               []int  `json:"equip_slots"`
	EquipRarityMin           int    `json:"equip_rarity_min"`
	EquipRarityMax           int    `json:"equip_rarity_max"`
	EquipIntensifyMin        int    `json:"equip_intensify_min"`
	EquipIntensifyMax        int    `json:"equip_intensify_max"`
	EquipSmithingMin         int    `json:"equip_smithing_min"`
	EquipSmithingMax         int    `json:"equip_smithing_max"`
	PreferEquipSets          bool   `json:"prefer_equip_sets"`
	EquipSetMinSlots         int    `json:"equip_set_min_slots"`
	AvatarSlots              []int  `json:"avatar_slots"`
	MinAvatarSlots           int    `json:"min_avatar_slots"`
	PreferAvatarSets         bool   `json:"prefer_avatar_sets"`
	AvatarSetMinSlots        int    `json:"avatar_set_min_slots"`
	PetEnabled               bool   `json:"pet_enabled"`
	PetProbabilityPercent    int    `json:"pet_probability_percent"`
	PetArtifactEnabled       bool   `json:"pet_artifact_enabled"`
	PetArtifactSlots         []int  `json:"pet_artifact_slots"`
	MinPetArtifactSlots      int    `json:"min_pet_artifact_slots"`
	MaxPetArtifactSlots      int    `json:"max_pet_artifact_slots"`
	// Auction house (拍卖行) behavior. The stock stage seeds each robot
	// character's bag with tradeable material stacks and a gold floor before the
	// robot logs in: the server's register gate reads the listed item from the
	// character's bound inventory record and its buy path debits the character's
	// gold stat, so neither can be produced from the session alone.
	//
	// AuctionListPerRobot is how many live listings one character keeps: zero
	// leaves the character as a pure buyer, and the register gate caps it at ten.
	AuctionEnabled                  bool   `json:"auction_enabled"`
	AuctionStockStacks              int    `json:"auction_stock_stacks"`
	AuctionStockGold                int    `json:"auction_stock_gold"`
	AuctionListPerRobot             int    `json:"auction_list_per_robot"`
	AuctionListUnitPercent          int    `json:"auction_list_unit_percent"`
	AuctionBuyEnabled               bool   `json:"auction_buy_enabled"`
	AuctionBuyUnitPercent           int    `json:"auction_buy_unit_percent"`
	AuctionBuyMaxTotal              int    `json:"auction_buy_max_total"`
	AuctionIntervalMinSec           int    `json:"auction_interval_min_sec"`
	AuctionIntervalMaxSec           int    `json:"auction_interval_max_sec"`
	StoreEquipmentPriceMin          int    `json:"store_equipment_price_min"`
	StoreEquipmentPriceMax          int    `json:"store_equipment_price_max"`
	StoreEquipmentLevelWeight       int    `json:"store_equipment_level_weight"`
	StoreEquipmentRarityWeight      int    `json:"store_equipment_rarity_weight"`
	StoreEquipmentIntensifyWeight   int    `json:"store_equipment_intensify_weight"`
	StoreMaterialPriceMin           int    `json:"store_material_price_min"`
	StoreMaterialPriceMax           int    `json:"store_material_price_max"`
	StoreEquipmentStartBox          int    `json:"store_equipment_start_box_index"`
	StoreMaterialStartBox           int    `json:"store_material_start_box_index"`
	StoreEquipmentIntensifyMin      int    `json:"store_equipment_intensify_min"`
	StoreEquipmentIntensifyMax      int    `json:"store_equipment_intensify_max"`
	StoreConfirmTimeoutSec          int    `json:"store_confirm_timeout_sec"`
	StoreEnchantRatioPercent        int    `json:"store_enchant_ratio_percent"`
	FollowAccount                   string `json:"follow_account"`
	FollowRadiusX                   int    `json:"follow_radius_x"`
	FollowRadiusY                   int    `json:"follow_radius_y"`
	ShoutDelayMS                    int    `json:"shout_delay_ms"`
	ShoutSendEnabled                bool   `json:"shout_send_enabled"`
	AutoActions                     bool   `json:"auto_actions"`
	AutoMailNotify                  bool   `json:"auto_mail_notify"`
	AutoPartyAccept                 bool   `json:"auto_party_accept"`
	AutoSystemAnnouncement          bool   `json:"auto_system_announcement"`
	AutoServerNotice                bool   `json:"auto_server_notice"`
	ServerNoticeRobotIntervalMinSec int    `json:"server_notice_robot_interval_min_sec"`
	ServerNoticeRobotIntervalMaxSec int    `json:"server_notice_robot_interval_max_sec"`
	ServerNoticeMinGapSec           int    `json:"server_notice_min_gap_sec"`
	ServerNoticeMaxGapSec           int    `json:"server_notice_max_gap_sec"`
	ServerNoticeMaxPerHour          int    `json:"server_notice_max_per_hour"`
	ServerNoticeLotteryPercent      int    `json:"server_notice_lottery_percent"`
	ServerNoticeVillage             int    `json:"server_notice_village"`
	ServerNoticeArea                int    `json:"server_notice_area"`
	AutoTargetOnlineCount           int    `json:"auto_target_online_count"`
	AutoMoveIntervalMinSec          int    `json:"auto_move_interval_min_sec"`
	AutoMoveIntervalMaxSec          int    `json:"auto_move_interval_max_sec"`
	AutoShoutIntervalMinSec         int    `json:"auto_shout_interval_min_sec"`
	AutoShoutIntervalMaxSec         int    `json:"auto_shout_interval_max_sec"`
	AutoStoreProbabilityPercent     int    `json:"auto_store_probability_percent"`
	AutoStoreIntervalMinSec         int    `json:"auto_store_interval_min_sec"`
	AutoStoreIntervalMaxSec         int    `json:"auto_store_interval_max_sec"`
	AutoStoreDurationSec            int    `json:"auto_store_duration_sec"`
	AutoStoreTickSec                int    `json:"auto_store_tick_sec"`
	AutoStoreMaxPositionTries       int    `json:"auto_store_max_position_tries"`
	AutoStoreFailCooldownSec        int    `json:"auto_store_fail_cooldown_sec"`
	AutoGamePortStableSec           int    `json:"auto_game_port_stable_sec"`
	AutoGamePortCheckTimeoutMS      int    `json:"auto_game_port_check_timeout_ms"`
	SchedulerBadRecoverSec          int    `json:"scheduler_bad_recover_sec"`
	SchedulerBadFailures            int    `json:"scheduler_bad_failures"`
	SchedulerMetricsIntervalSec     int    `json:"scheduler_metrics_interval_sec"`
	SchedulerStoreConcurrent        int    `json:"scheduler_store_concurrent"`
	SchedulerOnlineBatchSize        int    `json:"scheduler_online_batch_size"`
	SchedulerOnlineStartRate        int    `json:"scheduler_online_start_rate"`
	SchedulerOnlineFillTimeout      int    `json:"scheduler_online_fill_timeout_sec"`
	SchedulerBreakerAbnormalPct     int    `json:"scheduler_breaker_abnormal_percent"`
	SchedulerBreakerPauseSec        int    `json:"scheduler_breaker_pause_sec"`
	SchedulerBreakerReleaseBatch    int    `json:"scheduler_breaker_release_batch"`
	SchedulerBreakerFloorPct        int    `json:"scheduler_breaker_floor_percent"`
	SchedulerPortDownReleaseBatch   int    `json:"scheduler_port_down_release_batch"`
	// Online retry pacing. Every login attempt - the initial fill and each
	// actor retry - draws from the scheduler's adaptive attempt budget, so the
	// adaptive policy owns the retry rate instead of per-actor fixed delays.
	SchedulerOnlineRetryBaseMS     int `json:"scheduler_online_retry_base_ms"`
	SchedulerOnlineRetryMaxMS      int `json:"scheduler_online_retry_max_ms"`
	SchedulerOnlineRetryJitterPct  int `json:"scheduler_online_retry_jitter_percent"`
	SchedulerOnlineInFlight        int `json:"scheduler_online_in_flight"`
	SchedulerRecycleCooldownSec    int `json:"scheduler_recycle_cooldown_sec"`
	SchedulerOnlineBreakerPauseSec int `json:"scheduler_online_breaker_pause_sec"`
	SchedulerCreateBatchSize       int `json:"scheduler_create_batch_size"`
	SchedulerScaleDownBatch        int `json:"scheduler_scale_down_batch"`
	SystemActorPollMS              int `json:"system_actor_poll_ms"`
	SystemManualActionTimeoutSec   int `json:"system_manual_action_timeout_sec"`
	SystemPacketRatePerSec         int `json:"system_packet_rate_per_sec"`
}

func Clone(rc RuntimeConfig) RuntimeConfig {
	rc.Jobs = append([]int(nil), rc.Jobs...)
	rc.GrowTypes = append([]int(nil), rc.GrowTypes...)
	rc.EquipSlots = append([]int(nil), rc.EquipSlots...)
	rc.AvatarSlots = append([]int(nil), rc.AvatarSlots...)
	rc.PetArtifactSlots = append([]int(nil), rc.PetArtifactSlots...)
	return rc
}
