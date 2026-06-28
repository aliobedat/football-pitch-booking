package handlers

// Tests for POST /auth/password-login — the phone+password admin login. They run
// over a real gin router with an in-memory store (no Postgres) and assert the
// fail-closed contract: a session is minted ONLY on a correct phone+password for a
// dashboard role, and every other case is a generic 401 with no session. A minted
// session is proven to reach a RequireAuth+RequireRole-guarded admin route.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/repository"
)

// ─── in-memory PasswordLoginStore ────────────────────────────────────────────

type loginRow struct {
	user *models.User
	hash string // bcrypt hash, or "" for NULL/unprovisioned
}

type fakePasswordStore struct {
	rows     map[string]loginRow // keyed by normalised phone
	refreshN int
}

func newFakePasswordStore() *fakePasswordStore {
	return &fakePasswordStore{rows: map[string]loginRow{}}
}

func (f *fakePasswordStore) add(phone string, id int, role models.UserRole, hash string) {
	f.rows[phone] = loginRow{
		user: &models.User{ID: id, Phone: phone, Role: role, CreatedAt: time.Now(), UpdatedAt: time.Now()},
		hash: hash,
	}
}

func (f *fakePasswordStore) FindLoginByPhone(_ context.Context, phone string) (*models.User, string, error) {
	row, ok := f.rows[phone]
	if !ok {
		return nil, "", repository.ErrUserNotFound
	}
	return row.user, row.hash, nil
}

func (f *fakePasswordStore) StoreRefreshToken(_ context.Context, _ int, _ string, _ time.Time) error {
	f.refreshN++
	return nil
}

// ─── harness ─────────────────────────────────────────────────────────────────

type pwHarness struct {
	router *gin.Engine
	store  *fakePasswordStore
}

// newPwHarness builds a router exposing /auth/password-login plus an admin route
// guarded by the SAME middleware the real API uses (RequireAuth + RequireRole), so
// a test can prove a minted session actually reaches admin. limiterMax<=0 disables
// limiting; otherwise a 3-strike limiter is wired for the rate-limit test.
func newPwHarness(limiterMax int) *pwHarness {
	store := newFakePasswordStore()
	jwtManager := auth.NewJWTManager(testJWTSecret, 15*time.Minute, 168*time.Hour)
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret: testJWTSecret, AccessExpiry: 15 * time.Minute, RefreshExpiry: 168 * time.Hour,
		},
	}
	var limiter LoginRateLimiter
	if limiterMax > 0 {
		limiter = NewMemoryLoginLimiter(limiterMax, time.Hour)
	}
	h := NewPasswordAuthHandler(store, jwtManager, cfg, limiter)

	r := gin.New()
	r.POST("/auth/password-login", h.PasswordLogin)
	r.GET("/admin",
		middleware.RequireAuth(jwtManager),
		middleware.RequireRole(auth.RoleOwner, auth.RoleAdmin, auth.RoleStaff),
		func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"role": middleware.GetUserRole(c)}) },
	)
	return &pwHarness{router: r, store: store}
}

// hashPw bcrypt-hashes a password at MinCost (fast for tests; real provisioning
// uses the configured cost).
func hashPw(t *testing.T, pw string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt: %v", err)
	}
	return string(h)
}

// doCookieReq issues a request carrying the given cookies (the httpOnly session
// cookies the browser would replay), with no body.
func doCookieReq(r *gin.Engine, method, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// ─── tests ───────────────────────────────────────────────────────────────────

// TestPasswordLogin_CorrectPassword_MintsRoledSession: a seeded owner with the right
// password gets a roled session AND can reach the admin route with it.
func TestPasswordLogin_CorrectPassword_MintsRoledSession(t *testing.T) {
	h := newPwHarness(0)
	const phone = "+962790000010"
	h.store.add(phone, 1, models.RoleOwner, hashPw(t, "ownerpass1"))

	rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
		map[string]any{"phone": "0790000010", "password": "ownerpass1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	access := cookieByName(rec, cookieAccess)
	if access == nil || access.Value == "" || !access.HttpOnly {
		t.Fatalf("expected httpOnly access cookie, got %+v", access)
	}

	// The minted session reaches the admin route.
	rec2 := doCookieReq(h.router, http.MethodGet, "/admin", rec.Result().Cookies())
	if rec2.Code != http.StatusOK {
		t.Fatalf("admin route with session: status = %d, want 200; body=%s", rec2.Code, rec2.Body.String())
	}
}

// TestPasswordLogin_WrongPassword_401: wrong password → 401, no session.
func TestPasswordLogin_WrongPassword_401(t *testing.T) {
	h := newPwHarness(0)
	const phone = "+962790000011"
	h.store.add(phone, 2, models.RoleOwner, hashPw(t, "correct-pass"))

	rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
		map[string]any{"phone": phone, "password": "WRONG"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, cookieAccess) != nil {
		t.Error("access cookie set on wrong password; want none")
	}
}

// TestPasswordLogin_NoPassword_401: phone alone (empty password) → 401, no session.
func TestPasswordLogin_NoPassword_401(t *testing.T) {
	h := newPwHarness(0)
	const phone = "+962790000012"
	h.store.add(phone, 3, models.RoleOwner, hashPw(t, "somepass1"))

	rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
		map[string]any{"phone": phone, "password": ""})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, cookieAccess) != nil {
		t.Error("access cookie set on empty password; want none")
	}
}

// TestPasswordLogin_PlayerRole_401: a player (even with a password) can never get an
// admin session through this endpoint.
func TestPasswordLogin_PlayerRole_401(t *testing.T) {
	h := newPwHarness(0)
	const phone = "+962790000013"
	h.store.add(phone, 4, models.RolePlayer, hashPw(t, "playerpass1"))

	rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
		map[string]any{"phone": phone, "password": "playerpass1"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, cookieAccess) != nil {
		t.Error("access cookie set for a player; want none")
	}
}

// TestPasswordLogin_NullHash_401: a roled user with no provisioned password (empty
// hash) → 401.
func TestPasswordLogin_NullHash_401(t *testing.T) {
	h := newPwHarness(0)
	const phone = "+962790000014"
	h.store.add(phone, 5, models.RoleOwner, "") // NULL/unprovisioned

	rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
		map[string]any{"phone": phone, "password": "anything1"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, cookieAccess) != nil {
		t.Error("access cookie set for a null-hash user; want none")
	}
}

// TestPasswordLogin_PasswordHashed: the stored credential is a bcrypt hash, not
// plaintext — it differs from the input and verifies via bcrypt.
func TestPasswordLogin_PasswordHashed(t *testing.T) {
	const pw = "Sup3r-secret"
	stored := hashPw(t, pw)
	if stored == pw {
		t.Fatal("stored value equals plaintext; must be hashed")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(pw)); err != nil {
		t.Fatalf("stored value is not a valid bcrypt hash of the password: %v", err)
	}
	// And the hash actually authenticates through the endpoint.
	h := newPwHarness(0)
	const phone = "+962790000015"
	h.store.add(phone, 6, models.RoleAdmin, stored)
	rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
		map[string]any{"phone": phone, "password": pw})
	if rec.Code != http.StatusOK {
		t.Fatalf("login with hashed password: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

// TestPasswordLogin_RateLimited: repeated failed attempts are blocked (429) once the
// per-phone cap is hit, and a correct password afterwards is still blocked while
// locked.
func TestPasswordLogin_RateLimited(t *testing.T) {
	h := newPwHarness(3) // 3 failed attempts allowed, then locked
	const phone = "+962790000016"
	h.store.add(phone, 7, models.RoleOwner, hashPw(t, "rightpass1"))

	for i := range 3 {
		rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
			map[string]any{"phone": phone, "password": "wrong"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401; body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	// 4th attempt is blocked regardless of correctness.
	rec := doJSON(t, h.router, http.MethodPost, "/auth/password-login",
		map[string]any{"phone": phone, "password": "rightpass1"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked attempt: status = %d, want 429; body=%s", rec.Code, rec.Body.String())
	}
	if cookieByName(rec, cookieAccess) != nil {
		t.Error("access cookie set while rate-limited; want none")
	}
}
