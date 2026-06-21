package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HealthHandler struct {
	db *pgxpool.Pool
}

func NewHealthHandler(db *pgxpool.Pool) *HealthHandler {
	return &HealthHandler{db: db}
}

// Ping godoc
// @Summary      Health check
// @Description  Returns server status and database connectivity
// @Tags         health
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      503  {object}  map[string]interface{}
// @Router       /ping [get]
func (h *HealthHandler) Ping(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	dbStatus := "up"
	if err := h.db.Ping(ctx); err != nil {
		dbStatus = "down"
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status":   "degraded",
			"database": dbStatus,
			"error":    err.Error(),
		})
		return
	}

	stat := h.db.Stat()

	c.JSON(http.StatusOK, gin.H{
		"status":   "ok",
		"database": dbStatus,
		"pool": gin.H{
			"total_conns":    stat.TotalConns(),
			"acquired_conns": stat.AcquiredConns(),
			"idle_conns":     stat.IdleConns(),
		},
	})
}
