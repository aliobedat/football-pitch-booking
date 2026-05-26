package models

import "time"

// تعريف حالات الحجز عشان نستخدمها بشكل آمن
type BookingStatus string

const (
	StatusPending   BookingStatus = "pending"
	StatusConfirmed BookingStatus = "confirmed"
	StatusCancelled BookingStatus = "cancelled"
)

// الهيكل الأساسي للحجز (تم تحديث PlayerID إلى UserID)
type Booking struct {
	ID         int64         `json:"id"`
	PitchID    int64         `json:"pitch_id"`
	PitchName  string        `json:"pitch_name,omitempty"`
	UserID     int64         `json:"user_id"` // 👈 هون كان الإيرور
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
	
	UserID     int64     `json:"-"` // يتم تعبئته من التوكن (مخفي عن المستخدم)
}

// AdminBooking is the enriched booking record returned to owners/admins.
// It joins pitch and user data so the dashboard never needs a second request.
type AdminBooking struct {
	ID         int64         `json:"id"`
	PitchID    int64         `json:"pitch_id"`
	PitchName  string        `json:"pitch_name"`
	UserID     int64         `json:"user_id"`
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