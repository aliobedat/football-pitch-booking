package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/handlers"
	"github.com/ali/football-pitch-api/internal/middleware"
)

func Register(r *gin.Engine, db *pgxpool.Pool, jwtManager *auth.JWTManager, cfg *config.Config) {
	// ── Handler construction ─────────────────────────────────────────────────
	healthHandler  := handlers.NewHealthHandler(db)
	authHandler    := handlers.NewAuthHandler(db, jwtManager, cfg)
	bookingHandler := handlers.NewBookingHandler(db)
    pitchHandler   := &handlers.PitchHandler{Model: &data.PitchModel{DB: db}}
	v1 := r.Group("/api/v1")

	// ════════════════════════════════════════════════════════════════════════
	// PUBLIC ROUTES — no authentication required
	// ════════════════════════════════════════════════════════════════════════
	v1.GET("/ping", healthHandler.Ping)
v1.GET("/pitches", pitchHandler.ListPitches)
	v1.GET("/pitches/:id", pitchHandler.GetPitch)
	authRoutes := v1.Group("/auth")
	{
		authRoutes.POST("/register", authHandler.Register)
		authRoutes.POST("/login",    authHandler.Login)
		authRoutes.POST("/refresh",  authHandler.Refresh)
	}

	// ════════════════════════════════════════════════════════════════════════
	// PROTECTED ROUTES — valid JWT required for all routes below
	// ════════════════════════════════════════════════════════════════════════
	protected := v1.Group("/")
	protected.Use(middleware.RequireAuth(jwtManager))
	{
		// Auth actions that require identity
		protected.POST("/auth/logout", authHandler.Logout)

		// ── Bookings ─────────────────────────────────────────────────────────
		// Any authenticated user can create a booking, list their own, or check availability
		protected.GET("/bookings",                        bookingHandler.GetUserBookings)
		protected.POST("/bookings",                       bookingHandler.CreateBooking)
		protected.GET("/pitches/:id/availability",        bookingHandler.GetPitchAvailability)

		// Owner: manage their own pitches
		protected.POST("/pitches",
			middleware.RequireRole("owner"),
			pitchHandler.CreatePitch,
		)
		protected.GET("/owner/pitches",
			middleware.RequireRole("owner"),
			pitchHandler.GetOwnerPitches,
		)

		// Owner/admin: list all bookings across all users and pitches
		protected.GET("/admin/bookings",
			middleware.RequireRole("owner"),
			bookingHandler.GetAllBookings,
		)

		// Only pitch owners can confirm a booking
		protected.PATCH("/bookings/:id/confirm",
			middleware.RequireRole("owner"),
			bookingHandler.ConfirmBooking,
		)

		// Both players and owners can cancel (handler enforces ownership logic)
		protected.PATCH("/bookings/:id/cancel",
			middleware.RequireRole("player", "owner"),
			bookingHandler.CancelBooking,
		)
	}
}