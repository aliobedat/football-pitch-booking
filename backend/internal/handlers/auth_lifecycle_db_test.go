package handlers

// WO-AUTH-GHOST-LOGIN — DB-backed lifecycle tests for the ruled logout
// semantics: logout is authenticated by the REFRESH cookie (not RequireAuth),
// revokes that token server-side, clears every session cookie, is idempotent
// (204 always), and stays behind CSRF. Drives the REAL handlers + middleware
// against the scratch DB.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run AuthLifecycleDB -v

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/testutil"
)

type lifecycleEnv struct {
	pool   *pgxpool.Pool
	router *gin.Engine
	phone  string
	userID int64
}

func newLifecycleEnv(t *testing.T) *lifecycleEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping auth lifecycle DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	testutil.AssertSchemaBaseline(t, pool)

	suffix := testutil.UniqueSuffix() % 1_000_000
	phone := fmt.Sprintf("+96298%06d", suffix)
	hash, err := bcrypt.GenerateFromPassword([]byte("lifecycle-pass-1"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var userID int64
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (full_name, phone, role, password_hash) VALUES ('Lifecycle Owner', $1, 'owner', $2) RETURNING id`,
		phone, string(hash)).Scan(&userID); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM refresh_tokens WHERE user_id = $1`, userID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID)
	})

	cfg := &config.Config{
		AppEnv: "test", // dev-recognised → Lax + insecure cookies for httptest
		JWT: config.JWTConfig{
			Secret: "lifecycle-test-secret-0001", AccessExpiry: time.Minute, RefreshExpiry: time.Hour,
		},
	}
	jwtM := auth.NewJWTManager(cfg.JWT.Secret, cfg.JWT.AccessExpiry, cfg.JWT.RefreshExpiry)
	pw := NewPasswordAuthHandler(repository.NewAuthRepository(pool), jwtM, cfg, nil)
	ah := NewAuthHandler(pool, jwtM, cfg)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/auth/password-login", pw.PasswordLogin)
	r.POST("/auth/refresh", ah.Refresh)
	// Mirrors routes.go: refresh-cookie-authenticated, CSRF enforced, NO RequireAuth.
	r.POST("/auth/logout", middleware.RequireCSRF(), ah.Logout)

	return &lifecycleEnv{pool: pool, router: r, phone: phone, userID: userID}
}

// do sends a request carrying the jar's cookies plus extra headers; Set-Cookie
// responses are folded back into the jar (MaxAge<0 deletes).
func (e *lifecycleEnv) do(t *testing.T, jar map[string]*http.Cookie, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	for _, c := range jar {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(jar, c.Name)
		} else if c.Value != "" {
			jar[c.Name] = c
		}
	}
	return rec
}

func (e *lifecycleEnv) login(t *testing.T, jar map[string]*http.Cookie) {
	t.Helper()
	rec := e.do(t, jar, http.MethodPost, "/auth/password-login",
		fmt.Sprintf(`{"phone":%q,"password":"lifecycle-pass-1"}`, e.phone), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	for _, name := range []string{"malaab_access", "malaab_refresh", "malaab_csrf", "malaab_role", "malaab_expiry"} {
		if _, ok := jar[name]; !ok {
			t.Fatalf("login did not set %s", name)
		}
	}
}

// ── The ruled pins ───────────────────────────────────────────────────────────

func TestAuthLifecycleDB_ExpiredLogoutRevokesAndClears(t *testing.T) {
	e := newLifecycleEnv(t)
	jar := map[string]*http.Cookie{}
	e.login(t, jar)
	oldRefresh := jar["malaab_refresh"].Value

	// Simulate access-token expiry: the browser drops the short-lived cookies.
	delete(jar, "malaab_access")
	delete(jar, "malaab_expiry")

	// Expired-session logout → 204 (previously 401 behind RequireAuth — the
	// ghost-login root cause) and every session cookie Set-Cookie-expired.
	rec := e.do(t, jar, http.MethodPost, "/auth/logout", "",
		map[string]string{"X-CSRF-Token": jar["malaab_csrf"].Value})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expired-session logout = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	cleared := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	for _, name := range []string{"malaab_access", "malaab_refresh", "malaab_csrf", "malaab_role", "malaab_expiry"} {
		if !cleared[name] {
			t.Errorf("logout did not clear %s", name)
		}
	}

	// The refresh token must be REVOKED server-side, not merely cleared from
	// the browser: replaying the old cookie value must fail.
	replay := map[string]*http.Cookie{"malaab_refresh": {Name: "malaab_refresh", Value: oldRefresh}}
	rec = e.do(t, replay, http.MethodPost, "/auth/refresh", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("refresh after logout = %d, want 401 (token revoked)", rec.Code)
	}
}

func TestAuthLifecycleDB_LogoutWithoutRefreshCookie(t *testing.T) {
	e := newLifecycleEnv(t)
	// CSRF pair only (a browser whose refresh cookie already died) → same 204,
	// never an oracle.
	jar := map[string]*http.Cookie{
		"malaab_csrf": {Name: "malaab_csrf", Value: "no-refresh-csrf-0001"},
	}
	rec := e.do(t, jar, http.MethodPost, "/auth/logout", "",
		map[string]string{"X-CSRF-Token": "no-refresh-csrf-0001"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("no-refresh logout = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
	// Idempotent: repeat → 204 again.
	jar["malaab_csrf"] = &http.Cookie{Name: "malaab_csrf", Value: "no-refresh-csrf-0001"}
	rec = e.do(t, jar, http.MethodPost, "/auth/logout", "",
		map[string]string{"X-CSRF-Token": "no-refresh-csrf-0001"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("repeat logout = %d, want 204", rec.Code)
	}
}

func TestAuthLifecycleDB_LogoutStillRequiresCSRF(t *testing.T) {
	e := newLifecycleEnv(t)
	jar := map[string]*http.Cookie{}
	e.login(t, jar)

	// Missing header → 403; the session must remain intact.
	rec := e.do(t, jar, http.MethodPost, "/auth/logout", "", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("logout without CSRF header = %d, want 403", rec.Code)
	}
	// Mismatched header → 403.
	rec = e.do(t, jar, http.MethodPost, "/auth/logout", "",
		map[string]string{"X-CSRF-Token": "attacker-guess"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("logout with mismatched CSRF = %d, want 403", rec.Code)
	}
	// The refresh token must still work (nothing was revoked by the 403s).
	rec = e.do(t, jar, http.MethodPost, "/auth/refresh", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh after CSRF-blocked logout = %d, want 200", rec.Code)
	}
}

func TestAuthLifecycleDB_FailedLoginIs401(t *testing.T) {
	e := newLifecycleEnv(t)
	jar := map[string]*http.Cookie{}
	e.login(t, jar)
	// Server-side pin for interceptor fix 2: a wrong-password attempt while the
	// browser holds a live refresh cookie is a plain 401 — the CLIENT must
	// surface it (isAuthEndpoint now exempts password-login from auto-refresh;
	// the client half is covered by the manual browser script in the WO report).
	rec := e.do(t, jar, http.MethodPost, "/auth/password-login",
		fmt.Sprintf(`{"phone":%q,"password":"WRONG"}`, e.phone), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-password login = %d, want 401", rec.Code)
	}
}
