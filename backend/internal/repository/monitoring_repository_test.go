package repository

// Integration tests for the monitoring repository (WO-MONITORING-V1) against a
// live database. SKIPPED unless MONITORING_TEST_DATABASE_URL is set (same
// disposable-local-Postgres convention as the other repository integration
// tests — never run against production):
//
//	MONITORING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run Monitoring

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/testutil"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type monitoringEnv struct {
	pool     *pgxpool.Pool
	repo     MonitoringRepository
	ownerID  int64
	pitchID  int64
	venueID  int64
	pitchIDs []int64
	jobIDs   []int64
	wabaID   string
}

func newMonitoringEnv(t *testing.T) *monitoringEnv {
	t.Helper()
	dsn := os.Getenv("MONITORING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MONITORING_TEST_DATABASE_URL not set; skipping monitoring integration test")
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
	var ownerID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,'owner',TRUE) RETURNING id
	`, "MON Owner", fmt.Sprintf("+96279%07d", suffix)).Scan(&ownerID); err != nil {
		pool.Close()
		t.Fatalf("seed owner: %v", err)
	}

	model := &data.PitchModel{DB: pool}
	p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "MON Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}

	var venueID int64
	if err := pool.QueryRow(ctx, `SELECT venue_id FROM pitches WHERE id = $1`, p.ID).Scan(&venueID); err != nil {
		pool.Close()
		t.Fatalf("resolve seeded venue: %v", err)
	}

	e := &monitoringEnv{
		pool: pool, repo: NewMonitoringRepository(pool),
		ownerID: ownerID, pitchID: int64(p.ID), venueID: venueID,
		pitchIDs: []int64{int64(p.ID)},
		wabaID:   fmt.Sprintf("MON-WABA-%d", suffix),
	}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM notification_jobs WHERE id = ANY($1)`, e.jobIDs)
		_, _ = pool.Exec(cctx, `DELETE FROM waba_daily_sends WHERE waba_id = $1`, e.wabaID)
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = ANY($1)`, e.pitchIDs)
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = ANY($1)`, e.pitchIDs)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = ANY($1)`, e.pitchIDs)
		_, _ = pool.Exec(cctx, `DELETE FROM venues WHERE id = $1`, e.venueID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, ownerID)
		pool.Close()
	})
	return e
}

// seedBooking inserts a manual (walk-in) booking with an explicit created_at
// and contact snapshot so tests can control the exact monitoring row content.
func (e *monitoringEnv) seedBooking(t *testing.T, status string, createdAt, start, end time.Time, contactName, contactPhone string) int64 {
	t.Helper()
	var id int64
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO bookings (pitch_id, player_id, booking_range, status, source, total_price, guest_name, created_at, contact_name, contact_phone)
		VALUES ($1, NULL, tstzrange($2::timestamptz, $3::timestamptz, '[)'), $4, 'manual', 30, 'MON Guest', $5, $6, $7)
		RETURNING id
	`, e.pitchID, start, end, status, createdAt, contactName, contactPhone).Scan(&id)
	if err != nil {
		t.Fatalf("seed booking (%s): %v", status, err)
	}
	// created_at has a DEFAULT now() NOT NULL — force the requested value.
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE bookings SET created_at = $2 WHERE id = $1`, id, createdAt); err != nil {
		t.Fatalf("force created_at: %v", err)
	}
	return id
}

func (e *monitoringEnv) seedJob(t *testing.T, kind, status string, attempts int, lastError, recipient string) int64 {
	t.Helper()
	var id int64
	err := e.pool.QueryRow(context.Background(), `
		INSERT INTO notification_jobs (recipient, kind, envelope, status, attempts, last_error)
		VALUES ($1, $2, '{}'::jsonb, $3, $4, NULLIF($5, ''))
		RETURNING id
	`, recipient, kind, status, attempts, lastError).Scan(&id)
	if err != nil {
		t.Fatalf("seed job (%s/%s): %v", kind, status, err)
	}
	e.jobIDs = append(e.jobIDs, id)
	return id
}

func monitoringDay(offset int) (from, to time.Time) {
	anchor := time.Date(2032, 3, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, offset)
	return timeutil.AmmanDayBoundsUTC(anchor)
}

// 1. Booking summary groups every supported status correctly.
func TestMonitoring_BookingSummary_GroupsEveryStatus(t *testing.T) {
	e := newMonitoringEnv(t)
	from, to := monitoringDay(0)
	statuses := []string{"pending", "confirmed", "rejected", "completed", "cancelled", "no_show"}
	for i, s := range statuses {
		start := from.Add(time.Duration(i+1) * 2 * time.Hour)
		e.seedBooking(t, s, from.Add(time.Hour), start, start.Add(time.Hour), "P", "+962790000000")
	}

	summary, err := e.repo.BookingSummary(context.Background(), from, to, MonitoringFilter{})
	if err != nil {
		t.Fatalf("BookingSummary: %v", err)
	}
	if summary.Total != 6 {
		t.Errorf("total = %d, want 6", summary.Total)
	}
	if summary.Pending != 1 || summary.Confirmed != 1 || summary.Rejected != 1 ||
		summary.Completed != 1 || summary.Cancelled != 1 || summary.NoShow != 1 {
		t.Errorf("summary = %+v, want 1 in every bucket", summary)
	}
}

// 2. Selected Asia/Amman day bounds exclude records just outside the day.
func TestMonitoring_DayBounds_ExcludeJustOutside(t *testing.T) {
	e := newMonitoringEnv(t)
	from, to := monitoringDay(1)
	inside := from.Add(time.Minute)
	justBefore := from.Add(-time.Second)
	justAtEnd := to // half-open: to itself is excluded

	e.seedBooking(t, "confirmed", inside, inside, inside.Add(time.Hour), "In", "+962790000001")
	e.seedBooking(t, "confirmed", justBefore, justBefore.Add(2*time.Hour), justBefore.Add(3*time.Hour), "Before", "+962790000002")
	e.seedBooking(t, "confirmed", justAtEnd, justAtEnd.Add(4*time.Hour), justAtEnd.Add(5*time.Hour), "AtEnd", "+962790000003")

	summary, err := e.repo.BookingSummary(context.Background(), from, to, MonitoringFilter{})
	if err != nil {
		t.Fatalf("BookingSummary: %v", err)
	}
	if summary.Total != 1 {
		t.Fatalf("total = %d, want 1 (only the in-window row)", summary.Total)
	}

	rows, err := e.repo.RecentBookings(context.Background(), from, to, MonitoringFilter{}, 25)
	if err != nil {
		t.Fatalf("RecentBookings: %v", err)
	}
	if len(rows) != 1 || rows[0].ContactName != "In" {
		t.Fatalf("rows = %+v, want exactly the in-window booking", rows)
	}
}

// 3. Venue filter works.
func TestMonitoring_VenueFilter(t *testing.T) {
	e := newMonitoringEnv(t)
	from, to := monitoringDay(2)
	e.seedBooking(t, "confirmed", from.Add(time.Hour), from.Add(time.Hour), from.Add(2*time.Hour), "V", "+962790000004")

	matching, err := e.repo.BookingSummary(context.Background(), from, to, MonitoringFilter{VenueID: e.venueID})
	if err != nil {
		t.Fatalf("BookingSummary(matching venue): %v", err)
	}
	if matching.Total != 1 {
		t.Errorf("matching venue total = %d, want 1", matching.Total)
	}

	other, err := e.repo.BookingSummary(context.Background(), from, to, MonitoringFilter{VenueID: e.venueID + 999_000})
	if err != nil {
		t.Fatalf("BookingSummary(other venue): %v", err)
	}
	if other.Total != 0 {
		t.Errorf("other venue total = %d, want 0", other.Total)
	}
}

// 4. Status filter works.
func TestMonitoring_StatusFilter(t *testing.T) {
	e := newMonitoringEnv(t)
	from, to := monitoringDay(3)
	e.seedBooking(t, "confirmed", from.Add(time.Hour), from.Add(time.Hour), from.Add(2*time.Hour), "C", "+962790000005")
	e.seedBooking(t, "cancelled", from.Add(time.Hour), from.Add(3*time.Hour), from.Add(4*time.Hour), "X", "+962790000006")

	confirmedOnly, err := e.repo.BookingSummary(context.Background(), from, to, MonitoringFilter{Status: "confirmed"})
	if err != nil {
		t.Fatalf("BookingSummary: %v", err)
	}
	if confirmedOnly.Total != 1 || confirmedOnly.Confirmed != 1 || confirmedOnly.Cancelled != 0 {
		t.Errorf("confirmedOnly = %+v, want total=1 confirmed=1 cancelled=0", confirmedOnly)
	}
}

// 5. Recent bookings use the contact snapshot before mutable user/customer data.
// 6. Phone returned by the repository is masked.
func TestMonitoring_RecentBookings_SnapshotFirstAndMaskedPhone(t *testing.T) {
	e := newMonitoringEnv(t)
	from, to := monitoringDay(4)
	e.seedBooking(t, "confirmed", from.Add(time.Hour), from.Add(time.Hour), from.Add(2*time.Hour), "Snapshot Name", "+962791234567")

	rows, err := e.repo.RecentBookings(context.Background(), from, to, MonitoringFilter{}, 25)
	if err != nil {
		t.Fatalf("RecentBookings: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.ContactName != "Snapshot Name" {
		t.Errorf("contact_name = %q, want the contact_phone/contact_name snapshot", row.ContactName)
	}
	if strings.Contains(row.ContactPhoneMasked, "1234567") || row.ContactPhoneMasked == "+962791234567" {
		t.Fatalf("phone leaked unmasked: %q", row.ContactPhoneMasked)
	}
	if row.ContactPhoneMasked != "***4567" {
		t.Errorf("masked phone = %q, want ***4567", row.ContactPhoneMasked)
	}
	if row.VenueID != e.venueID {
		t.Errorf("venue_id = %d, want %d", row.VenueID, e.venueID)
	}
}

// 7. WABA usage matches the actual quota date bucket (outbox.QuotaStore's
// exact (wabaID, UTC-truncated send_date) semantics).
func TestMonitoring_WhatsAppUsage_MatchesQuotaBucket(t *testing.T) {
	e := newMonitoringEnv(t)
	now := time.Now()
	day := now.UTC().Truncate(24 * time.Hour)

	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO waba_daily_sends (waba_id, send_date, count) VALUES ($1, $2, 137)
	`, e.wabaID, day); err != nil {
		t.Fatalf("seed waba_daily_sends: %v", err)
	}

	usage, err := e.repo.WhatsAppUsage(context.Background(), e.wabaID, now)
	if err != nil {
		t.Fatalf("WhatsAppUsage: %v", err)
	}
	if usage.Count != 137 {
		t.Errorf("count = %d, want 137 (must match the exact enforcement bucket)", usage.Count)
	}
	if usage.Cap != 250 {
		t.Errorf("cap = %d, want 250 (from notification.WhatsAppQuotaHardCap)", usage.Cap)
	}
	if usage.Warning {
		t.Errorf("warning = true at count=137, want false (threshold is 200)")
	}
}

// 8. Count over 250 gives remaining=0 and blocked=true (proven at the DTO/
// handler-arithmetic level — remaining/blocked are computed by the handler,
// so this test proves the repository returns the raw count the handler needs).
func TestMonitoring_WhatsAppUsage_OverCap(t *testing.T) {
	e := newMonitoringEnv(t)
	now := time.Now()
	day := now.UTC().Truncate(24 * time.Hour)

	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO waba_daily_sends (waba_id, send_date, count) VALUES ($1, $2, 251)
	`, e.wabaID, day); err != nil {
		t.Fatalf("seed waba_daily_sends: %v", err)
	}

	usage, err := e.repo.WhatsAppUsage(context.Background(), e.wabaID, now)
	if err != nil {
		t.Fatalf("WhatsAppUsage: %v", err)
	}
	if usage.Count != 251 {
		t.Fatalf("count = %d, want 251", usage.Count)
	}
	remaining := max(usage.Cap-usage.Count, 0)
	if remaining != 0 {
		t.Errorf("remaining = %d, want 0", remaining)
	}
	if !(usage.Count > usage.Cap) {
		t.Errorf("count=%d cap=%d should compute blocked=true", usage.Count, usage.Cap)
	}
}

// 9. Notification pending versus retrying separation.
func TestMonitoring_NotificationJobCounts_PendingVsRetrying(t *testing.T) {
	e := newMonitoringEnv(t)
	e.seedJob(t, "booking_confirmed", "pending", 0, "", "+962790000010")
	e.seedJob(t, "booking_confirmed", "pending", 2, "provider 503", "+962790000011")
	e.seedJob(t, "booking_confirmed", "processing", 1, "", "+962790000012")
	e.seedJob(t, "booking_confirmed", "succeeded", 1, "", "+962790000013")
	e.seedJob(t, "booking_confirmed", "dead_letter", 5, "provider 500", "+962790000014")
	e.seedJob(t, "booking_confirmed", "blocked", 1, "opted out", "+962790000015")

	counts, err := e.repo.NotificationJobCounts(context.Background())
	if err != nil {
		t.Fatalf("NotificationJobCounts: %v", err)
	}
	if counts.Pending < 1 || counts.Retrying < 1 || counts.Processing < 1 ||
		counts.Succeeded < 1 || counts.DeadLetter < 1 || counts.Blocked < 1 {
		t.Errorf("counts = %+v, want at least 1 in every bucket seeded", counts)
	}
}

// 10. Recent failures exclude succeeded/pending jobs.
// 11. Raw last_error never appears in the response.
// 12. Raw recipient never appears in the response.
func TestMonitoring_RecentFailedJobs_ExcludesHealthyJobs_NeverLeaksRawFields(t *testing.T) {
	e := newMonitoringEnv(t)
	e.seedJob(t, "booking_confirmed", "succeeded", 1, "", "+962790000020")
	e.seedJob(t, "booking_confirmed", "pending", 0, "", "+962790000021")
	deadID := e.seedJob(t, "booking_confirmed", "dead_letter", 5,
		"notification/whatsapp: daily WABA send cap reached: count=251 cap=250", "+962790001234")
	blockedID := e.seedJob(t, "otp", "blocked", 1,
		"notification: recipient has opted out", "+962790005678")

	failures, err := e.repo.RecentFailedJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("RecentFailedJobs: %v", err)
	}

	if len(failures) < 2 {
		t.Fatalf("failures = %d, want at least the 2 seeded (dead_letter+blocked)", len(failures))
	}
	for _, f := range failures {
		if f.Status != "dead_letter" && f.Status != "blocked" {
			t.Fatalf("recent failures must only contain dead_letter/blocked, got status=%q", f.Status)
		}
		if strings.Contains(f.FailureCategory, "cap reached") || strings.Contains(f.FailureCategory, "opted out") {
			t.Fatalf("failure_category leaked raw error text: %q", f.FailureCategory)
		}
		if f.RecipientMasked == "+962790001234" || f.RecipientMasked == "+962790005678" {
			t.Fatalf("recipient leaked unmasked: %q", f.RecipientMasked)
		}
		if strings.HasPrefix(f.RecipientMasked, "+962") {
			t.Fatalf("masked recipient still starts with the raw E.164 prefix: %q", f.RecipientMasked)
		}
	}
	_ = deadID
	_ = blockedID

	// Specifically verify the quota-cap and opt-out rows classify safely.
	var quotaCategory, invalidRecipientCategory string
	for _, f := range failures {
		if f.Kind == "booking_confirmed" && f.Status == "dead_letter" {
			quotaCategory = f.FailureCategory
		}
		if f.Kind == "otp" && f.Status == "blocked" {
			invalidRecipientCategory = f.FailureCategory
		}
	}
	if quotaCategory != "quota_exhausted" {
		t.Errorf("quota-cap dead_letter category = %q, want quota_exhausted", quotaCategory)
	}
	if invalidRecipientCategory != "invalid_recipient" {
		t.Errorf("opted-out blocked category = %q, want invalid_recipient", invalidRecipientCategory)
	}
}
