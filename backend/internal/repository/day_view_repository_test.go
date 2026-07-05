package repository

// Integration tests for OwnerDayView against a live database. They prove owner
// scoping (own pitch OK; foreign owner → ErrPitchNotFound/404; admin unscoped),
// confirmed-only revenue (blocks contribute 0; cancelled excluded), and the
// inactive-pitch rule (occupancy still shown, zero available slots).
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set (same convention as the
// other repository integration tests — never run against production):
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run DayView

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type dayViewEnv struct {
	pool    *pgxpool.Pool
	repo    DayViewRepository
	model   *data.PitchModel
	ownerID int64
	otherID int64
	pitchID int64
	price   float64
}

func newDayViewEnv(t *testing.T) *dayViewEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping Day View integration test")
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

	suffix := time.Now().UnixNano() % 1_000_000
	mk := func(name, prefix, role string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, fmt.Sprintf("+962%s%06d", prefix, suffix), role).Scan(&id); err != nil {
			pool.Close()
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	ownerID := mk("DV Owner", "80", "owner")
	otherID := mk("DV Other", "81", "owner")

	const price = 30.0
	model := &data.PitchModel{DB: pool}
	p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "DV Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: price, OwnerID: int(ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}

	e := &dayViewEnv{
		pool: pool, repo: NewDayViewRepository(pool), model: model,
		ownerID: ownerID, otherID: otherID, pitchID: int64(p.ID), price: price,
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM operating_hours WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{ownerID, otherID})
		pool.Close()
	})
	return e
}

func (e *dayViewEnv) ownerActor() auth.Actor { return auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner} }
func (e *dayViewEnv) otherActor() auth.Actor { return auth.Actor{UserID: int(e.otherID), Role: auth.RoleOwner} }
func (e *dayViewEnv) adminActor() auth.Actor { return auth.Actor{UserID: int(e.ownerID), Role: auth.RoleAdmin} }

// seedBooking inserts a booking row directly (bypassing the write-path gate) so the
// test controls source/status/total_price precisely.
func (e *dayViewEnv) seedBooking(t *testing.T, source, status string, price float64, playerID *int64, start, end time.Time) int64 {
	t.Helper()
	ctx := context.Background()
	// The DB CHECK requires guest_name on manual/academy rows and none on player/block.
	var guestName any
	if source == "manual" || source == "academy" {
		guestName = "DV Guest"
	}
	var id int64
	err := e.pool.QueryRow(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, status, source, total_price, guest_name)
		VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), $5, $6, $7, $8)
		RETURNING id
	`, e.pitchID, playerID, start, end, status, source, price, guestName).Scan(&id)
	if err != nil {
		t.Fatalf("seed booking (%s/%s): %v", source, status, err)
	}
	return id
}

// tomorrow noon (Amman) avoids any past-hour edge in classification.
func (e *dayViewEnv) day() time.Time {
	return time.Now().In(timeutil.Amman()).AddDate(0, 0, 1)
}

func slotAtHour(t *testing.T, dv *DayView, ammanDate time.Time, h int) DayViewSlot {
	t.Helper()
	y, m, d := ammanDate.Date()
	target := time.Date(y, m, d, h, 0, 0, 0, timeutil.Amman()).UTC()
	for _, s := range dv.Slots {
		if s.Start.UTC().Equal(target) {
			return s
		}
	}
	t.Fatalf("no slot at %02d:00", h)
	return DayViewSlot{}
}

func TestDayView_OwnerSeesOwnPitch(t *testing.T) {
	e := newDayViewEnv(t)
	day := e.day()
	y, m, d := day.Date()
	loc := timeutil.Amman()
	start := time.Date(y, m, d, 18, 0, 0, 0, loc)
	end := time.Date(y, m, d, 19, 0, 0, 0, loc)
	e.seedBooking(t, "manual", "confirmed", e.price, nil, start, end)

	dv, err := e.repo.OwnerDayView(context.Background(), e.ownerActor(), e.pitchID, day)
	if err != nil {
		t.Fatalf("owner should read own pitch: %v", err)
	}
	if len(dv.Slots) != 48 {
		t.Fatalf("want 48 slots, got %d", len(dv.Slots))
	}
	if dv.Timezone != "Asia/Amman" || dv.SlotMinutes != 30 {
		t.Fatalf("unexpected header: tz=%s slot=%d", dv.Timezone, dv.SlotMinutes)
	}
	if s := slotAtHour(t, dv, day, 18); s.Status != "booked" || s.Booking == nil {
		t.Fatalf("18:00 want booked with booking, got %s", s.Status)
	}
	if dv.Summary.TotalBookings != 1 {
		t.Fatalf("total_bookings want 1, got %d", dv.Summary.TotalBookings)
	}
	if dv.Summary.ConfirmedRevenue != e.price {
		t.Fatalf("confirmed_revenue want %v, got %v", e.price, dv.Summary.ConfirmedRevenue)
	}
}

func TestDayView_ForeignOwnerNotFound(t *testing.T) {
	e := newDayViewEnv(t)
	_, err := e.repo.OwnerDayView(context.Background(), e.otherActor(), e.pitchID, e.day())
	if !errors.Is(err, ErrPitchNotFound) {
		t.Fatalf("foreign owner must get ErrPitchNotFound (→404), got %v", err)
	}
}

func TestDayView_AdminUnscoped(t *testing.T) {
	e := newDayViewEnv(t)
	dv, err := e.repo.OwnerDayView(context.Background(), e.adminActor(), e.pitchID, e.day())
	if err != nil {
		t.Fatalf("admin should read any pitch: %v", err)
	}
	if dv.PitchID != e.pitchID {
		t.Fatalf("admin got wrong pitch %d", dv.PitchID)
	}
}

func TestDayView_RevenueConfirmedOnly_BlocksAndCancelledExcluded(t *testing.T) {
	e := newDayViewEnv(t)
	day := e.day()
	y, m, d := day.Date()
	loc := timeutil.Amman()
	h := func(hr int) time.Time { return time.Date(y, m, d, hr, 0, 0, 0, loc) }

	e.seedBooking(t, "manual", "confirmed", e.price, nil, h(10), h(11)) // counts
	e.seedBooking(t, "block", "confirmed", 0, nil, h(12), h(13))        // 0 revenue
	e.seedBooking(t, "manual", "cancelled", 999, nil, h(14), h(15))     // excluded

	dv, err := e.repo.OwnerDayView(context.Background(), e.ownerActor(), e.pitchID, day)
	if err != nil {
		t.Fatalf("day view: %v", err)
	}
	if dv.Summary.ConfirmedRevenue != e.price {
		t.Fatalf("confirmed_revenue want %v (blocks 0, cancelled excluded), got %v", e.price, dv.Summary.ConfirmedRevenue)
	}
	// The block occupies but is not counted a "booking"; the cancelled row is absent.
	if dv.Summary.TotalBookings != 1 {
		t.Fatalf("total_bookings want 1 (manual only), got %d", dv.Summary.TotalBookings)
	}
	if s := slotAtHour(t, dv, day, 12); s.Status != "blocked" || s.Booking == nil || s.Booking.Source != "block" {
		t.Fatalf("12:00 block cell want blocked/source=block, got %s", s.Status)
	}
	if s := slotAtHour(t, dv, day, 14); s.Status == "booked" || s.Status == "blocked" {
		t.Fatalf("14:00 cancelled booking must not occupy")
	}
}

func TestDayView_InactivePitch_NoAvailableButShowsOccupancy(t *testing.T) {
	e := newDayViewEnv(t)
	day := e.day()
	y, m, d := day.Date()
	loc := timeutil.Amman()
	e.seedBooking(t, "manual", "confirmed", e.price, nil,
		time.Date(y, m, d, 16, 0, 0, 0, loc), time.Date(y, m, d, 17, 0, 0, 0, loc))

	// Deactivate the pitch directly.
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE pitches SET is_active = FALSE WHERE id = $1`, e.pitchID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	dv, err := e.repo.OwnerDayView(context.Background(), e.ownerActor(), e.pitchID, day)
	if err != nil {
		t.Fatalf("inactive pitch should still return day view: %v", err)
	}
	if dv.IsActive {
		t.Fatalf("expected is_active=false")
	}
	if dv.Summary.AvailableSlots != 0 {
		t.Fatalf("inactive pitch must expose 0 available, got %d", dv.Summary.AvailableSlots)
	}
	if s := slotAtHour(t, dv, day, 16); s.Status != "booked" {
		t.Fatalf("inactive pitch must still show occupancy at 16:00, got %s", s.Status)
	}
}
