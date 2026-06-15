// Package geo holds coordinate semantics + distance math for the platform. It is
// the SINGLE source of truth for "does this pitch have a usable location?" so no
// caller ever plots a pin — or computes a distance — for the legacy (0,0) sentinel
// (Null Island, in the Atlantic) or a NULL coordinate.
package geo

import "math"

// Coordinates is a lat/lng pair that may be unset. A column may be NULL (→ nil
// pointer) or carry the historical (0,0) sentinel meaning "no location set yet"
// (the pitches table defaulted longitude to 0 NOT NULL, latitude to 0 NULL, before
// real coordinates were backfilled). Both cases are "no usable location".
type Coordinates struct {
	Lat *float64
	Lng *float64
}

// HasUsableCoords reports whether this pair carries a real, plottable location.
// It is the ONE gate every consumer (distance sort, future map pins) must use:
// true ONLY IF both components are present AND the pair is not exactly (0,0).
func (c Coordinates) HasUsableCoords() bool {
	if c.Lat == nil || c.Lng == nil {
		return false
	}
	return !(*c.Lat == 0 && *c.Lng == 0)
}

// earthRadiusKm is the mean Earth radius used for the Haversine approximation.
const earthRadiusKm = 6371.0

// HaversineKm returns the great-circle distance in kilometres between two points.
// Callers MUST gate both endpoints on HasUsableCoords first — this function does
// no sentinel checking and will happily measure the distance to Null Island.
func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	rlat1 := lat1 * math.Pi / 180
	rlat2 := lat2 * math.Pi / 180
	dlat := (lat2 - lat1) * math.Pi / 180
	dlng := (lng2 - lng1) * math.Pi / 180

	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(rlat1)*math.Cos(rlat2)*math.Sin(dlng/2)*math.Sin(dlng/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
