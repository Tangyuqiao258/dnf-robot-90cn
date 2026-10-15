package robot

import (
	"fmt"
	"strings"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/shared"
)

type CreateRequest struct {
	Count int `json:"count"`
}

type Info struct {
	UID     int    `json:"uid"`
	CID     int    `json:"cid"`
	GuildID int    `json:"guild_id"`
	Name    string `json:"name"`
	Level   int    `json:"level"`
	Job     int    `json:"job"`
	Grow    int    `json:"grow_type"`
	Port    int    `json:"port"`
	Village int    `json:"village"`
	Area    int    `json:"area"`
	X       int    `json:"x"`
	Y       int    `json:"y"`
}

type EquipmentRecord struct {
	Info Info
	Raw  []byte
}

type PositionUpdate struct {
	UID     int
	Village int
	Area    int
	X       int
	Y       int
}

type StatusItem struct {
	UID               int               `json:"uid"`
	CID               int               `json:"cid"`
	Name              string            `json:"name"`
	Account           string            `json:"account"`
	DBState           string            `json:"db_state"`
	RobotState        shared.RobotState `json:"robot_state"`
	Operation         string            `json:"operation,omitempty"`
	ActorAttached     bool              `json:"actor_attached"`
	ActorSlot         int               `json:"actor_slot,omitempty"`
	ActorState        string            `json:"actor_state,omitempty"`
	ActorBusy         bool              `json:"actor_busy,omitempty"`
	ActorBusyKind     string            `json:"actor_busy_kind,omitempty"`
	Level             int               `json:"level"`
	Job               int               `json:"job"`
	Grow              int               `json:"grow"`
	State             int               `json:"state"`
	Online            bool              `json:"online"`
	DisconnectReason  int               `json:"disconnect_reason"`
	Reconnects        int               `json:"reconnects"`
	RobotType         int               `json:"robot_type"`
	StoreDisplaySent  bool              `json:"store_display_sent"`
	StoreDisplayAck   bool              `json:"store_display_ack"`
	StoreDisplayItems int               `json:"store_display_items"`
	StoreCreated      bool              `json:"store_created"`
	DisjointActive    bool              `json:"disjoint_active"`
	EnchantActive     bool              `json:"enchant_active"`
	Village           int               `json:"village"`
	VillageName       string            `json:"village_name,omitempty"`
	Area              int               `json:"area"`
	X                 int               `json:"x"`
	Y                 int               `json:"y"`
	UptimeSeconds     int               `json:"uptime_seconds"`
	MissingCore       bool              `json:"missing_core,omitempty"`
}

const (
	DBStateExists      = "exists"
	DBStateMissingCore = "missing_core"
)

func UIDs(robots []Info) []int {
	out := make([]int, 0, len(robots))
	for _, r := range robots {
		out = append(out, r.UID)
	}
	return out
}

type CommandRequest struct {
	Count int   `json:"count"`
	UIDs  []int `json:"uids"`
}

func CommandRequestScope(req CommandRequest) string {
	if len(req.UIDs) > 0 {
		return fmt.Sprintf("uids=%d", len(req.UIDs))
	}
	return fmt.Sprintf("count=%d", req.Count)
}

type CleanupRequest struct {
	UIDs                    []int `json:"uids"`
	MinUID                  int   `json:"uid_min"`
	MaxUID                  int   `json:"uid_max"`
	Force                   bool  `json:"force"`
	InternalConfirmedBroken bool  `json:"-"`
}

func CleanupRequestScope(req CleanupRequest) string {
	if len(req.UIDs) > 0 {
		return fmt.Sprintf("uids=%d force=%v", len(req.UIDs), req.Force)
	}
	if req.MinUID > 0 || req.MaxUID > 0 {
		return fmt.Sprintf("range=%d-%d force=%v", req.MinUID, req.MaxUID, req.Force)
	}
	return fmt.Sprintf("all force=%v", req.Force)
}

type ActionResult struct {
	UID     int    `json:"uid"`
	CID     int    `json:"cid,omitempty"`
	OK      bool   `json:"ok"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
}

const (
	ActionStateAccepted        = "accepted"
	ActionStateActorFull       = "actor_full"
	ActionStateAttached        = "attached"
	ActionStateAttachedOffline = "attached_offline"
	ActionStateCancelled       = "cancelled"
	ActionStateClosed          = "closed"
	ActionStateFailed          = "failed"
	ActionStateIdle            = "idle"
	ActionStateLogoutFailed    = "logout_failed"
	ActionStateMissing         = "missing"
	ActionStateMissingActor    = "missing_actor"
	ActionStateMoved           = "moved"
	ActionStateNoticed         = "noticed"
	ActionStateNotConfirmed    = "not_confirmed"
	ActionStateNotOnline       = "not_online"
	ActionStateOffline         = "offline"
	ActionStateOnlineTimeout   = "online_timeout"
	ActionStatePending         = "pending"
	ActionStateQueueFailed     = "queue_failed"
	ActionStateRunning         = "running"
	ActionStateSchedulerBusy   = "scheduler_busy"
	ActionStateSent            = "sent"
	ActionStateStore           = "store"
	ActionStateStoreBusy       = "store_busy"
	ActionStateStoreFailed     = "store_failed"
	// ActionStateStoreNoPoint is a placement failure, not a store failure: the
	// round found no usable coordinate, so no create was ever sent and the
	// server was never asked anything. It is reported apart from
	// ActionStateStoreFailed because the two have opposite remedies -- a full
	// market needs more room, a failing store needs the server looked at -- and
	// because a round that never reached the wire says nothing about the
	// server's health.
	ActionStateStoreNoPoint       = "store_no_point"
	ActionStateStorePrepareFailed = "store_prepare_failed"
	ActionStateStoreStartFailed   = "store_start_failed"
	ActionStateStoreExpired       = "store_expired"
	ActionStateTimeout            = "timeout"
	ActionStateUIDNotAttached     = "uid_not_attached"
	ActionStateUnknownCommand     = "unknown_command"
)

type CommandResult struct {
	Requested int            `json:"requested"`
	Accepted  int            `json:"accepted"`
	Confirmed int            `json:"confirmed"`
	Failed    int            `json:"failed"`
	Robots    []ActionResult `json:"robots"`
}

func NewCommandResult(requested int) CommandResult {
	return CommandResult{Requested: requested, Robots: make([]ActionResult, 0, requested)}
}

func CommandOperationSummary(res CommandResult, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("requested=%d accepted=%d confirmed=%d failed=%d", res.Requested, res.Accepted, res.Confirmed, res.Failed)
}

type CleanupCandidate struct {
	UID          int    `json:"uid"`
	CID          int    `json:"cid"`
	Name         string `json:"name"`
	Account      string `json:"account"`
	Protected    bool   `json:"protected"`
	MetadataOnly bool   `json:"metadata_only,omitempty"`
	Reason       string `json:"reason,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
}

type CleanupResult struct {
	DryRun     bool               `json:"dry_run"`
	Requested  int                `json:"requested"`
	Candidates []CleanupCandidate `json:"candidates"`
	Deleted    int                `json:"deleted"`
	Skipped    int                `json:"skipped"`
}

const (
	DangerousDeleteModeCID   = "cid"
	DangerousDeleteModeUID   = "uid"
	DangerousDeleteModeRange = "range"
)

type DangerousDeleteRequest struct {
	Mode   string `json:"mode"`
	CID    int    `json:"cid"`
	UID    int    `json:"uid"`
	MinUID int    `json:"uid_min"`
	MaxUID int    `json:"uid_max"`
}

type DangerousDeletePlan struct {
	Mode           string `json:"mode"`
	UID            int    `json:"uid,omitempty"`
	CID            int    `json:"cid,omitempty"`
	MinUID         int    `json:"uid_min,omitempty"`
	MaxUID         int    `json:"uid_max,omitempty"`
	CharacterName  string `json:"character_name,omitempty"`
	AccountCount   int    `json:"account_count"`
	CharacterCount int    `json:"character_count"`
	RegistryCount  int    `json:"registry_count"`
	UIDs           []int  `json:"-"`
	CIDs           []int  `json:"-"`
	RegistryUIDs   []int  `json:"-"`
}

type DangerousDeleteResult struct {
	Mode           string `json:"mode"`
	UID            int    `json:"uid,omitempty"`
	CID            int    `json:"cid,omitempty"`
	MinUID         int    `json:"uid_min,omitempty"`
	MaxUID         int    `json:"uid_max,omitempty"`
	AccountCount   int    `json:"account_count"`
	CharacterCount int    `json:"character_count"`
	RegistryCount  int    `json:"registry_count"`
	Deleted        bool   `json:"deleted"`
}

type PopulationBucket struct {
	Key     string  `json:"key"`
	Count   int     `json:"count"`
	Percent float64 `json:"percent"`
}

type PopulationArea struct {
	Village     int     `json:"village"`
	VillageName string  `json:"village_name,omitempty"`
	Area        int     `json:"area"`
	Count       int     `json:"count"`
	Percent     float64 `json:"percent"`
}

type PopulationLoadout struct {
	ExpectedSlots       int     `json:"expected_slots"`
	FilledSlots         int     `json:"filled_slots"`
	SlotCoveragePercent float64 `json:"slot_coverage_percent"`
	FullCharacters      int     `json:"full_characters"`
	FullPercent         float64 `json:"full_percent"`
	SetCharacters       int     `json:"set_characters"`
	SetPercent          float64 `json:"set_percent"`
}

type PopulationReport struct {
	Accounts              int                `json:"accounts"`
	Characters            int                `json:"characters"`
	Genders               []PopulationBucket `json:"genders"`
	Jobs                  []PopulationBucket `json:"jobs"`
	Equipment             PopulationLoadout  `json:"equipment"`
	Avatars               PopulationLoadout  `json:"avatars"`
	AvailableAreas        int                `json:"available_areas"`
	OccupiedAreas         int                `json:"occupied_areas"`
	AreaCoveragePercent   float64            `json:"area_coverage_percent"`
	UnknownAreaCharacters int                `json:"unknown_area_characters"`
	Areas                 []PopulationArea   `json:"areas"`
	GeneratedAt           time.Time          `json:"generated_at"`
}

func CleanupOperationSummary(res CleanupResult, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("candidates=%d deleted=%d skipped=%d", len(res.Candidates), res.Deleted, res.Skipped)
}

func IsStructuralOperation(typ string) bool {
	switch strings.TrimSpace(typ) {
	case "create", "cleanup", "dangerous_delete":
		return true
	default:
		return false
	}
}

type AutoStatus struct {
	Enabled              bool      `json:"enabled"`
	TargetOnline         int       `json:"target_online"`
	Actors               int       `json:"actors"`
	Leased               int       `json:"leased"`
	Idle                 int       `json:"idle"`
	Recycling            int       `json:"recycling"`
	BlockedUIDs          int       `json:"blocked_uids"`
	ActorIdle            int       `json:"actor_idle"`
	ActorAssigned        int       `json:"actor_assigned"`
	ActorOnline          int       `json:"actor_online"`
	ActorRunning         int       `json:"actor_running"`
	ActorBusy            int       `json:"actor_busy"`
	ActorReleasing       int       `json:"actor_releasing"`
	Running              int       `json:"running"`
	Connecting           int       `json:"connecting"`
	GamePortReady        bool      `json:"game_port_ready"`
	GamePortAddress      string    `json:"game_port_address,omitempty"`
	GamePortStableAt     time.Time `json:"game_port_stable_at,omitempty"`
	StoreProbability     int       `json:"store_probability_percent"`
	StoreRunning         int       `json:"store_running"`
	StoreItemRunning     int       `json:"store_item_running"`
	StoreDisjointRunning int       `json:"store_disjoint_running"`
	StoreEnchantRunning  int       `json:"store_enchant_running"`
	Created              int       `json:"created"`
	OnlineSuccess        int       `json:"online_success"`
	OnlineFailed         int       `json:"online_failed"`
	MoveSuccess          int       `json:"move_success"`
	MoveFailed           int       `json:"move_failed"`
	ShoutLocalSuccess    int       `json:"shout_local_success"`
	ShoutLocalFailed     int       `json:"shout_local_failed"`
	ShoutWorldSuccess    int       `json:"shout_world_success"`
	ShoutWorldFailed     int       `json:"shout_world_failed"`
	StoreSuccess         int       `json:"store_success"`
	StoreFailed          int       `json:"store_failed"`
	StoreExpired         int       `json:"store_expired"`
	// StoreNoPoint counts the rounds that ended because no coordinate was
	// claimable. It sits outside StoreFailed so the failure rate the operator
	// reads, and the pressure rule that throttles the store rotation, describe
	// the store and not the size of the market it had to fit into.
	StoreNoPoint          int       `json:"store_no_point"`
	ServerNoticeEnabled   bool      `json:"server_notice_enabled"`
	ServerNoticeSent      int       `json:"server_notice_sent"`
	ServerNoticeBroadcast int       `json:"server_notice_broadcast"`
	ServerNoticeFailed    int       `json:"server_notice_failed"`
	UpdatedAt             time.Time `json:"updated_at"`
}

type SchedulerStatus struct {
	Mode                    string    `json:"mode"`
	Reason                  string    `json:"reason"`
	RecentOperation         string    `json:"recent_operation,omitempty"`
	RecentOperationState    string    `json:"recent_operation_state,omitempty"`
	RecentOperationSummary  string    `json:"recent_operation_summary,omitempty"`
	TargetOnline            int       `json:"target_online"`
	Running                 int       `json:"running"`
	Connecting              int       `json:"connecting"`
	Actors                  int       `json:"actors"`
	Idle                    int       `json:"idle"`
	ActorIdle               int       `json:"actor_idle"`
	ActorAssigned           int       `json:"actor_assigned"`
	ActorOnline             int       `json:"actor_online"`
	ActorRunning            int       `json:"actor_running"`
	ActorBusy               int       `json:"actor_busy"`
	ActorReleasing          int       `json:"actor_releasing"`
	StoreRunning            int       `json:"store_running"`
	StoreTarget             int       `json:"store_target"`
	GamePortReady           bool      `json:"game_port_ready"`
	BreakerActive           bool      `json:"breaker_active"`
	CPUPercent              float64   `json:"cpu_percent"`
	MemoryMB                int       `json:"memory_mb"`
	Goroutines              int       `json:"goroutines"`
	OnlineBatchSize         int       `json:"online_batch_size"`
	OnlineStartRate         int       `json:"online_start_rate"`
	OnlineFillTimeoutSec    int       `json:"online_fill_timeout_sec"`
	MoveIntervalMinSec      int       `json:"move_interval_min_sec"`
	MoveIntervalMaxSec      int       `json:"move_interval_max_sec"`
	ShoutIntervalMinSec     int       `json:"shout_interval_min_sec"`
	ShoutIntervalMaxSec     int       `json:"shout_interval_max_sec"`
	StoreConcurrent         int       `json:"store_concurrent"`
	StoreProbabilityPercent int       `json:"store_probability_percent"`
	StoreIntervalMinSec     int       `json:"store_interval_min_sec"`
	StoreIntervalMaxSec     int       `json:"store_interval_max_sec"`
	StoreDurationSec        int       `json:"store_duration_sec"`
	StoreTickSec            int       `json:"store_tick_sec"`
	StoreMaxPositionTries   int       `json:"store_max_position_tries"`
	StoreFailCooldownSec    int       `json:"store_fail_cooldown_sec"`
	ScaleUpBatch            int       `json:"scale_up_batch"`
	ScaleDownBatch          int       `json:"scale_down_batch"`
	BreakerReleaseBatch     int       `json:"breaker_release_batch"`
	PortDownReleaseBatch    int       `json:"port_down_release_batch"`
	OnlineInFlight          int       `json:"online_in_flight"`
	OnlineWindowSuccess     int       `json:"online_window_success"`
	OnlineWindowFailed      int       `json:"online_window_failed"`
	OnlineRetryBaseMS       int       `json:"online_retry_base_ms"`
	CreateBatchSize         int       `json:"create_batch_size"`
	OperationActive         bool      `json:"operation_active"`
	Operation               string    `json:"operation,omitempty"`
	OperationStartedAt      time.Time `json:"operation_started_at,omitempty"`
	UpdatedAt               time.Time `json:"updated_at"`
}

const (
	SchedulerModeBootstrap   = "bootstrap"
	SchedulerModeFill        = "fill"
	SchedulerModeStable      = "stable"
	SchedulerModeStore       = "store_expand"
	SchedulerModePressure    = "pressure"
	SchedulerModeBreaker     = "breaker"
	SchedulerModeMaintenance = "maintenance"
	SchedulerModeManual      = "manual"
)

type OperationStatus struct {
	ID         int64     `json:"id"`
	Type       string    `json:"type"`
	Scope      string    `json:"scope,omitempty"`
	State      string    `json:"state"`
	Summary    string    `json:"summary,omitempty"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
}

const (
	OperationStateRunning = "running"
	OperationStateDone    = "done"
	OperationStateFailed  = "failed"
	OperationStateUnknown = "unknown"
)

type ConfigUpdateRequest struct {
	Text    string                 `json:"text"`
	Updates map[string]interface{} `json:"updates"`
}

type ConfigResult struct {
	Path   string                    `json:"path"`
	Text   string                    `json:"text"`
	Config robotconfig.RuntimeConfig `json:"config"`
}

type RuntimeStatus = shared.RuntimeStatus

const (
	RuntimeStateStop    = shared.RuntimeStateStop
	RuntimeStateInit    = shared.RuntimeStateInit
	RuntimeStateLogin   = shared.RuntimeStateLogin
	RuntimeStateRunning = shared.RuntimeStateRunning
	RuntimeStateClean   = shared.RuntimeStateClean
	RuntimeStateWrong   = shared.RuntimeStateWrong
	RuntimeStateSelect  = shared.RuntimeStateSelect
	RuntimeStateUnknown = shared.RuntimeStateUnknown
)

func ActiveRuntimeStatus(st RuntimeStatus) bool {
	return shared.ActiveRuntimeStatus(st)
}

func CopyRuntimeStatusMap(in map[int]RuntimeStatus) map[int]RuntimeStatus {
	return shared.CopyRuntimeStatusMap(in)
}

type RuntimeStatusSummary = shared.RuntimeStatusSummary

func SummarizeRuntimeStatusMap(status map[int]RuntimeStatus) RuntimeStatusSummary {
	return shared.SummarizeRuntimeStatusMap(status)
}
