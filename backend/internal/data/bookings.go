package data

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrOverlappingBooking = errors.New("pitch is already booked for the requested time slot")

type Booking struct {
	ID         int64     `json:"id"`
	PitchID    int64     `json:"pitch_id"`
	UserID     int64     `json:"user_id"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Status     string    `json:"status"`
	TotalPrice float64   `json:"total_price"`
	CreatedAt  time.Time `json:"created_at"`
}

type BookingModel struct {
	DB *pgxpool.Pool // وحدنا الاسم مع الملاعب
}

func (m *BookingModel) Insert(b *Booking) error {
	query := `
		INSERT INTO bookings (pitch_id, user_id, start_time, end_time, total_price)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, status, created_at`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := m.DB.QueryRow(ctx, query,
		b.PitchID,
		b.UserID,
		b.StartTime,
		b.EndTime,
		b.TotalPrice,
	).Scan(&b.ID, &b.Status, &b.CreatedAt)

	if err != nil {
		var pgErr *pgconn.PgError
		// 23P01 هو كود الإيرور الخاص بالـ EXCLUDE CONSTRAINT بالـ Postgres
		if errors.As(err, &pgErr) && pgErr.Code == "23P01" {
			return ErrOverlappingBooking
		}
		return err
	}

	return nil
}