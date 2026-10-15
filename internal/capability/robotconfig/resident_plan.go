package robotconfig

import (
	"fmt"
	"strconv"
	"strings"
)

// ResidentRow is one row of a resident plan: Count robots of the fleet stand in
// the named village, on one of the areas in [AreaMin,AreaMax], for as long as
// the fleet is running.
//
// The row names a map rather than a level because that is what the operator
// means by "this crowd is always there": the standing population is logged in
// whatever the player is doing, so there is no player level to match against.
// Which of the fleet's robots fill the row is the demand layer's choice, and it
// is the layer that has to answer the map's own level requirement.
type ResidentRow struct {
	Village int
	AreaMin int
	AreaMax int
	Count   int
	// Box is the rectangle the row's robots stand in. BoxSet reports whether the
	// operator gave one; without it the placement layer draws from the map's own
	// rectangle, the same one an unpinned robot would use.
	BoxSet bool
	XMin   int
	YMin   int
	XMax   int
	YMax   int
}

// ParseResidentPlan reads the `[demand] resident_plan` syntax: semicolon
// separated rows of
//
//	<village>/<area|first-last>:<count>[@<x_min>,<y_min>-<x_max>,<y_max>]
//
// e.g.
//
//	39/0:30@1686,155-2701,156;40/0:5;30/0-2:30
//
// Rows are separated by a semicolon rather than a comma because a box carries
// commas of its own, and a nested separator reads better than an escape.
//
// An empty spec returns no rows and no error: it means the operator kept the
// fleet's plain spawn behaviour, where nothing is pinned and every robot logs in
// on the map the spawn rules pick for it.
func ParseResidentPlan(spec string) ([]ResidentRow, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, nil
	}
	parts := strings.Split(spec, ";")
	rows := make([]ResidentRow, 0, len(parts))
	for _, part := range parts {
		row := strings.TrimSpace(part)
		if row == "" {
			return nil, fmt.Errorf("rows must not be empty")
		}
		parsed, err := parseResidentRow(row)
		if err != nil {
			return nil, err
		}
		rows = append(rows, parsed)
	}
	return rows, nil
}

func parseResidentRow(row string) (ResidentRow, error) {
	mapSpec, rest, ok := strings.Cut(row, ":")
	if !ok {
		return ResidentRow{}, fmt.Errorf("row %q must be written as <village>/<area>:<count>", row)
	}
	countSpec := rest
	boxSpec := ""
	hasBox := false
	if before, after, found := strings.Cut(rest, "@"); found {
		countSpec = before
		boxSpec = after
		hasBox = true
	}
	villageSpec, areaSpec, ok := strings.Cut(mapSpec, "/")
	if !ok {
		return ResidentRow{}, fmt.Errorf("row %q must be written as <village>/<area>:<count>", row)
	}
	village, err := strconv.Atoi(strings.TrimSpace(villageSpec))
	if err != nil {
		return ResidentRow{}, fmt.Errorf("row %q must name a village", row)
	}
	if village < 1 {
		return ResidentRow{}, fmt.Errorf("row %q must name a village", row)
	}
	areaMin, areaMax, err := parseRange(row, strings.TrimSpace(areaSpec), "area")
	if err != nil {
		return ResidentRow{}, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(countSpec))
	if err != nil {
		return ResidentRow{}, fmt.Errorf("row %q must end with a count", row)
	}
	if count <= 0 {
		return ResidentRow{}, fmt.Errorf("row %q must ask for at least one robot", row)
	}
	parsed := ResidentRow{Village: village, AreaMin: areaMin, AreaMax: areaMax, Count: count}
	if !hasBox {
		return parsed, nil
	}
	xMin, yMin, xMax, yMax, err := parseResidentBox(row, strings.TrimSpace(boxSpec))
	if err != nil {
		return ResidentRow{}, err
	}
	parsed.BoxSet = true
	parsed.XMin, parsed.YMin, parsed.XMax, parsed.YMax = xMin, yMin, xMax, yMax
	return parsed, nil
}

// parseRange reads a `<value|first-last>` range.
func parseRange(row, spec, label string) (int, int, error) {
	lowSpec, highSpec, ranged := strings.Cut(spec, "-")
	low, err := strconv.Atoi(strings.TrimSpace(lowSpec))
	if err != nil {
		return 0, 0, fmt.Errorf("row %q must name the %s", row, label)
	}
	high := low
	if ranged {
		if high, err = strconv.Atoi(strings.TrimSpace(highSpec)); err != nil {
			return 0, 0, fmt.Errorf("row %q must end its %s range with a number", row, label)
		}
	}
	if low < 0 || high > 65535 {
		return 0, 0, fmt.Errorf("row %q must keep its %s between 0 and 65535", row, label)
	}
	if high < low {
		return 0, 0, fmt.Errorf("row %q has an inverted %s range", row, label)
	}
	return low, high, nil
}

// parseResidentBox reads a `<x_min>,<y_min>-<x_max>,<y_max>` rectangle.
func parseResidentBox(row, spec string) (int, int, int, int, error) {
	lowSpec, highSpec, ok := strings.Cut(spec, "-")
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("row %q must write its box as <x_min>,<y_min>-<x_max>,<y_max>", row)
	}
	xMin, yMin, err := parsePoint(row, lowSpec)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	xMax, yMax, err := parsePoint(row, highSpec)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	if xMax < xMin || yMax < yMin {
		return 0, 0, 0, 0, fmt.Errorf("row %q has an inverted box", row)
	}
	return xMin, yMin, xMax, yMax, nil
}

func parsePoint(row, spec string) (int, int, error) {
	xSpec, ySpec, ok := strings.Cut(spec, ",")
	if !ok {
		return 0, 0, fmt.Errorf("row %q must write its box as <x_min>,<y_min>-<x_max>,<y_max>", row)
	}
	x, err := strconv.Atoi(strings.TrimSpace(xSpec))
	if err != nil {
		return 0, 0, fmt.Errorf("row %q must write its box as <x_min>,<y_min>-<x_max>,<y_max>", row)
	}
	y, err := strconv.Atoi(strings.TrimSpace(ySpec))
	if err != nil {
		return 0, 0, fmt.Errorf("row %q must write its box as <x_min>,<y_min>-<x_max>,<y_max>", row)
	}
	if x < 0 || y < 0 || x > 65535 || y > 65535 {
		return 0, 0, fmt.Errorf("row %q must keep its box between 0 and 65535", row)
	}
	return x, y, nil
}

// ResidentPlanSize is how many standing robots a plan describes.
func ResidentPlanSize(rows []ResidentRow) int {
	total := 0
	for _, row := range rows {
		total += row.Count
	}
	return total
}
