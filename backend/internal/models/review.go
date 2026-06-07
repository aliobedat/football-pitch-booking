package models

import "time"

// Review is a Verified-Player review: 1-to-1 per (player, pitch), backed by a
// past, non-cancelled booking. Keys are int64 to match the rest of the schema
// (bookings/pitches/users use INTEGER PKs). DeletedAt is never serialised — a
// soft-deleted review is simply absent from public listings.
type Review struct {
	ID        int64      `json:"id"`
	PitchID   int64      `json:"pitch_id"`
	PlayerID  int64      `json:"player_id"`
	BookingID int64      `json:"booking_id"`
	Rating    int16      `json:"rating"`
	Comment   *string    `json:"comment,omitempty"`
	IsFlagged bool       `json:"is_flagged"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"-"`

	// ReviewerName is an optional display enrichment for public listings; it is
	// joined from users and is empty on the write paths.
	ReviewerName string `json:"reviewer_name,omitempty"`
}

// RatingAggregate is the lightweight summary returned by the dedicated
// aggregate query — never the full review list.
type RatingAggregate struct {
	Average float64 `json:"average"`
	Count   int64   `json:"count"`
}

// ReviewEligibility is the payload of the Derived eligibility check. When
// Eligible is false the booking id / existing review are nil. ExistingReview is
// populated when the player has already reviewed this pitch (drives the
// Write-vs-Edit decision on the client).
type ReviewEligibility struct {
	Eligible            bool    `json:"eligible"`
	QualifyingBookingID *int64  `json:"qualifying_booking_id"`
	ExistingReview      *Review `json:"existing_review"`
}

// CreateReviewRequest is the bound body for POST /pitches/:id/reviews. ONLY
// rating and comment are accepted from the client. PitchID and PlayerID are
// injected server-side (path param + JWT), and QualifyingBookingID is derived
// server-side from CheckEligibility — never trusted from the body. A client-sent
// qualifying_booking_id is deliberately NOT bound here, so it cannot influence
// which booking backs the review (the eligibility re-check is authoritative).
type CreateReviewRequest struct {
	PitchID             int64   `json:"-"`
	PlayerID            int64   `json:"-"`
	QualifyingBookingID int64   `json:"-"`
	Rating              int16   `json:"rating" binding:"required,min=1,max=5"`
	Comment             *string `json:"comment" binding:"omitempty,max=1000"`
}

// UpdateReviewRequest is the bound body for PUT /reviews/:id.
type UpdateReviewRequest struct {
	Rating  int16   `json:"rating" binding:"required,min=1,max=5"`
	Comment *string `json:"comment" binding:"omitempty,max=1000"`
}
