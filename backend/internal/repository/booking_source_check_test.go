package repository

// Integration tests for the migration-016 invariant: the symmetric CHECK
// `(source = 'player') = (player_id IS NOT NULL)` and the source allowed-set.
// They exercise the REAL constraints against a live database (raw INSERTs), plus
// confirm the player write-path still inserts unchanged with source='player'.
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run SourceCheck

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
)

const pgCheckViolation = "23514"

type sourceCheckEnv struct {
	pool     *pgxpool.Pool
	repo     BookingRepository
	playerID int64
	pitchID  int64
}

func newSourceCheckEnv(t *testing.T) *sourceCheckEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping source CHECK integration test")
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
	var ownerID, playerID int64
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
	ownerID = mk("SC Owner", "86", "owner")
	playerID = mk("SC Player", "87", "player")

	model := &data.PitchModel{DB: pool}
	p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "SC Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}

	e := &sourceCheckEnv{pool: pool, repo: NewBookingRepository(pool), playerID: playerID, pitchID: int64(p.ID)}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, e.pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{ownerID, playerID})
		pool.Close()
	})
	return e
}

// rawInsert attempts a direct bookings INSERT (bypassing the repo) so the DB
// CHECK is what's under test, not the Go layer. playerID nil → NULL.
func (e *sourceCheckEnv) rawInsert(playerID *int64, source string) error {
	start := time.Now().UTC().Add(200 * time.Hour)
	_, err := e.pool.Exec(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), 30, 'confirmed', $5)
	`, e.pitchID, playerID, start, start.Add(time.Hour), source)
	return err
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgCheckViolation
}

// ── migration 018 re-tightened the relationship to the BRANCHY bidirectional
//    constraint (bookings_source_player_chk): source IN ('player','academy') ⟹
//    player_id IS NOT NULL. So a player row with NULL player_id is REJECTED again
//    (017's unidirectional relaxation was superseded by 018).

func TestSourceCheck_PlayerRequiresPlayerID(t *testing.T) {
	e := newSourceCheckEnv(t)
	if err := e.rawInsert(nil, "player"); !isCheckViolation(err) {
		t.Fatalf("player row with NULL player_id: err = %v, want check_violation (23514) — branchy constraint (018)", err)
	}
}

// ── CHECK still rejects a block row WITH a non-null player_id ─────────────────

func TestSourceCheck_BlockForbidsPlayerID(t *testing.T) {
	e := newSourceCheckEnv(t)
	pid := e.playerID
	if err := e.rawInsert(&pid, "block"); !isCheckViolation(err) {
		t.Fatalf("block row with non-null player_id: err = %v, want check_violation (23514)", err)
	}
}

// ── CHECK rejects a manual row WITH a non-null player_id ──────────────────────

func TestSourceCheck_ManualForbidsPlayerID(t *testing.T) {
	e := newSourceCheckEnv(t)
	pid := e.playerID
	if err := e.rawInsert(&pid, "manual"); !isCheckViolation(err) {
		t.Fatalf("manual row with non-null player_id: err = %v, want check_violation (23514)", err)
	}
}

// ── CHECK rejects a manual row WITHOUT a guest_name ───────────────────────────
// (rawInsert never sets guest_name, so a NULL-player manual row trips the
// guest-required CHECK — proving guest_name is mandatory for walk-ins.)

func TestSourceCheck_ManualRequiresGuestName(t *testing.T) {
	e := newSourceCheckEnv(t)
	if err := e.rawInsert(nil, "manual"); !isCheckViolation(err) {
		t.Fatalf("manual row with NULL guest_name: err = %v, want check_violation (23514)", err)
	}
}

// ── a block row with NULL player_id is accepted ──────────────────────────────

func TestSourceCheck_BlockWithNullPlayerOK(t *testing.T) {
	e := newSourceCheckEnv(t)
	if err := e.rawInsert(nil, "block"); err != nil {
		t.Fatalf("block row with NULL player_id should insert, got %v", err)
	}
}

// ── the source allowed-set CHECK rejects an unknown source ───────────────────

func TestSourceCheck_UnknownSourceRejected(t *testing.T) {
	e := newSourceCheckEnv(t)
	if err := e.rawInsert(nil, "totally_invalid"); !isCheckViolation(err) {
		t.Fatalf("unknown source: err = %v, want check_violation (23514)", err)
	}
}

// ── the existing player write-path still inserts, tagged source='player' ─────

func TestSourceCheck_PlayerWritePathUnchanged(t *testing.T) {
	e := newSourceCheckEnv(t)
	start := time.Now().UTC().Add(150 * time.Hour)
	b, err := e.repo.CreateBooking(context.Background(), models.CreateBookingRequest{
		PitchID:   e.pitchID,
		PlayerID:  e.playerID,
		StartTime: start,
		EndTime:   start.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("player booking via repo should succeed, got %v", err)
	}
	if b.Source != models.SourcePlayer {
		t.Fatalf("source = %q, want player", b.Source)
	}
	if b.PlayerID == nil || *b.PlayerID != e.playerID {
		t.Fatalf("player_id = %v, want %d", b.PlayerID, e.playerID)
	}
}
