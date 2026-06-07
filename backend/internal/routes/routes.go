package routes

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/cloudinary"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/handlers"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/repository"
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
	// The Cloudinary credentials are validated at config load (fail-fast), so the
	// only error here would be a programming/SDK error — panic to fail fast,
	// consistent with the other startup security assertions.
	cldSvc, err := cloudinary.New(cfg.Cloudinary)
	if err != nil {
		panic("ROUTES: could not initialise Cloudinary service: " + err.Error())
	}
	pitchHandler := &handlers.PitchHandler{Model: &data.PitchModel{DB: db}, Cloudinary: cldSvc}
	webhookHandler := handlers.NewWhatsAppWebhookHandler(deliveryStore, cfg.WhatsApp.WebhookVerifyToken)
	notificationHandler := handlers.NewNotificationHandler(optOutStore)
	reviewHandler := handlers.NewReviewHandler(repository.NewReviewRepository(db))
	v1 := r.Group("/api/v1")

	// ════════════════════════════════════════════════════════════════════════
	// PUBLIC ROUTES — no authentication required
	// ════════════════════════════════════════════════════════════════════════
	v1.GET("/ping", healthHandler.Ping)
	v1.GET("/pitches", pitchHandler.ListPitches)
	v1.GET("/pitches/:id", pitchHandler.GetPitch)
	// Public: anyone can read a pitch's reviews + rating aggregates.
	v1.GET("/pitches/:id/reviews", reviewHandler.ListPitchReviews)

	// Provider delivery-status webhooks (PART 6). Public: authentication is the
	// Meta verify-token handshake (GET) plus, in production, request-signature
	// validation at the edge — not a user JWT.
	v1.GET("/webhooks/whatsapp", webhookHandler.Verify)
	v1.POST("/webhooks/whatsapp", webhookHandler.Receive)
	authRoutes := v1.Group("/auth")
	{
		// Phone-first OTP is the SOLE login path. Email/password auth has been
		// removed (Step C).
		authRoutes.POST("/request-otp", phoneAuthHandler.RequestOTP)
		authRoutes.POST("/verify-otp", phoneAuthHandler.VerifyOTP)

		// Refresh is cookie-authenticated and state-changing (token rotation), so
		// it is CSRF-protected even though it lives outside the protected group.
		authRoutes.POST("/refresh", middleware.RequireCSRF(), authHandler.Refresh)
	}

	// ════════════════════════════════════════════════════════════════════════
	// PROTECTED ROUTES — valid JWT required for all routes below
	// ════════════════════════════════════════════════════════════════════════
	protected := v1.Group("/")
	protected.Use(middleware.RequireAuth(jwtManager))
	// Double-submit CSRF: enforced for unsafe methods on cookie-authenticated
	// requests; safe methods and Bearer-authenticated callers pass through.
	protected.Use(middleware.RequireCSRF())
	{
		// Auth actions that require identity
		protected.POST("/auth/logout", authHandler.Logout)

		// Current-user profile — session rehydration for cookie-based auth.
		protected.GET("/auth/me", phoneAuthHandler.GetCurrentUser)

		// Just-In-Time profile update (full_name capture at checkout). Strict
		// BOLA: the target id is taken from the session, never the body.
		protected.PATCH("/me", phoneAuthHandler.PatchMe)

		// ── Reviews ───────────────────────────────────────────────────────────
		// Derived eligibility probe (player books → can review after it ends).
		protected.GET("/pitches/:id/review-eligibility",
			middleware.RequireRole("player"),
			reviewHandler.GetEligibility,
		)
		// Create a verified review (player only; server re-validates eligibility
		// via the composite FK + unique index).
		protected.POST("/pitches/:id/reviews",
			middleware.RequireRole("player"),
			reviewHandler.CreateReview,
		)
		// Edit own review (player; ownership enforced server-side in the handler).
		protected.PUT("/reviews/:id",
			middleware.RequireRole("player"),
			reviewHandler.UpdateReview,
		)
		// Report a review — ANY authenticated role (not public, anti-griefing).
		protected.POST("/reviews/:id/flag", reviewHandler.FlagReview)
		// Admin moderation: soft-delete a review (role enforced at route + handler).
		protected.DELETE("/reviews/:id",
			middleware.RequireRole("admin"),
			reviewHandler.DeleteReview,
		)

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
		// Backend-signed direct-upload: hand the browser a signed payload to upload
		// a pitch image straight to Cloudinary. Owner/admin only; players → 403.
		protected.POST("/pitches/upload-signature",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.UploadSignature,
		)
		// Persist the result of a completed direct upload (URL + public_id),
		// actor-scoped, with the cloud-origin trust guard + old-asset cleanup.
		protected.PATCH("/pitches/:id/image",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.SetPitchImage,
		)
		protected.PATCH("/pitches/:id",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.UpdatePitch,
		)
		protected.DELETE("/pitches/:id",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.DeletePitch,
		)
		// Activate / deactivate toggle — intent-revealing, separate from the
		// full-object update. Players are categorically barred (403).
		protected.PATCH("/pitches/:id/active",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.ToggleActive,
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

		// Players, owners, and admins can cancel (handler enforces ownership logic;
		// admins/owners cancel from the dashboard, players from their bookings page)
		protected.PATCH("/bookings/:id/cancel",
			middleware.RequireRole("player", "owner", "admin"),
			bookingHandler.CancelBooking,
		)
	}
}
