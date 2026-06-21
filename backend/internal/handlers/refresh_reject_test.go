package handlers

// §5.4 — POST /auth/refresh MUST reject any request without a valid refresh
// token, and a rejection must NEVER mint a new session (no silent success). This
// matters specifically because CSRF was intentionally removed from /auth/refresh
// (defense-in-depth trade-off) — so refresh's own token validation is now the
// only barrier, and we prove it holds. Live-DB integration (the garbage + valid
// cases hit FindAndConsumeRefreshToken); SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run RefreshReject -v

import (
	"context"
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
	"github.com/ali/football-pitch-api/internal/repository"
)

// cookieByName is defined in phone_auth_test.go (same package).

// hasLiveSession reports whether the response minted a usable access cookie
// (non-empty value, not an expiry/clear). The thing a rejection must never do.
func hasLiveSession(rec *httptest.ResponseRecorder) bool {
	c := cookieByName(rec, cookieAccess)
	return c != nil && c.Value != "" && c.MaxAge >= 0
}

func TestRefreshReject(t *testing.T) {
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping refresh-reject integration test")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	jwtManager := auth.NewJWTManager("integration-test-secret-key-min-32-chars-long", 15*time.Minute, 168*time.Hour)
	cfg := &config.Config{AppEnv: "test"}
	cfg.JWT.Secret = "integration-test-secret-key-min-32-chars-long"
	cfg.JWT.AccessExpiry = 15 * time.Minute
	cfg.JWT.RefreshExpiry = 168 * time.Hour

	h := NewAuthHandler(pool, jwtManager, cfg)
	r := gin.New()
	r.POST("/auth/refresh", h.Refresh) // refresh is CSRF/auth-free by design

	// Seed a user + a VALID stored refresh token for the positive control.
	suffix := time.Now().UnixNano() % 1_000_000
	var userID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,$3,TRUE) RETURNING id
	`, "RR User", fmt.Sprintf("+96259%06d", suffix), auth.RoleOwner).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	rawRefresh, refreshHash, err := jwtManager.GenerateRefreshToken()
	if err != nil {
		t.Fatalf("gen refresh: %v", err)
	}
	seedRepo := repository.NewUserRepository(pool)
	if err := seedRepo.StoreRefreshToken(ctx, userID, refreshHash, time.Now().Add(cfg.JWT.RefreshExpiry)); err != nil {
		t.Fatalf("store refresh: %v", err)
	}
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	post := func(t *testing.T, cookie *http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// CASE 1 — No refresh cookie at all → 401, and NO session minted.
	t.Run("missing_cookie_unauthorized", func(t *testing.T) {
		rec := post(t, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("missing cookie = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
		}
		if hasLiveSession(rec) {
			t.Fatalf("CRITICAL: a missing-token refresh minted a session cookie")
		}
	})

	// CASE 2 — Garbage refresh token → 401, no session minted, and the stale
	// cookies are cleared (clearSessionCookies fired).
	t.Run("garbage_token_unauthorized", func(t *testing.T) {
		rec := post(t, &http.Cookie{Name: cookieRefresh, Value: "not-a-real-refresh-token-xxxxxxxx"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("garbage token = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
		}
		if hasLiveSession(rec) {
			t.Fatalf("CRITICAL: a garbage-token refresh minted a session cookie")
		}
		// clearSessionCookies sets malaab_access with MaxAge<0 to scrub the dead session.
		if c := cookieByName(rec, cookieAccess); c == nil || c.MaxAge >= 0 {
			t.Fatalf("garbage refresh did not clear the access cookie (got %+v)", c)
		}
	})

	// CASE 3 — Positive control: a VALID stored refresh token → 200 and a fresh
	// session cookie. Proves the suite isn't passing by rejecting everything.
	t.Run("valid_token_rotates_session", func(t *testing.T) {
		rec := post(t, &http.Cookie{Name: cookieRefresh, Value: rawRefresh})
		if rec.Code != http.StatusOK {
			t.Fatalf("valid token = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if !hasLiveSession(rec) {
			t.Fatalf("valid refresh did not mint a new access cookie (positive control failed)")
		}
	})
}
