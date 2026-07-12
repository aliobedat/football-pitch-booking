package data

// Integration tests for the date+time availability search (PR 4) against a live
// DB. They grade the 6 acceptance criteria: start-occupied exclusion, run-length
// capping (by next booking and by closing), the 60-minute floor, nearest-first
// sorting, and (0,0)/NULL sentinel handling. SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL is set.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/data/ -run Availability

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/geo"
	"github.com/ali/football-pitch-api/internal/testutil"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type availEnv struct {
	pool    *pgxpool.Pool
	model   *PitchModel
	ownerID int
	base    time.Time // an Amman civil date a few days out
	weekday int
	pitches []int
}

func newAvailEnv(t *testing.T) *availEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping availability integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	testutil.AssertSchemaBaseline(t, pool)

	suffix := testutil.UniqueSuffix() % 1_000_000
	var ownerID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ('Avail Owner', $1, 'owner', TRUE) RETURNING id
	`, fmt.Sprintf("+96278%06d", suffix)).Scan(&ownerID); err != nil {
		pool.Close()
		t.Fatalf("seed owner: %v", err)
	}

	base := time.Now().In(timeutil.Amman()).AddDate(0, 0, 5)
	e := &availEnv{pool: pool, model: &PitchModel{DB: pool}, ownerID: ownerID,
		base: base, weekday: int(base.Weekday())}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		for _, pid := range e.pitches {
			_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, pid)
			_, _ = pool.Exec(cctx, `DELETE FROM operating_hours WHERE pitch_id = $1`, pid)
			_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, pid)
			_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pid)
		}
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, ownerID)
		pool.Close()
	})
	return e
}

// mkPitch creates a visible pitch (CreatePitch seeds coords at the (0,0) sentinel).
func (e *availEnv) mkPitch(t *testing.T, name string) int {
	t.Helper()
	p, err := e.model.CreatePitch(context.Background(), CreatePitchRequest{
		Name: name, Neighborhood: "Khalda", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: e.ownerID,
	})
	if err != nil {
		t.Fatalf("mkPitch %s: %v", name, err)
	}
	e.pitches = append(e.pitches, p.ID)
	return p.ID
}

// setHours configures one open window on the search weekday.
func (e *availEnv) setHours(t *testing.T, pitchID int, open, close string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO operating_hours (pitch_id, weekday, open_time, close_time)
		VALUES ($1, $2, $3::time, $4::time)
	`, pitchID, e.weekday, open, close); err != nil {
		t.Fatalf("setHours: %v", err)
	}
}

func (e *availEnv) setCoords(t *testing.T, pitchID int, lat, lng float64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE pitches SET latitude = $1, longitude = $2 WHERE id = $3`, lat, lng, pitchID); err != nil {
		t.Fatalf("setCoords: %v", err)
	}
}

func (e *availEnv) setNullLat(t *testing.T, pitchID int) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE pitches SET latitude = NULL WHERE id = $1`, pitchID); err != nil {
		t.Fatalf("setNullLat: %v", err)
	}
}

// ammanAt builds the absolute instant for an Amman wall-clock time on the base date.
func (e *availEnv) ammanAt(h, m int) time.Time {
	y, mo, d := e.base.Date()
	return time.Date(y, mo, d, h, m, 0, 0, timeutil.Amman())
}

// seedBlock inserts a non-cancelled BLOCK occupying [start,end) (source-agnostic
// for occupancy; a block needs no player or guest).
func (e *availEnv) seedBlock(t *testing.T, pitchID int, start, end time.Time) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1, NULL, tstzrange($2::timestamptz, $3::timestamptz, '[)'), 0, 'confirmed', 'block')
	`, pitchID, start.UTC(), end.UTC()); err != nil {
		t.Fatalf("seedBlock: %v", err)
	}
}

func (e *availEnv) search(t *testing.T, player geo.Coordinates) []AvailabilityResult {
	t.Helper()
	res, err := e.model.SearchAvailability(context.Background(), AvailabilityQuery{
		AmmanDate: e.base, Start: e.ammanAt(8, 0), Player: player,
	})
	if err != nil {
		t.Fatalf("SearchAvailability: %v", err)
	}
	return res
}

func find(res []AvailabilityResult, id int) (*AvailabilityResult, int) {
	for i := range res {
		if res[i].ID == id {
			return &res[i], i
		}
	}
	return nil, -1
}

// ── 1. Booking 7:00–8:30 → excluded from an 8:00 search (start occupied) ─────

func TestAvailability_StartOccupiedExcluded(t *testing.T) {
	e := newAvailEnv(t)
	p := e.mkPitch(t, "occupied-start")
	e.setHours(t, p, "06:00", "23:00")
	e.seedBlock(t, p, e.ammanAt(7, 0), e.ammanAt(8, 30))

	if r, _ := find(e.search(t, geo.Coordinates{}), p); r != nil {
		t.Fatalf("pitch with a booking covering 08:00 should be excluded, got %+v", r)
	}
}

// ── 2. Booking 9:30–11:00, open till 11 → 8:00 returns 90 (capped by booking) ─

func TestAvailability_CappedByNextBooking(t *testing.T) {
	e := newAvailEnv(t)
	p := e.mkPitch(t, "capped-by-booking")
	e.setHours(t, p, "06:00", "11:00")
	e.seedBlock(t, p, e.ammanAt(9, 30), e.ammanAt(11, 0))

	r, _ := find(e.search(t, geo.Coordinates{}), p)
	if r == nil {
		t.Fatalf("pitch should be available from 08:00")
	}
	if r.AvailableMinutes != 90 {
		t.Fatalf("available_minutes = %d, want 90 (capped by the 09:30 booking)", r.AvailableMinutes)
	}
}

// ── 3. No bookings, closes 9:30 → 8:00 returns 90 (capped by closing) ────────

func TestAvailability_CappedByClosing(t *testing.T) {
	e := newAvailEnv(t)
	p := e.mkPitch(t, "capped-by-closing")
	e.setHours(t, p, "06:00", "09:30")

	r, _ := find(e.search(t, geo.Coordinates{}), p)
	if r == nil {
		t.Fatalf("pitch should be available from 08:00")
	}
	if r.AvailableMinutes != 90 {
		t.Fatalf("available_minutes = %d, want 90 (capped by the 09:30 close)", r.AvailableMinutes)
	}
}

// ── 4. Only 45 free min → excluded by the 60-min floor ───────────────────────

func TestAvailability_BelowFloorExcluded(t *testing.T) {
	e := newAvailEnv(t)
	p := e.mkPitch(t, "below-floor")
	e.setHours(t, p, "06:00", "08:45") // 45 min from 08:00

	if r, _ := find(e.search(t, geo.Coordinates{}), p); r != nil {
		t.Fatalf("pitch with only 45 free minutes should be excluded, got %d min", r.AvailableMinutes)
	}
}

// ── 5. With coords, the nearer pitch sorts first ─────────────────────────────

func TestAvailability_NearestFirst(t *testing.T) {
	e := newAvailEnv(t)
	near := e.mkPitch(t, "near")
	far := e.mkPitch(t, "far")
	e.setHours(t, near, "06:00", "23:00")
	e.setHours(t, far, "06:00", "23:00")

	player := geo.Coordinates{Lat: ptr(32.00), Lng: ptr(35.90)}
	e.setCoords(t, near, 32.001, 35.901) // ~0.15 km
	e.setCoords(t, far, 32.50, 36.40)    // far

	res := e.search(t, player)
	rNear, iNear := find(res, near)
	rFar, iFar := find(res, far)
	if rNear == nil || rFar == nil {
		t.Fatalf("both pitches should be available")
	}
	if rNear.DistanceKm == nil || rFar.DistanceKm == nil {
		t.Fatalf("both pitches should carry a distance (near=%v far=%v)", rNear.DistanceKm, rFar.DistanceKm)
	}
	if *rNear.DistanceKm >= *rFar.DistanceKm {
		t.Fatalf("near distance %.3f should be < far %.3f", *rNear.DistanceKm, *rFar.DistanceKm)
	}
	if iNear >= iFar {
		t.Fatalf("near pitch (idx %d) should sort before far (idx %d)", iNear, iFar)
	}
}

// ── 6. (0,0) and NULL-lat both tail, never gated out, never Null Island ──────

func TestAvailability_SentinelCoordsTailNotGated(t *testing.T) {
	e := newAvailEnv(t)
	real := e.mkPitch(t, "real-coords")
	zero := e.mkPitch(t, "zero-zero") // CreatePitch leaves it at (0,0)
	null := e.mkPitch(t, "null-lat")
	for _, p := range []int{real, zero, null} {
		e.setHours(t, p, "06:00", "23:00")
	}
	player := geo.Coordinates{Lat: ptr(32.00), Lng: ptr(35.90)}
	e.setCoords(t, real, 32.002, 35.902)
	e.setNullLat(t, null) // latitude NULL

	res := e.search(t, player)
	rReal, iReal := find(res, real)
	rZero, iZero := find(res, zero)
	rNull, iNull := find(res, null)

	// All three are returned — sentinel pitches are NEVER gated out.
	if rReal == nil || rZero == nil || rNull == nil {
		t.Fatalf("all three pitches must appear (real=%v zero=%v null=%v)", rReal, rZero, rNull)
	}
	// The sentinel pitches carry NO distance (never measured to Null Island).
	if rZero.DistanceKm != nil {
		t.Errorf("(0,0) pitch got a distance %.3f — must be nil (not Null Island)", *rZero.DistanceKm)
	}
	if rNull.DistanceKm != nil {
		t.Errorf("NULL-lat pitch got a distance %.3f — must be nil", *rNull.DistanceKm)
	}
	// The real-coords pitch is measured and sorts ahead of both sentinel tails.
	if rReal.DistanceKm == nil {
		t.Fatalf("real-coords pitch should carry a distance")
	}
	if iReal >= iZero || iReal >= iNull {
		t.Fatalf("real pitch (idx %d) should precede sentinel tails (zero=%d null=%d)", iReal, iZero, iNull)
	}
}

func ptr(f float64) *float64 { return &f }
