package robotconfig

type RuntimeConfig struct {
	LevelMin  int    `json:"level_min"`
	LevelMax  int    `json:"level_max"`
	LevelPlan string `json:"level_plan"`
	PoolSize  int    `json:"pool_size"`
	// Demand (需求) policy. The fleet is no longer "the first N robots in uid
	// order": ResidentPlan names the maps a standing crowd always occupies, and
	// the robots that follow the player fill the rest of the online budget. An
	// empty plan keeps the plain behaviour, where every robot is logged in on the
	// map the spawn rules pick for it.
	ResidentPlan         string `json:"resident_plan"`
	DemandPlayerPollSec  int    `json:"demand_player_poll_sec"`
	DemandPlayerGraceSec int    `json:"demand_player_grace_sec"`
	// ForbiddenVillages names the villages no robot may enter or spawn in. They
	// are not towns the fleet is meant to visit -- the guild hall, the
	// screenshot and configuration scenes, the wedding room, the dream Seria
	// room -- and a robot standing in one is a robot in a place no player
	// expects to meet one. An empty list closes nothing.
	ForbiddenVillages []int `json:"forbidden_villages"`
	// MaxRobotsPerArea is how many robots one single area may hold as a standing
	// crowd, whatever role the map plays. Zero leaves the world's own per-map
	// rules alone.
	//
	// It is applied on top of those rules rather than instead of them, which is
	// why it also clears the density exemption: an area the operator capped at N
	// is capped at N, and a map allowed to grow past it would make the number
	// meaningless. The gathering places are the one exception -- HotAreaMaxRobots
	// covers them instead and they keep the exemption, so they stay the busiest
	// maps in the world without being uncapped.
	MaxRobotsPerArea int `json:"max_robots_per_area"`
	// HotAreaMaxRobots is how many robots a gathering place may hold: the maps a
	// village's characters land in and the maps dungeon gates open on. A player
	// stands in those rather than walking past them, so they are worth more
	// robots than an ordinary area -- but the client draws every character in
	// sight, so the number is a ceiling and not a wish. It is read on its own,
	// whether or not MaxRobotsPerArea is set, and a value that does not exceed
	// MaxRobotsPerArea leaves the gathering places under the ordinary cap. Zero
	// turns the hot cap off.
	HotAreaMaxRobots int `json:"hot_area_max_robots"`
	// CrowdTransitSlots is how many robots beyond MaxRobotsPerArea an area still
	// lets through. The fleet moves, so a couple of passers-by are gone again in
	// a moment; without the allowance a full area would turn into a wall and the
	// robots behind it would look stuck rather than busy.
	CrowdTransitSlots int `json:"crowd_transit_slots"`
	// PlayerMapDistance is how many town transitions from the player still take
	// robots; maps farther away are closed. It trades against the online target:
	// a fleet spread over a whole world is not a crowd anybody sees, but a fleet
	// penned into a few rings of maps cannot reach its own target however many
	// robots it owns. Zero turns the walk off and opens the world.
	PlayerMapDistance int `json:"player_map_distance"`
	// MinRobotsPerArea is how many robots one area wants before an empty one
	// takes its first. It trades against how much of the world the fleet can
	// cover: a fleet spread one robot deep over every map of the world never
	// looks like a crowd anywhere, while a fleet that fills one map at a time
	// leaves the maps it has not reached yet empty. Zero keeps the old
	// behaviour, where every enterable map holds a robot before any map holds
	// two.
	MinRobotsPerArea int `json:"min_robots_per_area"`
	// JunctionMaxRobots is how many robots a connector mini-map may hold: the
	// small maps that exist to link two towns and hold nothing else. They are
	// roads rather than squares, so the fleet barely touches them, but an empty
	// road is a road nobody walks. Zero makes a junction an ordinary area again.
	JunctionMaxRobots        int    `json:"junction_max_robots"`
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
	// Titles (称号). A title is the one piece of equipment whose icon the client
	// animates, so a fleet wearing one costs every nearby client more to render
	// than the same fleet wearing none. That cost is why this is a switch of its
	// own rather than another entry in EquipSlots, and why it defaults to off.
	//
	// The items are pinned by id rather than drawn from the catalog: the
	// operator names the title the whole fleet wears and the one a robot at or
	// above TitleTopLevel wears instead. The pin is deliberate -- a title says
	// something about who is wearing it, and a random draw out of the archive's
	// thousands would not.
	TitleEnabled  bool `json:"title_enabled"`
	TitleItem     int  `json:"title_item"`
	TitleTopItem  int  `json:"title_top_item"`
	TitleTopLevel int  `json:"title_top_level"`
	// TitleTierItems turns the pin into a rank: when it lists more than one id,
	// every robot below TitleTopLevel draws one of them, which is the operator's
	// "一档随机发放". An empty list keeps the single TitleItem pin.
	TitleTierItems []int `json:"title_tier_items"`
	// Premium contracts (契约/会员). These are the account-level entitlements the
	// game server keeps as an absolute expiry per premium type in the account
	// metadata table, so the fleet is granted a date rather than an item.
	//
	// Black Diamond (type 56) is the one the operator asked for: it is what
	// raises a character's daily fatigue allowance and its dungeon exp rate, and
	// the client marks the character in the scene from the same flag. Unlike a
	// title it costs a client nothing to render, so it defaults to on.
	//
	// The term is deliberately long because the grant is a date, not a
	// subscription: the reconcile extends an expiry monotonically, so a shorter
	// term would only mean the fleet is renewed more often, never that the
	// membership ends sooner.
	BlackDiamondEnabled bool `json:"black_diamond_enabled"`
	BlackDiamondDays    int  `json:"black_diamond_days"`
	// Auction house (拍卖行) behavior. The stock stage seeds each robot
	// character's bag with tradeable material stacks and a gold floor before the
	// robot logs in: the server's register gate reads the listed item from the
	// character's bound inventory record and its buy path debits the character's
	// gold stat, so neither can be produced from the session alone.
	//
	// AuctionListPerRobot is how many live listings one character keeps: zero
	// leaves the character as a pure buyer, and the register gate caps it at ten.
	AuctionEnabled         bool `json:"auction_enabled"`
	AuctionStockStacks     int  `json:"auction_stock_stacks"`
	AuctionStockGold       int  `json:"auction_stock_gold"`
	AuctionListPerRobot    int  `json:"auction_list_per_robot"`
	AuctionListUnitPercent int  `json:"auction_list_unit_percent"`
	AuctionBuyEnabled      bool `json:"auction_buy_enabled"`
	AuctionBuyUnitPercent  int  `json:"auction_buy_unit_percent"`
	AuctionBuyMaxTotal     int  `json:"auction_buy_max_total"`
	AuctionIntervalMinSec  int  `json:"auction_interval_min_sec"`
	AuctionIntervalMaxSec  int  `json:"auction_interval_max_sec"`
	// Mailbox (邮件) behavior. A settled auction reaches its parties by system
	// mail rather than in the auction reply, so a robot that never opens its
	// mailbox never receives the item it bought or the gold it sold for. The
	// round opens the mailbox, claims everything the snapshot offers and then
	// deletes the letters it drained: the server keeps claimed letters, so
	// without the delete a robot's mailbox grows for as long as it trades.
	MailEnabled                      bool   `json:"mail_enabled"`
	MailIntervalMinSec               int    `json:"mail_interval_min_sec"`
	MailIntervalMaxSec               int    `json:"mail_interval_max_sec"`
	StoreEquipmentPriceMin           int    `json:"store_equipment_price_min"`
	StoreEquipmentPriceMax           int    `json:"store_equipment_price_max"`
	StoreEquipmentLevelWeight        int    `json:"store_equipment_level_weight"`
	StoreEquipmentRarityWeight       int    `json:"store_equipment_rarity_weight"`
	StoreEquipmentIntensifyWeight    int    `json:"store_equipment_intensify_weight"`
	StoreMaterialPriceMin            int    `json:"store_material_price_min"`
	StoreMaterialPriceMax            int    `json:"store_material_price_max"`
	StoreEquipmentStartBox           int    `json:"store_equipment_start_box_index"`
	StoreMaterialStartBox            int    `json:"store_material_start_box_index"`
	StoreEquipmentIntensifyMin       int    `json:"store_equipment_intensify_min"`
	StoreEquipmentIntensifyMax       int    `json:"store_equipment_intensify_max"`
	StoreConfirmTimeoutSec           int    `json:"store_confirm_timeout_sec"`
	StoreEnchantRatioPercent         int    `json:"store_enchant_ratio_percent"`
	FollowAccount                    string `json:"follow_account"`
	FollowRadiusX                    int    `json:"follow_radius_x"`
	FollowRadiusY                    int    `json:"follow_radius_y"`
	ShoutDelayMS                     int    `json:"shout_delay_ms"`
	ShoutSendEnabled                 bool   `json:"shout_send_enabled"`
	AutoActions                      bool   `json:"auto_actions"`
	AutoMailNotify                   bool   `json:"auto_mail_notify"`
	AutoPartyAccept                  bool   `json:"auto_party_accept"`
	AutoPartyFollow                  bool   `json:"auto_party_follow"`
	AutoDungeonRevive                bool   `json:"auto_dungeon_revive"`
	AutoDungeonEnter                 bool   `json:"auto_dungeon_enter"`
	AutoDungeonEnterID               int    `json:"auto_dungeon_enter_id"`
	AutoDungeonEnterDifficulty       int    `json:"auto_dungeon_enter_difficulty"`
	AutoDungeonEnterIntervalMinSec   int    `json:"auto_dungeon_enter_interval_min_sec"`
	AutoDungeonEnterIntervalMaxSec   int    `json:"auto_dungeon_enter_interval_max_sec"`
	AutoPartyDungeon                 bool   `json:"auto_party_dungeon"`
	AutoPartyDungeonSize             int    `json:"auto_party_dungeon_size"`
	AutoPartyDungeonIntervalMinSec   int    `json:"auto_party_dungeon_interval_min_sec"`
	AutoPartyDungeonIntervalMaxSec   int    `json:"auto_party_dungeon_interval_max_sec"`
	AutoPartyDungeonDwellSec         int    `json:"auto_party_dungeon_dwell_sec"`
	AutoPartyDungeonInviteTimeoutSec int    `json:"auto_party_dungeon_invite_timeout_sec"`
	// AutoDungeonClear drives the run a party entry starts: the leader reports
	// the room's actors dead, walks the maze and settles the run instead of
	// standing in the entry room until the dwell timer leaves. It is opt-in
	// separately from entry because it moves characters through the server's
	// own maze topology, which is a property of each server's PVF.
	AutoDungeonClear bool `json:"auto_dungeon_clear"`
	// DungeonFollowCombat controls whether a party follower clears the room it
	// was moved into by reporting its actors dead (op39). It stays on by default
	// because that report is the only thing that lets a follower-driven run
	// progress. It goes off when the room is cleared by someone else on the
	// same run - a human client driving the follower's actor through the
	// server's native party placement - so the two sides do not race to report
	// the same deaths. The per-room loading acknowledgement (op37) is separate
	// and stays on either way.
	DungeonFollowCombat             bool `json:"dungeon_follow_combat"`
	AutoChatReply                   bool `json:"auto_chat_reply"`
	AutoTrade                       bool `json:"auto_trade"`
	AutoSystemAnnouncement          bool `json:"auto_system_announcement"`
	AutoServerNotice                bool `json:"auto_server_notice"`
	ServerNoticeRobotIntervalMinSec int  `json:"server_notice_robot_interval_min_sec"`
	ServerNoticeRobotIntervalMaxSec int  `json:"server_notice_robot_interval_max_sec"`
	ServerNoticeMinGapSec           int  `json:"server_notice_min_gap_sec"`
	ServerNoticeMaxGapSec           int  `json:"server_notice_max_gap_sec"`
	ServerNoticeMaxPerHour          int  `json:"server_notice_max_per_hour"`
	ServerNoticeLotteryPercent      int  `json:"server_notice_lottery_percent"`
	ServerNoticeVillage             int  `json:"server_notice_village"`
	ServerNoticeArea                int  `json:"server_notice_area"`
	AutoTargetOnlineCount           int  `json:"auto_target_online_count"`
	AutoMoveIntervalMinSec          int  `json:"auto_move_interval_min_sec"`
	AutoMoveIntervalMaxSec          int  `json:"auto_move_interval_max_sec"`
	AutoEmote                       bool `json:"auto_emote"`
	AutoEmoteIntervalMinSec         int  `json:"auto_emote_interval_min_sec"`
	AutoEmoteIntervalMaxSec         int  `json:"auto_emote_interval_max_sec"`
	AutoEmoteIndexMin               int  `json:"auto_emote_index_min"`
	AutoEmoteIndexMax               int  `json:"auto_emote_index_max"`
	AutoShoutIntervalMinSec         int  `json:"auto_shout_interval_min_sec"`
	AutoShoutIntervalMaxSec         int  `json:"auto_shout_interval_max_sec"`
	AutoStoreProbabilityPercent     int  `json:"auto_store_probability_percent"`
	AutoStoreIntervalMinSec         int  `json:"auto_store_interval_min_sec"`
	AutoStoreIntervalMaxSec         int  `json:"auto_store_interval_max_sec"`
	AutoStoreDurationSec            int  `json:"auto_store_duration_sec"`
	AutoStoreTickSec                int  `json:"auto_store_tick_sec"`
	AutoStoreMaxPositionTries       int  `json:"auto_store_max_position_tries"`
	AutoStoreFailCooldownSec        int  `json:"auto_store_fail_cooldown_sec"`
	AutoGamePortStableSec           int  `json:"auto_game_port_stable_sec"`
	AutoGamePortCheckTimeoutMS      int  `json:"auto_game_port_check_timeout_ms"`
	SchedulerBadRecoverSec          int  `json:"scheduler_bad_recover_sec"`
	SchedulerBadFailures            int  `json:"scheduler_bad_failures"`
	SchedulerMetricsIntervalSec     int  `json:"scheduler_metrics_interval_sec"`
	SchedulerStoreConcurrent        int  `json:"scheduler_store_concurrent"`
	SchedulerOnlineBatchSize        int  `json:"scheduler_online_batch_size"`
	SchedulerOnlineStartRate        int  `json:"scheduler_online_start_rate"`
	SchedulerOnlineFillTimeout      int  `json:"scheduler_online_fill_timeout_sec"`
	SchedulerBreakerAbnormalPct     int  `json:"scheduler_breaker_abnormal_percent"`
	SchedulerBreakerPauseSec        int  `json:"scheduler_breaker_pause_sec"`
	SchedulerBreakerReleaseBatch    int  `json:"scheduler_breaker_release_batch"`
	SchedulerBreakerFloorPct        int  `json:"scheduler_breaker_floor_percent"`
	SchedulerPortDownReleaseBatch   int  `json:"scheduler_port_down_release_batch"`
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
	// Offline release pacing. A robot the demand layer stops wanting is logged
	// out for real -- the session is closed and the lease released -- so the
	// sweep runs in small batches with a pause between them instead of closing
	// a whole layer at once. The pause is what keeps a re-shuffle from reading
	// as a mass disconnect to the server's login path.
	SchedulerOfflineReleaseBatch  int `json:"scheduler_offline_release_batch"`
	SchedulerOfflineReleaseGapSec int `json:"scheduler_offline_release_gap_sec"`
	SystemActorPollMS             int `json:"system_actor_poll_ms"`
	SystemManualActionTimeoutSec  int `json:"system_manual_action_timeout_sec"`
	SystemPacketRatePerSec        int `json:"system_packet_rate_per_sec"`
}

func Clone(rc RuntimeConfig) RuntimeConfig {
	rc.Jobs = append([]int(nil), rc.Jobs...)
	rc.GrowTypes = append([]int(nil), rc.GrowTypes...)
	rc.EquipSlots = append([]int(nil), rc.EquipSlots...)
	rc.AvatarSlots = append([]int(nil), rc.AvatarSlots...)
	rc.PetArtifactSlots = append([]int(nil), rc.PetArtifactSlots...)
	return rc
}
