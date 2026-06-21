package repository

// §5.8 — review flagging bounds. Any authenticated role may flag (anti-grief),
// but a flag must ONLY record the report: it flips is_flagged and must NOT mutate
// the review's content (rating/comment), must NOT change visibility (deleted_at
// stays NULL — policy: flagged reviews stay visible until an admin soft-deletes),
// and repeated flags must NOT escalate into any state change (no flag-count auto-
// hide). Confirmed contract from reviewRepo.FlagReview: SET is_flagged=true only.
//
// SKIPPED unless REVIEWS_TEST_DATABASE_URL (reviews need migration 013).
//
//	REVIEWS_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run ReviewFlagBounds -v

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/models"
)

func TestReviewFlagBounds(t *testing.T) {
	dsn := os.Getenv("REVIEWS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("REVIEWS_TEST_DATABASE_URL not set; skipping review-flag bounds test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	repo := NewReviewRepository(pool)
	suffix := time.Now().UnixNano() % 1_000_000
	mkUser := func(name, prefix, role string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, fmt.Sprintf("+962%s%06d", prefix, suffix), role).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	owner := mkUser("FB Owner", "44", auth.RoleOwner)
	player := mkUser("FB Player", "45", auth.RolePlayer)

	pm := &data.PitchModel{DB: pool}
	pitch, err := pm.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "FB Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(owner),
	})
	if err != nil {
		t.Fatalf("seed pitch: %v", err)
	}
	pitchID := int64(pitch.ID)

	// A PAST confirmed booking → makes the player eligible to review.
	start := time.Now().Add(-6 * time.Hour)
	var bookingID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1,$2, tstzrange($3::timestamptz,$4::timestamptz,'[)'), 30, 'confirmed', 'player')
		RETURNING id
	`, pitchID, player, start, start.Add(time.Hour)).Scan(&bookingID); err != nil {
		t.Fatalf("seed booking: %v", err)
	}

	comment := "great pitch"
	rv, err := repo.CreateReview(ctx, models.CreateReviewRequest{
		PitchID: pitchID, PlayerID: player, QualifyingBookingID: bookingID,
		Rating: 4, Comment: &comment,
	})
	if err != nil {
		t.Fatalf("create review: %v", err)
	}

	t.Cleanup(func() {
		cctx, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(cctx, `DELETE FROM reviews WHERE pitch_id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{owner, player})
	})

	// content snapshots the flag must never touch.
	type snap struct {
		rating   int
		comment  string
		flagged  bool
		deleted  bool
	}
	read := func() snap {
		var s snap
		var del *time.Time
		var cmt *string
		if err := pool.QueryRow(ctx,
			`SELECT rating, comment, is_flagged, deleted_at FROM reviews WHERE id=$1`, rv.ID).
			Scan(&s.rating, &cmt, &s.flagged, &del); err != nil {
			t.Fatalf("read review: %v", err)
		}
		if cmt != nil {
			s.comment = *cmt
		}
		s.deleted = del != nil
		return s
	}

	before := read()
	if before.rating != 4 || before.comment != comment || before.flagged || before.deleted {
		t.Fatalf("seeded review unexpected: %+v", before)
	}

	// First flag: is_flagged → true; content + visibility untouched.
	if err := repo.FlagReview(ctx, rv.ID); err != nil {
		t.Fatalf("first flag: %v", err)
	}
	afterFirst := read()
	if !afterFirst.flagged {
		t.Fatalf("after flag: is_flagged = false, want true")
	}
	if afterFirst.rating != before.rating || afterFirst.comment != before.comment {
		t.Fatalf("CRITICAL: flag mutated content: rating %d→%d comment %q→%q",
			before.rating, afterFirst.rating, before.comment, afterFirst.comment)
	}
	if afterFirst.deleted {
		t.Fatalf("CRITICAL: flag hid the review (deleted_at set) — policy is visible-until-admin")
	}

	// Repeated flags must NOT escalate: still just is_flagged=true, still visible,
	// content still intact (no flag-count-driven auto-hide).
	for i := range 3 {
		if err := repo.FlagReview(ctx, rv.ID); err != nil {
			t.Fatalf("repeat flag %d: %v", i, err)
		}
	}
	afterRepeat := read()
	if !afterRepeat.flagged || afterRepeat.deleted ||
		afterRepeat.rating != before.rating || afterRepeat.comment != before.comment {
		t.Fatalf("CRITICAL: repeated flags escalated state: %+v (want flagged, visible, content intact)", afterRepeat)
	}
}
