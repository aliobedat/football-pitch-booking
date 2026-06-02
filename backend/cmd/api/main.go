package main

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	"github.com/ali/football-pitch-api/internal/otp"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/routes"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("[CONFIG] No .env file found — relying on system environment")
	}

	cfg := config.Load()

	if cfg.AppEnv == "production" {
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

	channelOpts := []notification.Option{
		notification.WithChannel(notification.ChannelFake, notification.NewFakeChannel()),
		notification.WithChannel(notification.ChannelSMS, sms),
		notification.WithOptInChecker(notification.OptInFunc(authRepo.HasOptedIn)),
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
				notification.NewFallbackChannel(wa, sms)))
	}

	notifier := notification.NewService(activeChannel, channelOpts...)

	otpSvc := otp.New(notifier, otpStore, otpStore, otpHasher, otp.DefaultConfig())

	// ── Booking orchestration wiring (PART 5/5.1) ─────────────────────────────
	// The BookingService persists each state transition with its audit row and
	// routes the player notification through the same NotificationService used
	// for OTP. The HTTP handlers create/cancel exclusively through this service.
	bookingRepo := repository.NewBookingRepository(pool)
	bookingSvc := booking.NewService(bookingRepo, notifier)

	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	allowedOrigins := map[string]bool{"http://localhost:3000": true}
	if raw := os.Getenv("CORS_ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(o); trimmed != "" {
				allowedOrigins[trimmed] = true
			}
		}
	}

	router.Use(cors.New(cors.Config{
		AllowOriginFunc:  func(origin string) bool { return allowedOrigins[origin] },
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	routes.Register(router, pool, jwtManager, cfg, otpSvc, authRepo, bookingSvc) // ← updated signature

	server := &http.Server{
		Addr:         fmt.Sprintf(":%s", cfg.ServerPort),
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("[FATAL] Forced shutdown: %v", err)
	}

	log.Println("[SERVER] Shutdown complete.")
}
