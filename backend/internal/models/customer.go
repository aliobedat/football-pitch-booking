package models

import "time"

// Customer is an owner-scoped CRM contact (Cockpit WO1): EITHER a linked platform
// player (PlayerID set) OR a standalone owner-entered walk-in (PlayerID nil). The
// dedup key is (owner_id, phone). Notes are the owner's private free text.
type Customer struct {
	ID        int64     `json:"id"`
	PlayerID  *int64    `json:"player_id"`
	Name      string    `json:"name"`
	Phone     string    `json:"phone"`
	Notes     string    `json:"notes"`
	CreatedAt time.Time `json:"created_at"`

	// IsAppPlayer is a derived convenience flag for the UI: true when this contact
	// is a registered platform player (PlayerID != nil) vs a manual walk-in guest.
	IsAppPlayer bool `json:"is_app_player"`
}

// CustomerListItem is a row in the Regulars list: the contact plus the rolled-up
// history figures the owner scans (count, recency, reliability).
type CustomerListItem struct {
	Customer
	BookingCount int        `json:"booking_count"`
	LastBooked   *time.Time `json:"last_booked"`
	NoShowCount  int        `json:"no_show_count"`
}

// PreferredSlot is a derived "regular time" for a customer: the Amman weekday +
// hour they book most. Weekday is 0=Sunday … 6=Saturday (Postgres DOW).
type PreferredSlot struct {
	Weekday int `json:"weekday"`
	Hour    int `json:"hour"`
	Count   int `json:"count"`
}

// CustomerBookingHistory is one past/booked slot shown on the profile timeline.
type CustomerBookingHistory struct {
	ID         int64     `json:"id"`
	PitchName  string    `json:"pitch_name"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Status     string    `json:"status"`
	Attendance string    `json:"attendance"`
}

// CustomerProfile is the full per-customer view: identity, reliability stats,
// derived preferred slots, and recent history.
type CustomerProfile struct {
	Customer       Customer                 `json:"customer"`
	BookingCount   int                      `json:"booking_count"`
	NoShowCount    int                      `json:"no_show_count"`
	CheckedInCount int                      `json:"checked_in_count"`
	LastBooked     *time.Time               `json:"last_booked"`
	PreferredSlots []PreferredSlot          `json:"preferred_slots"`
	RecentBookings []CustomerBookingHistory `json:"recent_bookings"`
}
