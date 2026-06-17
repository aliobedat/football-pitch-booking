package geo

import "sort"

// SortByDistance is the SINGLE distance-sort referee for every "nearest" surface
// (the time-availability search AND the /pitches listing both call it — there is no
// second implementation). For each row it measures the great-circle distance from
// origin via HaversineKm (the one distance impl), records it on the row, then
// STABLE-sorts the slice ascending by that distance.
//
// Gating + tail behaviour (identical to the prior inline block it replaces):
//   - A distance is computed ONLY when BOTH origin and the row carry usable
//     coordinates (HasUsableCoords — rejects NULL and the (0,0) Null-Island
//     sentinel). Otherwise the row's distance is set to nil.
//   - When origin itself is unusable, every row gets a nil distance and the sort is
//     a no-op: the caller's incoming order is preserved untouched.
//   - Rows with a nil distance sink to a STABLE tail, keeping their incoming
//     relative order. Because the sort is stable, equal-distance rows also keep
//     their incoming order — so a caller that pre-orders by featured/price/id gets
//     that as the natural tiebreak among equidistant rows.
//
// coordsOf returns a row's own coordinates; setDist records the computed distance
// (nil when not computable) onto the row; getDist reads it back for comparison.
// rows is mutated in place.
func SortByDistance[T any](
	rows []T,
	origin Coordinates,
	coordsOf func(T) Coordinates,
	setDist func(*T, *float64),
	getDist func(T) *float64,
) {
	originUsable := origin.HasUsableCoords()
	for i := range rows {
		var d *float64
		if originUsable {
			if rc := coordsOf(rows[i]); rc.HasUsableCoords() {
				km := HaversineKm(*origin.Lat, *origin.Lng, *rc.Lat, *rc.Lng)
				d = &km
			}
		}
		setDist(&rows[i], d)
	}

	sort.SliceStable(rows, func(i, j int) bool {
		di, dj := getDist(rows[i]), getDist(rows[j])
		switch {
		case di != nil && dj != nil:
			return *di < *dj
		case di != nil:
			return true // i has a distance, j doesn't → i first
		default:
			return false // j first, or both nil → keep stable incoming order
		}
	})
}
