package repository

// Integration tests for the MANUAL (walk-in) write path against a live database.
// They prove: the operating-hours gate is enforced by default but bypassed by
// force_bypass_hours (soft override), the lock-held overlap pre-check with a
// NULL-player-safe conflict detail (manual→guest_name, block→null), guest
// persistence, and ownership scoping. Recurrence-specific behaviour (the loop,
// all-or-nothing rollback, idempotency) lives in booking_recurring_test.go.
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run ManualWrite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

// createOneManual is a test helper for the single-occurrence path: it asserts
// exactly one booking is returned and hands back that row (or the error).
func (e *blockEnv) createOneManual(t *testing.T, p ManualBookingParams) (*models.Booking, error) {
	t.Helper()
	bs, replayed, err := e.repo.CreateManualBooking(context.Background(), p)
	if err != nil {
		return nil, err
	}
	if replayed {
		t.Fatalf("unexpected replay for a fresh single manual booking")
	}
	if len(bs) != 1 {
		t.Fatalf("expected exactly 1 booking, got %d", len(bs))
	}
	return &bs[0], nil
}

// ── operating-hours gate: enforced by default, bypassed by the soft override ──

func TestManualWrite_OperatingHoursGateAndSoftOverride(t *testing.T) {
	e := newBlockEnv(t)
	date := time.Now().In(timeutil.Amman()).AddDate(0, 0, 3)
	wd := int(date.Weekday())
	if err := e.model.ReplaceOperatingHours(context.Background(), int(e.pitchID), e.ownerActor(),
		[]data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "12:00"}}); err != nil {
		t.Fatalf("set hours: %v", err)
	}
	y, m, d := date.Date()
	loc := timeutil.Amman()
	outStart := time.Date(y, m, d, 20, 0, 0, 0, loc) // 20:00–21:00, outside 09–12
	outEnd := outStart.Add(time.Hour)

	// Gate ON → rejected with an outside_hours RecurrenceConflictError naming week 1.
	_, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: outStart, EndTime: outEnd,
		GuestName: "خارج الدوام",
	})
	var rec *RecurrenceConflictError
	if !errors.As(err, &rec) || rec.Reason != "outside_hours" {
		t.Fatalf("out-of-hours manual (gate on): err = %v, want outside_hours RecurrenceConflictError", err)
	}
	if rec.Week != 1 {
		t.Errorf("failing week = %d, want 1", rec.Week)
	}

	// Soft override → accepted.
	b, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: outStart, EndTime: outEnd,
		GuestName: "تجاوز الدوام", BypassHours: true,
	})
	if err != nil {
		t.Fatalf("out-of-hours manual (force_bypass_hours): want success, got %v", err)
	}
	if b.Source != models.SourceManual || b.PlayerID != nil {
		t.Fatalf("source=%q player_id=%v, want manual/nil", b.Source, b.PlayerID)
	}
	if b.GuestName == nil || *b.GuestName != "تجاوز الدوام" {
		t.Fatalf("guest_name = %v, want \"تجاوز الدوام\"", b.GuestName)
	}
	if b.TotalPrice <= 0 {
		t.Errorf("total_price = %v, want a priced walk-in (>0)", b.TotalPrice)
	}
}

// ── in-hours manual succeeds and persists the guest ──────────────────────────

func TestManualWrite_InHoursPersistsGuest(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(110)
	end := start.Add(time.Hour)
	phone := "+962790000111"
	b, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
		GuestName: "أبو محمد", GuestPhone: phone,
	})
	if err != nil {
		t.Fatalf("manual booking (no schedule → open 24/7): %v", err)
	}
	if b.GuestPhone == nil || *b.GuestPhone != phone {
		t.Errorf("guest_phone = %v, want %q", b.GuestPhone, phone)
	}
	if b.RecurrenceGroupID != nil {
		t.Errorf("one-off booking recurrence_group_id = %v, want nil", *b.RecurrenceGroupID)
	}
}

// ── CRITICAL: manual-over-manual conflict carries guest_name (NULL player) ────

func TestManualWrite_ConflictWithManualCarriesGuestName(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(120)
	end := start.Add(time.Hour)
	if _, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
		GuestName: "ضيف أول",
	}); err != nil {
		t.Fatalf("seed first manual: %v", err)
	}
	_, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
		GuestName: "ضيف ثانٍ",
	})
	var rec *RecurrenceConflictError
	if !errors.As(err, &rec) || rec.Reason != "conflict" {
		t.Fatalf("err = %v, want conflict RecurrenceConflictError", err)
	}
	if len(rec.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(rec.Conflicts))
	}
	c := rec.Conflicts[0]
	if c.Source != models.SourceManual {
		t.Errorf("conflict source = %q, want manual", c.Source)
	}
	// The null-player formatter fix: a manual conflict (player_id NULL) must fall
	// back to guest_name, not crash or return null.
	if c.PlayerName == nil || *c.PlayerName != "ضيف أول" {
		t.Errorf("conflict player_name = %v, want guest \"ضيف أول\"", c.PlayerName)
	}
}

// ── manual over a block → 409, block conflict carries null name ──────────────

func TestManualWrite_ConflictWithBlockHasNullName(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(130)
	end := start.Add(time.Hour)
	if _, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
	}); err != nil {
		t.Fatalf("seed block: %v", err)
	}
	_, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
		GuestName: "ضيف",
	})
	var rec *RecurrenceConflictError
	if !errors.As(err, &rec) {
		t.Fatalf("err = %v, want RecurrenceConflictError", err)
	}
	if len(rec.Conflicts) != 1 || rec.Conflicts[0].Source != models.SourceBlock {
		t.Fatalf("conflicts = %+v, want one block conflict", rec.Conflicts)
	}
	if rec.Conflicts[0].PlayerName != nil {
		t.Errorf("block conflict player_name = %v, want nil", *rec.Conflicts[0].PlayerName)
	}
}

// ── manual over a player booking carries the player's name ───────────────────

func TestManualWrite_ConflictWithPlayerCarriesPlayerName(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(140)
	end := start.Add(time.Hour)
	if _, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: end,
	}); err != nil {
		t.Fatalf("seed player booking: %v", err)
	}
	_, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
		GuestName: "ضيف",
	})
	var rec *RecurrenceConflictError
	if !errors.As(err, &rec) {
		t.Fatalf("err = %v, want RecurrenceConflictError", err)
	}
	c := rec.Conflicts[0]
	if c.Source != models.SourcePlayer || c.PlayerName == nil || *c.PlayerName != "BK Player" {
		t.Errorf("conflict = {source:%q name:%v}, want player/\"BK Player\"", c.Source, c.PlayerName)
	}
}

// ── foreign owner cannot log a walk-in on another owner's pitch (→ 404) ───────

func TestManualWrite_ForeignOwnerGets404(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(150)
	_, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: auth.Actor{UserID: int(e.otherID), Role: auth.RoleOwner},
		StartTime: start, EndTime: start.Add(time.Hour), GuestName: "ضيف",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign owner manual: err = %v, want pgx.ErrNoRows (→404)", err)
	}
}

// ── manual cancels through the standard owner CancelBooking path ─────────────

func TestManualWrite_CancelThroughStandardOwnerPath(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(160)
	b, err := e.createOneManual(t, ManualBookingParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: start.Add(time.Hour),
		GuestName: "ضيف للإلغاء",
	})
	if err != nil {
		t.Fatalf("create manual: %v", err)
	}
	ownerID := e.ownerID
	cancelled, err := e.repo.CancelBooking(context.Background(), CancelBookingParams{
		BookingID: b.ID, ActorID: &ownerID, ActorRole: ActorOwner,
	})
	if err != nil {
		t.Fatalf("owner cancel of manual booking: %v", err)
	}
	if cancelled.Status != models.StatusCancelled {
		t.Fatalf("status = %q, want cancelled", cancelled.Status)
	}
	if _, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: start.Add(time.Hour),
	}); err != nil {
		t.Fatalf("player booking after manual cancel should succeed, got %v", err)
	}
}
