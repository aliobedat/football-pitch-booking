package repository

// Integration tests for the operating-hours WRITE-PATH GATE — the server-side
// enforcement that a player booking must fall fully inside a configured open
// window. They exercise the REAL insertConfirmedBookingTx gate (reading
// operating_hours under the pitch lock, the fail-open-on-unconfigured branch, the
// containment check, and the BypassHoursGate exemption) against a live database.
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set, so `go test ./...` stays
// green offline.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run OperatingHoursGate

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
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type ohGateEnv struct {
	pool     *pgxpool.Pool
	repo     BookingRepository
	model    *data.PitchModel
	ownerID  int64
	playerID int64
	pitchID  int64
}

func newOHGateEnv(t *testing.T) *ohGateEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping operating-hours gate integration test")
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
	mkUser := func(name, prefix, role string) int64 {
		var id int64
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, phone, role).Scan(&id); err != nil {
			pool.Close()
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}

	e := &ohGateEnv{pool: pool, repo: NewBookingRepository(pool), model: &data.PitchModel{DB: pool}}
	e.ownerID = mkUser("OH Owner", "84", auth.RoleOwner)
	e.playerID = mkUser("OH Player", "85", auth.RolePlayer)

	p, err := e.model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "OH Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(e.ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}
	e.pitchID = int64(p.ID)

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM operating_hours WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{e.ownerID, e.playerID})
		pool.Close()
	})
	return e
}

// ammanSlot builds a [start, end) booking on the given Amman calendar date at the
// given local hours — the way a player picks a slot. The repo normalises to UTC.
func ammanSlot(date time.Time, startHour, endHour int) (time.Time, time.Time) {
	y, m, d := date.Date()
	loc := timeutil.Amman()
	return time.Date(y, m, d, startHour, 0, 0, 0, loc), time.Date(y, m, d, endHour, 0, 0, 0, loc)
}

func (e *ohGateEnv) setSchedule(t *testing.T, windows []data.OperatingWindow) {
	t.Helper()
	if err := e.model.ReplaceOperatingHours(context.Background(), int(e.pitchID),
		auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}, windows); err != nil {
		t.Fatalf("set schedule: %v", err)
	}
}

func (e *ohGateEnv) book(start, end time.Time, bypass bool) (*models.Booking, error) {
	return e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID:         e.pitchID,
		PlayerID:        e.playerID,
		StartTime:       start,
		EndTime:         end,
		BypassHoursGate: bypass,
	})
}

// A future date whose Amman weekday we read, so the seeded window lands on the
// right day. ~35 days out keeps it clear of any pre-existing seed data.
func (e *ohGateEnv) futureDate() time.Time {
	return time.Now().In(timeutil.Amman()).AddDate(0, 0, 35)
}

// ── in-hours slot is accepted ────────────────────────────────────────────────

func TestOperatingHoursGate_InHoursAccepted(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	wd := int(date.Weekday())
	e.setSchedule(t, []data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "17:00"}})

	start, end := ammanSlot(date, 10, 11)
	b, err := e.book(start, end, false)
	if err != nil {
		t.Fatalf("in-hours booking should succeed, got %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", b.Status)
	}
}

// ── out-of-hours slot is rejected, with ZERO side effects ────────────────────

func TestOperatingHoursGate_OutOfHoursRejected(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	wd := int(date.Weekday())
	e.setSchedule(t, []data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "17:00"}})

	start, end := ammanSlot(date, 20, 21) // outside 09:00–17:00
	_, err := e.book(start, end, false)
	if !errors.Is(err, ErrSlotOutsideOperatingHours) {
		t.Fatalf("out-of-hours booking: err = %v, want ErrSlotOutsideOperatingHours (→422)", err)
	}
	// Rejection happens before any INSERT — no booking row was created.
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM bookings WHERE pitch_id = $1`, e.pitchID).Scan(&n); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	if n != 0 {
		t.Fatalf("found %d bookings after a rejected out-of-hours booking, want 0", n)
	}
}

// ── a slot straddling the close edge (partially outside) is rejected ─────────

func TestOperatingHoursGate_StraddlesCloseRejected(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	wd := int(date.Weekday())
	e.setSchedule(t, []data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "17:00"}})

	start, end := ammanSlot(date, 16, 18) // 16:00–18:00 runs past the 17:00 close
	if _, err := e.book(start, end, false); !errors.Is(err, ErrSlotOutsideOperatingHours) {
		t.Fatalf("slot past close: err = %v, want ErrSlotOutsideOperatingHours", err)
	}
}

// ── unconfigured pitch is open 24/7 (fail-open) ──────────────────────────────

func TestOperatingHoursGate_UnconfiguredFailsOpen(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()

	// No schedule set → any (otherwise valid) slot is bookable.
	start, end := ammanSlot(date, 20, 21)
	b, err := e.book(start, end, false)
	if err != nil {
		t.Fatalf("unconfigured pitch should be open 24/7, got %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", b.Status)
	}
}

// ── BypassHoursGate exempts the write (owner/admin seam) ─────────────────────

func TestOperatingHoursGate_BypassExempt(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	wd := int(date.Weekday())
	e.setSchedule(t, []data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "17:00"}})

	start, end := ammanSlot(date, 20, 21) // out-of-hours, but exempt
	b, err := e.book(start, end, true)
	if err != nil {
		t.Fatalf("bypassed booking should succeed regardless of hours, got %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", b.Status)
	}
}

// ── cross-midnight: early-hours slot accepted via the previous day's window ──

func TestOperatingHoursGate_CrossMidnightTailAccepted(t *testing.T) {
	e := newOHGateEnv(t)
	date := e.futureDate()
	// Seed a cross-midnight window on the day BEFORE the target date: 16:00→02:00.
	prevWd := int(date.AddDate(0, 0, -1).Weekday())
	e.setSchedule(t, []data.OperatingWindow{{Weekday: prevWd, OpenTime: "16:00", CloseTime: "02:00"}})

	// A 01:00–02:00 slot on the target date is covered by yesterday's window tail.
	start, end := ammanSlot(date, 1, 2)
	b, err := e.book(start, end, false)
	if err != nil {
		t.Fatalf("early-hours slot in cross-midnight tail should succeed, got %v", err)
	}
	if b.Status != models.StatusConfirmed {
		t.Fatalf("status = %q, want confirmed", b.Status)
	}
}
