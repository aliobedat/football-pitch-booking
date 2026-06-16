package models

import "time"

// تعريف حالات الحجز عشان نستخدمها بشكل آمن
type BookingStatus string

const (
	StatusPending   BookingStatus = "pending"
	StatusConfirmed BookingStatus = "confirmed"
	StatusCancelled BookingStatus = "cancelled"
)

// BookingSource discriminates a bookings row: a player booking, an owner-created
// BLOCK (held time, no player), or — in PR 3 — an ACADEMY session. The
// anti-double-booking EXCLUDE is status-only, so non-cancelled rows of every
// source conflict with each other automatically. Invariant (DB CHECK):
// source == 'player'  ⟺  player_id IS NOT NULL.
type BookingSource string

const (
	SourcePlayer  BookingSource = "player"
	SourceAcademy BookingSource = "academy"
	SourceBlock   BookingSource = "block"
	// SourceManual is an owner-logged offline booking (walk-in / phone). It has no
	// platform player (player_id IS NULL) but names a guest (guest_name, DB CHECK:
	// source='manual' ⟹ guest_name IS NOT NULL). It shares the anti-double-booking
	// EXCLUDE, so online and offline bookings can never collide.
	SourceManual BookingSource = "manual"
)

// الهيكل الأساسي للحجز. PlayerID يطابق عمود player_id في قاعدة البيانات.
type Booking struct {
	ID         int64         `json:"id"`
	PitchID    int64         `json:"pitch_id"`
	PitchName  string        `json:"pitch_name,omitempty"`
	// PlayerID is a pointer because non-player rows (block, academy) have a NULL
	// player_id (DB CHECK: source='player' ⟺ player_id IS NOT NULL). For player
	// bookings it is always set.
	PlayerID   *int64        `json:"player_id"`
	StartTime  time.Time     `json:"start_time"`
	EndTime    time.Time     `json:"end_time"`
	Status     BookingStatus `json:"status"`
	Source     BookingSource `json:"source"`
	TotalPrice float64       `json:"total_price"`
	CreatedAt  time.Time     `json:"created_at"`

	// Guest identity — populated ONLY for manual (walk-in) rows; nil otherwise.
	GuestName  *string `json:"guest_name,omitempty"`
	GuestPhone *string `json:"guest_phone,omitempty"`

	// RecurrenceGroupID groups the occurrences of one recurring walk-in. nil for a
	// one-off booking; a shared UUID across every occurrence of a recurring series.
	RecurrenceGroupID *string `json:"recurrence_group_id,omitempty"`
}

// البيانات المطلوبة لإنشاء حجز جديد
type CreateBookingRequest struct {
	PitchID    int64     `json:"pitch_id" binding:"required"`
	StartTime  time.Time `json:"start_time" binding:"required"`
	EndTime    time.Time `json:"end_time" binding:"required"`
	TotalPrice float64   `json:"total_price" binding:"required"`

	PlayerID int64 `json:"-"` // يتم تعبئته من التوكن (مخفي عن المستخدم)

	// Idempotency is set by the handler from the Idempotency-Key request header
	// (nil when absent). When present, booking creation is routed through the
	// idempotent path so a double-tap / retry replays the original booking instead
	// of creating a second one. Never bound from the JSON body.
	Idempotency *IdempotencyParams `json:"-"`

	// BypassHoursGate exempts this write from the operating-hours containment gate
	// (locked decision #2: owner/admin-initiated writes are not bound by player
	// open-hours). The player POST /bookings path leaves it false, so player
	// bookings are always gated. It is the seam for the future owner/academy/block
	// write-paths (which do not exist yet); never bound from the JSON body.
	BypassHoursGate bool `json:"-"`
}

// IdempotencyParams carries everything the idempotent create path needs to claim,
// fingerprint, and (on replay) match a booking attempt. Keys are user-scoped (the
// user id is the request's PlayerID); Fingerprint is a hash of the canonical
// request so a reused key with a different body is rejected rather than replayed.
type IdempotencyParams struct {
	Key         string
	Endpoint    string
	Fingerprint string
}

// AdminBooking is the enriched booking record returned to owners/admins.
// It joins pitch and user data so the dashboard never needs a second request.
type AdminBooking struct {
	ID        int64  `json:"id"`
	PitchID   int64  `json:"pitch_id"`
	PitchName string `json:"pitch_name"`
	// PlayerID / user fields are NULL/empty for non-player rows (block, academy):
	// a block has no player, so the LEFT JOIN to users yields nothing. Source lets
	// the dashboard label the row distinctly and N/A the phone column.
	PlayerID   *int64        `json:"player_id"`
	UserName   string        `json:"user_name"`
	UserEmail  string        `json:"user_email"`
	UserPhone  string        `json:"user_phone"`
	StartTime  time.Time     `json:"start_time"`
	EndTime    time.Time     `json:"end_time"`
	Status     BookingStatus `json:"status"`
	Source     BookingSource `json:"source"`
	TotalPrice float64       `json:"total_price"`
	CreatedAt  time.Time     `json:"created_at"`

	// PaymentStatus is the cash-settlement marker (WO-F1): unpaid | paid_cash. The
	// dashboard's "Collected" figures sum total_price over paid_cash rows.
	PaymentStatus string `json:"payment_status"`

	// Guest identity for manual (walk-in) rows — the dashboard shows guest_name in
	// place of the (absent) player. nil for every non-manual source.
	GuestName  *string `json:"guest_name,omitempty"`
	GuestPhone *string `json:"guest_phone,omitempty"`

	// RecurrenceGroupID lets the dashboard branch recurring vs one-off walk-ins
	// (e.g. offer "cancel all future occurrences"). nil for one-off bookings.
	RecurrenceGroupID *string `json:"recurrence_group_id"`
}

// هيكل أوقات الفراغ (للاستعلام عن الحجوزات المتاحة)
type AvailabilitySlot struct {
	// BookingID is populated for internal use but NOT serialized: the availability
	// endpoint is public (browse funnel), so we expose only the busy time ranges +
	// status, never internal booking identifiers.
	BookingID int64         `json:"-"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Status    BookingStatus `json:"status"`
}