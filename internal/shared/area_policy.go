package shared

const GuildAgitVillage = 8

// GenericAreaAllowed reports whether CMD 38 may be used for a destination.
// Guild agit entry requires an adapter-specific protocol because its logical
// area maps to a guild instance that generic town movement cannot represent.
func GenericAreaAllowed(guildID, village int) bool {
	return guildID == 0 || village != GuildAgitVillage
}

func FilterGenericAreaMaps(maps []MapCatalogItem, guildID int) []MapCatalogItem {
	if len(maps) == 0 {
		return nil
	}
	out := make([]MapCatalogItem, 0, len(maps))
	for _, mp := range maps {
		if GenericAreaAllowed(guildID, mp.Village) {
			out = append(out, mp)
		}
	}
	return out
}

// The operator's closed-village and per-area population policy lives in
// CrowdAreaPolicy (crowd.go) rather than here. A village is closed by the same
// rule that caps an area, so the world has one policy to read instead of a
// catalog rewrite plus a rule table that could disagree with each other.
