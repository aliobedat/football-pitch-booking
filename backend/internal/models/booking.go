package models

import "time"

// تعريف حالات الحجز عشان نستخدمها بشكل آمن
type BookingStatus string

const (
	StatusPending   BookingStatus = "pending"
	StatusConfirmed BookingStatus = "confirmed"
	StatusCancelled BookingStatus = "cancelled"
)

// الهيكل الأساسي للحجز. PlayerID يطابق عمود player_id في قاعدة البيانات.
type Booking struct {
	ID         int64         `json:"id"`
	PitchID    int64         `json:"pitch_id"`
	PitchName  string        `json:"pitch_name,omitempty"`
	PlayerID   int64         `json:"player_id"`
	StartTime  time.Time     `json:"start_time"`
	EndTime    time.Time     `json:"end_time"`
	Status     BookingStatus `json:"status"`
	TotalPrice float64       `json:"total_price"`
	CreatedAt  time.Time     `json:"created_at"`
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
	ID         int64         `json:"id"`
	PitchID    int64         `json:"pitch_id"`
	PitchName  string        `json:"pitch_name"`
	PlayerID   int64         `json:"player_id"`
	UserName   string        `json:"user_name"`
	UserEmail  string        `json:"user_email"`
	StartTime  time.Time     `json:"start_time"`
	EndTime    time.Time     `json:"end_time"`
	Status     BookingStatus `json:"status"`
	TotalPrice float64       `json:"total_price"`
	CreatedAt  time.Time     `json:"created_at"`
}

// هيكل أوقات الفراغ (للاستعلام عن الحجوزات المتاحة)
type AvailabilitySlot struct {
	BookingID int64         `json:"booking_id"`
	StartTime time.Time     `json:"start_time"`
	EndTime   time.Time     `json:"end_time"`
	Status    BookingStatus `json:"status"`
}