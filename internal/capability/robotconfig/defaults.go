package robotconfig

func Default() RuntimeConfig {
	return RuntimeConfig{
		LevelMin: 50, LevelMax: 85, Jobs: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, GrowTypes: []int{0, 1, 2},
		ReconcileAwakening: true,
		RobotUIDStart:      17000000,
		RobotUIDEnd:        17000999,
		RobotUIDGuard:      17999999,
		NameASCIIFallback:  false, NameASCIIPrefix: "twbot",
		SpawnFixed: false, SpawnVillage: 3, SpawnFallbackVillage: 1, SpawnArea: 0, SpawnXMin: 240, SpawnXMax: 1800, SpawnYMin: 180, SpawnYMax: 460,
		MoveSpeedMin: 180, MoveSpeedMax: 260, MoveType: 5, MoveSteps: 4, MoveStepDelayMS: 1200,
		LoginDelayMS: 1000, ReconnectDelayMS: 5000, MaxReconnect: 2, MaxOnlineRobots: 10000, MaxOnlinePerCommand: 1000, OnlineDispatchIntervalMS: 1000, OnlineConfirmTimeoutMS: 90000,
		InventoryCapacity: 16,
		EquipSlots:        []int{1, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}, EquipRarityMin: 0, EquipRarityMax: 5, EquipIntensifyMin: 7, EquipIntensifyMax: 10, EquipSmithingMin: 0, EquipSmithingMax: 8,
		PreferEquipSets: true, EquipSetMinSlots: 5,
		AvatarSlots: []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, MinAvatarSlots: 8, PreferAvatarSets: true, AvatarSetMinSlots: 6,
		PetEnabled: true, PetProbabilityPercent: 80, PetArtifactEnabled: true, PetArtifactSlots: []int{31, 32, 33}, MinPetArtifactSlots: 1, MaxPetArtifactSlots: 2,
		StoreEquipmentStartBox: 7, StoreMaterialStartBox: 105, StoreEquipmentIntensifyMin: 7, StoreEquipmentIntensifyMax: 13,
		StoreEquipmentPriceMin: 500000, StoreEquipmentPriceMax: 1000000, StoreMaterialPriceMin: 10, StoreMaterialPriceMax: 50,
		StoreEquipmentLevelWeight: 35, StoreEquipmentRarityWeight: 40, StoreEquipmentIntensifyWeight: 25,
		StoreConfirmTimeoutSec:   30,
		StoreEnchantRatioPercent: 30,
		AuctionEnabled:           true, AuctionStockStacks: 6, AuctionStockGold: 2000000, AuctionListPerRobot: 3,
		AuctionListUnitPercent: 130, AuctionBuyEnabled: true, AuctionBuyUnitPercent: 100, AuctionBuyMaxTotal: 500000,
		AuctionIntervalMinSec: 300, AuctionIntervalMaxSec: 900,
		MailEnabled: true, MailIntervalMinSec: 300, MailIntervalMaxSec: 900,
		FollowRadiusX: 120, FollowRadiusY: 30, ShoutDelayMS: 1000, ShoutSendEnabled: true,
		AutoActions: true, AutoMailNotify: true, AutoPartyAccept: true, AutoPartyFollow: true, AutoDungeonRevive: true, AutoChatReply: true, AutoSystemAnnouncement: true, AutoTargetOnlineCount: 20,
		AutoServerNotice:                false,
		ServerNoticeRobotIntervalMinSec: 900,
		ServerNoticeRobotIntervalMaxSec: 3600,
		ServerNoticeMinGapSec:           600,
		ServerNoticeMaxGapSec:           1200,
		ServerNoticeMaxPerHour:          10,
		ServerNoticeLotteryPercent:      60,
		ServerNoticeVillage:             -1,
		ServerNoticeArea:                -1,
		AutoMoveIntervalMinSec:          6, AutoMoveIntervalMaxSec: 18, AutoShoutIntervalMinSec: 45, AutoShoutIntervalMaxSec: 120,
		AutoEmote: true, AutoEmoteIntervalMinSec: 180, AutoEmoteIntervalMaxSec: 600, AutoEmoteIndexMin: 1, AutoEmoteIndexMax: 8,
		AutoStoreProbabilityPercent: 5, AutoStoreIntervalMinSec: 120, AutoStoreIntervalMaxSec: 180, AutoStoreDurationSec: 120, AutoStoreTickSec: 10, AutoStoreMaxPositionTries: 10, AutoStoreFailCooldownSec: 60,
		AutoGamePortStableSec: 15, AutoGamePortCheckTimeoutMS: 800,
		SchedulerBadRecoverSec: 60, SchedulerBadFailures: 3, SchedulerMetricsIntervalSec: 10, SchedulerStoreConcurrent: 30, SchedulerOnlineBatchSize: 120, SchedulerOnlineStartRate: 20, SchedulerOnlineFillTimeout: 120,
		SchedulerBreakerAbnormalPct: 30, SchedulerBreakerPauseSec: 300, SchedulerBreakerReleaseBatch: 20, SchedulerBreakerFloorPct: 70, SchedulerPortDownReleaseBatch: 20,
		SchedulerOnlineRetryBaseMS: 5000, SchedulerOnlineRetryMaxMS: 300000, SchedulerOnlineRetryJitterPct: 20, SchedulerOnlineInFlight: 12,
		SchedulerRecycleCooldownSec: 600, SchedulerOnlineBreakerPauseSec: 60, SchedulerCreateBatchSize: 10, SchedulerScaleDownBatch: 15,
		SystemActorPollMS: 3000, SystemManualActionTimeoutSec: 60, SystemPacketRatePerSec: 20,
	}
}

// Normalize clamps every configurable value into its supported range. Values
// are applied group by group in a fixed order because later groups depend on
// earlier capacity defaults (for example the target online count is clamped
// against max_online_robots).
func Normalize(rc *RuntimeConfig) {
	if rc == nil {
		return
	}
	normalizeOnlineCapacity(rc)
	normalizeMovementAndInventory(rc)
	normalizeIdentityDefaults(rc)
	normalizeEquipmentDefaults(rc)
	normalizeAvatarDefaults(rc)
	normalizePetDefaults(rc)
	normalizeAutoSchedule(rc)
	normalizeServerNotice(rc)
	normalizeGamePortProbe(rc)
	normalizeStoreSchedule(rc)
	normalizeSchedulerDefaults(rc)
	normalizeSystemDefaults(rc)
	normalizeStoreEconomy(rc)
	normalizeAuction(rc)
	normalizeMail(rc)
	normalizeSpawnDefaults(rc)
}

// Auction limits following the DNF90 auction owner: one character keeps at most
// ten active listings, and the stock pool holds at most twelve distinct stacks.
const (
	auctionMaxListingsPerCharacter = 10
	auctionStockStackLimit         = 12
)

func normalizeAuction(rc *RuntimeConfig) {
	if rc.AuctionStockStacks <= 0 {
		rc.AuctionStockStacks = 6
	}
	if rc.AuctionStockStacks > auctionStockStackLimit {
		rc.AuctionStockStacks = auctionStockStackLimit
	}
	if rc.AuctionStockGold < 0 {
		rc.AuctionStockGold = 0
	}
	if rc.AuctionStockGold > 100000000 {
		rc.AuctionStockGold = 100000000
	}
	if rc.AuctionListPerRobot < 0 {
		rc.AuctionListPerRobot = 0
	}
	// The server refuses the eleventh active listing of one character, so a
	// higher value would only burn register attempts.
	if rc.AuctionListPerRobot > auctionMaxListingsPerCharacter {
		rc.AuctionListPerRobot = auctionMaxListingsPerCharacter
	}
	if rc.AuctionListUnitPercent < 100 {
		rc.AuctionListUnitPercent = 100
	}
	if rc.AuctionListUnitPercent > 1000 {
		rc.AuctionListUnitPercent = 1000
	}
	if rc.AuctionBuyUnitPercent <= 0 {
		rc.AuctionBuyUnitPercent = 100
	}
	if rc.AuctionBuyUnitPercent > 1000 {
		rc.AuctionBuyUnitPercent = 1000
	}
	if rc.AuctionBuyMaxTotal < 0 {
		rc.AuctionBuyMaxTotal = 0
	}
	if rc.AuctionBuyMaxTotal > 400000000 {
		rc.AuctionBuyMaxTotal = 400000000
	}
	if rc.AuctionIntervalMinSec <= 0 {
		rc.AuctionIntervalMinSec = 300
	}
	if rc.AuctionIntervalMaxSec <= 0 {
		rc.AuctionIntervalMaxSec = 900
	}
	if rc.AuctionIntervalMaxSec < rc.AuctionIntervalMinSec {
		rc.AuctionIntervalMinSec, rc.AuctionIntervalMaxSec = rc.AuctionIntervalMaxSec, rc.AuctionIntervalMinSec
	}
}

// normalizeMail paces the mailbox round on the same scale as the auction round
// that feeds it: a settlement is only waiting after a listing has sold, so
// there is nothing to gain from checking faster than the board turns over.
func normalizeMail(rc *RuntimeConfig) {
	if rc.MailIntervalMinSec <= 0 {
		rc.MailIntervalMinSec = 300
	}
	if rc.MailIntervalMaxSec <= 0 {
		rc.MailIntervalMaxSec = 900
	}
	if rc.MailIntervalMaxSec < rc.MailIntervalMinSec {
		rc.MailIntervalMinSec, rc.MailIntervalMaxSec = rc.MailIntervalMaxSec, rc.MailIntervalMinSec
	}
}

func normalizeOnlineCapacity(rc *RuntimeConfig) {
	if rc.MaxOnlineRobots <= 0 {
		rc.MaxOnlineRobots = 10000
	}
	if rc.MaxOnlinePerCommand <= 0 || rc.MaxOnlinePerCommand > rc.MaxOnlineRobots {
		rc.MaxOnlinePerCommand = rc.MaxOnlineRobots
	}
	if rc.OnlineDispatchIntervalMS < 0 {
		rc.OnlineDispatchIntervalMS = 1000
	}
	if rc.OnlineConfirmTimeoutMS < 5000 {
		rc.OnlineConfirmTimeoutMS = 5000
	}
	if rc.LoginDelayMS < 1000 {
		rc.LoginDelayMS = 1000
	}
	if rc.ReconnectDelayMS < 5000 {
		rc.ReconnectDelayMS = 5000
	}
	if rc.MaxReconnect < 0 {
		rc.MaxReconnect = 0
	}
	if rc.MaxReconnect > 10 {
		rc.MaxReconnect = 10
	}
}

func normalizeMovementAndInventory(rc *RuntimeConfig) {
	if rc.ShoutDelayMS < 0 {
		rc.ShoutDelayMS = 0
	}
	if rc.MoveSteps <= 0 {
		rc.MoveSteps = 4
	}
	if rc.MoveSteps > 12 {
		rc.MoveSteps = 12
	}
	if rc.MoveStepDelayMS < 0 {
		rc.MoveStepDelayMS = 0
	}
	if rc.InventoryCapacity <= 0 {
		rc.InventoryCapacity = 16
	}
}

func normalizeIdentityDefaults(rc *RuntimeConfig) {
	if len(rc.Jobs) == 0 {
		rc.Jobs = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	}
	if len(rc.GrowTypes) == 0 {
		rc.GrowTypes = []int{0, 1, 2}
	}
	if rc.RobotUIDStart < 100000 {
		rc.RobotUIDStart = 17000000
	}
	if rc.RobotUIDEnd < rc.RobotUIDStart {
		rc.RobotUIDEnd = rc.RobotUIDStart + 999
	}
}

func normalizeEquipmentDefaults(rc *RuntimeConfig) {
	if rc.EquipRarityMax < rc.EquipRarityMin {
		rc.EquipRarityMin, rc.EquipRarityMax = rc.EquipRarityMax, rc.EquipRarityMin
	}
	if rc.EquipIntensifyMax < rc.EquipIntensifyMin {
		rc.EquipIntensifyMin, rc.EquipIntensifyMax = rc.EquipIntensifyMax, rc.EquipIntensifyMin
	}
	if rc.EquipSmithingMax < rc.EquipSmithingMin {
		rc.EquipSmithingMin, rc.EquipSmithingMax = rc.EquipSmithingMax, rc.EquipSmithingMin
	}
	if rc.EquipSetMinSlots <= 1 {
		rc.EquipSetMinSlots = 5
	}
	if rc.PreferEquipSets && rc.EquipSetMinSlots < 5 {
		rc.EquipSetMinSlots = 5
	}
	if rc.PreferEquipSets && len(rc.EquipSlots) > 0 && rc.EquipSetMinSlots > len(rc.EquipSlots) {
		rc.EquipSetMinSlots = len(rc.EquipSlots)
	}
}

func normalizeAvatarDefaults(rc *RuntimeConfig) {
	if rc.MinAvatarSlots < 0 {
		rc.MinAvatarSlots = 0
	}
	if rc.AvatarSetMinSlots <= 1 {
		rc.AvatarSetMinSlots = 6
	}
	if rc.PreferAvatarSets && rc.AvatarSetMinSlots < 6 {
		rc.AvatarSetMinSlots = 6
	}
	if rc.PreferAvatarSets && len(rc.AvatarSlots) > 0 && rc.AvatarSetMinSlots > len(rc.AvatarSlots) {
		rc.AvatarSetMinSlots = len(rc.AvatarSlots)
	}
}

func normalizePetDefaults(rc *RuntimeConfig) {
	if len(rc.PetArtifactSlots) == 0 {
		rc.PetArtifactSlots = []int{31, 32, 33}
	}
	if rc.PetProbabilityPercent < 0 {
		rc.PetProbabilityPercent = 0
	}
	if rc.PetProbabilityPercent > 100 {
		rc.PetProbabilityPercent = 100
	}
	if rc.MinPetArtifactSlots < 0 {
		rc.MinPetArtifactSlots = 0
	}
	if rc.MaxPetArtifactSlots < 0 {
		rc.MaxPetArtifactSlots = 0
	}
	if rc.MaxPetArtifactSlots > len(rc.PetArtifactSlots) {
		rc.MaxPetArtifactSlots = len(rc.PetArtifactSlots)
	}
	if rc.MaxPetArtifactSlots < rc.MinPetArtifactSlots {
		rc.MinPetArtifactSlots, rc.MaxPetArtifactSlots = rc.MaxPetArtifactSlots, rc.MinPetArtifactSlots
	}
}

func normalizeAutoSchedule(rc *RuntimeConfig) {
	if rc.AutoMoveIntervalMinSec <= 0 {
		rc.AutoMoveIntervalMinSec = 6
	}
	if rc.AutoMoveIntervalMaxSec < rc.AutoMoveIntervalMinSec {
		rc.AutoMoveIntervalMaxSec = rc.AutoMoveIntervalMinSec + 8
	}
	if rc.AutoShoutIntervalMinSec <= 0 {
		rc.AutoShoutIntervalMinSec = 45
	}
	if rc.AutoShoutIntervalMaxSec < rc.AutoShoutIntervalMinSec {
		rc.AutoShoutIntervalMaxSec = rc.AutoShoutIntervalMinSec + 60
	}
	if rc.AutoEmoteIntervalMinSec <= 0 {
		rc.AutoEmoteIntervalMinSec = 180
	}
	if rc.AutoEmoteIntervalMaxSec < rc.AutoEmoteIntervalMinSec {
		rc.AutoEmoteIntervalMaxSec = rc.AutoEmoteIntervalMinSec + 420
	}
	// The index range is cosmetic and the server stores whatever it is sent, so
	// the clamp only has to keep the draw well formed: a floor of 1 keeps the
	// neutral mood (0) out of the rotation.
	if rc.AutoEmoteIndexMin < 1 {
		rc.AutoEmoteIndexMin = 1
	}
	if rc.AutoEmoteIndexMax < rc.AutoEmoteIndexMin {
		rc.AutoEmoteIndexMax = rc.AutoEmoteIndexMin
	}
	if rc.AutoTargetOnlineCount < 0 {
		rc.AutoTargetOnlineCount = 0
	}
	if rc.AutoTargetOnlineCount > rc.MaxOnlineRobots {
		rc.AutoTargetOnlineCount = rc.MaxOnlineRobots
	}
}

// normalizeServerNotice clamps the robot-side server-notice schedule. The
// global gap and hourly cap pace the fleet even while many robots are due at
// the same time.
func normalizeServerNotice(rc *RuntimeConfig) {
	if rc.ServerNoticeLotteryPercent < 0 {
		rc.ServerNoticeLotteryPercent = 0
	}
	if rc.ServerNoticeLotteryPercent > 100 {
		rc.ServerNoticeLotteryPercent = 100
	}
	if rc.ServerNoticeRobotIntervalMinSec < 60 {
		rc.ServerNoticeRobotIntervalMinSec = 60
	}
	if rc.ServerNoticeRobotIntervalMaxSec < rc.ServerNoticeRobotIntervalMinSec {
		rc.ServerNoticeRobotIntervalMaxSec = rc.ServerNoticeRobotIntervalMinSec + 60
	}
	if rc.ServerNoticeRobotIntervalMaxSec > 86400 {
		rc.ServerNoticeRobotIntervalMaxSec = 86400
	}
	if rc.ServerNoticeMinGapSec < 30 {
		rc.ServerNoticeMinGapSec = 30
	}
	if rc.ServerNoticeMaxGapSec < rc.ServerNoticeMinGapSec {
		rc.ServerNoticeMaxGapSec = rc.ServerNoticeMinGapSec + 60
	}
	if rc.ServerNoticeMaxGapSec > 86400 {
		rc.ServerNoticeMaxGapSec = 86400
	}
	if rc.ServerNoticeMaxPerHour < 0 {
		rc.ServerNoticeMaxPerHour = 0
	}
	if rc.ServerNoticeMaxPerHour > 3600 {
		rc.ServerNoticeMaxPerHour = 3600
	}
	if rc.ServerNoticeVillage != -1 && rc.ServerNoticeVillage < 0 {
		rc.ServerNoticeVillage = -1
	}
	if rc.ServerNoticeArea < -1 {
		rc.ServerNoticeArea = -1
	}
}

func normalizeGamePortProbe(rc *RuntimeConfig) {
	if rc.AutoGamePortStableSec <= 0 {
		rc.AutoGamePortStableSec = 15
	}
	if rc.AutoGamePortStableSec > 300 {
		rc.AutoGamePortStableSec = 300
	}
	if rc.AutoGamePortCheckTimeoutMS <= 0 {
		rc.AutoGamePortCheckTimeoutMS = 800
	}
	if rc.AutoGamePortCheckTimeoutMS > 10000 {
		rc.AutoGamePortCheckTimeoutMS = 10000
	}
}

func normalizeStoreSchedule(rc *RuntimeConfig) {
	if rc.AutoStoreProbabilityPercent < 0 {
		rc.AutoStoreProbabilityPercent = 0
	}
	if rc.AutoStoreProbabilityPercent > 100 {
		rc.AutoStoreProbabilityPercent = 100
	}
	if rc.AutoStoreIntervalMinSec <= 0 {
		rc.AutoStoreIntervalMinSec = 60
	}
	if rc.AutoStoreIntervalMaxSec < rc.AutoStoreIntervalMinSec {
		rc.AutoStoreIntervalMaxSec = rc.AutoStoreIntervalMinSec + 120
	}
	if rc.AutoStoreDurationSec <= 0 {
		rc.AutoStoreDurationSec = 120
	}
	if rc.AutoStoreDurationSec < 60 {
		rc.AutoStoreDurationSec = 60
	}
	if rc.AutoStoreDurationSec > 86400 {
		rc.AutoStoreDurationSec = 86400
	}
	if rc.AutoStoreTickSec <= 0 {
		rc.AutoStoreTickSec = 10
	}
	if rc.AutoStoreTickSec > 300 {
		rc.AutoStoreTickSec = 300
	}
	if rc.AutoStoreMaxPositionTries <= 0 {
		rc.AutoStoreMaxPositionTries = 10
	}
	if rc.AutoStoreMaxPositionTries > 10000 {
		rc.AutoStoreMaxPositionTries = 10000
	}
	if rc.AutoStoreFailCooldownSec <= 0 {
		rc.AutoStoreFailCooldownSec = 60
	}
	if rc.AutoStoreFailCooldownSec > 3600 {
		rc.AutoStoreFailCooldownSec = 3600
	}
}

func normalizeSchedulerDefaults(rc *RuntimeConfig) {
	if rc.SchedulerBadRecoverSec <= 0 {
		rc.SchedulerBadRecoverSec = 60
	}
	if rc.SchedulerBadFailures <= 0 {
		rc.SchedulerBadFailures = 3
	}
	if rc.SchedulerMetricsIntervalSec <= 0 {
		rc.SchedulerMetricsIntervalSec = 10
	}
	if rc.SchedulerMetricsIntervalSec > 300 {
		rc.SchedulerMetricsIntervalSec = 300
	}
	if rc.SchedulerStoreConcurrent <= 0 {
		rc.SchedulerStoreConcurrent = 30
	}
	if rc.SchedulerOnlineBatchSize > 120 {
		rc.SchedulerOnlineBatchSize = 120
	}
	if rc.SchedulerOnlineStartRate <= 0 {
		rc.SchedulerOnlineStartRate = 20
	}
	if rc.SchedulerOnlineStartRate > 60 {
		rc.SchedulerOnlineStartRate = 60
	}
	if rc.SchedulerOnlineFillTimeout <= 0 {
		rc.SchedulerOnlineFillTimeout = 60
	}
	if rc.SchedulerBreakerAbnormalPct <= 0 {
		rc.SchedulerBreakerAbnormalPct = 30
	}
	if rc.SchedulerBreakerAbnormalPct > 100 {
		rc.SchedulerBreakerAbnormalPct = 100
	}
	if rc.SchedulerBreakerPauseSec <= 0 {
		rc.SchedulerBreakerPauseSec = 300
	}
	if rc.SchedulerBreakerPauseSec < 30 {
		rc.SchedulerBreakerPauseSec = 30
	}
	if rc.SchedulerBreakerPauseSec > 3600 {
		rc.SchedulerBreakerPauseSec = 3600
	}
	if rc.SchedulerBreakerReleaseBatch <= 0 {
		rc.SchedulerBreakerReleaseBatch = 20
	}
	if rc.SchedulerBreakerReleaseBatch > 120 {
		rc.SchedulerBreakerReleaseBatch = 120
	}
	if rc.SchedulerBreakerFloorPct < 0 {
		rc.SchedulerBreakerFloorPct = 0
	}
	if rc.SchedulerBreakerFloorPct > 100 {
		rc.SchedulerBreakerFloorPct = 100
	}
	if rc.SchedulerPortDownReleaseBatch <= 0 {
		rc.SchedulerPortDownReleaseBatch = 20
	}
	if rc.SchedulerPortDownReleaseBatch > 120 {
		rc.SchedulerPortDownReleaseBatch = 120
	}
	// Online retry pacing. The retry budget is intentionally clamped well below
	// the old 20-60/s fill rates: the mandatory login/select flow is expensive
	// and a retry must never outrun the scheduler's adaptive attempt rate.
	if rc.SchedulerOnlineRetryBaseMS < 1000 {
		rc.SchedulerOnlineRetryBaseMS = 5000
	}
	if rc.SchedulerOnlineRetryBaseMS > 60000 {
		rc.SchedulerOnlineRetryBaseMS = 60000
	}
	if rc.SchedulerOnlineRetryMaxMS < rc.SchedulerOnlineRetryBaseMS {
		rc.SchedulerOnlineRetryMaxMS = 300000
	}
	if rc.SchedulerOnlineRetryMaxMS < rc.SchedulerOnlineRetryBaseMS {
		rc.SchedulerOnlineRetryMaxMS = rc.SchedulerOnlineRetryBaseMS
	}
	if rc.SchedulerOnlineRetryMaxMS > 1800000 {
		rc.SchedulerOnlineRetryMaxMS = 1800000
	}
	if rc.SchedulerOnlineRetryJitterPct < 0 {
		rc.SchedulerOnlineRetryJitterPct = 20
	}
	if rc.SchedulerOnlineRetryJitterPct > 50 {
		rc.SchedulerOnlineRetryJitterPct = 50
	}
	if rc.SchedulerOnlineInFlight <= 0 {
		rc.SchedulerOnlineInFlight = 12
	}
	if rc.SchedulerOnlineInFlight > 64 {
		rc.SchedulerOnlineInFlight = 64
	}
	if rc.SchedulerRecycleCooldownSec <= 0 {
		rc.SchedulerRecycleCooldownSec = 600
	}
	if rc.SchedulerRecycleCooldownSec > 3600 {
		rc.SchedulerRecycleCooldownSec = 3600
	}
	if rc.SchedulerOnlineBreakerPauseSec <= 0 {
		rc.SchedulerOnlineBreakerPauseSec = 60
	}
	if rc.SchedulerOnlineBreakerPauseSec < 15 {
		rc.SchedulerOnlineBreakerPauseSec = 15
	}
	if rc.SchedulerOnlineBreakerPauseSec > 300 {
		rc.SchedulerOnlineBreakerPauseSec = 300
	}
	if rc.SchedulerCreateBatchSize <= 0 {
		rc.SchedulerCreateBatchSize = 10
	}
	if rc.SchedulerCreateBatchSize > 40 {
		rc.SchedulerCreateBatchSize = 40
	}
	if rc.SchedulerScaleDownBatch <= 0 {
		rc.SchedulerScaleDownBatch = 15
	}
	if rc.SchedulerScaleDownBatch > 50 {
		rc.SchedulerScaleDownBatch = 50
	}
}

func normalizeSystemDefaults(rc *RuntimeConfig) {
	if rc.SystemActorPollMS <= 0 {
		rc.SystemActorPollMS = 1000
	}
	if rc.SystemActorPollMS < 100 {
		rc.SystemActorPollMS = 100
	}
	if rc.SystemActorPollMS > 10000 {
		rc.SystemActorPollMS = 10000
	}
	if rc.SystemManualActionTimeoutSec <= 0 {
		rc.SystemManualActionTimeoutSec = 60
	}
	if rc.SystemManualActionTimeoutSec > 3600 {
		rc.SystemManualActionTimeoutSec = 3600
	}
	if rc.SystemPacketRatePerSec <= 0 {
		rc.SystemPacketRatePerSec = 20
	}
}

func normalizeStoreEconomy(rc *RuntimeConfig) {
	if rc.StoreEquipmentPriceMin <= 0 {
		rc.StoreEquipmentPriceMin = 500000
	}
	if rc.StoreEquipmentPriceMax <= 0 {
		rc.StoreEquipmentPriceMax = 1000000
	}
	if rc.StoreEquipmentPriceMax < rc.StoreEquipmentPriceMin {
		rc.StoreEquipmentPriceMin, rc.StoreEquipmentPriceMax = rc.StoreEquipmentPriceMax, rc.StoreEquipmentPriceMin
	}
	if rc.StoreEquipmentLevelWeight < 0 {
		rc.StoreEquipmentLevelWeight = 0
	}
	if rc.StoreEquipmentRarityWeight < 0 {
		rc.StoreEquipmentRarityWeight = 0
	}
	if rc.StoreEquipmentIntensifyWeight < 0 {
		rc.StoreEquipmentIntensifyWeight = 0
	}
	if rc.StoreEquipmentLevelWeight == 0 && rc.StoreEquipmentRarityWeight == 0 && rc.StoreEquipmentIntensifyWeight == 0 {
		rc.StoreEquipmentLevelWeight = 35
		rc.StoreEquipmentRarityWeight = 40
		rc.StoreEquipmentIntensifyWeight = 25
	}
	if rc.StoreMaterialPriceMin <= 0 {
		rc.StoreMaterialPriceMin = 10
	}
	if rc.StoreMaterialPriceMax <= 0 {
		rc.StoreMaterialPriceMax = 50
	}
	if rc.StoreMaterialPriceMax < rc.StoreMaterialPriceMin {
		rc.StoreMaterialPriceMin, rc.StoreMaterialPriceMax = rc.StoreMaterialPriceMax, rc.StoreMaterialPriceMin
	}
	if rc.StoreEquipmentStartBox < 7 || rc.StoreEquipmentStartBox > 43 {
		rc.StoreEquipmentStartBox = 7
	}
	if rc.StoreMaterialStartBox < 103 || rc.StoreMaterialStartBox > 139 {
		rc.StoreMaterialStartBox = 105
	}
	if rc.StoreEquipmentIntensifyMin < 0 {
		rc.StoreEquipmentIntensifyMin = 0
	}
	if rc.StoreEquipmentIntensifyMax < 0 {
		rc.StoreEquipmentIntensifyMax = 0
	}
	if rc.StoreEquipmentIntensifyMin > 31 {
		rc.StoreEquipmentIntensifyMin = 31
	}
	if rc.StoreEquipmentIntensifyMax > 31 {
		rc.StoreEquipmentIntensifyMax = 31
	}
	if rc.StoreEquipmentIntensifyMax < rc.StoreEquipmentIntensifyMin {
		rc.StoreEquipmentIntensifyMin, rc.StoreEquipmentIntensifyMax = rc.StoreEquipmentIntensifyMax, rc.StoreEquipmentIntensifyMin
	}
	if rc.StoreConfirmTimeoutSec <= 0 {
		rc.StoreConfirmTimeoutSec = 30
	}
	if rc.StoreConfirmTimeoutSec > 35 {
		rc.StoreConfirmTimeoutSec = 35
	}
	if rc.StoreEnchantRatioPercent < 0 {
		rc.StoreEnchantRatioPercent = 0
	}
	if rc.StoreEnchantRatioPercent > 100 {
		rc.StoreEnchantRatioPercent = 100
	}
}

func normalizeSpawnDefaults(rc *RuntimeConfig) {
	if rc.SpawnVillage < 1 {
		rc.SpawnVillage = 1
	}
}
