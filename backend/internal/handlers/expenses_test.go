package handlers

// Boundary tests for the Expense Ledger + Net engine (Cockpit WO-F2). Owner/admin
// only — staff/players are barred at the route (RequireRole) AND the handler guard;
// owners reach the repo as themselves (repo applies OwnerScopeFilter). Validation
// (category/amount/date) is rejected before any write. No Postgres required.

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

type fakeExpenseRepo struct {
	createCalls int
	lastActor   auth.Actor
}

func (f *fakeExpenseRepo) Create(_ context.Context, a auth.Actor, in repository.ExpenseInput) (*models.Expense, error) {
	f.createCalls++
	f.lastActor = a
	return &models.Expense{ID: 1, Category: in.Category, Amount: in.Amount}, nil
}
func (f *fakeExpenseRepo) Update(_ context.Context, a auth.Actor, id int64, in repository.ExpenseInput) (*models.Expense, error) {
	f.lastActor = a
	return &models.Expense{ID: id, Category: in.Category, Amount: in.Amount}, nil
}
func (f *fakeExpenseRepo) SoftDelete(_ context.Context, a auth.Actor, _ int64) error { f.lastActor = a; return nil }
func (f *fakeExpenseRepo) List(_ context.Context, a auth.Actor, _, _ time.Time, _ string) ([]models.Expense, error) {
	f.lastActor = a
	return []models.Expense{}, nil
}
func (f *fakeExpenseRepo) SumExpenses(_ context.Context, _ auth.Actor, _, _ time.Time) (float64, error) {
	return 0, nil
}
func (f *fakeExpenseRepo) ByCategory(_ context.Context, _ auth.Actor, _, _ time.Time) ([]models.CategorySubtotal, error) {
	return nil, nil
}
func (f *fakeExpenseRepo) ByBucket(_ context.Context, _ auth.Actor, _ string, _, _ time.Time) (map[string]float64, error) {
	return map[string]float64{}, nil
}

func expenseRouter(eh *ExpenseHandler, fh *FinancialsHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	g := middleware.RequireRole("owner", "admin")
	r.GET("/owner/expenses", inject, g, eh.ListExpenses)
	r.POST("/owner/expenses", inject, g, eh.CreateExpense)
	r.PATCH("/owner/expenses/:id", inject, g, eh.UpdateExpense)
	r.DELETE("/owner/expenses/:id", inject, g, eh.DeleteExpense)
	if fh != nil {
		r.GET("/owner/financials", inject, g, fh.GetNetSummary)
	}
	return r
}

func TestExpenses_StaffForbidden(t *testing.T) {
	repo := &fakeExpenseRepo{}
	r := expenseRouter(NewExpenseHandler(repo), nil, 9, "staff")
	rec := doJSON(t, r, http.MethodPost, "/owner/expenses",
		map[string]any{"category": "Water", "amount": 10, "occurred_on": "2026-06-16"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for staff creating expense", rec.Code)
	}
	if repo.createCalls != 0 {
		t.Fatalf("repo wrote for staff; route must block")
	}
}

func TestExpenses_OwnerScopedCreate(t *testing.T) {
	repo := &fakeExpenseRepo{}
	const ownerID = 42
	r := expenseRouter(NewExpenseHandler(repo), nil, ownerID, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/expenses",
		map[string]any{"category": "Staff", "amount": 25.5, "occurred_on": "2026-06-16"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	if repo.lastActor.UserID != ownerID {
		t.Fatalf("create actor = %+v, want owner #%d", repo.lastActor, ownerID)
	}
}

func TestExpenses_InvalidCategoryRejected(t *testing.T) {
	repo := &fakeExpenseRepo{}
	r := expenseRouter(NewExpenseHandler(repo), nil, 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/expenses",
		map[string]any{"category": "Rent", "amount": 10, "occurred_on": "2026-06-16"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for unknown category", rec.Code)
	}
	if repo.createCalls != 0 {
		t.Fatalf("repo wrote with invalid category; must validate first")
	}
}

func TestExpenses_NonPositiveAmountRejected(t *testing.T) {
	repo := &fakeExpenseRepo{}
	r := expenseRouter(NewExpenseHandler(repo), nil, 42, "owner")
	rec := doJSON(t, r, http.MethodPost, "/owner/expenses",
		map[string]any{"category": "Water", "amount": 0, "occurred_on": "2026-06-16"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 for non-positive amount", rec.Code)
	}
}

func TestFinancials_PlayerForbidden(t *testing.T) {
	er := &fakeExpenseRepo{}
	ar := &fakeAnalyticsRepo{}
	fh := NewFinancialsHandler(ar, er)
	r := expenseRouter(NewExpenseHandler(er), fh, 3, "player")
	rec := doJSON(t, r, http.MethodGet, "/owner/financials", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for player hitting financials", rec.Code)
	}
}

func TestFinancials_OwnerScoped(t *testing.T) {
	er := &fakeExpenseRepo{}
	ar := &fakeAnalyticsRepo{}
	fh := NewFinancialsHandler(ar, er)
	const ownerID = 42
	r := expenseRouter(NewExpenseHandler(er), fh, ownerID, "owner")
	rec := doJSON(t, r, http.MethodGet, "/owner/financials?granularity=day", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if ar.lastActor.UserID != ownerID {
		t.Fatalf("financials collected leg actor = %+v, want owner #%d", ar.lastActor, ownerID)
	}
}
