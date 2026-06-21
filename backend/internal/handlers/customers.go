package handlers

// Regulars CRM HTTP surface (Cockpit WO1). Owner/admin only — staff/players are
// barred at the route (RequireRole) and re-asserted here. Every read/write is
// owner-scoped in SQL via the repository's OwnerScopeFilter, so Owner A can never
// reach Owner B's contacts.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

type CustomerHandler struct {
	repo repository.CustomerRepository
}

func NewCustomerHandler(repo repository.CustomerRepository) *CustomerHandler {
	return &CustomerHandler{repo: repo}
}

// guardOwnerAdmin re-asserts the CRM boundary inside the handler (defence in depth
// behind the route's RequireRole).
func (h *CustomerHandler) guardOwnerAdmin(c *gin.Context) (auth.Actor, bool) {
	actor := middleware.GetActor(c)
	if actor.Role != auth.RoleOwner && actor.Role != auth.RoleAdmin {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
			"error": "forbidden", "message": "customer data is restricted to pitch owners",
		})
		return actor, false
	}
	return actor, true
}

// GetCustomers — GET /owner/customers?search=&sort=
func (h *CustomerHandler) GetCustomers(c *gin.Context) {
	actor, ok := h.guardOwnerAdmin(c)
	if !ok {
		return
	}

	list, err := h.repo.ListCustomers(c.Request.Context(), actor,
		c.Query("search"), strings.TrimSpace(c.Query("sort")))
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not load customers",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": list, "count": len(list)})
}

// GetCustomerProfile — GET /owner/customers/:id
func (h *CustomerHandler) GetCustomerProfile(c *gin.Context) {
	actor, ok := h.guardOwnerAdmin(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	profile, err := h.repo.GetCustomerProfile(c.Request.Context(), actor, int64(id))
	if err != nil {
		if errors.Is(err, repository.ErrCustomerNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "العميل غير موجود",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not load customer",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": profile})
}

// PatchCustomerNotes — PATCH /owner/customers/:id/notes
func (h *CustomerHandler) PatchCustomerNotes(c *gin.Context) {
	actor, ok := h.guardOwnerAdmin(c)
	if !ok {
		return
	}
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	var body struct {
		Notes string `json:"notes"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	// Cap free-text notes to keep the column sane.
	if len(body.Notes) > 2000 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "notes_too_long", "message": "الملاحظات طويلة جداً (الحد 2000 حرف)",
		})
		return
	}

	customer, err := h.repo.UpdateNotes(c.Request.Context(), actor, int64(id), strings.TrimSpace(body.Notes))
	if err != nil {
		if errors.Is(err, repository.ErrCustomerNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "العميل غير موجود",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_error", "message": "could not save notes",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": customer})
}
