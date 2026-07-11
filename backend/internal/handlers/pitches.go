package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/ali/football-pitch-api/internal/cloudinary"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/geo"
	"github.com/ali/football-pitch-api/internal/middleware"
)

// maxDescriptionLen caps a pitch description (counted in UTF-8 runes, so Arabic
// text is measured fairly). Stored raw; escaped at render time.
const maxDescriptionLen = 1000

// maxLabelLen caps the pitch's in-venue label («تسمية الملعب», Gate 1d-minimal) —
// short display text, same rune-counted fairness as the description.
const maxLabelLen = 60

// normalizeDescription trims surrounding whitespace and enforces the length cap.
// Returns the cleaned value and false when it exceeds the cap (→ 400).
func normalizeDescription(raw string) (string, bool) {
	d := strings.TrimSpace(raw)
	if utf8.RuneCountInString(d) > maxDescriptionLen {
		return "", false
	}
	return d, true
}

type PitchHandler struct {
	Model      *data.PitchModel
	Cloudinary *cloudinary.Service
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/pitches  (public)
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) ListPitches(c *gin.Context) {
	filters := data.PitchFilters{
		Neighborhood: c.Query("neighborhood"),
		Format:       c.Query("format"),
		FeaturedOnly: c.Query("featured") == "true",
	}

	// Optional player coordinates for the "الأقرب إلي" (nearest) sort. Reuses Path
	// A's parser/contract: both-or-neither; exactly-one or unparseable → 422. Absent
	// → no location supplied (the listing keeps its default order).
	player, ok := parsePlayerCoords(c)
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "invalid_coords", "message": "lat and lng must be provided together as numbers",
		})
		return
	}

	pitches, err := h.Model.GetAll(c.Request.Context(), filters)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	// Nearest-first ONLY when usable player coordinates were supplied. The SQL
	// fetch order (is_featured DESC, price_per_hour ASC, id ASC) is untouched; the
	// shared, STABLE geo.SortByDistance reorders ascending by distance and keeps
	// that SQL order as the tiebreak among equidistant pitches. NULL/(0,0)-coord
	// pitches stay in the stable tail. No coords → the default order is returned as-is.
	if player.HasUsableCoords() {
		geo.SortByDistance(
			pitches, player,
			func(p data.Pitch) geo.Coordinates { return geo.Coordinates{Lat: &p.Latitude, Lng: &p.Longitude} },
			func(p *data.Pitch, d *float64) { p.DistanceKm = d },
			func(p data.Pitch) *float64 { return p.DistanceKm },
		)
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  pitches,
		"count": len(pitches),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/pitches/:id  (public)
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) GetPitch(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "bad_request",
			"message": "رقم الملعب غير صحيح",
		})
		return
	}

	pitch, err := h.Model.GetByID(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "الملعب غير موجود",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": pitch})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/pitches  (owner only)
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) CreatePitch(c *gin.Context) {
	var req data.CreatePitchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "invalid_request", "message": err.Error(),
		})
		return
	}

	req.OwnerID = middleware.GetUserID(c)
	req.ActorRole = middleware.GetActor(c).Role // admin: owner derived from venue
	req.MapsURL = strings.TrimSpace(req.MapsURL)

	// Label (Gate 1d-minimal): trim + rune-cap; empty is fine (falls back to name).
	req.Label = strings.TrimSpace(req.Label)
	if utf8.RuneCountInString(req.Label) > maxLabelLen {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_label",
			"field":   "label",
			"message": fmt.Sprintf("التسمية تتجاوز الحد الأقصى (%d حرفاً)", maxLabelLen),
		})
		return
	}

	// Trim + length-cap the free-text description before it reaches the INSERT.
	if cleaned, ok := normalizeDescription(req.Description); ok {
		req.Description = cleaned
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_description",
			"message": fmt.Sprintf("الوصف يتجاوز الحد الأقصى (%d حرف)", maxDescriptionLen),
		})
		return
	}

	// Mandatory location: a new pitch has no coordinates yet, so the predicate
	// reduces to "a valid Google maps_url is required". Reject (422, with a
	// field-level error the UI renders inline on the URL field).
	if !geo.RequireLocationSource(geo.LocationState{MapsURL: req.MapsURL}) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "invalid_maps_url",
			"field":   "maps_url",
			"message": "رابط موقع الملعب على خرائط جوجل مطلوب (انسخ رابط المشاركة من تطبيق الخرائط)",
		})
		return
	}

	// Trust guard: an image may be set at creation, but only a URL from OUR
	// Cloudinary account may be persisted (bytes never hit the backend, so the URL
	// string is the validation boundary). An image_url without its public_id (or
	// vice versa) is incoherent for later cleanup, so require them together.
	if req.ImageURL != "" || req.ImagePublicID != "" {
		if req.ImageURL == "" || req.ImagePublicID == "" || !h.Cloudinary.OwnsURL(req.ImageURL) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_image",
				"message": "رابط الصورة غير صالح",
			})
			return
		}
	}

	pitch, err := h.Model.CreatePitch(c.Request.Context(), req)
	if err != nil {
		// WO-VENUES: an unknown/foreign/deleted venue_id is a 404, never a 403
		// (existence not leaked — the pitches convention).
		if errors.Is(err, data.ErrVenueNotFound) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "venue_not_found", "message": "المجمع غير موجود أو لا تملك صلاحية استخدامه",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ أثناء إنشاء الملعب",
		})
		return
	}

	// Post-commit, best-effort: resolve coordinates from the link in the background
	// so the owner's response is instant. On failure the pitch stays in the
	// manual-pin queue (maps_url set, no usable coordinates).
	h.Model.ResolveCoordsAsync(pitch.ID, req.MapsURL)

	c.JSON(http.StatusCreated, gin.H{
		"message": "تم إنشاء الملعب بنجاح",
		"data":    pitch,
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /api/v1/pitches/:id  (owner only — must own the pitch)
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) UpdatePitch(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_request", "message": "رقم الملعب غير صحيح"})
		return
	}

	var req data.UpdatePitchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	// Trim + length-cap the free-text description before it reaches the UPDATE.
	if cleaned, ok := normalizeDescription(req.Description); ok {
		req.Description = cleaned
	} else {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_description",
			"message": fmt.Sprintf("الوصف يتجاوز الحد الأقصى (%d حرف)", maxDescriptionLen),
		})
		return
	}

	if req.MapsURL != nil {
		trimmed := strings.TrimSpace(*req.MapsURL)
		req.MapsURL = &trimmed
	}

	// Ownership scoping AND the mandatory-location gate both live in the data layer:
	// UpdatePitch reads the current maps_url + coordinates under the actor's
	// ownership predicate, so a missing/foreign pitch is a 404 and a location-less
	// result is ErrLocationRequired — neither leaks existence.
	pitch, err := h.Model.UpdatePitch(c.Request.Context(), id, middleware.GetActor(c), req)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "الملعب غير موجود أو لا تملك صلاحية تعديله",
			})
		case errors.Is(err, data.ErrLocationRequired):
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"error":   "invalid_maps_url",
				"field":   "maps_url",
				"message": "يجب أن يحتفظ الملعب برابط موقع على خرائط جوجل أو بإحداثيات صالحة",
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "internal_server_error",
				"message": "حدث خطأ أثناء تحديث الملعب",
			})
		}
		return
	}

	// Best-effort background resolution: only when the pitch still lacks usable
	// coordinates and now carries a resolvable link.
	pitchCoords := geo.Coordinates{Lat: &pitch.Latitude, Lng: &pitch.Longitude}
	if !pitchCoords.HasUsableCoords() && geo.ValidGoogleMapsURL(pitch.MapsURL) {
		h.Model.ResolveCoordsAsync(pitch.ID, pitch.MapsURL)
	}

	c.JSON(http.StatusOK, gin.H{"message": "تم تحديث الملعب بنجاح", "data": pitch})
}

// ─────────────────────────────────────────────────────────────────────────────
// DELETE /api/v1/pitches/:id  (owner only — must own the pitch)
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) DeletePitch(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_request", "message": "رقم الملعب غير صحيح"})
		return
	}

	// Soft delete, scoped to the actor, guarded against future bookings, audited —
	// all in one transaction inside the data layer.
	futureCount, err := h.Model.SoftDeletePitch(c.Request.Context(), id, middleware.GetActor(c))
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			// Not found OR not owned — same response, no existence leak.
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "الملعب غير موجود أو لا تملك صلاحية حذفه",
			})
		case errors.Is(err, data.ErrPitchHasFutureBookings):
			c.JSON(http.StatusConflict, gin.H{
				"error":   "pitch_has_future_bookings",
				"message": fmt.Sprintf("لا يمكن حذف الملعب لوجود %d حجز مؤكد قادم. يرجى إلغاء الحجوزات أولاً.", futureCount),
				"count":   futureCount,
			})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "internal_server_error",
				"message": "حدث خطأ أثناء حذف الملعب",
			})
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "تم حذف الملعب بنجاح"})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /api/v1/pitches/:id/active  (owner/admin — intent-revealing toggle)
// ─────────────────────────────────────────────────────────────────────────────

// ToggleActive activates or deactivates a pitch. Deactivation removes it from
// player-facing listing/availability without touching existing bookings. Scoped
// to the actor in the data layer (owner → own only → 404 otherwise; admin → any;
// players are barred at the route via RequireRole). Idempotent.
func (h *PitchHandler) ToggleActive(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_request", "message": "رقم الملعب غير صحيح"})
		return
	}

	// Pointer so an omitted field is rejected (binding:"required" treats a nil
	// pointer as missing) while an explicit `false` is still accepted.
	var req struct {
		IsActive *bool `json:"is_active" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "الحقل is_active مطلوب"})
		return
	}

	if err := h.Model.SetPitchActive(c.Request.Context(), id, middleware.GetActor(c), *req.IsActive); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "الملعب غير موجود أو لا تملك صلاحية تعديله",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ أثناء تحديث حالة الملعب",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "تم تحديث حالة الملعب",
		"data":    gin.H{"id": id, "is_active": *req.IsActive},
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// POST /api/v1/pitches/upload-signature  (owner/admin — players barred at route)
// ─────────────────────────────────────────────────────────────────────────────

// UploadSignature returns a short-lived, backend-signed payload the browser uses
// to upload a pitch image DIRECTLY to Cloudinary. The API secret is used only to
// compute the signature and never leaves the backend; folder + upload_preset are
// pinned into the signature so a leaked payload cannot retarget the upload.
func (h *PitchHandler) UploadSignature(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": h.Cloudinary.SignUpload()})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /api/v1/pitches/:id/image  (owner/admin — must own the pitch)
// ─────────────────────────────────────────────────────────────────────────────

// SetPitchImage persists the result of a completed direct upload (secure_url +
// public_id) onto a pitch, scoped to the actor (owner → own / 404; admin → any)
// and only while the pitch is live (deleted_at IS NULL). The SERVER-SIDE TRUST
// GUARD rejects any image_url that is not a delivery URL for our Cloudinary
// account — the real validation boundary, since bytes never transit the backend.
// On a successful replace it best-effort destroys the previous asset so storage
// does not accumulate orphans.
//
// An empty body ({image_url:"", public_id:""}) clears the image (the frontend
// "remove image" path) and still triggers cleanup of the asset being removed.
func (h *PitchHandler) SetPitchImage(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_request", "message": "رقم الملعب غير صحيح"})
		return
	}

	var req struct {
		ImageURL string `json:"image_url"`
		PublicID string `json:"public_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	// Setting an image requires BOTH fields, and the URL must belong to our cloud.
	// Clearing the image requires BOTH empty. Any other combination is rejected.
	setting := req.ImageURL != "" || req.PublicID != ""
	if setting {
		if req.ImageURL == "" || req.PublicID == "" || !h.Cloudinary.OwnsURL(req.ImageURL) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error":   "invalid_image",
				"message": "رابط الصورة غير صالح",
			})
			return
		}
	}

	oldPublicID, err := h.Model.SetPitchImage(c.Request.Context(), id, middleware.GetActor(c), req.ImageURL, req.PublicID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "الملعب غير موجود أو لا تملك صلاحية تعديله",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ أثناء تحديث صورة الملعب",
		})
		return
	}

	// Best-effort cleanup of the replaced asset. A destroy failure must not fail
	// the request — the new image is already persisted — so we only log it.
	if oldPublicID != "" {
		if derr := h.Cloudinary.Destroy(c.Request.Context(), oldPublicID); derr != nil {
			log.Printf("[CLOUDINARY] failed to destroy replaced asset %q for pitch %d: %v", oldPublicID, id, derr)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "تم تحديث صورة الملعب",
		"data":    gin.H{"id": id, "image_url": req.ImageURL, "image_public_id": req.PublicID},
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/owner/pitches  (owner only)
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) GetOwnerPitches(c *gin.Context) {
	// Scoping is in the data layer: admin → all pitches, owner → only their own.
	pitches, err := h.Model.ListForActor(c.Request.Context(), middleware.GetActor(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":  pitches,
		"count": len(pitches),
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// PATCH /api/v1/pitches/:id/venue  (owner/admin — «نقل إلى مجمع», WO-VENUES)
// ─────────────────────────────────────────────────────────────────────────────

// ReassignVenue moves a pitch to another venue owned by the same owner. An
// unknown/foreign pitch OR venue is a 404 (existence not leaked). If the old
// venue is emptied it is soft-deleted in the same transaction; the move is
// audited in pitch_audit_log.
func (h *PitchHandler) ReassignVenue(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_id", "message": "معرّف الملعب غير صالح"})
		return
	}
	var req struct {
		VenueID int64 `json:"venue_id" binding:"required,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": "venue_id مطلوب"})
		return
	}

	pitch, err := h.Model.ReassignVenue(c.Request.Context(), middleware.GetActor(c), id, req.VenueID)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الملعب غير موجود أو لا تملك صلاحية تعديله"})
		case errors.Is(err, data.ErrVenueNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": "venue_not_found", "message": "المجمع غير موجود أو لا تملك صلاحية استخدامه"})
		case errors.Is(err, data.ErrVenueOwnerMismatch):
			// Admin-only surface: visible-but-refused (ownership invariant).
			c.JSON(http.StatusConflict, gin.H{"error": "venue_owner_mismatch", "message": "المجمع يعود لمالك مختلف عن مالك الملعب — لا يمكن النقل"})
		default:
			c.Error(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "internal_error", "message": "تعذّر نقل الملعب"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "تم نقل الملعب إلى المجمع", "data": pitch})
}
