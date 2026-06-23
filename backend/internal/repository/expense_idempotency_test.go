package repository

// Integration tests for expense-create idempotency (close the MED double-submit
// finding). They exercise the REAL ON CONFLICT (owner_id, idempotency_key) partial
// unique index path against a live database: same key twice → ONE row (the second
// call returns the original), two distinct keys → TWO rows (legit repeats allowed),
// and the legacy NULL-key path → TWO rows (the partial index permits multiple
// NULLs). SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set, so the default
// `go test ./...` run stays green.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run ExpenseIdempotency

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
)

type expenseEnv struct {
	pool    *pgxpool.Pool
	repo    ExpenseRepository
	ownerID int
}

func newExpenseEnv(t *testing.T) *expenseEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping expense idempotency integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	suffix := time.Now().UnixNano() % 1_000_000
	var ownerID int
	phone := fmt.Sprintf("+96283%06d", suffix)
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,'owner',TRUE) RETURNING id
	`, "EXP Owner", phone).Scan(&ownerID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM expenses WHERE owner_id = $1`, ownerID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, ownerID)
		pool.Close()
	})
	return &expenseEnv{pool: pool, repo: NewExpenseRepository(pool), ownerID: ownerID}
}

func (e *expenseEnv) actor() auth.Actor { return auth.Actor{UserID: e.ownerID, Role: auth.RoleOwner} }

func (e *expenseEnv) count(ctx context.Context, t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM expenses WHERE owner_id = $1`, e.ownerID).Scan(&n); err != nil {
		t.Fatalf("count expenses: %v", err)
	}
	return n
}

func (e *expenseEnv) input(key *string) ExpenseInput {
	return ExpenseInput{
		Category:       "Water",
		Amount:         12.5,
		OccurredAt:     time.Now().UTC(),
		Note:           "",
		IdempotencyKey: key,
	}
}

// Same key twice → exactly ONE row; the second call returns the first row's id.
func TestExpenseIdempotency_SameKeyOneRow(t *testing.T) {
	env := newExpenseEnv(t)
	ctx := context.Background()
	key := "exp-key-A"

	first, err := env.repo.Create(ctx, env.actor(), env.input(&key))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, err := env.repo.Create(ctx, env.actor(), env.input(&key))
	if err != nil {
		t.Fatalf("retry create: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("retry returned expense %d, want original %d", second.ID, first.ID)
	}
	if n := env.count(ctx, t); n != 1 {
		t.Errorf("expense count = %d, want 1 (no duplicate from retry)", n)
	}
}

// Two DISTINCT keys, identical amount/category/day → TWO rows (legit repeats).
func TestExpenseIdempotency_DistinctKeysTwoRows(t *testing.T) {
	env := newExpenseEnv(t)
	ctx := context.Background()
	k1, k2 := "exp-key-1", "exp-key-2"

	a, err := env.repo.Create(ctx, env.actor(), env.input(&k1))
	if err != nil {
		t.Fatalf("create #1: %v", err)
	}
	b, err := env.repo.Create(ctx, env.actor(), env.input(&k2))
	if err != nil {
		t.Fatalf("create #2: %v", err)
	}
	if a.ID == b.ID {
		t.Errorf("distinct keys collapsed to one row id=%d", a.ID)
	}
	if n := env.count(ctx, t); n != 2 {
		t.Errorf("expense count = %d, want 2 (distinct keys are separate expenses)", n)
	}
}

// NULL key twice (legacy path) → TWO rows; the partial unique index permits
// multiple NULL idempotency_key values.
func TestExpenseIdempotency_NullKeyTwoRows(t *testing.T) {
	env := newExpenseEnv(t)
	ctx := context.Background()

	if _, err := env.repo.Create(ctx, env.actor(), env.input(nil)); err != nil {
		t.Fatalf("create #1 (nil key): %v", err)
	}
	if _, err := env.repo.Create(ctx, env.actor(), env.input(nil)); err != nil {
		t.Fatalf("create #2 (nil key): %v", err)
	}
	if n := env.count(ctx, t); n != 2 {
		t.Errorf("expense count = %d, want 2 (legacy keyless path unchanged)", n)
	}
}
