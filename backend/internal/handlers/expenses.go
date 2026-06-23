package handlers

// Expense Ledger CRUD (Cockpit WO-F2). Owner/admin only — staff/players barred at
// the route (RequireRole) and re-asserted here. Every read/write is owner-scoped
// in the repository SQL (admin unscoped per the Actor model). Mutations are CSRF-
// protected by the protected-group middleware. Soft-delete preserves history.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

type ExpenseHandler struct {
	repo repository.ExpenseRepository
}

func NewExpenseHandler(repo repository.ExpenseRepository) *ExpenseHandler {
	return &ExpenseHandler{repo: repo}
}

func (h *ExpenseHandler) guard(c *gin.Context) (auth.Actor, bool) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "expenses are restricted to pitch owners",
		})
		return actor, false
	}
	return actor, true
}

// expenseBody is the create/update payload. occurred_on is a plain Amman calendar
// date (YYYY-MM-DD) — the cost is attributed to that civil day (anchored to Amman
// noon so it lands inside the day regardless of zone math), consistent with the
// Amman bucketing of the Net engine.
type expenseBody struct {
	PitchID    *int64  `json:"pitch_id"`
	Category   string  `json:"category"`
	Amount     float64 `json:"amount"`
	OccurredOn string  `json:"occurred_on"`
	Note       string  `json:"note"`
}

func (b expenseBody) toInput() (repository.ExpenseInput, string) {
	cat := strings.TrimSpace(b.Category)
	if !models.IsValidExpenseCategory(cat) {
		return repository.ExpenseInput{}, "category must be one of: Electricity, Staff, Water, Maintenance, Marketing, Other"
	}
	if b.Amount <= 0 {
		return repository.ExpenseInput{}, "amount must be greater than zero"
	}
	d, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(b.OccurredOn), timeutil.Amman())
	if err != nil {
		return repository.ExpenseInput{}, "occurred_on must be a date (YYYY-MM-DD)"
	}
	// Anchor to Amman noon so the instant unambiguously belongs to that civil day.
	y, m, day := d.Date()
	occurred := time.Date(y, m, day, 12, 0, 0, 0, timeutil.Amman()).UTC()
	if len(b.Note) > 500 {
		return repository.ExpenseInput{}, "note is too long (max 500 chars)"
	}
	return repository.ExpenseInput{
		PitchID: b.PitchID, Category: cat, Amount: b.Amount,
		OccurredAt: occurred, Note: strings.TrimSpace(b.Note),
	}, ""
}

func (h *ExpenseHandler) writeRepoErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrExpenseNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "المصروف غير موجود"})
	case errors.Is(err, repository.ErrPitchNotOwned):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_pitch", "message": "الملعب المحدد ليس ضمن ملاعبك"})
	default:
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "تعذّر تنفيذ العملية"})
	}
}

// ListExpenses — GET /owner/expenses?from=&to=&category=
func (h *ExpenseHandler) ListExpenses(c *gin.Context) {
	actor, ok := h.guard(c)
	if !ok {
		return
	}
	// Default window: trailing 30 Amman days.
	now := time.Now().UTC()
	_, toEnd := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now))
	fromStart, _ := timeutil.AmmanDayBoundsUTC(timeutil.InAmman(now).AddDate(0, 0, -29))
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		if d, err := time.Parse("2006-01-02", raw); err == nil {
			fromStart, _ = timeutil.AmmanDayBoundsUTC(d)
		}
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		if d, err := time.Parse("2006-01-02", raw); err == nil {
			_, toEnd = timeutil.AmmanDayBoundsUTC(d)
		}
	}
	category := strings.TrimSpace(c.Query("category"))
	if category != "" && !models.IsValidExpenseCategory(category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_category", "message": "unknown category filter"})
		return
	}

	list, err := h.repo.List(c.Request.Context(), actor, fromStart, toEnd, category)
	if err != nil {
		h.writeRepoErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "count": len(list)})
}

// CreateExpense — POST /owner/expenses
func (h *ExpenseHandler) CreateExpense(c *gin.Context) {
	actor, ok := h.guard(c)
	if !ok {
		return
	}
	var body expenseBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	in, msg := body.toInput()
	if msg != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_expense", "message": msg})
		return
	}
	// Idempotency: when the client supplies an Idempotency-Key, attach it so a
	// double-tap / retry returns the original expense instead of inserting a second
	// ledger row. Absent header → nil → legacy non-idempotent path (same idiom as
	// POST /bookings). owner_id stays server-derived from the actor in the repo.
	if key := strings.TrimSpace(c.GetHeader(idempotencyHeader)); key != "" {
		in.IdempotencyKey = &key
	}
	e, err := h.repo.Create(c.Request.Context(), actor, in)
	if err != nil {
		h.writeRepoErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": e})
}

// UpdateExpense — PATCH /owner/expenses/:id
func (h *ExpenseHandler) UpdateExpense(c *gin.Context) {
	actor, ok := h.guard(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var body expenseBody
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	in, msg := body.toInput()
	if msg != "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_expense", "message": msg})
		return
	}
	e, err := h.repo.Update(c.Request.Context(), actor, int64(id), in)
	if err != nil {
		h.writeRepoErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": e})
}

// DeleteExpense — DELETE /owner/expenses/:id (soft delete)
func (h *ExpenseHandler) DeleteExpense(c *gin.Context) {
	actor, ok := h.guard(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.repo.SoftDelete(c.Request.Context(), actor, int64(id)); err != nil {
		h.writeRepoErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "تم حذف المصروف"})
}
