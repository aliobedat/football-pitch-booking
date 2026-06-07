package handlers

// Deterministic (no-DB) security tests for the review HTTP boundary. A
// programmable fake ReviewRepository lets each test pin one branch: the
// eligibility gate on POST, the ownership gate on PUT, the admin gate on DELETE,
// and the sentinel→status mappings. The DB-truth cases (real eligibility
// derivation, FK/unique enforcement) live in the gated integration test.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

// ─── Fake repository ──────────────────────────────────────────────────────────

type fakeReviewRepo struct {
	eligibility     models.ReviewEligibility
	eligibilityErr  error
	createReview    *models.Review
	createErr       error
	createdWithBkID int64 // captured booking id actually passed to CreateReview
	getByID         *models.Review
	getByIDErr      error
	updated         *models.Review
	updateErr       error
	softDeleteErr   error
	flagErr         error
}

func (f *fakeReviewRepo) CreateReview(_ context.Context, req models.CreateReviewRequest) (*models.Review, error) {
	f.createdWithBkID = req.QualifyingBookingID
	if f.createErr != nil {
		return nil, f.createErr
	}
	return f.createReview, nil
}
func (f *fakeReviewRepo) GetPitchReviews(context.Context, int64, int, int) ([]models.Review, error) {
	return nil, nil
}
func (f *fakeReviewRepo) GetPitchRatingAggregates(context.Context, int64) (models.RatingAggregate, error) {
	return models.RatingAggregate{}, nil
}
func (f *fakeReviewRepo) GetReviewByID(_ context.Context, _ int64) (*models.Review, error) {
	return f.getByID, f.getByIDErr
}
func (f *fakeReviewRepo) UpdateReview(_ context.Context, _ int64, _ int16, _ *string) (*models.Review, error) {
	return f.updated, f.updateErr
}
func (f *fakeReviewRepo) SoftDeleteReview(context.Context, int64) error { return f.softDeleteErr }
func (f *fakeReviewRepo) FlagReview(context.Context, int64) error       { return f.flagErr }
func (f *fakeReviewRepo) CheckEligibility(_ context.Context, _, _ int64) (models.ReviewEligibility, error) {
	return f.eligibility, f.eligibilityErr
}

// router wires the review routes exactly as production does (role middleware +
// the handler), injecting the given identity in place of RequireAuth.
func router(repo repository.ReviewRepository, userID int, role string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewReviewHandler(repo)
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/pitches/:id/reviews", inject, middleware.RequireRole("player"), h.CreateReview)
	r.PUT("/reviews/:id", inject, middleware.RequireRole("player"), h.UpdateReview)
	r.DELETE("/reviews/:id", inject, middleware.RequireRole("admin"), h.DeleteReview)
	return r
}

func do(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func ptr[T any](v T) *T { return &v }

// ─── POST eligibility gate ────────────────────────────────────────────────────

// Cases 1–4: every "ineligible" reason (no history / future-only / cancelled /
// owner) surfaces from CheckEligibility as eligible=false; the handler must 403.
func TestCreateReview_Ineligible_403(t *testing.T) {
	repo := &fakeReviewRepo{eligibility: models.ReviewEligibility{Eligible: false}}
	w := do(router(repo, 5, auth.RolePlayer), http.MethodPost, "/pitches/9/reviews", `{"rating":5}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("ineligible POST: got %d, want 403 (body %s)", w.Code, w.Body.String())
	}
}

// Security core: a client-supplied qualifying_booking_id must be IGNORED; the
// handler uses the server-derived id from CheckEligibility.
func TestCreateReview_IgnoresClientBookingID(t *testing.T) {
	repo := &fakeReviewRepo{
		eligibility:  models.ReviewEligibility{Eligible: true, QualifyingBookingID: ptr(int64(42))},
		createReview: &models.Review{ID: 1, PitchID: 9, PlayerID: 5, BookingID: 42, Rating: 5},
	}
	// Body tries to forge booking 999.
	w := do(router(repo, 5, auth.RolePlayer), http.MethodPost, "/pitches/9/reviews",
		`{"rating":5,"qualifying_booking_id":999}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("eligible POST: got %d, want 201 (body %s)", w.Code, w.Body.String())
	}
	if repo.createdWithBkID != 42 {
		t.Fatalf("handler passed booking id %d to CreateReview, want server-derived 42 (client forged 999)", repo.createdWithBkID)
	}
}

// Case 5: duplicate review → 409.
func TestCreateReview_Duplicate_409(t *testing.T) {
	repo := &fakeReviewRepo{
		eligibility: models.ReviewEligibility{Eligible: true, QualifyingBookingID: ptr(int64(42))},
		createErr:   repository.ErrAlreadyReviewed,
	}
	w := do(router(repo, 5, auth.RolePlayer), http.MethodPost, "/pitches/9/reviews", `{"rating":4}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("duplicate POST: got %d, want 409 (body %s)", w.Code, w.Body.String())
	}
}

// Case 6: FK backstop (booking belongs to another player) → 422.
func TestCreateReview_ForgedBookingFK_422(t *testing.T) {
	repo := &fakeReviewRepo{
		eligibility: models.ReviewEligibility{Eligible: true, QualifyingBookingID: ptr(int64(42))},
		createErr:   repository.ErrReviewBookingInvalid,
	}
	w := do(router(repo, 5, auth.RolePlayer), http.MethodPost, "/pitches/9/reviews", `{"rating":4}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("forged-FK POST: got %d, want 422 (body %s)", w.Code, w.Body.String())
	}
}

// ─── PUT ownership gate (case 7) ──────────────────────────────────────────────

func TestUpdateReview_NonOwner_403(t *testing.T) {
	repo := &fakeReviewRepo{getByID: &models.Review{ID: 1, PlayerID: 999}} // owned by someone else
	w := do(router(repo, 5, auth.RolePlayer), http.MethodPut, "/reviews/1", `{"rating":3}`)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-owner PUT: got %d, want 403 (body %s)", w.Code, w.Body.String())
	}
}

func TestUpdateReview_Owner_200(t *testing.T) {
	repo := &fakeReviewRepo{
		getByID: &models.Review{ID: 1, PlayerID: 5},
		updated: &models.Review{ID: 1, PlayerID: 5, Rating: 3},
	}
	w := do(router(repo, 5, auth.RolePlayer), http.MethodPut, "/reviews/1", `{"rating":3}`)
	if w.Code != http.StatusOK {
		t.Fatalf("owner PUT: got %d, want 200 (body %s)", w.Code, w.Body.String())
	}
}

// Case 9: editing a soft-deleted review → 404 (GetReviewByID filters deleted).
func TestUpdateReview_SoftDeleted_404(t *testing.T) {
	repo := &fakeReviewRepo{getByIDErr: repository.ErrReviewNotFound}
	w := do(router(repo, 5, auth.RolePlayer), http.MethodPut, "/reviews/1", `{"rating":3}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("soft-deleted PUT: got %d, want 404 (body %s)", w.Code, w.Body.String())
	}
}

// ─── DELETE admin gate (case 8) ───────────────────────────────────────────────

func TestDeleteReview_NonAdmin_403(t *testing.T) {
	repo := &fakeReviewRepo{}
	w := do(router(repo, 5, auth.RolePlayer), http.MethodDelete, "/reviews/1", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin DELETE: got %d, want 403 (body %s)", w.Code, w.Body.String())
	}
}

func TestDeleteReview_Admin_200(t *testing.T) {
	repo := &fakeReviewRepo{softDeleteErr: nil}
	w := do(router(repo, 1, auth.RoleAdmin), http.MethodDelete, "/reviews/1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("admin DELETE: got %d, want 200 (body %s)", w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Deleted bool `json:"deleted"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil || !resp.Data.Deleted {
		t.Fatalf("admin DELETE body: %s (err %v)", w.Body.String(), err)
	}
}

// Defence-in-depth: even if the admin role middleware were removed from the
// route, the handler's own role re-assertion still 403s a non-admin.
func TestDeleteReview_HandlerReassertsAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewReviewHandler(&fakeReviewRepo{})
	r.DELETE("/reviews/:id", func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, 5)
		c.Set(middleware.ContextKeyRole, auth.RolePlayer) // non-admin, no RequireRole
		c.Next()
	}, h.DeleteReview)
	w := do(r, http.MethodDelete, "/reviews/1", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("handler admin re-assert: got %d, want 403 (body %s)", w.Code, w.Body.String())
	}
}
