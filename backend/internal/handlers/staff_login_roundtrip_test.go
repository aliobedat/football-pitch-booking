package handlers

// DB-gated end-to-end: provision a staff member with a password through the staff
// onboarding flow, then log in via POST /auth/password-login exactly like an owner
// (phone + password, no OTP) and reach a staff-allowed route with the minted
// malaab_* session cookies.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set:
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run StaffLoginRoundTrip -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
)

func TestStaffLoginRoundTrip(t *testing.T) {
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping staff login round-trip")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	suffix := time.Now().UnixNano() % 1_000_000
	// Seed an owner + a pitch they own.
	var ownerID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ('RT Owner',$1,'owner',TRUE) RETURNING id
	`, fmt.Sprintf("+96277%07d", suffix)).Scan(&ownerID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	pitch, err := (&data.PitchModel{DB: pool}).CreatePitch(ctx, data.CreatePitchRequest{
		Name: "RT Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: ownerID,
	})
	if err != nil {
		t.Fatalf("seed pitch: %v", err)
	}
	staffPhone := fmt.Sprintf("+96279%07d", suffix)
	const staffPassword = "staff-login-pass"

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM staff WHERE pitch_id = $1`, pitch.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pitch.ID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE phone = ANY($1)`,
			[]string{fmt.Sprintf("+96277%07d", suffix), staffPhone})
	})

	jwtManager := auth.NewJWTManager(testJWTSecret, 15*time.Minute, 168*time.Hour)
	cfg := &config.Config{BcryptCost: 10, JWT: config.JWTConfig{
		Secret: testJWTSecret, AccessExpiry: 15 * time.Minute, RefreshExpiry: 168 * time.Hour,
	}}

	// 1. Provision a brand-new staff member with a password (the onboarding flow).
	staffRepo := repository.NewStaffRepository(pool)
	hash, err := auth.HashPassword(staffPassword, cfg.BcryptCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := staffRepo.CreateStaffBindings(ctx,
		auth.Actor{UserID: ownerID, Role: auth.RoleOwner}, []int{int(pitch.ID)}, staffPhone,
		repository.StaffProvision{FullName: "RT Staff", PasswordHash: hash}); err != nil {
		t.Fatalf("provision staff: %v", err)
	}

	// 2. Router: password-login + a staff-allowed protected route (same middleware).
	pwHandler := NewPasswordAuthHandler(repository.NewAuthRepository(pool), jwtManager, cfg, nil)
	r := gin.New()
	r.POST("/auth/password-login", pwHandler.PasswordLogin)
	r.GET("/schedule",
		middleware.RequireAuth(jwtManager),
		middleware.RequireRole("staff", "owner", "admin"),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"role": middleware.GetUserRole(c)}) },
	)

	// 3. Log in with phone + password (no OTP).
	body, _ := json.Marshal(map[string]any{"phone": staffPhone, "password": staffPassword})
	req := httptest.NewRequest(http.MethodPost, "/auth/password-login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("password-login: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	access := cookieByName(rec, "malaab_access")
	refresh := cookieByName(rec, "malaab_refresh")
	if access == nil || access.Value == "" || !access.HttpOnly {
		t.Fatalf("expected httpOnly malaab_access cookie, got %+v", access)
	}
	if refresh == nil || refresh.Value == "" || !refresh.HttpOnly {
		t.Fatalf("expected httpOnly malaab_refresh cookie, got %+v", refresh)
	}

	// 4. The staff session reaches a staff-allowed route.
	req2 := httptest.NewRequest(http.MethodGet, "/schedule", nil)
	for _, ck := range rec.Result().Cookies() {
		req2.AddCookie(ck)
	}
	rec2 := httptest.NewRecorder()
	r.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("staff route with session: status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
}
