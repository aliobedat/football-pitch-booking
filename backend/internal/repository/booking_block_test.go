package repository

// Integration tests for the BLOCK write/cancel path (PR 2 Phase 3) against a live
// database. They prove: owner bypass of operating hours, the lock-held overlap
// pre-check with conflict detail (player + block), unblock releasing the slot,
// and ownership scoping (foreign owner → 404). SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL is set.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run BlockWrite

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type blockEnv struct {
	pool      *pgxpool.Pool
	repo      BookingRepository
	model     *data.PitchModel
	ownerID   int64
	otherID   int64 // a different owner (for the foreign-owner 404 case)
	playerID  int64
	pitchID   int64
}

func newBlockEnv(t *testing.T) *blockEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping block write integration test")
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
	ownerID := mk("BK Owner", "90", "owner")
	otherID := mk("BK Other", "91", "owner")
	playerID := mk("BK Player", "92", "player")

	model := &data.PitchModel{DB: pool}
	p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "BK Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}

	e := &blockEnv{
		pool: pool, repo: NewBookingRepository(pool), model: model,
		ownerID: ownerID, otherID: otherID, playerID: playerID, pitchID: int64(p.ID),
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM operating_hours WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{ownerID, otherID, playerID})
		pool.Close()
	})
	return e
}

func (e *blockEnv) ownerActor() auth.Actor {
	return auth.Actor{UserID: int(e.ownerID), Role: auth.RoleOwner}
}

// future returns an Amman-anchored future instant for slot ranges.
func (e *blockEnv) futureAt(hoursFromNow int) time.Time {
	return time.Now().In(timeutil.Amman()).Add(time.Duration(hoursFromNow) * time.Hour).Truncate(time.Minute)
}

// ── owner bypass: a block succeeds both inside AND outside operating hours ────

func TestBlockWrite_OwnerBypassesOperatingHours(t *testing.T) {
	e := newBlockEnv(t)
	// Configure tight hours so we can target both inside and outside.
	date := time.Now().In(timeutil.Amman()).AddDate(0, 0, 3)
	wd := int(date.Weekday())
	if err := e.model.ReplaceOperatingHours(context.Background(), int(e.pitchID), e.ownerActor(),
		[]data.OperatingWindow{{Weekday: wd, OpenTime: "09:00", CloseTime: "12:00"}}); err != nil {
		t.Fatalf("set hours: %v", err)
	}
	y, m, d := date.Date()
	loc := timeutil.Amman()
	inside := func() (time.Time, time.Time) { // 10:00–11:00, inside 09–12
		return time.Date(y, m, d, 10, 0, 0, 0, loc), time.Date(y, m, d, 11, 0, 0, 0, loc)
	}
	outside := func() (time.Time, time.Time) { // 20:00–21:00, outside hours
		return time.Date(y, m, d, 20, 0, 0, 0, loc), time.Date(y, m, d, 21, 0, 0, 0, loc)
	}

	insideStart, insideEnd := inside()
	outsideStart, outsideEnd := outside()
	for _, tc := range []struct {
		name       string
		start, end time.Time
	}{
		{"inside hours", insideStart, insideEnd},
		{"outside hours", outsideStart, outsideEnd},
	} {
		b, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
			PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: tc.start, EndTime: tc.end,
		})
		if err != nil {
			t.Fatalf("%s: block should succeed (owner bypasses hours), got %v", tc.name, err)
		}
		if b.Source != models.SourceBlock || b.PlayerID != nil {
			t.Fatalf("%s: source=%q player_id=%v, want block/nil", tc.name, b.Source, b.PlayerID)
		}
	}
}

// ── block over an existing PLAYER booking → 409 with detail incl. name ───────

func TestBlockWrite_ConflictWithPlayerBookingCarriesDetail(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(50)
	end := start.Add(time.Hour)
	// Seed a player booking via the real write-path.
	if _, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: end,
	}); err != nil {
		t.Fatalf("seed player booking: %v", err)
	}

	// Block overlapping it.
	_, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start.Add(30 * time.Minute), EndTime: end.Add(time.Hour),
	})
	var conflict *BlockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want *BlockConflictError", err)
	}
	if len(conflict.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(conflict.Conflicts))
	}
	c := conflict.Conflicts[0]
	if c.Source != models.SourcePlayer {
		t.Errorf("conflict source = %q, want player", c.Source)
	}
	if c.PlayerName == nil || *c.PlayerName != "BK Player" {
		t.Errorf("conflict player_name = %v, want \"BK Player\"", c.PlayerName)
	}
	if !c.StartTime.Equal(start.UTC()) {
		t.Errorf("conflict start = %s, want %s", c.StartTime, start.UTC())
	}
}

// ── block over an existing BLOCK → 409, player_name null ─────────────────────

func TestBlockWrite_ConflictWithBlockHasNullName(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(60)
	end := start.Add(time.Hour)
	if _, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
	}); err != nil {
		t.Fatalf("seed first block: %v", err)
	}
	_, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
	})
	var conflict *BlockConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want *BlockConflictError", err)
	}
	if len(conflict.Conflicts) != 1 || conflict.Conflicts[0].Source != models.SourceBlock {
		t.Fatalf("conflicts = %+v, want one block conflict", conflict.Conflicts)
	}
	if conflict.Conflicts[0].PlayerName != nil {
		t.Errorf("block conflict player_name = %v, want nil", *conflict.Conflicts[0].PlayerName)
	}
}

// ── unblock releases the slot: a later overlapping player booking succeeds ───

func TestBlockWrite_UnblockReleasesSlot(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(70)
	end := start.Add(time.Hour)
	blk, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID: e.pitchID, Actor: e.ownerActor(), StartTime: start, EndTime: end,
	})
	if err != nil {
		t.Fatalf("create block: %v", err)
	}

	// While blocked, a player booking on the same slot must conflict.
	if _, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: end,
	}); !errors.Is(err, ErrDoubleBooking) {
		t.Fatalf("player booking over a live block: err = %v, want ErrDoubleBooking", err)
	}

	// Unblock via the source-aware cancel path.
	ownerID := e.ownerID
	if _, err := e.repo.CancelBooking(context.Background(), CancelBookingParams{
		BookingID: blk.ID, ActorID: &ownerID, ActorRole: ActorOwner, RequireSource: "block",
	}); err != nil {
		t.Fatalf("unblock: %v", err)
	}

	// Now the slot is free — the player booking succeeds.
	if _, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: end,
	}); err != nil {
		t.Fatalf("player booking after unblock should succeed, got %v", err)
	}
}

// ── unblock refuses a non-block id (RequireSource) and a foreign owner ───────

func TestBlockWrite_UnblockRejectsNonBlock(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(80)
	// A player booking (source='player') — must not be cancellable via the block path.
	pb, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID: e.pitchID, PlayerID: e.playerID, StartTime: start, EndTime: start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("seed player booking: %v", err)
	}
	ownerID := e.ownerID
	if _, err := e.repo.CancelBooking(context.Background(), CancelBookingParams{
		BookingID: pb.ID, ActorID: &ownerID, ActorRole: ActorOwner, RequireSource: "block",
	}); !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("cancelling a player booking via the block path: err = %v, want ErrBookingNotFound (404)", err)
	}
}

// ── foreign owner cannot block another owner's pitch (→ 404) ─────────────────

func TestBlockWrite_ForeignOwnerGets404(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(90)
	_, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID:   e.pitchID,
		Actor:     auth.Actor{UserID: int(e.otherID), Role: auth.RoleOwner}, // not the pitch owner
		StartTime: start,
		EndTime:   start.Add(time.Hour),
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("foreign owner blocking: err = %v, want pgx.ErrNoRows (→404)", err)
	}
}

// ── admin can block any pitch (unscoped) ─────────────────────────────────────

func TestBlockWrite_AdminCanBlockAnyPitch(t *testing.T) {
	e := newBlockEnv(t)
	start := e.futureAt(100)
	b, err := e.repo.CreateBlock(context.Background(), CreateBlockParams{
		PitchID:   e.pitchID,
		Actor:     auth.Actor{UserID: int(e.otherID), Role: auth.RoleAdmin}, // admin, not owner
		StartTime: start,
		EndTime:   start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("admin block: %v", err)
	}
	if b.Source != models.SourceBlock {
		t.Fatalf("source = %q, want block", b.Source)
	}
}
