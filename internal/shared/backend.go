package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type BackendID string

type BackendCapability string

const (
	CapabilityProvision          BackendCapability = "provision"
	CapabilityTownMove           BackendCapability = "town_move"
	CapabilityDungeonMove        BackendCapability = "dungeon_move"
	CapabilityDungeonFollow      BackendCapability = "dungeon_follow"
	CapabilityDungeonRevive      BackendCapability = "dungeon_revive"
	CapabilityShout              BackendCapability = "shout"
	CapabilityWorldShout         BackendCapability = "world_shout"
	CapabilityStore              BackendCapability = "store"
	CapabilityParty              BackendCapability = "party"
	CapabilityGuildInvite        BackendCapability = "guild_invite"
	CapabilityPartyDebug         BackendCapability = "party_debug"
	CapabilitySkill              BackendCapability = "skill"
	CapabilityEmote              BackendCapability = "emote"
	CapabilityMarket             BackendCapability = "market"
	CapabilityTrade              BackendCapability = "trade"
	CapabilityCleanup            BackendCapability = "cleanup"
	CapabilityDangerousDelete    BackendCapability = "dangerous_delete"
	CapabilityMailNotification   BackendCapability = "mail_notification"
	CapabilityDatabase           BackendCapability = "database"
	CapabilityDiagnostics        BackendCapability = "diagnostics"
	CapabilitySystemAnnouncement BackendCapability = "system_announcement"
	CapabilityServerNotice       BackendCapability = "server_notice"
	CapabilityServiceControl     BackendCapability = "service_control"
)

// ShoutModeDirected marks a backend whose CapabilityShout is enabled for
// messages that name their target rather than for broadcast. Nothing can be
// shouted at "everyone nearby" on such a backend, so a caller holding a
// target-less intent has to be refused instead of having a target invented for
// it. Backends that broadcast leave the mode empty.
const ShoutModeDirected = "directed"

type CapabilityStatus struct {
	Enabled bool   `json:"enabled"`
	Reason  string `json:"reason,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type BackendInfo struct {
	ID           BackendID                              `json:"id"`
	DisplayName  string                                 `json:"display_name"`
	SupportedOS  []string                               `json:"supported_os"`
	Selectable   bool                                   `json:"selectable"`
	Reason       string                                 `json:"reason,omitempty"`
	Capabilities map[BackendCapability]CapabilityStatus `json:"capabilities"`
	Settings     []BackendSetting                       `json:"settings,omitempty"`
	MaxOnline    int                                    `json:"max_online,omitempty"`
}

type BackendSetting struct {
	Key           string   `json:"key"`
	Label         string   `json:"label"`
	LabelZH       string   `json:"label_zh,omitempty"`
	Hint          string   `json:"hint,omitempty"`
	HintZH        string   `json:"hint_zh,omitempty"`
	InputType     string   `json:"input_type"`
	Required      bool     `json:"required"`
	Placeholder   string   `json:"placeholder,omitempty"`
	Default       string   `json:"default,omitempty"`
	RuntimeSource string   `json:"runtime_source,omitempty"`
	DerivedFrom   string   `json:"derived_from,omitempty"`
	PathSuffix    []string `json:"path_suffix,omitempty"`
}

type BackendSelection struct {
	BackendID        BackendID         `json:"backend_id"`
	ConfigGeneration uint64            `json:"config_generation"`
	SelectedAt       time.Time         `json:"selected_at"`
	Settings         map[string]string `json:"settings,omitempty"`
}

type ProvisionCharacterRequest struct {
	AccountName   string
	PasswordHash  string
	CharacterName string
	Job           int
	// RobotUID is robot-owned metadata and is not serialized into a backend
	// protocol packet. It keeps a successful protocol result linked to the
	// scheduler's local robot record.
	RobotUID int
}

type ProvisionCharacterResult struct {
	Backend       BackendID
	CharacterName string
	Created       bool
	// Reused reports that the backend account already contained this character.
	// Callers must preserve its server-owned profile and loadout.
	Reused   bool
	RobotUID int
	// BackendSlot is meaningful for backends whose roster is slot-based.
	// UID/CID remain unset when the adapter does not expose persistent IDs.
	BackendSlot  *uint16
	ProfileKnown bool
	Job          int
	Grow         int
	Level        int
}

type CharacterProvisioner interface {
	ProvisionCharacter(context.Context, ProvisionCharacterRequest) (ProvisionCharacterResult, error)
}

// BatchCharacterProvisioner is optional. Callers can use it when a backend
// can provision several characters through its own protocol workflow while
// preserving partial results on interruption.
type BatchCharacterProvisioner interface {
	ProvisionCharacters(context.Context, []ProvisionCharacterRequest) ([]ProvisionCharacterResult, error)
}

func DecodeBackendSelection(data []byte) (BackendSelection, error) {
	selection := BackendSelection{}
	if len(data) == 0 {
		return selection, fmt.Errorf("empty backend selection")
	}
	if err := json.Unmarshal(data, &selection); err != nil {
		return BackendSelection{}, fmt.Errorf("invalid backend selection: %w", err)
	}
	if selection.BackendID == "" {
		return BackendSelection{}, fmt.Errorf("backend selection has empty backend_id")
	}
	return selection, nil
}

// allCapabilities is the catalog: every capability a backend may declare. It is
// the seed of CapabilityMatrix, so a backend that only overrides the entries it
// implements still answers every question about itself instead of leaving a
// capability absent -- an absent key reads as disabled, which hides a missing
// seed behind a plausible answer. The architecture test holds this list to the
// declared Capability* constants, so a new capability cannot be introduced
// without also being seeded here.
var allCapabilities = []BackendCapability{
	CapabilityProvision, CapabilityTownMove, CapabilityDungeonMove, CapabilityDungeonFollow,
	CapabilityDungeonRevive,
	CapabilityShout, CapabilityWorldShout, CapabilityStore, CapabilityParty, CapabilityGuildInvite,
	CapabilityPartyDebug, CapabilitySkill, CapabilityEmote,
	CapabilityMarket, CapabilityTrade, CapabilityCleanup, CapabilityDangerousDelete,
	CapabilityMailNotification,
	CapabilityDatabase, CapabilityDiagnostics,
	CapabilitySystemAnnouncement, CapabilityServerNotice, CapabilityServiceControl,
}

// AllCapabilities returns the capability catalog. Callers that need to reason
// about the whole set get a copy, so no caller can empty the catalog for the
// others.
func AllCapabilities() []BackendCapability {
	return append([]BackendCapability(nil), allCapabilities...)
}

func CapabilityMatrix(status CapabilityStatus) map[BackendCapability]CapabilityStatus {
	capabilities := make(map[BackendCapability]CapabilityStatus, len(allCapabilities))
	for _, capability := range allCapabilities {
		capabilities[capability] = status
	}
	return capabilities
}

func (b BackendInfo) Supports(capability BackendCapability) bool {
	return b.Capabilities[capability].Enabled
}

const CodeBackendCapabilityUnsupported = "backend_capability_unsupported"

type UnsupportedCapabilityError struct {
	Backend   BackendID
	Operation BackendCapability
	Reason    string
}

func (e UnsupportedCapabilityError) Error() string {
	return fmt.Sprintf("%s: backend=%s operation=%s: %s", CodeBackendCapabilityUnsupported, e.Backend, e.Operation, e.Reason)
}

func (b BackendInfo) Require(capability BackendCapability) error {
	if b.Supports(capability) {
		return nil
	}
	status := b.Capabilities[capability]
	if status.Reason == "" {
		status.Reason = "operation is not implemented by this backend"
	}
	return UnsupportedCapabilityError{Backend: b.ID, Operation: capability, Reason: status.Reason}
}
