package handlers

// Native Verified Review System — HTTP layer. Authorization is enforced here
// (role middleware on the routes) AND server-side inside the handlers for the
// object-level checks the middleware cannot express: review ownership on PUT and
// the admin gate on DELETE. IDs are int64 end-to-end (no UUIDs in this schema).

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

// ReviewHandler serves the review + eligibility + moderation endpoints.
type ReviewHandler struct {
	repo repository.ReviewRepository
}

func NewReviewHandler(repo repository.ReviewRepository) *ReviewHandler {
	return &ReviewHandler{repo: repo}
}

// parseID64 reads a positive path param as int64. It delegates to the shared
// parseIDParam (which writes the 400 on failure) and widens to int64 — review
// keys are int64 throughout the data layer.
func parseID64(c *gin.Context, name string) (int64, bool) {
	id, ok := parseIDParam(c, name)
	return int64(id), ok
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /pitches/:id/review-eligibility   (player)
// ─────────────────────────────────────────────────────────────────────────────

func (h *ReviewHandler) GetEligibility(c *gin.Context) {
	pitchID, ok := parseID64(c, "id")
	if !ok {
		return
	}
	playerID := int64(middleware.GetUserID(c))

	elig, err := h.repo.CheckEligibility(c.Request.Context(), playerID, pitchID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	// Exact contract: { eligible, qualifying_booking_id, existing_review }.
	c.JSON(http.StatusOK, gin.H{
		"eligible":              elig.Eligible,
		"qualifying_booking_id": elig.QualifyingBookingID,
		"existing_review":       elig.ExistingReview,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /pitches/:id/reviews   (public) — paginated, newest first, masked names
// ─────────────────────────────────────────────────────────────────────────────

func (h *ReviewHandler) ListPitchReviews(c *gin.Context) {
	pitchID, ok := parseID64(c, "id")
	if !ok {
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	reviews, err := h.repo.GetPitchReviews(c.Request.Context(), pitchID, limit, offset)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	agg, err := h.repo.GetPitchRatingAggregates(c.Request.Context(), pitchID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":      reviews,
		"count":     len(reviews),
		"aggregate": agg,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /pitches/:id/reviews   (player)
// ─────────────────────────────────────────────────────────────────────────────

func (h *ReviewHandler) CreateReview(c *gin.Context) {
	pitchID, ok := parseID64(c, "id")
	if !ok {
		return
	}

	var req models.CreateReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "bad_request", "message": "صيغة الطلب غير صحيحة",
		})
		return
	}

	// Server-injected identity + scope — never trusted from the body (BOLA).
	req.PitchID = pitchID
	req.PlayerID = int64(middleware.GetUserID(c))

	// CRITICAL: the qualifying booking is NOT taken from the client. We re-run the
	// Derived eligibility check server-side (past, non-cancelled booking + owner
	// exclusion). The DB FK only proves player+pitch consistency, not the
	// "completed" condition — so this re-check is the authoritative gate.
	elig, err := h.repo.CheckEligibility(c.Request.Context(), req.PlayerID, pitchID)
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}
	if !elig.Eligible || elig.QualifyingBookingID == nil {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "not_eligible",
			"message": "لا يمكنك تقييم هذا الملعب: لا يوجد حجز منتهٍ مؤهل",
		})
		return
	}
	// Use the server-derived booking id, discarding anything the client sent.
	req.QualifyingBookingID = *elig.QualifyingBookingID

	review, err := h.repo.CreateReview(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrAlreadyReviewed):
			c.JSON(http.StatusConflict, gin.H{
				"error": "already_reviewed", "message": "لقد قمت بتقييم هذا الملعب مسبقاً",
			})
		case errors.Is(err, repository.ErrReviewBookingInvalid):
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error": "invalid_booking", "message": "لا يمكن التقييم: لا يوجد حجز مؤهل لهذا الملعب",
			})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
			})
		}
		return
	}

	c.JSON(http.StatusCreated, gin.H{"data": review})
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /reviews/:id   (player) — strict server-side ownership
// ─────────────────────────────────────────────────────────────────────────────

func (h *ReviewHandler) UpdateReview(c *gin.Context) {
	reviewID, ok := parseID64(c, "id")
	if !ok {
		return
	}

	var req models.UpdateReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "bad_request", "message": "صيغة الطلب غير صحيحة",
		})
		return
	}

	callerID := int64(middleware.GetUserID(c))

	// Ownership is enforced HERE, server-side: load the review and compare its
	// player_id to the caller. A non-owner gets 403 and a missing review 404.
	existing, err := h.repo.GetReviewByID(c.Request.Context(), reviewID)
	if err != nil {
		if errors.Is(err, repository.ErrReviewNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "التقييم غير موجود",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}
	if existing.PlayerID != callerID {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "لا يمكنك تعديل تقييم لا يخصك",
		})
		return
	}

	updated, err := h.repo.UpdateReview(c.Request.Context(), reviewID, req.Rating, req.Comment)
	if err != nil {
		if errors.Is(err, repository.ErrReviewNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "التقييم غير موجود",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": updated})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /reviews/:id/flag   (any authenticated user — NOT public)
// ─────────────────────────────────────────────────────────────────────────────

func (h *ReviewHandler) FlagReview(c *gin.Context) {
	reviewID, ok := parseID64(c, "id")
	if !ok {
		return
	}

	if err := h.repo.FlagReview(c.Request.Context(), reviewID); err != nil {
		if errors.Is(err, repository.ErrReviewNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "التقييم غير موجود",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"flagged": true}})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /reviews/:id   (admin) — strict server-side admin enforcement
// ─────────────────────────────────────────────────────────────────────────────

func (h *ReviewHandler) DeleteReview(c *gin.Context) {
	reviewID, ok := parseID64(c, "id")
	if !ok {
		return
	}

	// Defence in depth: the route is RequireRole("admin"), but re-assert it here
	// so the destructive soft-delete can never run for a non-admin even if the
	// route were ever re-wired.
	if middleware.GetActor(c).Role != auth.RoleAdmin {
		c.JSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "هذا الإجراء مخصص للمشرفين فقط",
		})
		return
	}

	if err := h.repo.SoftDeleteReview(c.Request.Context(), reviewID); err != nil {
		if errors.Is(err, repository.ErrReviewNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "التقييم غير موجود",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": gin.H{"deleted": true}})
}
