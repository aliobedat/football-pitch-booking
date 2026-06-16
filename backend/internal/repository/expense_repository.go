package repository

// ExpenseRepository backs the Expense Ledger (Cockpit WO-F2). Owner-scoped via the
// canonical OwnerScopeFilter (owner → own rows; admin → all). Soft-delete preserves
// ledger history. Amounts are JOD/fils (NUMERIC(10,3) in the DB), rounded to 3 dp
// at the boundary so sums reconcile to the fil against WO-F1's collected leg.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/models"
)

// ErrExpenseNotFound — the expense does not exist within the actor's scope (a real
// miss and an out-of-scope row are indistinguishable: no cross-tenant probing).
var ErrExpenseNotFound = errors.New("expense not found")

// (ErrPitchNotOwned is defined in staff_repository.go and reused here: a supplied
// pitch_id that is not a pitch the actor manages.)

// round3 snaps a JOD amount to the milli-JOD (fil). Mirrors the manual-booking
// price rounding so money is consistent across the codebase.
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// ExpenseInput carries a create/update payload (validated by the handler).
type ExpenseInput struct {
	PitchID    *int64
	Category   string
	Amount     float64
	OccurredAt time.Time
	Note       string
}

type ExpenseRepository interface {
	Create(ctx context.Context, actor auth.Actor, in ExpenseInput) (*models.Expense, error)
	Update(ctx context.Context, actor auth.Actor, id int64, in ExpenseInput) (*models.Expense, error)
	SoftDelete(ctx context.Context, actor auth.Actor, id int64) error
	List(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time, category string) ([]models.Expense, error)

	// SumExpenses is the period total (owner-scoped, Amman window [from,to)).
	SumExpenses(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) (float64, error)
	// ByCategory returns per-category subtotals for the period (desc by total).
	ByCategory(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]models.CategorySubtotal, error)
	// ByBucket returns expenses grouped by Amman calendar bucket — the SAME
	// date_trunc(...AT TIME ZONE 'Asia/Amman') basis as the F1 collected series.
	ByBucket(ctx context.Context, actor auth.Actor, granularity string, fromUTC, toUTC time.Time) (map[string]float64, error)
}

type expenseRepo struct {
	db *pgxpool.Pool
}

func NewExpenseRepository(db *pgxpool.Pool) ExpenseRepository {
	return &expenseRepo{db: db}
}

// assertPitchInScope guards that a supplied pitch_id belongs to the actor's scope
// (so an owner can't tag an expense onto another owner's pitch). NULL is allowed
// (overhead). Admins are unscoped.
func (r *expenseRepo) assertPitchInScope(ctx context.Context, actor auth.Actor, pitchID *int64) error {
	if pitchID == nil {
		return nil
	}
	clause, args := actor.OwnerScopeFilter("owner_id", 2)
	args = append([]any{*pitchID}, args...)
	var ok bool
	err := r.db.QueryRow(ctx, fmt.Sprintf(
		`SELECT EXISTS (SELECT 1 FROM pitches WHERE id = $1 AND deleted_at IS NULL AND %s)`, clause),
		args...).Scan(&ok)
	if err != nil {
		return fmt.Errorf("assertPitchInScope: %w", err)
	}
	if !ok {
		return ErrPitchNotOwned
	}
	return nil
}

func (r *expenseRepo) Create(ctx context.Context, actor auth.Actor, in ExpenseInput) (*models.Expense, error) {
	if err := r.assertPitchInScope(ctx, actor, in.PitchID); err != nil {
		return nil, err
	}
	var e models.Expense
	err := r.db.QueryRow(ctx, `
		INSERT INTO expenses (owner_id, pitch_id, category, amount, occurred_at, note)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6,''))
		RETURNING id, pitch_id, category, amount::float8, occurred_at, note, created_at, updated_at
	`, actor.UserID, in.PitchID, in.Category, round3(in.Amount), in.OccurredAt, in.Note).Scan(
		&e.ID, &e.PitchID, &e.Category, &e.Amount, &e.OccurredAt, &e.Note, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("Create expense: %w", err)
	}
	return &e, nil
}

func (r *expenseRepo) Update(ctx context.Context, actor auth.Actor, id int64, in ExpenseInput) (*models.Expense, error) {
	if err := r.assertPitchInScope(ctx, actor, in.PitchID); err != nil {
		return nil, err
	}
	// Args $1..$6 are the SET payload; the owner-scope arg (if any) starts at $7.
	clause, scopeArgs := actor.OwnerScopeFilter("owner_id", 7)
	allArgs := append([]any{id, in.PitchID, in.Category, round3(in.Amount), in.OccurredAt, in.Note}, scopeArgs...)

	var e models.Expense
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		UPDATE expenses
		SET pitch_id = $2, category = $3, amount = $4, occurred_at = $5,
		    note = NULLIF($6,''), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND %s
		RETURNING id, pitch_id, category, amount::float8, occurred_at, note, created_at, updated_at
	`, clause), allArgs...).Scan(
		&e.ID, &e.PitchID, &e.Category, &e.Amount, &e.OccurredAt, &e.Note, &e.CreatedAt, &e.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrExpenseNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("Update expense: %w", err)
	}
	return &e, nil
}

func (r *expenseRepo) SoftDelete(ctx context.Context, actor auth.Actor, id int64) error {
	clause, scopeArgs := actor.OwnerScopeFilter("owner_id", 2)
	args := append([]any{id}, scopeArgs...)
	ct, err := r.db.Exec(ctx, fmt.Sprintf(`
		UPDATE expenses SET deleted_at = now(), updated_at = now()
		WHERE id = $1 AND deleted_at IS NULL AND %s
	`, clause), args...)
	if err != nil {
		return fmt.Errorf("SoftDelete expense: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrExpenseNotFound
	}
	return nil
}

func (r *expenseRepo) List(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time, category string) ([]models.Expense, error) {
	clause, args := actor.OwnerScopeFilter("e.owner_id", 1)
	args = append(args, fromUTC, toUTC)
	fromIdx, toIdx := len(args)-1, len(args)
	catClause := ""
	if category != "" {
		args = append(args, category)
		catClause = fmt.Sprintf(" AND e.category = $%d", len(args))
	}
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT e.id, e.pitch_id, p.name, e.category, e.amount::float8, e.occurred_at, e.note, e.created_at, e.updated_at
		FROM expenses e
		LEFT JOIN pitches p ON p.id = e.pitch_id
		WHERE e.deleted_at IS NULL AND %s
		  AND e.occurred_at >= $%d AND e.occurred_at < $%d%s
		ORDER BY e.occurred_at DESC, e.id DESC
	`, clause, fromIdx, toIdx, catClause), args...)
	if err != nil {
		return nil, fmt.Errorf("List expenses: %w", err)
	}
	defer rows.Close()
	out := make([]models.Expense, 0)
	for rows.Next() {
		var e models.Expense
		if err := rows.Scan(&e.ID, &e.PitchID, &e.PitchName, &e.Category, &e.Amount,
			&e.OccurredAt, &e.Note, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("List expenses: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *expenseRepo) SumExpenses(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) (float64, error) {
	clause, args := actor.OwnerScopeFilter("owner_id", 1)
	args = append(args, fromUTC, toUTC)
	var sum float64
	err := r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT COALESCE(SUM(amount), 0)::float8
		FROM expenses
		WHERE deleted_at IS NULL AND %s AND occurred_at >= $%d AND occurred_at < $%d
	`, clause, len(args)-1, len(args)), args...).Scan(&sum)
	if err != nil {
		return 0, fmt.Errorf("SumExpenses: %w", err)
	}
	return round3(sum), nil
}

func (r *expenseRepo) ByCategory(ctx context.Context, actor auth.Actor, fromUTC, toUTC time.Time) ([]models.CategorySubtotal, error) {
	clause, args := actor.OwnerScopeFilter("owner_id", 1)
	args = append(args, fromUTC, toUTC)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT category, SUM(amount)::float8
		FROM expenses
		WHERE deleted_at IS NULL AND %s AND occurred_at >= $%d AND occurred_at < $%d
		GROUP BY category
		ORDER BY SUM(amount) DESC
	`, clause, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("ByCategory: %w", err)
	}
	defer rows.Close()
	out := make([]models.CategorySubtotal, 0)
	for rows.Next() {
		var c models.CategorySubtotal
		if err := rows.Scan(&c.Category, &c.Total); err != nil {
			return nil, fmt.Errorf("ByCategory: scan: %w", err)
		}
		c.Total = round3(c.Total)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *expenseRepo) ByBucket(ctx context.Context, actor auth.Actor, granularity string, fromUTC, toUTC time.Time) (map[string]float64, error) {
	clause, args := actor.OwnerScopeFilter("owner_id", 1)
	args = append(args, fromUTC, toUTC)
	// IDENTICAL bucketing basis to the F1 collected series: truncate the Amman-local
	// wall time of occurred_at.
	bucketExpr := fmt.Sprintf("date_trunc('%s', occurred_at AT TIME ZONE 'Asia/Amman')", granularity)
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT to_char(%s, 'YYYY-MM-DD'), SUM(amount)::float8
		FROM expenses
		WHERE deleted_at IS NULL AND %s AND occurred_at >= $%d AND occurred_at < $%d
		GROUP BY 1
	`, bucketExpr, clause, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("ByBucket: %w", err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("ByBucket: scan: %w", err)
		}
		out[k] = round3(v)
	}
	return out, rows.Err()
}
