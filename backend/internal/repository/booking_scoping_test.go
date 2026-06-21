package repository

// Integration test for Admin-vs-Owner scoping of GetAllBookings. Exercises the
// REAL SQL ownership predicate (owner sees only bookings for pitches they own;
// admin sees all) against a live database. SKIPPED unless
// PITCH_SCOPING_TEST_DATABASE_URL is set, matching the other integration tests.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run BookingScoping

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

func TestBookingScoping_GetAllBookings_OwnerVsAdmin(t *testing.T) {
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping booking scoping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

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
	ownerA := mkUser("BS Owner A", "74", auth.RoleOwner)
	ownerB := mkUser("BS Owner B", "75", auth.RoleOwner)
	admin := mkUser("BS Admin", "76", auth.RoleAdmin)
	player := mkUser("BS Player", "77", auth.RolePlayer)

	pitchModel := &data.PitchModel{DB: pool}
	mkPitch := func(name string, owner int64) int64 {
		p, err := pitchModel.CreatePitch(ctx, data.CreatePitchRequest{
			Name: name, Neighborhood: "Amman", Surface: "artificial_grass",
			Format: "خماسي", PricePerHour: 30, OwnerID: int(owner),
		})
		if err != nil {
			t.Fatalf("seed pitch %s: %v", name, err)
		}
		return int64(p.ID)
	}
	pitchA := mkPitch("BS Pitch A", ownerA)
	pitchB := mkPitch("BS Pitch B", ownerB)

	mkBooking := func(pitch int64) {
		start := time.Now().UTC().Add(48 * time.Hour)
		if _, err := pool.Exec(ctx, `
			INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
			VALUES ($1,$2, tstzrange($3::timestamptz,$4::timestamptz,'[)'), 30, 'confirmed', 'player')
		`, pitch, player, start, start.Add(time.Hour)); err != nil {
			t.Fatalf("seed booking: %v", err)
		}
	}
	mkBooking(pitchA)
	mkBooking(pitchB)

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = ANY($1)`, []int64{pitchA, pitchB})
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = ANY($1)`, []int64{pitchA, pitchB})
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int64{ownerA, ownerB, admin, player})
	})

	repo := NewBookingRepository(pool)

	// Owner A sees only bookings for pitch A.
	aBookings, err := repo.GetAllBookings(ctx, auth.Actor{UserID: int(ownerA), Role: auth.RoleOwner}, nil, BookingFilter{})
	if err != nil {
		t.Fatalf("GetAllBookings owner A: %v", err)
	}
	for _, b := range aBookings {
		if b.PitchID == pitchB {
			t.Fatalf("owner A must not see bookings for owner B's pitch")
		}
	}
	if !hasBookingForPitch(aBookings, pitchA) {
		t.Fatalf("owner A must see their own pitch's booking")
	}

	// Admin sees bookings for both pitches.
	adminBookings, err := repo.GetAllBookings(ctx, auth.Actor{UserID: int(admin), Role: auth.RoleAdmin}, nil, BookingFilter{})
	if err != nil {
		t.Fatalf("GetAllBookings admin: %v", err)
	}
	if !hasBookingForPitch(adminBookings, pitchA) || !hasBookingForPitch(adminBookings, pitchB) {
		t.Fatalf("admin must see bookings for all pitches")
	}

	// Staff bound to pitch A sees ONLY pitch A's bookings — the scope predicate is
	// b.pitch_id = ANY(boundPitchIDs), independent of pitch ownership. Owner B's
	// pitch must never appear. (Unbound staff are 403'd by ResolveScope before the
	// handler, so that case is covered by the middleware's own tests, not here.)
	staff := mkUser("BS Staff", "78", auth.RoleStaff)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, staff)
	})
	staffBookings, err := repo.GetAllBookings(ctx, auth.Actor{UserID: int(staff), Role: auth.RoleStaff}, []int{int(pitchA)}, BookingFilter{})
	if err != nil {
		t.Fatalf("GetAllBookings staff: %v", err)
	}
	for _, b := range staffBookings {
		if b.PitchID == pitchB {
			t.Fatalf("staff bound to pitch A must not see bookings for pitch B")
		}
	}
	if !hasBookingForPitch(staffBookings, pitchA) {
		t.Fatalf("staff bound to pitch A must see pitch A's booking")
	}
}

func hasBookingForPitch(bs []models.AdminBooking, pitchID int64) bool {
	for _, b := range bs {
		if b.PitchID == pitchID {
			return true
		}
	}
	return false
}
