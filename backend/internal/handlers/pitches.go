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
	"github.com/ali/football-pitch-api/internal/middleware"
)

// maxDescriptionLen caps a pitch description (counted in UTF-8 runes, so Arabic
// text is measured fairly). Stored raw; escaped at render time.
const maxDescriptionLen = 1000

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

	pitches, err := h.Model.GetAll(c.Request.Context(), filters)
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
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ أثناء إنشاء الملعب",
		})
		return
	}

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

	// maps_url: only validated when present (non-nil) and non-empty. An empty
	// string is a legitimate "clear the link"; a non-empty value must be an https
	// URL. No deeper URL parsing — paste-and-save only.
	if req.MapsURL != nil && *req.MapsURL != "" && !strings.HasPrefix(*req.MapsURL, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "invalid_maps_url",
			"message": "رابط الموقع يجب أن يبدأ بـ https://",
		})
		return
	}

	// Ownership scoping lives in the data layer: an owner may update only their
	// own pitch, an admin any. The Actor carries {UserID, Role} from the JWT.
	pitch, err := h.Model.UpdatePitch(c.Request.Context(), id, middleware.GetActor(c), req)
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
			"message": "حدث خطأ أثناء تحديث الملعب",
		})
		return
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
