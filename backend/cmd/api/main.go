package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/booking"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/database"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/notification/outbox"
	"github.com/ali/football-pitch-api/internal/otp"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/routes"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("[CONFIG] No .env file found — relying on system environment")
	}

	cfg := config.Load()

	// FAIL-CLOSED: Gin runs in ReleaseMode by default; only an explicit dev
	// APP_ENV (see config.IsDevEnv) opts into DebugMode. An unset/typo'd value
	// inherits production behaviour.
	if cfg.IsDev() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	pool, err := database.NewPool(&cfg.DB)
	if err != nil {
		log.Fatalf("[FATAL] Could not connect to database: %v", err)
	}
	defer pool.Close()

	// Construct JWTManager once — shared across all handlers and middleware
	jwtManager := auth.NewJWTManager(
		cfg.JWT.Secret,
		cfg.JWT.AccessExpiry,
		cfg.JWT.RefreshExpiry,
	)

	// ── Phone-first auth wiring (PART 3B) ─────────────────────────────────────
	// AuthRepository persists phone identities + opt-in consent; it also backs
	// the notification opt-in gate (HasOptedIn). The OTP service is composed from
	// the Postgres store/limiter, the HMAC hasher (keyed by the configured
	// pepper), and the NotificationService routing to the active channel.
	authRepo := repository.NewAuthRepository(pool)

	otpStore := otp.NewPostgresStore(pool)

	otpHasher, err := otp.NewHMACHasher(cfg.OTP.Pepper)
	if err != nil {
		log.Fatalf("[FATAL] Could not initialise OTP hasher: %v", err)
	}

	activeChannel, err := notification.ActiveChannelFromEnv()
	if err != nil {
		log.Fatalf("[FATAL] Invalid notification channel configuration: %v", err)
	}

	// Register every delivery adapter under its channel name; the Service routes
	// to whichever matches activeChannel. The opt-in gate reads consent from the
	// users table via authRepo.
	//
	// The SMS adapter doubles as the fallback target for WhatsApp: when the
	// WhatsApp channel is selected we register a FallbackChannel that tries the
	// Meta Cloud API first and transparently falls back to SMS on failure (e.g.
	// an unapproved AUTHENTICATION template while Meta verification is pending).
	sms := notification.NewSmsChannel()

	// Config-driven type→sink routing policy (MVP auth channel). Booking kinds all
	// share the booking route; OTP has its own; unmapped kinds hit the fail-safe
	// default (a non-delivering sink, so a new kind never burns messaging budget).
	bookingRoute := notification.ChannelName(cfg.Notification.BookingRoute)
	routePolicy := map[notification.MessageKind]notification.ChannelName{
		notification.KindOTP:              notification.ChannelName(cfg.Notification.OTPRoute),
		notification.KindBookingConfirmed: bookingRoute,
		notification.KindBookingReminder:  bookingRoute,
		notification.KindBookingCancelled: bookingRoute,
		notification.KindBookingRejected:  bookingRoute,
	}
	defaultRoute := notification.ChannelName(cfg.Notification.DefaultRoute)

	channelOpts := []notification.Option{
		notification.WithChannel(notification.ChannelFake, notification.NewFakeChannel()),
		notification.WithChannel(notification.ChannelSMS, sms),
		// Non-delivering log sink: booking events route here during the closed beta
		// (zero messaging budget) while we observe exactly what would be sent.
		notification.WithChannel(notification.ChannelLogOnly, notification.NewLogOnlySink(slog.Default())),
		notification.WithOptInChecker(notification.OptInFunc(authRepo.HasOptedIn)),
		// Opt-out gate (PART 6): a user who withdrew consent receives NOTHING,
		// regardless of message kind. Backed by users.opt_out via authRepo.
		notification.WithOptOutChecker(notification.OptOutFunc(authRepo.HasOptedOut)),
		notification.WithRoutingPolicy(routePolicy, defaultRoute),
		notification.WithServiceLogger(slog.Default()),
	}

	// Register the Twilio SMS adapter when configured (closed-beta OTP delivery).
	// If OTP routes to twilio_sms but credentials are absent, the routing-safety
	// assertion below fails boot — a precise "OTP has no real sender" error.
	if cfg.Twilio.Configured() {
		twilioCh, twErr := notification.NewTwilioChannel(cfg.Twilio)
		if twErr != nil {
			log.Fatalf("[FATAL] Twilio configured but adapter init failed: %v", twErr)
		}
		channelOpts = append(channelOpts,
			notification.WithChannel(notification.ChannelTwilioSMS, twilioCh))
		log.Printf("[NOTIFY] Twilio SMS adapter registered (from=%s)", cfg.Twilio.FromNumber)
	} else {
		log.Printf("[NOTIFY] Twilio not configured — twilio_sms adapter not registered")
	}

	if wa, waErr := notification.NewWhatsAppChannel(cfg.WhatsApp); waErr != nil {
		// Missing credentials are fatal only if WhatsApp is the active channel;
		// otherwise we simply skip registration so FAKE/SMS deployments run clean.
		if activeChannel == notification.ChannelWhatsApp {
			log.Fatalf("[FATAL] NOTIFICATION_CHANNEL=WHATSAPP but WhatsApp is not configured: %v", waErr)
		}
		log.Printf("[NOTIFY] WhatsApp channel not configured (%v) — skipping registration", waErr)
	} else {
		channelOpts = append(channelOpts,
			notification.WithChannel(notification.ChannelWhatsApp,
				notification.NewFallbackChannel(
					notification.NewQuotaGuardedChannel(wa, outbox.NewQuotaStore(pool), cfg.WhatsApp.WABAID, slog.Default()),
					sms)))
	}

	notifier := notification.NewService(activeChannel, channelOpts...)

	// SAFETY INVARIANT: OTP MUST resolve to a real delivery adapter (not a log
	// sink, not an unregistered name). A silent OTP→log route is a total login
	// outage, so we fail boot here rather than discover it in production.
	if err := notifier.ValidateRouting(); err != nil {
		log.Fatalf("[FATAL] %v — set TWILIO_* and/or NOTIFY_OTP_ROUTE so OTP has a real sender", err)
	}

	// OTP stays SYNCHRONOUS through the service: a one-time code is time-sensitive,
	// so it must be dispatched (and its opt-in/opt-out gates evaluated) inline, not
	// deferred behind the retry/backoff queue. The global daily ceiling is aligned
	// with the Twilio trial limit (TIER-0 anti-AIT breaker ↔ provider budget).
	otpCfg := otp.DefaultConfig()
	otpCfg.MaxGlobalDay = cfg.OTP.GlobalDailyCap
	if otpCfg.MaxGlobalHour > otpCfg.MaxGlobalDay {
		otpCfg.MaxGlobalHour = otpCfg.MaxGlobalDay
	}
	// LOCAL-DEV OTP mock: in a dev environment (config.IsDev, fail-closed) the OTP
	// service logs the code to the console instead of dispatching via Twilio,
	// saving credits during local testing. Production/unset APP_ENV → real sender.
	otpSvc := otp.New(notifier, otpStore, otpStore, otpHasher, otpCfg,
		otp.WithDevBypass(cfg.IsDev()))
	if cfg.IsDev() {
		log.Printf("[OTP] LOCAL DEV bypass ENABLED — OTP codes are logged to the console, not sent via Twilio")
	}

	// ── Durable notification outbox (PART 6) ──────────────────────────────────
	// The Postgres-backed outbox persists each async message as a job; a worker
	// drains it through the SAME NotificationService (so the opt-out/opt-in gates
	// and active channel are unchanged), retrying transient failures with
	// exponential backoff and dead-lettering permanent ones. The store also backs
	// the delivery-status webhook (message_deliveries).
	outboxStore := outbox.NewPostgresStore(pool)
	failureMonitor := outbox.NewFailureMonitor(5*time.Minute, 0.5, 20)
	outboxWorker := outbox.NewWorker(outboxStore, notifier, outbox.Config{},
		outbox.WithDeliveryStore(outboxStore),
		outbox.WithFailureMonitor(failureMonitor),
	)

	// ── Booking orchestration wiring (PART 5/5.1) ─────────────────────────────
	// The BookingService persists each state transition with its audit row and
	// routes the player notification through the durable outbox: an Enqueuer drops
	// in wherever a notifier is expected, turning best-effort booking events into
	// queued, retried jobs the worker delivers. The HTTP handlers create/cancel
	// exclusively through this service.
	bookingRepo := repository.NewBookingRepository(pool)
	bookingNotifier := outbox.NewEnqueuer(outboxStore)
	bookingSvc := booking.NewService(bookingRepo, bookingNotifier)

	// ── Automated 24-hour reminder worker (PART 7) ────────────────────────────
	// A background runner periodically claims confirmed bookings starting within
	// the next 24h that have not been reminded (SELECT ... FOR UPDATE SKIP LOCKED,
	// safe for future horizontal scaling), marks each reminded, and enqueues a
	// durable booking_reminder onto the SAME outbox — so the existing worker above
	// delivers it through the unchanged gates and active channel.
	reminderRepo := repository.NewReminderRepository(pool)
	reminderWorker := booking.NewReminderWorker(reminderRepo, booking.ReminderConfig{})

	// Drain the outbox and scan for reminders in the background until shutdown
	// cancels workerCtx.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	go func() {
		if err := outboxWorker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[OUTBOX] worker exited: %v", err)
		}
	}()
	go func() {
		if err := reminderWorker.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("[REMINDER] worker exited: %v", err)
		}
	}()

	// Idempotency-key TTL cleanup (PART: booking idempotency). Booking attempts
	// record a per-user Idempotency-Key for ~24h to dedupe double-taps/retries;
	// this background sweep prunes expired rows so the table stays small. It is
	// pure storage hygiene — correctness does not depend on it (expired keys are
	// random UUIDs that are never reused) — so a failed sweep only logs.
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			if n, err := bookingRepo.DeleteExpiredIdempotencyKeys(workerCtx, time.Now()); err != nil {
				if workerCtx.Err() == nil {
					log.Printf("[IDEMPOTENCY] cleanup error: %v", err)
				}
			} else if n > 0 {
				log.Printf("[IDEMPOTENCY] pruned %d expired key(s)", n)
			}
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	router := gin.New()

	// Allowed CORS origins. The browser's `Origin` header never carries a
	// trailing slash or surrounding space, and the lookup below is exact-match,
	// so every configured origin is normalised the same way (trim space, drop a
	// trailing "/"). A mismatched origin makes gin-contrib/cors abort the
	// preflight 403 with NO Access-Control-Allow-Origin header — which surfaces
	// in the browser as "No 'Access-Control-Allow-Origin' header is present".
	normalizeOrigin := func(o string) string {
		return strings.TrimRight(strings.TrimSpace(o), "/")
	}
	allowedOrigins := map[string]bool{
		// Local dev B2C player app.
		"http://localhost:3000": true,
		// Local dev admin dashboard (Dashboard PR 3, cross-origin auth). The admin
		// app authenticates against this same backend; gin-contrib/cors echoes the
		// matched origin (never "*") so AllowCredentials stays spec-compliant.
		"http://localhost:3001": true,
		// Production frontend (Vercel). Hardcoded as a fallback so the deploy
		// works even if CORS_ALLOWED_ORIGINS is unset/misconfigured on Railway;
		// the env var below can still add more origins (e.g. preview URLs and the
		// production admin-dashboard origin, e.g. https://admin.<domain>).
		"https://football-pitch-booking-liart.vercel.app": true,
	}
	if raw := os.Getenv("CORS_ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if trimmed := normalizeOrigin(o); trimmed != "" {
				allowedOrigins[trimmed] = true
			}
		}
	}

	// Surface the effective allow-list in the boot logs so a CORS failure can be
	// diagnosed from Railway logs without redeploying.
	origins := make([]string, 0, len(allowedOrigins))
	for o := range allowedOrigins {
		origins = append(origins, o)
	}
	log.Printf("[CORS] allowed origins: %s", strings.Join(origins, ", "))

	// CORS is registered FIRST so the Access-Control-* headers are applied to
	// every response — including 404/405 and any later middleware abort — before
	// anything else in the chain runs. (Gin folds engine-level middleware into
	// the NoRoute chain regardless of order, but front-loading CORS removes all
	// ambiguity and is the conventional placement.)
	router.Use(cors.New(cors.Config{
		AllowOriginFunc:  func(origin string) bool { return allowedOrigins[normalizeOrigin(origin)] },
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token", "Idempotency-Key"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// Log every unmatched route. Remember the API is served under /api/v1 — a 404
	// on a bare path (e.g. /auth/request-otp without the prefix) means the caller
	// dropped the prefix, usually a frontend NEXT_PUBLIC_API_URL missing /api/v1.
	router.NoRoute(func(c *gin.Context) {
		log.Printf("[404] no route: %s %s (origin=%q)",
			c.Request.Method, c.Request.URL.Path, c.Request.Header.Get("Origin"))
		c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "path": c.Request.URL.Path})
	})

	// deliveryStore (outboxStore) backs the WhatsApp status webhook; optOutStore
	// (authRepo) backs the consent-withdrawal endpoint.
	routes.Register(router, pool, jwtManager, cfg, otpSvc, authRepo, bookingSvc, outboxStore, authRepo)

	// Explicit server timeouts bound slow/hung clients (Slowloris) and stuck
	// connections. There are NO long-lived/streaming endpoints — image uploads go
	// browser→Cloudinary directly, not through this backend — so a 15s write
	// timeout cannot truncate a legitimate response. ReadHeaderTimeout caps the
	// header read separately from the (larger) full-request ReadTimeout.
	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.ServerPort),
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("[SERVER] Running on port %s (env: %s)\n", cfg.ServerPort, cfg.AppEnv)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("[FATAL] Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[SERVER] Shutdown signal received — draining connections...")
	stopWorker() // stop claiming new outbox jobs before draining HTTP

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("[FATAL] Forced shutdown: %v", err)
	}

	log.Println("[SERVER] Shutdown complete.")
}
