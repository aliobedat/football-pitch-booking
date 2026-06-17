package repository

// Integration tests for the REAL eligibility derivation + FK/unique enforcement
// of the Verified Review System. These exercise live SQL (the "completed"
// condition the FK can NOT express, plus owner-exclusion, the partial unique
// index, and the composite FK backstop) against a database that already has
// migration 013 applied. SKIPPED unless REVIEWS_TEST_DATABASE_URL is set,
// matching the other repository integration tests.
//
//	REVIEWS_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Reviews

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
)

func TestReviews_EligibilityAndSecurity(t *testing.T) {
	dsn := os.Getenv("REVIEWS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("REVIEWS_TEST_DATABASE_URL not set; skipping reviews integration test")
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
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
		`, name, phone, role).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	owner := mkUser("RV Owner", "60", auth.RoleOwner)
	player := mkUser("RV Player", "61", auth.RolePlayer)
	other := mkUser("RV Other", "62", auth.RolePlayer)

	pitchModel := &data.PitchModel{DB: pool}
	pitch, err := pitchModel.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "RV Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(owner),
	})
	if err != nil {
		t.Fatalf("seed pitch: %v", err)
	}
	pitchID := int64(pitch.ID)

	// mkBooking inserts a booking with an explicit tstzrange + status.
	mkBooking := func(playerID int64, start, end time.Time, status string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
			VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), $5, $6::booking_status, 'player')
			RETURNING id
		`, pitchID, playerID, start, end, 30, status).Scan(&id); err != nil {
			t.Fatalf("seed booking: %v", err)
		}
		return id
	}

	now := time.Now()
	past := func(h int) (time.Time, time.Time) {
		return now.Add(time.Duration(-h-1) * time.Hour), now.Add(time.Duration(-h) * time.Hour)
	}
	future := func(h int) (time.Time, time.Time) {
		return now.Add(time.Duration(h) * time.Hour), now.Add(time.Duration(h+1) * time.Hour)
	}

	// ── Case 1: no booking history → not eligible ──────────────────────────────
	t.Run("NoHistory_NotEligible", func(t *testing.T) {
		e, err := repo.CheckEligibility(ctx, player, pitchID)
		if err != nil {
			t.Fatal(err)
		}
		if e.Eligible || e.QualifyingBookingID != nil {
			t.Fatalf("no history: eligible=%v bookingID=%v, want false/nil", e.Eligible, e.QualifyingBookingID)
		}
	})

	// ── Case 2: only a FUTURE booking → not eligible ───────────────────────────
	t.Run("FutureOnly_NotEligible", func(t *testing.T) {
		fs, fe := future(2)
		mkBooking(player, fs, fe, "confirmed")
		e, err := repo.CheckEligibility(ctx, player, pitchID)
		if err != nil {
			t.Fatal(err)
		}
		if e.Eligible {
			t.Fatalf("future-only: eligible=true, want false")
		}
	})

	// ── Case 3: a CANCELLED past booking → not eligible ────────────────────────
	t.Run("CancelledPast_NotEligible", func(t *testing.T) {
		ps, pe := past(10)
		mkBooking(player, ps, pe, "cancelled")
		e, err := repo.CheckEligibility(ctx, player, pitchID)
		if err != nil {
			t.Fatal(err)
		}
		if e.Eligible {
			t.Fatalf("cancelled past: eligible=true, want false")
		}
	})

	// ── Case 5/happy: a PAST confirmed booking → eligible, derived id ──────────
	var qualifyingBooking int64
	t.Run("PastConfirmed_Eligible", func(t *testing.T) {
		ps, pe := past(5)
		qualifyingBooking = mkBooking(player, ps, pe, "confirmed")
		e, err := repo.CheckEligibility(ctx, player, pitchID)
		if err != nil {
			t.Fatal(err)
		}
		if !e.Eligible || e.QualifyingBookingID == nil {
			t.Fatalf("past confirmed: eligible=%v id=%v, want true/non-nil", e.Eligible, e.QualifyingBookingID)
		}
		if *e.QualifyingBookingID != qualifyingBooking {
			t.Fatalf("derived booking id=%d, want %d", *e.QualifyingBookingID, qualifyingBooking)
		}
	})

	// ── Case 4: owner-exclusion (owner has a past booking but still barred) ─────
	t.Run("OwnerExclusion_NotEligible", func(t *testing.T) {
		ps, pe := past(8)
		mkBooking(owner, ps, pe, "confirmed")
		e, err := repo.CheckEligibility(ctx, owner, pitchID)
		if err != nil {
			t.Fatal(err)
		}
		if e.Eligible {
			t.Fatalf("owner self-review: eligible=true, want false")
		}
	})

	// ── Happy create + Case 5 duplicate (409 sentinel) ─────────────────────────
	var createdID int64
	t.Run("CreateThenDuplicate", func(t *testing.T) {
		rv, err := repo.CreateReview(ctx, models.CreateReviewRequest{
			PitchID: pitchID, PlayerID: player, QualifyingBookingID: qualifyingBooking,
			Rating: 5, Comment: ptrStr("ممتاز"),
		})
		if err != nil {
			t.Fatalf("first create: %v", err)
		}
		createdID = rv.ID

		_, err = repo.CreateReview(ctx, models.CreateReviewRequest{
			PitchID: pitchID, PlayerID: player, QualifyingBookingID: qualifyingBooking,
			Rating: 4,
		})
		if !errors.Is(err, ErrAlreadyReviewed) {
			t.Fatalf("duplicate create: err=%v, want ErrAlreadyReviewed", err)
		}
	})

	// ── Case 6: forged booking — claim PLAYER's booking under OTHER. The triple
	//    (qualifyingBooking, other, pitchID) matches no bookings row, so the
	//    composite FK fires (23503) → ErrReviewBookingInvalid (422). ───────────
	t.Run("ForgedBooking_FKBackstop", func(t *testing.T) {
		_, err := repo.CreateReview(ctx, models.CreateReviewRequest{
			PitchID: pitchID, PlayerID: other, QualifyingBookingID: qualifyingBooking,
			Rating: 3,
		})
		if !errors.Is(err, ErrReviewBookingInvalid) {
			t.Fatalf("forged booking: err=%v, want ErrReviewBookingInvalid", err)
		}
	})

	// ── Case 9: soft-delete then edit → ErrReviewNotFound (404) ────────────────
	t.Run("SoftDeleteThenEdit_NotFound", func(t *testing.T) {
		if err := repo.SoftDeleteReview(ctx, createdID); err != nil {
			t.Fatalf("soft delete: %v", err)
		}
		_, err := repo.UpdateReview(ctx, createdID, 2, nil)
		if !errors.Is(err, ErrReviewNotFound) {
			t.Fatalf("edit soft-deleted: err=%v, want ErrReviewNotFound", err)
		}
		// Slot freed: the player can review again after moderation.
		if _, err := repo.CreateReview(ctx, models.CreateReviewRequest{
			PitchID: pitchID, PlayerID: player, QualifyingBookingID: qualifyingBooking, Rating: 4,
		}); err != nil {
			t.Fatalf("re-review after soft delete: %v", err)
		}
	})
}

func ptrStr(s string) *string { return &s }
