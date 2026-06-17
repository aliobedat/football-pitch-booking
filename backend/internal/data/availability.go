package data

import (
	"context"
	"fmt"
	"time"

	"github.com/ali/football-pitch-api/internal/geo"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// AvailabilityQuery is the input to a date+time availability search.
type AvailabilityQuery struct {
	// AmmanDate is the civil day to search (only Y/M/D are read, in Amman terms).
	AmmanDate time.Time
	// Start is the absolute instant the player wants to begin (built in Amman).
	Start time.Time
	// Player coordinates — optional. nil when the browser gave no location.
	Player geo.Coordinates
}

// AvailabilityResult is one pitch that is open at the requested start and free
// from it for at least the 60-minute floor.
type AvailabilityResult struct {
	ID               int       `json:"id"`
	Name             string    `json:"name"`
	Area             string    `json:"area"`      // neighborhood
	ImageURL         string    `json:"image_url"` // primary image
	AvailableUntil   time.Time `json:"available_until"`
	AvailableMinutes int       `json:"available_minutes"`
	// DistanceKm is set only when BOTH the player and the pitch have usable
	// coordinates; nil otherwise (the pitch then rides the default-order tail).
	DistanceKm *float64 `json:"distance_km"`

	// coords carries the pitch's own coordinates to the shared distance sorter
	// (geo.SortByDistance). Unexported → never serialized; the result shape is
	// unchanged.
	coords geo.Coordinates
}

// minAvailableMinutes is the continuous-availability floor: a pitch must be free
// for at least this long from the requested start to be returned.
const minAvailableMinutes = 60

// candidate is the per-pitch working row assembled before the open/occupancy math.
type candidate struct {
	id     int
	name   string
	area   string
	image  string
	coords geo.Coordinates
}

// SearchAvailability returns player-visible pitches open at q.Start and free from
// it for ≥ 60 minutes, each with the continuous duration available, sorted
// nearest-first when the player and pitch both have usable coordinates (others keep
// the default order in the tail). Operating hours are resolved through the SAME
// path the booking write uses (ResolveWindowsForDate), so the read can never offer
// a slot the write would reject. All instants are UTC; intervals are half-open [).
func (m *PitchModel) SearchAvailability(ctx context.Context, q AvailabilityQuery) ([]AvailabilityResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := q.Start.UTC()

	// 1. Player-visible candidates (same visibility predicate as the public list),
	//    in the default display order so the no-distance tail stays stable later.
	rows, err := m.DB.Query(ctx, `
		SELECT id, name, neighborhood, COALESCE(image_url, ''), latitude, longitude
		FROM pitches
		WHERE deleted_at IS NULL AND is_active = true
		ORDER BY is_featured DESC, price_per_hour ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("SearchAvailability: candidates: %w", err)
	}
	var cands []candidate
	var ids []int
	for rows.Next() {
		var c candidate
		var lat, lng *float64
		if err := rows.Scan(&c.id, &c.name, &c.area, &c.image, &lat, &lng); err != nil {
			rows.Close()
			return nil, fmt.Errorf("SearchAvailability: scan candidate: %w", err)
		}
		c.coords = geo.Coordinates{Lat: lat, Lng: lng}
		cands = append(cands, c)
		ids = append(ids, c.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("SearchAvailability: candidate rows: %w", err)
	}
	if len(cands) == 0 {
		return []AvailabilityResult{}, nil
	}

	// 2. Operating windows for every candidate (single read), grouped per pitch.
	windowsByPitch, err := m.loadWindowsForPitches(ctx, ids)
	if err != nil {
		return nil, err
	}

	// 3. Occupancy facts per pitch: is the START instant covered by a non-cancelled
	//    booking (any source), and what is the next booking start strictly after it?
	covered, nextStart, err := m.loadOccupancy(ctx, ids, start)
	if err != nil {
		return nil, err
	}

	// End of the Amman civil day — the closing cap for an unconfigured (24/7) pitch.
	_, dayEndUTC := timeutil.AmmanDayBoundsUTC(q.AmmanDate)

	results := make([]AvailabilityResult, 0, len(cands))
	for _, c := range cands {
		// Skip a pitch whose start instant is already occupied (source-agnostic).
		if covered[c.id] {
			continue
		}

		// Determine open-at-start + the closing instant from the SAME resolver the
		// booking path uses. No configured windows → fail-open 24/7 (closing = day end).
		var closing time.Time
		if w := windowsByPitch[c.id]; len(w) == 0 {
			closing = dayEndUTC // 24/7 → bounded to the rest of the civil day
		} else {
			resolved, err := ResolveWindowsForDate(w, q.AmmanDate)
			if err != nil {
				return nil, fmt.Errorf("SearchAvailability: resolve pitch %d: %w", c.id, err)
			}
			iv, ok := windowContaining(resolved, start)
			if !ok {
				continue // configured but NOT open at the requested start → drop
			}
			closing = iv.End
		}

		// available_until = earlier of (next booking start) and (closing).
		availableUntil := closing
		if ns, ok := nextStart[c.id]; ok && ns.Before(closing) {
			availableUntil = ns
		}
		mins := int(availableUntil.Sub(start).Minutes())
		if mins < minAvailableMinutes {
			continue // below the continuous-availability floor
		}

		res := AvailabilityResult{
			ID: c.id, Name: c.name, Area: c.area, ImageURL: c.image,
			AvailableUntil: availableUntil, AvailableMinutes: mins,
			coords: c.coords, // carried for the shared distance sorter
		}
		results = append(results, res)
	}

	// 4. Nearest-first via the SINGLE shared distance referee: ascending distance,
	//    nil-distance rows in a stable tail, default (featured/price/id) order kept
	//    as the tiebreak. Same Haversine + same stable order as the prior inline
	//    block — byte-identical output.
	geo.SortByDistance(
		results, q.Player,
		func(r AvailabilityResult) geo.Coordinates { return r.coords },
		func(r *AvailabilityResult, d *float64) { r.DistanceKm = d },
		func(r AvailabilityResult) *float64 { return r.DistanceKm },
	)

	return results, nil
}

// loadWindowsForPitches reads the weekly windows for all given pitches at once,
// grouped per pitch. A pitch absent from the map has no configured schedule.
func (m *PitchModel) loadWindowsForPitches(ctx context.Context, ids []int) (map[int][]OperatingWindow, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT pitch_id, weekday, to_char(open_time, 'HH24:MI'), to_char(close_time, 'HH24:MI')
		FROM operating_hours
		WHERE pitch_id = ANY($1)
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("SearchAvailability: windows: %w", err)
	}
	defer rows.Close()

	out := make(map[int][]OperatingWindow)
	for rows.Next() {
		var pid int
		var w OperatingWindow
		if err := rows.Scan(&pid, &w.Weekday, &w.OpenTime, &w.CloseTime); err != nil {
			return nil, fmt.Errorf("SearchAvailability: scan window: %w", err)
		}
		out[pid] = append(out[pid], w)
	}
	return out, rows.Err()
}

// loadOccupancy returns, per pitch, whether `start` is covered by a non-cancelled
// booking (any source) and the next non-cancelled booking start strictly after it.
func (m *PitchModel) loadOccupancy(ctx context.Context, ids []int, start time.Time) (covered map[int]bool, nextStart map[int]time.Time, err error) {
	rows, err := m.DB.Query(ctx, `
		SELECT pitch_id,
		       bool_or(booking_range @> $2::timestamptz) AS covered,
		       min(lower(booking_range)) FILTER (WHERE lower(booking_range) > $2::timestamptz) AS next_start
		FROM bookings
		WHERE pitch_id = ANY($1)
		  AND status <> 'cancelled'
		  AND upper(booking_range) > $2::timestamptz
		GROUP BY pitch_id
	`, ids, start.UTC())
	if err != nil {
		return nil, nil, fmt.Errorf("SearchAvailability: occupancy: %w", err)
	}
	defer rows.Close()

	covered = make(map[int]bool)
	nextStart = make(map[int]time.Time)
	for rows.Next() {
		var pid int
		var cov bool
		var ns *time.Time
		if err := rows.Scan(&pid, &cov, &ns); err != nil {
			return nil, nil, fmt.Errorf("SearchAvailability: scan occupancy: %w", err)
		}
		covered[pid] = cov
		if ns != nil {
			nextStart[pid] = ns.UTC()
		}
	}
	return covered, nextStart, rows.Err()
}

// windowContaining returns the resolved interval whose half-open span [Start,End)
// contains `start` (Start <= start < End), if any.
func windowContaining(windows []ConcreteInterval, start time.Time) (ConcreteInterval, bool) {
	for _, iv := range windows {
		if !iv.Start.After(start) && start.Before(iv.End) {
			return iv, true
		}
	}
	return ConcreteInterval{}, false
}
