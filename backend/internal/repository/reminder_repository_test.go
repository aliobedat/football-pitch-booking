package repository

// Integration tests for the PART 7 reminder claim. They exercise the REAL SQL —
// including the eligibility window and the SELECT ... FOR UPDATE SKIP LOCKED
// concurrency guard — against a live database, and are SKIPPED unless
// REMINDER_TEST_DATABASE_URL is set, so the default `go test ./...` run (and CI
// without a database) stays green. The in-memory worker tests in internal/booking
// cover the orchestration semantics; these confirm the SQL honours them.
//
// To run: point REMINDER_TEST_DATABASE_URL at a database with migrations
// 002–006 applied:
//
//	REMINDER_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Reminder

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/testutil"
)

// reminderTestEnv is a seeded owner/player/pitch and a connection, with cleanup.
type reminderTestEnv struct {
	pool     *pgxpool.Pool
	repo     ReminderRepository
	pitchID  int64
	playerID int64
	phone    string
}

func newReminderTestEnv(t *testing.T) *reminderTestEnv {
	t.Helper()
	dsn := os.Getenv("REMINDER_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("REMINDER_TEST_DATABASE_URL not set; skipping Postgres reminder integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	// Unique marker so parallel/leftover runs never collide.
	suffix := testutil.UniqueSuffix() % 1_000_000
	phone := fmt.Sprintf("+96279%06d", suffix)
	ownerPhone := fmt.Sprintf("+96278%06d", suffix)

	var playerID, ownerID, venueID, pitchID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ('Reminder Player', $1, 'player', TRUE) RETURNING id
	`, phone).Scan(&playerID); err != nil {
		pool.Close()
		t.Fatalf("seed player: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ('Reminder Owner', $1, 'owner', TRUE) RETURNING id
	`, ownerPhone).Scan(&ownerID); err != nil {
		pool.Close()
		t.Fatalf("seed owner: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO venues (owner_id, name, slug, neighborhood, maps_url)
		VALUES ($1, 'Reminder Venue', $2, 'Amman', '') RETURNING id
	`, ownerID, fmt.Sprintf("reminder-venue-%06d", suffix)).Scan(&venueID); err != nil {
		pool.Close()
		t.Fatalf("seed venue: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO pitches (name, neighborhood, surface, format, price_per_hour, owner_id, venue_id)
		VALUES ('Reminder Pitch', 'Amman', 'artificial_grass', 'خماسي', 30, $1, $2) RETURNING id
	`, ownerID, venueID).Scan(&pitchID); err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}

	env := &reminderTestEnv{
		pool: pool, repo: NewReminderRepository(pool),
		pitchID: pitchID, playerID: playerID, phone: phone,
	}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM notification_jobs WHERE recipient = $1`, phone)
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM venues WHERE id = $1`, venueID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id IN ($1, $2)`, playerID, ownerID)
		pool.Close()
	})
	return env
}

// seedBooking inserts a confirmed booking starting at the given offset from now.
func (e *reminderTestEnv) seedBooking(t *testing.T, startOffset time.Duration) int64 {
	t.Helper()
	ctx := context.Background()
	start := time.Now().UTC().Add(startOffset)
	end := start.Add(time.Hour)
	var id int64
	if err := e.pool.QueryRow(ctx, `
		INSERT INTO bookings (pitch_id, player_id, booking_range, total_price, status, source)
		VALUES ($1, $2, tstzrange($3::timestamptz, $4::timestamptz, '[)'), 30, 'confirmed', 'player')
		RETURNING id
	`, e.pitchID, e.playerID, start, end).Scan(&id); err != nil {
		t.Fatalf("seed booking (offset %s): %v", startOffset, err)
	}
	return id
}

// buildEnvelope is a production-shaped ReminderBuildFunc for the tests.
func buildEnvelope(d DueReminder) (ReminderJob, error) {
	env, err := notification.MarshalOutbound(notification.OutboundMessage{
		Recipient: d.Phone,
		Kind:      notification.KindBookingReminder,
		Payload: notification.BookingReminderPayload{
			BookingID: d.BookingID, PitchName: d.PitchName,
			StartTime: d.StartTime, EndTime: d.EndTime,
		},
	})
	if err != nil {
		return ReminderJob{}, err
	}
	return ReminderJob{
		Recipient: d.Phone, Kind: string(notification.KindBookingReminder),
		Envelope: env, MaxAttempts: 5,
	}, nil
}

func (e *reminderTestEnv) reminderSent(t *testing.T, id int64) bool {
	t.Helper()
	var sent bool
	if err := e.pool.QueryRow(context.Background(),
		`SELECT reminder_sent FROM bookings WHERE id = $1`, id).Scan(&sent); err != nil {
		t.Fatalf("read reminder_sent: %v", err)
	}
	return sent
}

func (e *reminderTestEnv) jobCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM notification_jobs WHERE recipient = $1 AND kind = 'booking_reminder'`,
		e.phone).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	return n
}

// TestReminder_ClaimsOnlyEligible seeds bookings in every window and asserts only
// the ones starting in (now, now+24h] are claimed, marked, and enqueued.
func TestReminder_ClaimsOnlyEligible(t *testing.T) {
	env := newReminderTestEnv(t)

	eligibleSoon := env.seedBooking(t, 2*time.Hour)  // eligible
	eligibleEdge := env.seedBooking(t, 23*time.Hour) // eligible
	beyond := env.seedBooking(t, 30*time.Hour)       // beyond horizon
	past := env.seedBooking(t, -2*time.Hour)         // already started

	n, err := env.repo.ClaimDueReminders(context.Background(), time.Now(), 24*time.Hour, 100, buildEnvelope)
	if err != nil {
		t.Fatalf("ClaimDueReminders: %v", err)
	}
	if n != 2 {
		t.Fatalf("claimed = %d, want 2", n)
	}

	if !env.reminderSent(t, eligibleSoon) || !env.reminderSent(t, eligibleEdge) {
		t.Error("eligible bookings should be marked reminder_sent")
	}
	if env.reminderSent(t, beyond) || env.reminderSent(t, past) {
		t.Error("ineligible bookings must NOT be marked reminder_sent")
	}
	if got := env.jobCount(t); got != 2 {
		t.Errorf("outbox jobs = %d, want 2", got)
	}
}

// TestReminder_ExactlyOnce confirms a second claim over the same data enqueues
// nothing more — the reminder_sent guard makes the claim idempotent.
func TestReminder_ExactlyOnce(t *testing.T) {
	env := newReminderTestEnv(t)
	env.seedBooking(t, 3*time.Hour)

	if _, err := env.repo.ClaimDueReminders(context.Background(), time.Now(), 24*time.Hour, 100, buildEnvelope); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	n, err := env.repo.ClaimDueReminders(context.Background(), time.Now(), 24*time.Hour, 100, buildEnvelope)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if n != 0 {
		t.Errorf("second claim = %d, want 0", n)
	}
	if got := env.jobCount(t); got != 1 {
		t.Errorf("outbox jobs = %d, want 1 (exactly once)", got)
	}
}

// TestReminder_ConcurrentClaimsSkipLocked runs several claimers concurrently and
// asserts the SKIP LOCKED partitioning never double-sends: every eligible booking
// is enqueued exactly once across all workers.
func TestReminder_ConcurrentClaimsSkipLocked(t *testing.T) {
	env := newReminderTestEnv(t)

	const eligible = 12
	for i := 0; i < eligible; i++ {
		env.seedBooking(t, time.Duration(i+1)*time.Hour) // all within 24h
	}

	const workers = 4
	var wg sync.WaitGroup
	var mu sync.Mutex
	total := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Small batch so the work is genuinely shared across claimers.
			n, err := env.repo.ClaimDueReminders(context.Background(), time.Now(), 24*time.Hour, 5, buildEnvelope)
			if err != nil {
				t.Errorf("concurrent claim: %v", err)
				return
			}
			mu.Lock()
			total += n
			mu.Unlock()
		}()
	}
	wg.Wait()

	// A single pass with small batches may not drain everything; mop up the rest.
	for {
		n, err := env.repo.ClaimDueReminders(context.Background(), time.Now(), 24*time.Hour, 100, buildEnvelope)
		if err != nil {
			t.Fatalf("drain claim: %v", err)
		}
		if n == 0 {
			break
		}
		total += n
	}

	if total != eligible {
		t.Errorf("total claimed across workers = %d, want %d", total, eligible)
	}
	if got := env.jobCount(t); got != eligible {
		t.Errorf("outbox jobs = %d, want %d (no duplicates)", got, eligible)
	}
}
