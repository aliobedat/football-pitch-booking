package handlers

// PART 6: notification consent management. A user may withdraw consent at any
// time; once withdrawn, the NotificationService opt-out gate blocks EVERY
// outbound message to them (OTP and booking events alike). This endpoint records
// that withdrawal against the authenticated user.

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

// OptOutStore is the narrow persistence seam the opt-out handler needs.
// repository.AuthRepository satisfies it; tests use an in-memory fake.
type OptOutStore interface {
	SetOptOut(ctx context.Context, userID int, optOut bool) error
}

// NotificationHandler serves consent-management endpoints.
type NotificationHandler struct {
	store OptOutStore
}

// NewNotificationHandler wires the handler with its consent store.
func NewNotificationHandler(store OptOutStore) *NotificationHandler {
	return &NotificationHandler{store: store}
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/notifications/opt-out
// ─────────────────────────────────────────────────────────────────────────────

// OptOut records the authenticated user's withdrawal of consent. It is
// idempotent: opting out an already-opted-out user succeeds. Identity comes from
// the JWT (set by RequireAuth), so no body is required — a user can only opt
// themselves out.
func (h *NotificationHandler) OptOut(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "unauthorized", "message": "authentication is required",
		})
		return
	}

	if err := h.store.SetOptOut(c.Request.Context(), userID, true); err != nil {
		if errors.Is(err, repository.ErrUserNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "user_not_found", "message": "no user exists for this session",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not update notification preferences, please try again",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "you have been opted out of all notifications",
		"opted_out": true,
	})
}
