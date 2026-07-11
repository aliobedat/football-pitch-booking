package repository

// §5.6 Batch 1 — cross-owner WRITE isolation for the owner-scoped mutations whose
// ONLY barrier is the OwnerScopeFilter WHERE-clause predicate (mechanism (a),
// confirmed in Step 0): customer-notes update, expense update, expense soft-delete.
//
// For each: owner A acting on owner B's resource must be rejected (typed
// not-found) AND leave owner B's row byte-unchanged at the field level — the
// "rejected but wrote" nightmare. A positive control proves the path works for
// the owner's own resource, and an admin assertion proves the predicate degrades
// to TRUE for admin (so the suite isn't accidentally proving "nobody can mutate").
//
// Repo-layer is the correct layer: the guard lives in the SQL WHERE clause.
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/repository/ -run OwnerScopeMutation -v

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/testutil"
)

type ownerScopeEnv struct {
	pool             *pgxpool.Pool
	custRepo         CustomerRepository
	expRepo          ExpenseRepository
	ownerA, ownerB   int
	custB            int64 // owner B's customer
	custA            int64 // owner A's customer (positive control)
	expB             int64 // owner B's expense
	expAUpd, expADel int64 // owner A's expenses (positive controls for update + delete)
}

func actorOwner(id int) auth.Actor { return auth.Actor{UserID: id, Role: auth.RoleOwner} }

func newOwnerScopeEnv(t *testing.T) *ownerScopeEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping owner-scope mutation integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	suffix := testutil.UniqueSuffix() % 1_000_000
	mkUser := func(name, prefix string) int {
		var id int
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,'owner',TRUE) RETURNING id
		`, name, phone).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	e := &ownerScopeEnv{
		pool:     pool,
		custRepo: NewCustomerRepository(pool),
		expRepo:  NewExpenseRepository(pool),
		ownerA:   mkUser("OS Owner A", "50"),
		ownerB:   mkUser("OS Owner B", "51"),
	}

	// Customers (raw insert; notes seeded to a known sentinel so "unchanged" is exact).
	mkCustomer := func(owner int, prefix, notes string) int64 {
		var id int64
		phone := fmt.Sprintf("+962%s%06d", prefix, suffix)
		if err := pool.QueryRow(ctx, `
			INSERT INTO customers (owner_id, phone, name, notes) VALUES ($1,$2,$3,$4) RETURNING id
		`, owner, phone, "Cust", notes).Scan(&id); err != nil {
			t.Fatalf("seed customer (owner %d): %v", owner, err)
		}
		return id
	}
	e.custB = mkCustomer(e.ownerB, "52", "owner-B-original-notes")
	e.custA = mkCustomer(e.ownerA, "53", "owner-A-original-notes")

	// Expenses via the repo Create (owner_id = actor.UserID); PitchID nil = general.
	mkExpense := func(owner int, note string) int64 {
		exp, err := e.expRepo.Create(ctx, actorOwner(owner), ExpenseInput{
			PitchID: nil, Category: "Maintenance", Amount: 12.5,
			OccurredAt: time.Now().UTC(), Note: note,
		})
		if err != nil {
			t.Fatalf("seed expense (owner %d): %v", owner, err)
		}
		return exp.ID
	}
	e.expB = mkExpense(e.ownerB, "owner-B-expense")
	e.expAUpd = mkExpense(e.ownerA, "owner-A-expense-update")
	e.expADel = mkExpense(e.ownerA, "owner-A-expense-delete")

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM expenses WHERE owner_id = ANY($1)`, []int{e.ownerA, e.ownerB})
		_, _ = pool.Exec(cctx, `DELETE FROM customers WHERE owner_id = ANY($1)`, []int{e.ownerA, e.ownerB})
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int{e.ownerA, e.ownerB})
		pool.Close()
	})
	return e
}

func (e *ownerScopeEnv) customerNotes(t *testing.T, id int64) string {
	t.Helper()
	var notes string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.pool.QueryRow(ctx, `SELECT COALESCE(notes,'') FROM customers WHERE id = $1`, id).Scan(&notes); err != nil {
		t.Fatalf("read customer notes: %v", err)
	}
	return notes
}

func (e *ownerScopeEnv) expenseFields(t *testing.T, id int64) (category, note string, amount float64, deleted bool) {
	t.Helper()
	var del *time.Time
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.pool.QueryRow(ctx,
		`SELECT category, COALESCE(note,''), amount::float8, deleted_at FROM expenses WHERE id = $1`, id).
		Scan(&category, &note, &amount, &del); err != nil {
		t.Fatalf("read expense fields: %v", err)
	}
	return category, note, amount, del != nil
}

// ── customer notes ──────────────────────────────────────────────────────────

func TestOwnerScopeMutation_CustomerNotes(t *testing.T) {
	e := newOwnerScopeEnv(t)
	ctx := context.Background()

	// Cross-owner: owner A → owner B's customer → ErrCustomerNotFound, notes intact.
	before := e.customerNotes(t, e.custB)
	_, err := e.custRepo.UpdateNotes(ctx, actorOwner(e.ownerA), e.custB, "HACKED by owner A")
	if !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("cross-owner notes update err = %v, want ErrCustomerNotFound", err)
	}
	if after := e.customerNotes(t, e.custB); after != before {
		t.Fatalf("CRITICAL: owner B's notes mutated despite rejection: %q → %q", before, after)
	}

	// Positive control: owner A → own customer → success, notes reflect change.
	if _, err := e.custRepo.UpdateNotes(ctx, actorOwner(e.ownerA), e.custA, "owner A new notes"); err != nil {
		t.Fatalf("owner A own-customer notes update: %v", err)
	}
	if got := e.customerNotes(t, e.custA); got != "owner A new notes" {
		t.Fatalf("own-customer notes = %q, want updated", got)
	}

	// Admin: OwnerScopeFilter → TRUE, so admin may set any owner's customer notes.
	if _, err := e.custRepo.UpdateNotes(ctx, auth.Actor{UserID: 0, Role: auth.RoleAdmin}, e.custB, "admin note"); err != nil {
		t.Fatalf("admin notes update on owner B's customer: %v", err)
	}
	if got := e.customerNotes(t, e.custB); got != "admin note" {
		t.Fatalf("admin notes update did not apply: %q", got)
	}
}

// ── expense update ────────────────────────────────────────────────────────────

func TestOwnerScopeMutation_ExpenseUpdate(t *testing.T) {
	e := newOwnerScopeEnv(t)
	ctx := context.Background()

	catBefore, noteBefore, amtBefore, _ := e.expenseFields(t, e.expB)
	_, err := e.expRepo.Update(ctx, actorOwner(e.ownerA), e.expB, ExpenseInput{
		PitchID: nil, Category: "Marketing", Amount: 999, OccurredAt: time.Now().UTC(), Note: "HACKED",
	})
	if !errors.Is(err, ErrExpenseNotFound) {
		t.Fatalf("cross-owner expense update err = %v, want ErrExpenseNotFound", err)
	}
	catAfter, noteAfter, amtAfter, _ := e.expenseFields(t, e.expB)
	if catAfter != catBefore || noteAfter != noteBefore || amtAfter != amtBefore {
		t.Fatalf("CRITICAL: owner B's expense mutated despite rejection: (%q,%q,%v) → (%q,%q,%v)",
			catBefore, noteBefore, amtBefore, catAfter, noteAfter, amtAfter)
	}

	// Positive control: owner A → own expense → success.
	if _, err := e.expRepo.Update(ctx, actorOwner(e.ownerA), e.expAUpd, ExpenseInput{
		PitchID: nil, Category: "Water", Amount: 7.25, OccurredAt: time.Now().UTC(), Note: "updated",
	}); err != nil {
		t.Fatalf("owner A own-expense update: %v", err)
	}
	if cat, note, amt, _ := e.expenseFields(t, e.expAUpd); cat != "Water" || note != "updated" || amt != 7.25 {
		t.Fatalf("own-expense update not applied: (%q,%q,%v)", cat, note, amt)
	}

	// Admin: predicate → TRUE, may update any owner's expense.
	if _, err := e.expRepo.Update(ctx, auth.Actor{UserID: 0, Role: auth.RoleAdmin}, e.expB, ExpenseInput{
		PitchID: nil, Category: "Other", Amount: 1, OccurredAt: time.Now().UTC(), Note: "admin",
	}); err != nil {
		t.Fatalf("admin expense update on owner B's expense: %v", err)
	}
}

// ── expense soft-delete ───────────────────────────────────────────────────────

func TestOwnerScopeMutation_ExpenseDelete(t *testing.T) {
	e := newOwnerScopeEnv(t)
	ctx := context.Background()

	// Cross-owner: owner A → owner B's expense → ErrExpenseNotFound, and owner B's
	// row must remain LIVE (deleted_at IS NULL) — the soft-delete nuance.
	if _, _, _, deleted := e.expenseFields(t, e.expB); deleted {
		t.Fatalf("precondition: owner B's expense already soft-deleted")
	}
	err := e.expRepo.SoftDelete(ctx, actorOwner(e.ownerA), e.expB)
	if !errors.Is(err, ErrExpenseNotFound) {
		t.Fatalf("cross-owner expense delete err = %v, want ErrExpenseNotFound", err)
	}
	if _, _, _, deleted := e.expenseFields(t, e.expB); deleted {
		t.Fatalf("CRITICAL: owner B's expense was soft-deleted by owner A (deleted_at set)")
	}

	// Positive control: owner A → own expense → success, deleted_at SET.
	if err := e.expRepo.SoftDelete(ctx, actorOwner(e.ownerA), e.expADel); err != nil {
		t.Fatalf("owner A own-expense delete: %v", err)
	}
	if _, _, _, deleted := e.expenseFields(t, e.expADel); !deleted {
		t.Fatalf("own-expense delete did not set deleted_at")
	}

	// Admin: predicate → TRUE, may soft-delete any owner's expense.
	if err := e.expRepo.SoftDelete(ctx, auth.Actor{UserID: 0, Role: auth.RoleAdmin}, e.expB); err != nil {
		t.Fatalf("admin expense delete on owner B's expense: %v", err)
	}
	if _, _, _, deleted := e.expenseFields(t, e.expB); !deleted {
		t.Fatalf("admin delete did not set deleted_at on owner B's expense")
	}
}
