package repository

// CustomerRepository backs the Regulars CRM (Cockpit WO1). All reads/writes are
// owner-scoped via the canonical auth.Actor.OwnerScopeFilter primitive (owner →
// their own customers; admin → all). A customer belongs to exactly one owner, so
// Owner A can never retrieve Owner B's contacts/phones through this layer.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/phone"
)

// ErrCustomerNotFound is returned when a customer id does not exist within the
// actor's scope (a real miss and an out-of-scope row are indistinguishable on
// purpose — an owner must not be able to probe another tenant's id space).
var ErrCustomerNotFound = errors.New("customer not found")

// customerSort maps the API sort key to a safe ORDER BY clause (allow-list — never
// interpolate a raw client value).
var customerSort = map[string]string{
	"name":          "c.name ASC NULLS LAST",
	"last_booked":   "last_booked DESC NULLS LAST",
	"booking_count": "booking_count DESC",
	"no_show":       "no_show_count DESC",
}

type CustomerRepository interface {
	ListCustomers(ctx context.Context, actor auth.Actor, search, sort string) ([]models.CustomerListItem, error)
	GetCustomerProfile(ctx context.Context, actor auth.Actor, id int64) (*models.CustomerProfile, error)
	UpdateNotes(ctx context.Context, actor auth.Actor, id int64, notes string) (*models.Customer, error)
	// AssociateBookingCustomer match-or-creates the customer for a SINGLE booking
	// and links it (go-forward equivalent of the backfill). Tolerant: a booking
	// with no usable phone is a no-op, never an error — so it can be called
	// best-effort after booking creation without risking the write path.
	AssociateBookingCustomer(ctx context.Context, bookingID int64) error
}

type customerRepo struct {
	db *pgxpool.Pool
}

func NewCustomerRepository(db *pgxpool.Pool) CustomerRepository {
	return &customerRepo{db: db}
}

func (r *customerRepo) ListCustomers(ctx context.Context, actor auth.Actor, search, sort string) ([]models.CustomerListItem, error) {
	ownerClause, args := actor.OwnerScopeFilter("c.owner_id", 1)

	searchClause := ""
	if s := strings.TrimSpace(search); s != "" {
		args = append(args, "%"+s+"%")
		searchClause = fmt.Sprintf(" AND (c.name ILIKE $%d OR c.phone ILIKE $%d)", len(args), len(args))
	}

	orderBy, ok := customerSort[sort]
	if !ok {
		orderBy = customerSort["name"]
	}

	// History aggregates exclude cancelled rows (they are not real occupancy). The
	// LEFT JOIN keeps zero-booking contacts in the list.
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT c.id, c.player_id, COALESCE(c.name,''), c.phone, COALESCE(c.notes,''), c.created_at,
		       count(b.id)                                          AS booking_count,
		       max(lower(b.booking_range))                          AS last_booked,
		       count(*) FILTER (WHERE b.attendance = 'no_show')     AS no_show_count
		FROM customers c
		LEFT JOIN bookings b ON b.customer_id = c.id AND b.status <> 'cancelled'
		WHERE %s%s
		GROUP BY c.id
		ORDER BY %s
	`, ownerClause, searchClause, orderBy), args...)
	if err != nil {
		return nil, fmt.Errorf("ListCustomers: query: %w", err)
	}
	defer rows.Close()

	list := make([]models.CustomerListItem, 0)
	for rows.Next() {
		var it models.CustomerListItem
		if err := rows.Scan(
			&it.ID, &it.PlayerID, &it.Name, &it.Phone, &it.Notes, &it.CreatedAt,
			&it.BookingCount, &it.LastBooked, &it.NoShowCount,
		); err != nil {
			return nil, fmt.Errorf("ListCustomers: scan: %w", err)
		}
		it.IsAppPlayer = it.PlayerID != nil
		list = append(list, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ListCustomers: rows: %w", err)
	}
	return list, nil
}

func (r *customerRepo) GetCustomerProfile(ctx context.Context, actor auth.Actor, id int64) (*models.CustomerProfile, error) {
	ownerClause, args := actor.OwnerScopeFilter("c.owner_id", 1)
	args = append(args, id)
	idIdx := len(args)

	var p models.CustomerProfile
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT c.id, c.player_id, COALESCE(c.name,''), c.phone, COALESCE(c.notes,''), c.created_at,
		       count(b.id),
		       count(*) FILTER (WHERE b.attendance = 'no_show'),
		       count(*) FILTER (WHERE b.attendance = 'checked_in'),
		       max(lower(b.booking_range))
		FROM customers c
		LEFT JOIN bookings b ON b.customer_id = c.id AND b.status <> 'cancelled'
		WHERE %s AND c.id = $%d
		GROUP BY c.id
	`, ownerClause, idIdx), args...).Scan(
		&p.Customer.ID, &p.Customer.PlayerID, &p.Customer.Name, &p.Customer.Phone,
		&p.Customer.Notes, &p.Customer.CreatedAt,
		&p.BookingCount, &p.NoShowCount, &p.CheckedInCount, &p.LastBooked,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("GetCustomerProfile: %w", err)
	}
	p.Customer.IsAppPlayer = p.Customer.PlayerID != nil
	p.PreferredSlots = make([]models.PreferredSlot, 0)
	p.RecentBookings = make([]models.CustomerBookingHistory, 0)

	// Derived preferred slots — the Amman weekday+hour the customer books most.
	slotRows, err := r.db.Query(ctx, `
		SELECT extract(dow  FROM lower(b.booking_range) AT TIME ZONE 'Asia/Amman')::int AS dow,
		       extract(hour FROM lower(b.booking_range) AT TIME ZONE 'Asia/Amman')::int AS hr,
		       count(*) AS n
		FROM bookings b
		WHERE b.customer_id = $1 AND b.status <> 'cancelled'
		GROUP BY dow, hr
		ORDER BY n DESC, dow, hr
		LIMIT 3
	`, id)
	if err != nil {
		return nil, fmt.Errorf("GetCustomerProfile: slots: %w", err)
	}
	defer slotRows.Close()
	for slotRows.Next() {
		var s models.PreferredSlot
		if err := slotRows.Scan(&s.Weekday, &s.Hour, &s.Count); err != nil {
			return nil, fmt.Errorf("GetCustomerProfile: slot scan: %w", err)
		}
		p.PreferredSlots = append(p.PreferredSlots, s)
	}
	if err := slotRows.Err(); err != nil {
		return nil, fmt.Errorf("GetCustomerProfile: slot rows: %w", err)
	}

	// Recent history (most recent slots first).
	histRows, err := r.db.Query(ctx, `
		SELECT b.id, COALESCE(pi.name,''),
		       lower(b.booking_range), upper(b.booking_range),
		       b.status::text, b.attendance
		FROM bookings b
		JOIN pitches pi ON pi.id = b.pitch_id
		WHERE b.customer_id = $1
		ORDER BY lower(b.booking_range) DESC
		LIMIT 20
	`, id)
	if err != nil {
		return nil, fmt.Errorf("GetCustomerProfile: history: %w", err)
	}
	defer histRows.Close()
	for histRows.Next() {
		var h models.CustomerBookingHistory
		if err := histRows.Scan(&h.ID, &h.PitchName, &h.StartTime, &h.EndTime, &h.Status, &h.Attendance); err != nil {
			return nil, fmt.Errorf("GetCustomerProfile: history scan: %w", err)
		}
		p.RecentBookings = append(p.RecentBookings, h)
	}
	if err := histRows.Err(); err != nil {
		return nil, fmt.Errorf("GetCustomerProfile: history rows: %w", err)
	}

	return &p, nil
}

func (r *customerRepo) UpdateNotes(ctx context.Context, actor auth.Actor, id int64, notes string) (*models.Customer, error) {
	ownerClause, args := actor.OwnerScopeFilter("owner_id", 1)
	args = append(args, id, notes)

	var c models.Customer
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE customers
		SET notes = NULLIF($%d,''), updated_at = now()
		WHERE %s AND id = $%d
		RETURNING id, player_id, COALESCE(name,''), phone, COALESCE(notes,''), created_at
	`, len(args), ownerClause, len(args)-1), args...).Scan(
		&c.ID, &c.PlayerID, &c.Name, &c.Phone, &c.Notes, &c.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrCustomerNotFound
		}
		return nil, fmt.Errorf("UpdateNotes: %w", err)
	}
	c.IsAppPlayer = c.PlayerID != nil
	return &c, nil
}

func (r *customerRepo) AssociateBookingCustomer(ctx context.Context, bookingID int64) error {
	// Resolve the booking's owner + identity. Only player/manual rows carry a
	// customer identity; blocks/academy are ignored.
	var (
		ownerID       int64
		source        string
		playerID      *int64
		userPhone     *string
		userName      *string
		guestName     string
		guestPhone    string
		alreadyLinked bool
	)
	err := r.db.QueryRow(ctx, `
		SELECT p.owner_id, b.source, b.player_id, u.phone, u.full_name,
		       COALESCE(b.guest_name,''), COALESCE(b.guest_phone,''),
		       b.customer_id IS NOT NULL
		FROM bookings b
		JOIN pitches p ON p.id = b.pitch_id
		LEFT JOIN users u ON u.id = b.player_id
		WHERE b.id = $1
	`, bookingID).Scan(&ownerID, &source, &playerID, &userPhone, &userName,
		&guestName, &guestPhone, &alreadyLinked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // booking vanished (cancelled/race) — nothing to do
		}
		return fmt.Errorf("AssociateBookingCustomer: resolve: %w", err)
	}
	if alreadyLinked {
		return nil // idempotent
	}

	// Determine the canonical phone + name by source.
	var normPhone, name string
	switch source {
	case "player":
		if userPhone == nil || *userPhone == "" {
			return nil // phone-less player — skip (linkable later)
		}
		normPhone = *userPhone // users.phone is already E.164
		if userName != nil {
			name = *userName
		}
	case "manual":
		n, err := phone.Normalize(guestPhone)
		if err != nil {
			return nil // no/invalid guest phone — skip
		}
		normPhone = n
		name = guestName
	default:
		return nil // block / academy — no customer identity
	}

	var custID int64
	if err := r.db.QueryRow(ctx, `
		INSERT INTO customers (owner_id, player_id, phone, name)
		VALUES ($1, $2, $3, NULLIF($4,''))
		ON CONFLICT (owner_id, phone) DO UPDATE
		  SET player_id = COALESCE(customers.player_id, EXCLUDED.player_id),
		      name      = COALESCE(customers.name, NULLIF(EXCLUDED.name,'')),
		      updated_at = now()
		RETURNING id
	`, ownerID, playerID, normPhone, name).Scan(&custID); err != nil {
		return fmt.Errorf("AssociateBookingCustomer: upsert: %w", err)
	}

	if _, err := r.db.Exec(ctx,
		`UPDATE bookings SET customer_id = $1 WHERE id = $2 AND customer_id IS NULL`,
		custID, bookingID); err != nil {
		return fmt.Errorf("AssociateBookingCustomer: link: %w", err)
	}
	return nil
}
