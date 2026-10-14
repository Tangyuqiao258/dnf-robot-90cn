package robotspawn

import (
	"testing"

	"robot/internal/shared"
)

// sequenceRandom walks a fixed list of values so a test can steer a draw to the
// point it wants. The point draw reads one area weight and then an X and a Y,
// so a repeating (weight, x, y) sequence names each candidate in turn.
type sequenceRandom struct {
	values []int
	index  int
}

func (r *sequenceRandom) RandBetween(min, max int) int {
	value := r.values[r.index%len(r.values)]
	r.index++
	switch {
	case value < min:
		return min
	case value > max:
		return max
	default:
		return value
	}
}

func TestArrivalGateOfReadsTheCatalogCoordinates(t *testing.T) {
	plain := ArrivalGateOf(shared.MapCatalogItem{Village: 1, Area: 1})
	if plain.Radius != 0 || plain.KeepsOut(0, 0) {
		t.Fatalf("map without an arrival gate kept a zone free: %+v", plain)
	}
	gate := ArrivalGateOf(shared.MapCatalogItem{Village: 1, Area: 1, GateX: 400, GateY: 300})
	if gate.X != 400 || gate.Y != 300 || gate.Radius != ArrivalClearance {
		t.Fatalf("gate=%+v, want 400/300 r=%d", gate, ArrivalClearance)
	}
	if !gate.KeepsOut(400, 300) || !gate.KeepsOut(400+ArrivalClearance-1, 300) {
		t.Fatal("a point inside the clearance must be kept out")
	}
	if gate.KeepsOut(400+ArrivalClearance, 300) || gate.KeepsOut(0, 0) {
		t.Fatal("a point outside the clearance must stay available")
	}
}

func TestBestRandomPointStepsOffTheArrivalPad(t *testing.T) {
	rectangles := []shared.MapRectangle{{XMin: 0, XMax: 1000, YMin: 0, YMax: 1000}}
	gate := ArrivalGateOf(shared.MapCatalogItem{Village: 1, Area: 1, GateX: 100, GateY: 100})
	// The first draw lands on the gate, the second well clear of it.
	env := &sequenceRandom{values: []int{1, 100, 100, 1, 800, 800}}
	x, y, ok := bestRandomPoint(env, rectangles, nil, gate)
	if !ok {
		t.Fatal("bestRandomPoint kept no point")
	}
	if x == 100 && y == 100 {
		t.Fatalf("robot was placed on the arrival pad: %d/%d", x, y)
	}
	if x != 800 || y != 800 {
		t.Fatalf("point=%d/%d, want the clear draw 800/800", x, y)
	}
	// The same draws without a gate keep the first candidate, which proves the
	// clearance -- not the draw order -- moved the robot.
	if x, y, ok = bestRandomPoint(&sequenceRandom{values: []int{1, 100, 100, 1, 800, 800}}, rectangles, nil, ArrivalGate{}); !ok || x != 100 || y != 100 {
		t.Fatalf("point=%d/%d ok=%v, want the first draw 100/100", x, y, ok)
	}
}

func TestBestRandomPointFallsBackWhenThePadCoversTheMap(t *testing.T) {
	// The whole map sits inside the clearance, so every draw lands on the pad.
	rectangles := []shared.MapRectangle{{XMin: 0, XMax: 100, YMin: 0, YMax: 100}}
	gate := ArrivalGateOf(shared.MapCatalogItem{Village: 1, Area: 1, GateX: 50, GateY: 50})
	x, y, ok := bestRandomPoint(zeroRandom{}, rectangles, nil, gate)
	if !ok {
		t.Fatal("a map fully covered by the arrival pad must still take a robot")
	}
	if x != 0 || y != 0 {
		t.Fatalf("fallback point=%d/%d, want the draw every candidate shared: 0/0", x, y)
	}
}
