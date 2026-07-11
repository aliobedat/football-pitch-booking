package handlers

// VenueHandler — WO-VENUES / Gate 1b. Owner/admin venue CRUD (same layering and
// 404-not-403 conventions as pitches) + the unauthenticated B2C venue reads.
// Slug is validated here (pattern) and by the DB (ci-unique index → 409) and is
// IMMUTABLE: no update path exists for it.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/geo"
	"github.com/ali/football-pitch-api/internal/middleware"
)

type VenueHandler struct {
	Model *data.VenueModel
}

func NewVenueHandler(model *data.VenueModel) *VenueHandler {
	return &VenueHandler{Model: model}
}

// CreateVenue — POST /venues (owner/admin).
func (h *VenueHandler) CreateVenue(c *gin.Context) {
	var req data.CreateVenueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	req.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	if !data.ValidSlug(req.Slug) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_slug", "field": "slug",
			"message": "المعرّف يجب أن يكون أحرفاً إنجليزية صغيرة وأرقاماً تفصلها شرطات (مثال: elite-arena)",
		})
		return
	}
	req.OwnerID = middleware.GetUserID(c)

	venue, err := h.Model.Create(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, data.ErrSlugTaken) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "slug_taken", "field": "slug",
				"message": "هذا المعرّف مستخدم لمجمع آخر — اختر معرّفاً مختلفاً",
			})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "تعذّر إنشاء المجمع"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"message": "تم إنشاء المجمع", "data": venue})
}

// OwnerListVenues — GET /owner/venues (owner-scoped; admin unscoped).
func (h *VenueHandler) OwnerListVenues(c *gin.Context) {
	venues, err := h.Model.OwnerList(c.Request.Context(), middleware.GetActor(c))
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "تعذّر تحميل المجمعات"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": venues, "count": len(venues)})
}

// UpdateVenue — PATCH /venues/:id. Every field except slug/owner.
func (h *VenueHandler) UpdateVenue(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id", "message": "معرّف المجمع غير صالح"})
		return
	}
	var req data.UpdateVenueRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}
	venue, err := h.Model.Update(c.Request.Context(), middleware.GetActor(c), id, req)
	if err != nil {
		if errors.Is(err, data.ErrVenueNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "المجمع غير موجود أو لا تملك صلاحية تعديله"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "تعذّر تعديل المجمع"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "تم تعديل المجمع", "data": venue})
}

// ToggleVenueActive — PATCH /venues/:id/active {is_active} (mirrors pitches).
func (h *VenueHandler) ToggleVenueActive(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id", "message": "معرّف المجمع غير صالح"})
		return
	}
	var req struct {
		IsActive *bool `json:"is_active" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.IsActive == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "is_active مطلوب"})
		return
	}
	venue, err := h.Model.ToggleActive(c.Request.Context(), middleware.GetActor(c), id, *req.IsActive)
	if err != nil {
		if errors.Is(err, data.ErrVenueNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "المجمع غير موجود أو لا تملك صلاحية تعديله"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "تعذّر تحديث حالة المجمع"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "تم تحديث حالة المجمع", "data": venue})
}

// DeleteVenue — DELETE /venues/:id (soft). Refuses while live pitches remain.
func (h *VenueHandler) DeleteVenue(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id", "message": "معرّف المجمع غير صالح"})
		return
	}
	err = h.Model.SoftDelete(c.Request.Context(), middleware.GetActor(c), id)
	if err != nil {
		switch {
		case errors.Is(err, data.ErrVenueNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "المجمع غير موجود أو لا تملك صلاحية حذفه"})
		case errors.Is(err, data.ErrVenueHasPitches):
			c.JSON(http.StatusConflict, gin.H{
				"error": "venue_has_pitches", "message": "لا يمكن حذف مجمع لا تزال فيه ملاعب — انقل الملاعب أو احذفها أولاً",
			})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "تعذّر حذف المجمع"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "تم حذف المجمع"})
}

// PublicVenueBySlug — GET /venues/:id (public; the param is the SLUG — the
// route param is named :id only to satisfy gin's single-wildcard-name rule
// against PATCH /venues/:id). 404 on unknown, inactive, or soft-deleted.
func (h *VenueHandler) PublicVenueBySlug(c *gin.Context) {
	slug := strings.TrimSpace(c.Param("id"))
	venue, err := h.Model.PublicBySlug(c.Request.Context(), slug)
	if err != nil {
		if errors.Is(err, data.ErrVenueNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "المجمع غير موجود"})
			return
		}
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "حدث خطأ داخلي في الخادم"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": venue})
}

// PublicListVenues — GET /venues (public B2C listing: one card per venue).
// Optional ?lat&lng → nearest-first using VENUE coordinates (fixes the per-pitch
// duplicate-distance-rows issue from the Gate 0 risk register).
func (h *VenueHandler) PublicListVenues(c *gin.Context) {
	player, ok := parsePlayerCoords(c)
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_coords", "message": "lat and lng must be provided together as numbers",
		})
		return
	}

	venues, err := h.Model.PublicList(c.Request.Context())
	if err != nil {
		c.Error(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "حدث خطأ داخلي في الخادم"})
		return
	}

	if player.HasUsableCoords() {
		geo.SortByDistance(
			venues, player,
			func(v data.Venue) geo.Coordinates { return geo.Coordinates{Lat: &v.Latitude, Lng: &v.Longitude} },
			func(v *data.Venue, d *float64) { v.DistanceKm = d },
			func(v data.Venue) *float64 { return v.DistanceKm },
		)
	}

	c.JSON(http.StatusOK, gin.H{"data": venues, "count": len(venues)})
}
