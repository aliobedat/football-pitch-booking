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
	// Cockpit WO1: Regulars CRM. The booking create paths attach the customer
	// go-forward via the same repository the CRM reads from.
	customerRepo := repository.NewCustomerRepository(db)
	customerHandler := handlers.NewCustomerHandler(customerRepo)
	// Cockpit WO2: Visual Calendar Command Center (read).
	calendarHandler := handlers.NewCalendarHandler(repository.NewCalendarRepository(db))
	// Phase 2 / WO-F2: Expense Ledger + Net Profit (reuses the analytics collected leg).
	analyticsRepo := repository.NewAnalyticsRepository(db)
	expenseRepo := repository.NewExpenseRepository(db)
	expenseHandler := handlers.NewExpenseHandler(expenseRepo)
	financialsHandler := handlers.NewFinancialsHandler(analyticsRepo, expenseRepo)
	bookingHandler := handlers.NewBookingHandler(db, bookingSvc).WithCustomers(customerRepo)
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
	// Dashboard PR 2 (RBAC): staff provisioning + finance analytics.
	staffRepo := repository.NewStaffRepository(db)
	staffHandler := handlers.NewStaffHandler(staffRepo)
	analyticsHandler := handlers.NewAnalyticsHandler(repository.NewAnalyticsRepository(db))
	// Dashboard PR 4: staff daily schedule + attendance.
	scheduleHandler := handlers.NewScheduleHandler(repository.NewScheduleRepository(db))
	v1 := r.Group("/api/v1")

	// ════════════════════════════════════════════════════════════════════════
	// PUBLIC ROUTES — no authentication required
	// ════════════════════════════════════════════════════════════════════════
	v1.GET("/ping", healthHandler.Ping)
	v1.GET("/pitches", pitchHandler.ListPitches)
	// Public availability search: date + start time (+ optional coords) → pitches
	// open and free from that start, nearest-first when coords are present. Static
	// segment registered before the :id param route (Gin matches static first).
	v1.GET("/pitches/availability", pitchHandler.SearchAvailability)
	v1.GET("/pitches/:id", pitchHandler.GetPitch)
	// Public: anyone can read a pitch's reviews + rating aggregates.
	v1.GET("/pitches/:id/reviews", reviewHandler.ListPitchReviews)
	// Public: availability (booked slots for a date) is browse-funnel data — a
	// visitor sees open slots on the public pitch detail page before logging in.
	// The handler reads only the pitch id + date query; no session identity.
	v1.GET("/pitches/:id/availability", bookingHandler.GetPitchAvailability)
	// Public: a pitch's weekly operating hours — the player detail page renders
	// bookable/closed from it (alongside availability). Read-only; no identity.
	v1.GET("/pitches/:id/operating-hours", pitchHandler.GetOperatingHours)

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

		// Refresh is cookie-authenticated and state-changing (token rotation). CSRF
		// protection is intentionally NOT applied here: the interceptor's automatic
		// refresh fires before a readable malaab_csrf cookie is reliably available
		// (e.g. first load after sign-in), and a CSRF attacker gains nothing from a
		// forced rotation — the rotated tokens are httpOnly and the response is
		// CORS-blocked. (Defense-in-depth trade-off, accepted.)
		authRoutes.POST("/refresh", authHandler.Refresh)
	}

	// ════════════════════════════════════════════════════════════════════════
	// PROTECTED ROUTES — valid JWT required for all routes below
	// ════════════════════════════════════════════════════════════════════════
	protected := v1.Group("/")
	protected.Use(middleware.RequireAuth(jwtManager))
	// Double-submit CSRF: enforced for unsafe methods on cookie-authenticated
	// requests; safe methods and Bearer-authenticated callers pass through.
	protected.Use(middleware.RequireCSRF())
	// Central scope guard (Dashboard PR 2): resolve each actor's scope from the DB
	// once per request and inject it. A `staff` actor with no pitch binding is
	// rejected here (403); non-staff carry an empty scope. Scope is intentionally
	// NOT in the JWT — a rebind/revoke takes effect on the next request.
	protected.Use(middleware.ResolveScope(staffRepo))
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
		// Any authenticated user can create a booking or list their own.
		protected.GET("/bookings", bookingHandler.GetUserBookings)
		protected.POST("/bookings", bookingHandler.CreateBooking)

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
		// Replace the whole weekly operating-hours schedule (full grid). Owner/admin
		// only; actor-scoped + audited in the data layer. Players are barred (403).
		protected.PUT("/pitches/:id/operating-hours",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.PutOperatingHours,
		)
		protected.GET("/owner/pitches",
			middleware.RequireRole("owner", "admin"),
			pitchHandler.GetOwnerPitches,
		)

		// ── Finance / Analytics (owner/admin ONLY — staff hard-rejected) ───────
		// The canonical RBAC boundary the dashboard's Analytics nav is gated on.
		// RequireRole bars staff/player at the route; the handler re-asserts it.
		protected.GET("/owner/analytics",
			middleware.RequireRole("owner", "admin"),
			analyticsHandler.GetRevenueSummary,
		)
		protected.GET("/owner/analytics/kpis",
			middleware.RequireRole("owner", "admin"),
			analyticsHandler.GetKPIs,
		)
		protected.GET("/owner/analytics/timeseries",
			middleware.RequireRole("owner", "admin"),
			analyticsHandler.GetTimeSeries,
		)

		// ── Regulars CRM (owner/admin ONLY — staff/players barred) ─────────────
		// Owner-scoped customer directory + per-customer profile + private notes.
		// Scope enforced in SQL via the repository's OwnerScopeFilter.
		protected.GET("/owner/customers",
			middleware.RequireRole("owner", "admin"),
			customerHandler.GetCustomers,
		)
		protected.GET("/owner/customers/:id",
			middleware.RequireRole("owner", "admin"),
			customerHandler.GetCustomerProfile,
		)
		protected.PATCH("/owner/customers/:id/notes",
			middleware.RequireRole("owner", "admin"),
			customerHandler.PatchCustomerNotes,
		)

		// ── Visual Calendar (owner/admin ONLY) — per-day resource timeline ─────
		protected.GET("/owner/calendar",
			middleware.RequireRole("owner", "admin"),
			calendarHandler.GetDayCalendar,
		)

		// ── Financials / Expense Ledger (owner/admin ONLY) ─────────────────────
		// Net Profit = (WO-F1 collected) − expenses. CRUD mutations are CSRF-guarded
		// by the protected group; reads/writes owner-scoped in SQL.
		protected.GET("/owner/financials",
			middleware.RequireRole("owner", "admin"),
			financialsHandler.GetNetSummary,
		)
		protected.GET("/owner/expenses",
			middleware.RequireRole("owner", "admin"),
			expenseHandler.ListExpenses,
		)
		protected.POST("/owner/expenses",
			middleware.RequireRole("owner", "admin"),
			expenseHandler.CreateExpense,
		)
		protected.PATCH("/owner/expenses/:id",
			middleware.RequireRole("owner", "admin"),
			expenseHandler.UpdateExpense,
		)
		protected.DELETE("/owner/expenses/:id",
			middleware.RequireRole("owner", "admin"),
			expenseHandler.DeleteExpense,
		)

		// ── Staff provisioning (owner-scoped) ──────────────────────────────────
		// Owner invites a guard by phone and binds them to ONE OR MORE pitches they
		// OWN (1:N). pitch_ids travels in the body; the ownership invariant (owner
		// owns every pitch) is enforced in the repository transaction.
		protected.POST("/owner/staff",
			middleware.RequireRole("owner", "admin"),
			staffHandler.InviteStaff,
		)
		protected.GET("/owner/staff",
			middleware.RequireRole("owner", "admin"),
			staffHandler.ListStaff,
		)
		// Revoke: delete the binding + demote the user to player. Owner-scoped
		// (an owner can only revoke staff they provisioned).
		protected.DELETE("/owner/staff/:userId",
			middleware.RequireRole("owner", "admin"),
			staffHandler.RevokeStaff,
		)

		// ── Staff daily schedule + attendance (staff/owner/admin; players barred) ──
		// Scope enforced in SQL (staff → bound pitch, owner → owned, admin → any).
		protected.GET("/schedule",
			middleware.RequireRole("staff", "owner", "admin"),
			scheduleHandler.GetDailySchedule,
		)
		protected.PATCH("/bookings/:id/attendance",
			middleware.RequireRole("staff", "owner", "admin"),
			scheduleHandler.PatchAttendance,
		)
		// Cash-Settlement Marker (WO-F1): unpaid | paid_cash on a non-cancelled booking.
		protected.PATCH("/bookings/:id/payment",
			middleware.RequireRole("staff", "owner", "admin"),
			scheduleHandler.PatchPayment,
		)

		// Owner/admin BLOCKS: create held time (source='block'), or remove it.
		// Not bound by operating hours (owner bypass); blocks still conflict with
		// existing bookings via the EXCLUDE (pre-checked for a detailed 409).
		protected.POST("/pitches/:id/blocks",
			middleware.RequireRole("owner", "admin"),
			bookingHandler.CreateBlock,
		)
		protected.DELETE("/pitches/:id/blocks/:bookingId",
			middleware.RequireRole("owner", "admin"),
			bookingHandler.CancelBlock,
		)

		// Owner/admin MANUAL (walk-in) bookings: log offline occupancy (source=
		// 'manual', player_id NULL, guest_name set). Honours operating hours unless
		// force_bypass_hours (soft override). Cancel goes through the standard
		// /bookings/:id/cancel owner path (manual rows are real, audited occupancy).
		protected.POST("/pitches/:id/bookings/manual",
			middleware.RequireRole("owner", "admin"),
			bookingHandler.CreateManualBooking,
		)
		// Owner/admin ACADEMY generator: expand recurrence rules (days_of_week ×
		// date-range at a fixed time window) into DISCRETE bookings (source='academy').
		// All-or-nothing — any overlap rolls the series back and returns the conflicting
		// dates. recurrence_group_id is the idempotency key + bulk-cancel handle.
		protected.POST("/pitches/:id/bookings/bulk-academy",
			middleware.RequireRole("owner", "admin"),
			bookingHandler.CreateAcademyBookings,
		)
		// Bulk-cancel all FUTURE occurrences of a recurring walk-in group (past
		// occurrences preserved). Owner/admin-scoped; idempotent (empty → 200, count 0).
		protected.DELETE("/pitches/:id/bookings/group/:groupId",
			middleware.RequireRole("owner", "admin"),
			bookingHandler.CancelGroup,
		)

		// Owner/admin: list all bookings across all users and pitches. Staff are
		// also permitted but scoped in SQL to their bound pitch(es) (ResolveScope
		// 403s unprovisioned staff before this handler).
		protected.GET("/admin/bookings",
			middleware.RequireRole("staff", "owner", "admin"),
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
