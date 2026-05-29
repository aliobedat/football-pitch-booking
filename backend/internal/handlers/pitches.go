package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
)

type PitchHandler struct {
	Model *data.PitchModel
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

	ownerID := middleware.GetUserID(c)
	pitch, err := h.Model.UpdatePitch(c.Request.Context(), id, ownerID, req)
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

	ownerID := middleware.GetUserID(c)
	if err := h.Model.DeletePitch(c.Request.Context(), id, ownerID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{
				"error":   "not_found",
				"message": "الملعب غير موجود أو لا تملك صلاحية حذفه",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "internal_server_error",
			"message": "حدث خطأ أثناء حذف الملعب",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "تم حذف الملعب بنجاح"})
}

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/owner/pitches  (owner only)
// ─────────────────────────────────────────────────────────────────────────────

func (h *PitchHandler) GetOwnerPitches(c *gin.Context) {
	ownerID := middleware.GetUserID(c)

	pitches, err := h.Model.GetByOwnerID(c.Request.Context(), ownerID)
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
