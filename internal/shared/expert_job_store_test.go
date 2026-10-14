package shared

import "testing"

func TestExpertJobStoreKindForUIDSplitsByConfiguredShare(t *testing.T) {
	cases := []struct {
		name    string
		uid     int
		ratio   int
		want    ExpertJobStoreKind
		comment string
	}{
		{name: "share off", uid: 17001019, ratio: 0, want: ExpertJobStoreDisjoint, comment: "no enchanting share means every robot disassembles"},
		{name: "share full", uid: 17001099, ratio: 100, want: ExpertJobStoreEnchant, comment: "a full share leaves no disassembler"},
		{name: "below share", uid: 17001018, ratio: 19, want: ExpertJobStoreEnchant, comment: "uid%100=18 < 19"},
		{name: "at share", uid: 17001019, ratio: 19, want: ExpertJobStoreDisjoint, comment: "uid%100=19 is the first robot past the share"},
		{name: "above share", uid: 17001020, ratio: 19, want: ExpertJobStoreDisjoint},
		{name: "unknown uid", uid: 0, ratio: 19, want: ExpertJobStoreDisjoint, comment: "a uid the hash cannot place stays a disassembler"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExpertJobStoreKindForUID(tc.uid, tc.ratio); got != tc.want {
				t.Fatalf("ExpertJobStoreKindForUID(%d, %d) = %s, want %s", tc.uid, tc.ratio, got.Name(), tc.want.Name())
			}
		})
	}
}

// TestExpertJobStoreKindForUIDFillsNineteenSeriaExits pins the live fleet shape:
// the world has 19 Seria exits and 81 dungeon entrances, so a 100-robot fleet
// has to split 19/81 or some market never opens.
func TestExpertJobStoreKindForUIDFillsNineteenSeriaExits(t *testing.T) {
	const (
		uidStart     = 17001000
		fleetSize    = 100
		wantEnchant  = 19
		enchantRatio = 19
	)
	enchants, disassembles := 0, 0
	for index := 0; index < fleetSize; index++ {
		switch ExpertJobStoreKindForUID(uidStart+index, enchantRatio) {
		case ExpertJobStoreEnchant:
			enchants++
		case ExpertJobStoreDisjoint:
			disassembles++
		}
	}
	if enchants != wantEnchant || disassembles != fleetSize-wantEnchant {
		t.Fatalf("fleet split %d enchant / %d disjoint, want %d / %d", enchants, disassembles, wantEnchant, fleetSize-wantEnchant)
	}
}
