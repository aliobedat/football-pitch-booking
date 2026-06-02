package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/handlers"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/notification"
)

func Register(
	r *gin.Engine,
	db *pgxpool.Pool,
	jwtManager *auth.JWTManager,
	cfg *config.Config,
	otpSvc notification.OtpService,
	authStore handlers.PhoneAuthStore,
	bookingSvc handlers.BookingService,
	deliveryStore handlers.WhatsAppDeliveryStore,
	optOutStore handlers.OptOutStore,
) {
	// ── Handler construction ─────────────────────────────────────────────────
	healthHandler := handlers.NewHealthHandler(db)
	authHandler := handlers.NewAuthHandler(db, jwtManager, cfg)
	phoneAuthHandler := handlers.NewPhoneAuthHandler(otpSvc, authStore, jwtManager, cfg)
	bookingHandler := handlers.NewBookingHandler(db, bookingSvc)
	pitchHandler := &handlers.PitchHandler{Model: &data.PitchModel{DB: db}}
	webhookHandler := handlers.NewWhatsAppWebhookHandler(deliveryStore, cfg.WhatsApp.WebhookVerifyToken)
	notificationHandler := handlers.NewNotificationHandler(optOutStore)
	v1 := r.Group("/api/v1")

	// ════════════════════════════════════════════════════════════════════════
	// PUBLIC ROUTES — no authentication required
	// ════════════════════════════════════════════════════════════════════════
	v1.GET("/ping", healthHandler.Ping)
	v1.GET("/pitches", pitchHandler.ListPitches)
	v1.GET("/pitches/:id", pitchHandler.GetPitch)

	// Provider delivery-status webhooks (PART 6). Public: authentication is the
	// Meta verify-token handshake (GET) plus, in production, request-signature
	// validation at the edge — not a user JWT.
	v1.GET("/webhooks/whatsapp", webhookHandler.Verify)
	v1.POST("/webhooks/whatsapp", webhookHandler.Receive)
	authRoutes := v1.Group("/auth")
	{
		// Phone-first auth (PART 3B): the primary login path.
		authRoutes.POST("/request-otp", phoneAuthHandler.RequestOTP)
		authRoutes.POST("/verify-otp", phoneAuthHandler.VerifyOTP)

		// Email/password auth (legacy / secondary identity).
		authRoutes.POST("/register", authHandler.Register)
		authRoutes.POST("/login", authHandler.Login)
		authRoutes.POST("/refresh", authHandler.Refresh)
	}

	// ════════════════════════════════════════════════════════════════════════
	// PROTECTED ROUTES — valid JWT required for all routes below
	// ════════════════════════════════════════════════════════════════════════
	protected := v1.Group("/")
	protected.Use(middleware.RequireAuth(jwtManager))
	{
		// Auth actions that require identity
		protected.POST("/auth/logout", authHandler.Logout)

		// Notification consent (PART 6): a user withdraws consent for themselves.
		protected.POST("/notifications/opt-out", notificationHandler.OptOut)

		// ── Bookings ─────────────────────────────────────────────────────────
		// Any authenticated user can create a booking, list their own, or check availability
		protected.GET("/bookings", bookingHandler.GetUserBookings)
		protected.POST("/bookings", bookingHandler.CreateBooking)
		protected.GET("/pitches/:id/availability", bookingHandler.GetPitchAvailability)

		// Owner: manage their own pitches
		protected.POST("/pitches",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.CreatePitch,
		)
		protected.PATCH("/pitches/:id",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.UpdatePitch,
		)
		protected.DELETE("/pitches/:id",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.DeletePitch,
		)
		protected.GET("/owner/pitches",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.GetOwnerPitches,
		)

		// Owner/admin: list all bookings across all users and pitches
		protected.GET("/admin/bookings",
			middleware.RequireRole("owner", "admin"),
			bookingHandler.GetAllBookings,
		)

		// Both players and owners can cancel (handler enforces ownership logic)
		protected.PATCH("/bookings/:id/cancel",
			middleware.RequireRole("player", "owner"),
			bookingHandler.CancelBooking,
		)
	}
}
