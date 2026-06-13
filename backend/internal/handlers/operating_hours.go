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

// maxOperatingWindows caps the number of windows in one PUT. A weekly schedule
// has at most a handful of windows per day; this bounds a hostile payload without
// constraining any realistic owner.
const maxOperatingWindows = 100

// ─────────────────────────────────────────────────────────────────────────────
// GET /api/v1/pitches/:id/operating-hours  (public — players need it to render)
// ─────────────────────────────────────────────────────────────────────────────

// GetOperatingHours returns a pitch's weekly open-window schedule. Public: the
// player pitch-detail page renders bookable / booked / closed from it, and the
// owner editor seeds itself from it. An empty `data` array means the pitch has no
// configured hours (open 24/7 per the PR's fail-open decision).
func (h *PitchHandler) GetOperatingHours(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_request", "message": "رقم الملعب غير صحيح"})
		return
	}

	windows, err := h.Model.GetOperatingHours(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "الملعب غير موجود"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ داخلي في الخادم",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{"data": windows, "count": len(windows)})
}

// ─────────────────────────────────────────────────────────────────────────────
// PUT /api/v1/pitches/:id/operating-hours  (owner/admin — must own the pitch)
// ─────────────────────────────────────────────────────────────────────────────

// PutOperatingHours replaces a pitch's whole weekly schedule atomically. The
// editor submits the FULL grid; there are no granular per-window endpoints. The
// payload is validated (fail-closed: bad time, open==close, or any overlap
// including cross-midnight spillover → 400) before the actor-scoped hard replace.
func (h *PitchHandler) PutOperatingHours(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad_request", "message": "رقم الملعب غير صحيح"})
		return
	}

	var req struct {
		Windows []data.OperatingWindow `json:"windows"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_request", "message": err.Error()})
		return
	}

	if len(req.Windows) > maxOperatingWindows {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "too_many_windows", "message": "عدد فترات العمل يتجاوز الحد المسموح",
		})
		return
	}

	// Fail-closed validation BEFORE touching the DB. The message is safe to surface.
	if err := data.ValidateSchedule(req.Windows); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_operating_hours", "message": err.Error()})
		return
	}

	if err := h.Model.ReplaceOperatingHours(c.Request.Context(), id, middleware.GetActor(c), req.Windows); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not_found", "message": "الملعب غير موجود أو لا تملك صلاحية تعديله",
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "internal_server_error", "message": "حدث خطأ أثناء تحديث أوقات العمل",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "تم تحديث أوقات العمل",
		"data":    gin.H{"id": id, "windows": req.Windows},
	})
}
