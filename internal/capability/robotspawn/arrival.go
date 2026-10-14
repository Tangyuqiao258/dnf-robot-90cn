package robotspawn

import "robot/internal/shared"

// ArrivalClearance is the radius around a map's arrival gate that placement
// keeps robots out of. A character entering an area appears on the gate, so a
// robot standing on the pad would be seen to blink into existence the moment
// the player teleports in.
const ArrivalClearance = 200

// ArrivalGate is the point inside a map where a character appears when a portal
// drops them into the area, together with the radius around it that placement
// leaves free. The zero value keeps nothing out, which is what a map that
// declares no arrival gate asks for.
type ArrivalGate struct {
	X      int
	Y      int
	Radius int
}

// ArrivalGateOf returns the arrival gate of a map. A map that declares no gate
// -- both coordinates zero, per the catalog contract -- keeps no zone free.
func ArrivalGateOf(mp shared.MapCatalogItem) ArrivalGate {
	if mp.GateX == 0 && mp.GateY == 0 {
		return ArrivalGate{}
	}
	return ArrivalGate{X: mp.GateX, Y: mp.GateY, Radius: ArrivalClearance}
}

// KeepsOut reports whether a point lies inside the zone the gate keeps free.
func (g ArrivalGate) KeepsOut(x, y int) bool {
	if g.Radius <= 0 {
		return false
	}
	dx, dy := x-g.X, y-g.Y
	return dx*dx+dy*dy < g.Radius*g.Radius
}
