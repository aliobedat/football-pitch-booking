package repository

// WO-MONITORING-V1 Gate 1: the smallest read-only admin monitoring repository.
// Every query here reuses existing tables/conventions — no new table, no new
// index, no security_events. Admin-only (unscoped), matching the existing
// GetAllBookings admin branch.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/notification"
)

// MonitoringBookingSummary tallies today's (selected-date's) bookings by status.
type MonitoringBookingSummary struct {
	Total     int
	Pending   int
	Confirmed int
	Rejected  int
	Completed int
	Cancelled int
	NoShow    int
}

// MonitoringBookingRow is one row of the recent-bookings table. Phone is
// PRE-MASKED by the repository — the handler/frontend never see the raw value.
type MonitoringBookingRow struct {
	ID                 int64
	CreatedAt          time.Time
	ContactName        string
	ContactPhoneMasked string
	VenueID            int64
	VenueName          string
	PitchID            int64
	PitchName          string
	StartTime          time.Time
	EndTime            time.Time
	Status             string
}

// MonitoringWhatsAppUsage mirrors the actual quota-guard enforcement counter —
// same wabaID, same UTC send_date bucket (outbox.QuotaStore.Reserve semantics).
type MonitoringWhatsAppUsage struct {
	Count   int
	Cap     int
	Warning bool
}

// MonitoringJobCounts groups notification_jobs by status, splitting 'pending'
// into true-pending (attempts=0) and retrying (attempts>0).
type MonitoringJobCounts struct {
	Pending    int
	Retrying   int
	Processing int
	Succeeded  int
	DeadLetter int
	Blocked    int
}

// MonitoringFailedJob is one recent dead_letter/blocked job. Recipient is
// PRE-MASKED and LastError is reduced to a safe category — the raw error
// string and raw recipient never leave the repository layer.
type MonitoringFailedJob struct {
	Kind            string
	Status          string
	Attempts        int
	FailureCategory string
	RecipientMasked string
	UpdatedAt       time.Time
}

// MonitoringFilter is the optional narrowing applied to the summary and the
// recent-bookings query alike.
type MonitoringFilter struct {
	VenueID int64  // 0 = all venues
	Status  string // "" = all statuses
}

type MonitoringRepository interface {
	BookingSummary(ctx context.Context, dayStart, dayEnd time.Time, filter MonitoringFilter) (MonitoringBookingSummary, error)
	RecentBookings(ctx context.Context, dayStart, dayEnd time.Time, filter MonitoringFilter, limit int) ([]MonitoringBookingRow, error)
	WhatsAppUsage(ctx context.Context, wabaID string, now time.Time) (MonitoringWhatsAppUsage, error)
	NotificationJobCounts(ctx context.Context) (MonitoringJobCounts, error)
	RecentFailedJobs(ctx context.Context, limit int) ([]MonitoringFailedJob, error)
}

type monitoringRepo struct {
	db *pgxpool.Pool
}

func NewMonitoringRepository(db *pgxpool.Pool) MonitoringRepository {
	return &monitoringRepo{db: db}
}

// monitoringFilterClause appends venue_id/status predicates onto args/where,
// starting numbering at len(args)+1. Shared by BookingSummary and RecentBookings
// so the two queries agree on exactly the same rows.
func monitoringFilterClause(args []any, filter MonitoringFilter) (string, []any) {
	where := ""
	if filter.VenueID > 0 {
		args = append(args, filter.VenueID)
		where += fmt.Sprintf(" AND p.venue_id = $%d", len(args))
	}
	if filter.Status != "" {
		args = append(args, filter.Status)
		where += fmt.Sprintf(" AND b.status = $%d", len(args))
	}
	return where, args
}

// BookingSummary groups bookings CREATED within [dayStart, dayEnd) by status.
// Admin is unscoped by design (no owner_id predicate) — this is a cross-tenant
// operational view, matching GetAllBookings' admin branch.
func (r *monitoringRepo) BookingSummary(ctx context.Context, dayStart, dayEnd time.Time, filter MonitoringFilter) (MonitoringBookingSummary, error) {
	var s MonitoringBookingSummary
	args := []any{dayStart, dayEnd}
	where, args := monitoringFilterClause(args, filter)

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT b.status, count(*)
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		WHERE b.created_at >= $1 AND b.created_at < $2%s
		GROUP BY b.status
	`, where), args...)
	if err != nil {
		return s, fmt.Errorf("MonitoringRepository.BookingSummary: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return s, fmt.Errorf("MonitoringRepository.BookingSummary: scan: %w", err)
		}
		s.Total += n
		switch status {
		case "pending":
			s.Pending = n
		case "confirmed":
			s.Confirmed = n
		case "rejected":
			s.Rejected = n
		case "completed":
			s.Completed = n
		case "cancelled":
			s.Cancelled = n
		case "no_show":
			s.NoShow = n
		}
	}
	return s, rows.Err()
}

// RecentBookings lists the most recent bookings created within the window,
// newest first, capped at limit. Contact name/phone use the snapshot-first
// COALESCE convention (contact_name/contact_phone before the mutable
// users/guest fields) already established by GetAllBookings. Phone is masked
// here, in the repository, before it ever reaches the handler.
func (r *monitoringRepo) RecentBookings(ctx context.Context, dayStart, dayEnd time.Time, filter MonitoringFilter, limit int) ([]MonitoringBookingRow, error) {
	args := []any{dayStart, dayEnd}
	where, args := monitoringFilterClause(args, filter)
	args = append(args, limit)

	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT
			b.id, b.created_at,
			COALESCE(b.contact_name, u.full_name, b.guest_name, '') AS contact_name,
			COALESCE(b.contact_phone, u.phone, b.guest_phone, '') AS contact_phone,
			COALESCE(p.venue_id, 0) AS venue_id, COALESCE(v.name, '') AS venue_name,
			b.pitch_id, COALESCE(`+pitchDisplayNameExpr+`, '') AS pitch_name,
			lower(b.booking_range) AS start_time, upper(b.booking_range) AS end_time,
			b.status
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		LEFT JOIN venues v ON v.id = p.venue_id
		LEFT JOIN users u ON u.id = b.player_id
		WHERE b.created_at >= $1 AND b.created_at < $2%s
		ORDER BY b.created_at DESC
		LIMIT $%d
	`, where, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("MonitoringRepository.RecentBookings: query: %w", err)
	}
	defer rows.Close()

	out := make([]MonitoringBookingRow, 0)
	for rows.Next() {
		var row MonitoringBookingRow
		var rawPhone string
		if err := rows.Scan(
			&row.ID, &row.CreatedAt,
			&row.ContactName, &rawPhone,
			&row.VenueID, &row.VenueName,
			&row.PitchID, &row.PitchName,
			&row.StartTime, &row.EndTime,
			&row.Status,
		); err != nil {
			return nil, fmt.Errorf("MonitoringRepository.RecentBookings: scan: %w", err)
		}
		row.ContactPhoneMasked = maskPhoneForMonitoring(rawPhone)
		out = append(out, row)
	}
	return out, rows.Err()
}

// WhatsAppUsage reads today's count from the SAME (wabaID, UTC send_date)
// bucket the quota guard's Reserve call writes to (outbox.QuotaStore), so the
// dashboard value can never drift from the actual enforcement counter. A
// missing row (no sends yet today) is zero, not an error.
func (r *monitoringRepo) WhatsAppUsage(ctx context.Context, wabaID string, now time.Time) (MonitoringWhatsAppUsage, error) {
	day := now.UTC().Truncate(24 * time.Hour) // matches outbox.QuotaStore.Reserve exactly

	var count int
	err := r.db.QueryRow(ctx, `
		SELECT count FROM waba_daily_sends WHERE waba_id = $1 AND send_date = $2
	`, wabaID, day).Scan(&count)
	if err != nil {
		if err == pgx.ErrNoRows {
			count = 0
		} else {
			return MonitoringWhatsAppUsage{}, fmt.Errorf("MonitoringRepository.WhatsAppUsage: %w", err)
		}
	}

	capVal := notification.WhatsAppQuotaHardCap()
	warnThreshold := notification.WhatsAppQuotaWarnThreshold()
	return MonitoringWhatsAppUsage{
		Count:   count,
		Cap:     capVal,
		Warning: count > warnThreshold,
	}, nil
}

// NotificationJobCounts groups notification_jobs by status, splitting pending
// (attempts=0) from retrying (attempts>0, still 'pending' status — a job is
// only re-queued to 'pending' after a transient failure, per outbox/postgres.go).
func (r *monitoringRepo) NotificationJobCounts(ctx context.Context) (MonitoringJobCounts, error) {
	var c MonitoringJobCounts
	rows, err := r.db.Query(ctx, `
		SELECT status, (attempts > 0) AS retried, count(*)
		FROM notification_jobs
		GROUP BY status, retried
	`)
	if err != nil {
		return c, fmt.Errorf("MonitoringRepository.NotificationJobCounts: query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var status string
		var retried bool
		var n int
		if err := rows.Scan(&status, &retried, &n); err != nil {
			return c, fmt.Errorf("MonitoringRepository.NotificationJobCounts: scan: %w", err)
		}
		switch status {
		case "pending":
			if retried {
				c.Retrying += n
			} else {
				c.Pending += n
			}
		case "processing":
			c.Processing += n
		case "succeeded":
			c.Succeeded += n
		case "dead_letter":
			c.DeadLetter += n
		case "blocked":
			c.Blocked += n
		}
	}
	return c, rows.Err()
}

// RecentFailedJobs returns the most recently updated dead_letter/blocked jobs,
// newest first, capped at limit. last_error and recipient are NEVER returned
// raw — only a safe failure category and a masked recipient.
func (r *monitoringRepo) RecentFailedJobs(ctx context.Context, limit int) ([]MonitoringFailedJob, error) {
	rows, err := r.db.Query(ctx, `
		SELECT kind, status, attempts, COALESCE(last_error, ''), recipient, updated_at
		FROM notification_jobs
		WHERE status IN ('dead_letter', 'blocked')
		ORDER BY updated_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("MonitoringRepository.RecentFailedJobs: query: %w", err)
	}
	defer rows.Close()

	out := make([]MonitoringFailedJob, 0)
	for rows.Next() {
		var j MonitoringFailedJob
		var rawError, rawRecipient string
		if err := rows.Scan(&j.Kind, &j.Status, &j.Attempts, &rawError, &rawRecipient, &j.UpdatedAt); err != nil {
			return nil, fmt.Errorf("MonitoringRepository.RecentFailedJobs: scan: %w", err)
		}
		j.FailureCategory = classifyNotificationFailure(rawError)
		j.RecipientMasked = maskPhoneForMonitoring(rawRecipient)
		out = append(out, j)
	}
	return out, rows.Err()
}

// classifyNotificationFailure maps an existing notification_jobs.last_error
// string to one of a small, safe set of categories. It NEVER returns the raw
// string, a provider payload, or provider response text — only substring
// matches against the typed sentinel error messages the notification package
// already produces (errors.New(...).Error() text, e.g.
// "notification/whatsapp: daily WABA send cap reached"). A small private
// mapping local to this file is preferred over a generic PII-redaction
// framework (WO-MONITORING-V1 Part 3).
func classifyNotificationFailure(rawError string) string {
	lower := strings.ToLower(rawError)
	switch {
	case rawError == "":
		return "unknown"
	case strings.Contains(lower, "daily waba send cap reached"):
		return "quota_exhausted"
	case strings.Contains(lower, "quota accounting unavailable"):
		return "quota_unavailable"
	case strings.Contains(lower, "paid whatsapp sending is disabled"):
		return "paid_whatsapp_disabled"
	case strings.Contains(lower, "opted out") || strings.Contains(lower, "invalid recipient") || strings.Contains(lower, "invalid phone"):
		return "invalid_recipient"
	case strings.Contains(lower, "delivery") || strings.Contains(lower, "provider") || strings.Contains(lower, "failed"):
		return "delivery_failed"
	default:
		return "unknown"
	}
}

// maskPhoneForMonitoring redacts everything but the last 4 digits (or fewer if
// the value is short), e.g. "+962790001234" -> "***1234". Distinct from
// notification.maskPhone (log-oriented, keeps the country-code prefix) because
// the monitoring DTO's masked_phone field is documented/tested as "***" + last
// 4 digits — an admin-facing display format, not a log-redaction format.
func maskPhoneForMonitoring(phone string) string {
	if phone == "" {
		return ""
	}
	if len(phone) <= 4 {
		return "***" + phone
	}
	return "***" + phone[len(phone)-4:]
}
