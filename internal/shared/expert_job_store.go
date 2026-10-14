package shared

// ExpertJobStoreKind identifies an expert-job stall kind in shared
// orchestration. Wire kind bytes stay in the adapter's protocol package.
type ExpertJobStoreKind int

const (
	ExpertJobStoreNone ExpertJobStoreKind = iota
	ExpertJobStoreDisjoint
	ExpertJobStoreEnchant
)

// Name returns a stable lowercase name for logs and API payloads.
func (k ExpertJobStoreKind) Name() string {
	switch k {
	case ExpertJobStoreDisjoint:
		return "disjoint"
	case ExpertJobStoreEnchant:
		return "enchant"
	default:
		return "none"
	}
}

// ExpertJobStoreKindForUID picks the expert-job trade a robot holds from its uid
// and the configured enchanting share. A character holds one profession for
// life, so both the store cycle and the placement that stations a robot have to
// read the same choice from the same inputs.
func ExpertJobStoreKindForUID(uid, enchantRatioPercent int) ExpertJobStoreKind {
	if enchantRatioPercent <= 0 {
		return ExpertJobStoreDisjoint
	}
	if enchantRatioPercent >= 100 {
		return ExpertJobStoreEnchant
	}
	if uid > 0 && uid%100 < enchantRatioPercent {
		return ExpertJobStoreEnchant
	}
	return ExpertJobStoreDisjoint
}
