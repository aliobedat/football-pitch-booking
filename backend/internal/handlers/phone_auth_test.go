package handlers

// PART 3B integration tests: drive the phone-first auth endpoints over a real
// gin router using the in-memory OTP service + Fake notification channel — no
// Postgres required. The Fake channel records the dispatched OTP so the test can
// read the code back and complete the request -> verify -> protected-route
// lifecycle exactly as a client would.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/models"
	"github.com/ali/football-pitch-api/internal/notification"
	"github.com/ali/football-pitch-api/internal/otp"
	"github.com/ali/football-pitch-api/internal/repository"
)

const (
	testJWTSecret = "test-jwt-secret-key-that-is-long-enough-32+"
	testPepper    = "test-otp-hmac-pepper-16+"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	m.Run()
}

// ─────────────────────────────────────────────────────────────────────────────
// In-memory PhoneAuthStore for tests
// ─────────────────────────────────────────────────────────────────────────────

type fakeAuthStore struct {
	mu       sync.Mutex
	optIn    map[string]bool
	users    map[string]*models.User
	nextID   int
	refreshN int
}

func newFakeAuthStore() *fakeAuthStore {
	return &fakeAuthStore{
		optIn: map[string]bool{},
		users: map[string]*models.User{},
	}
}

func (f *fakeAuthStore) SetOptIn(_ context.Context, phone string, optIn bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.optIn[phone] = optIn
	return nil
}

// HasOptedIn backs the notification opt-in gate.
func (f *fakeAuthStore) HasOptedIn(_ context.Context, phone string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.optIn[phone], nil
}

func (f *fakeAuthStore) EnsureVerifiedUser(_ context.Context, phone string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u, ok := f.users[phone]; ok {
		return u, nil
	}
	f.nextID++
	u := &models.User{
		ID:        f.nextID,
		Phone:     phone,
		Role:      models.RolePlayer,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	f.users[phone] = u
	return u, nil
}

func (f *fakeAuthStore) StoreRefreshToken(_ context.Context, _ int, _ string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshN++
	return nil
}

// FindByID backs GET /auth/me. It scans the phone-keyed map for a matching id.
func (f *fakeAuthStore) FindByID(_ context.Context, id int) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, u := range f.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, repository.ErrUserNotFound
}

// ─────────────────────────────────────────────────────────────────────────────
// Test harness
// ─────────────────────────────────────────────────────────────────────────────

type harness struct {
	router *gin.Engine
	fake   *notification.FakeChannel
	store  *fakeAuthStore
	jwt    *auth.JWTManager
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	store := newFakeAuthStore()
	fake := notification.NewFakeChannel(notification.FakeSilent())

	notifier := notification.NewService(
		notification.ChannelFake,
		notification.WithChannel(notification.ChannelFake, fake),
		notification.WithOptInChecker(notification.OptInFunc(store.HasOptedIn)),
	)

	hasher, err := otp.NewHMACHasher(testPepper)
	if err != nil {
		t.Fatalf("NewHMACHasher: %v", err)
	}

	mem := otp.NewMemoryStore()
	otpSvc := otp.New(notifier, mem, mem, hasher, otp.DefaultConfig())

	jwtManager := auth.NewJWTManager(testJWTSecret, 15*time.Minute, 168*time.Hour)
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:        testJWTSecret,
			AccessExpiry:  15 * time.Minute,
			RefreshExpiry: 168 * time.Hour,
		},
	}

	h := NewPhoneAuthHandler(otpSvc, store, jwtManager, cfg)

	r := gin.New()
	r.POST("/auth/request-otp", h.RequestOTP)
	r.POST("/auth/verify-otp", h.VerifyOTP)
	// A representative protected route guarded by the same middleware the rest of
	// the API uses.
	r.GET("/me", middleware.RequireAuth(jwtManager), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user_id": middleware.GetUserID(c)})
	})

	return &harness{router: r, fake: fake, store: store, jwt: jwtManager}
}

func (h *harness) do(t *testing.T, method, path string, body any, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// doCookies issues a request carrying the given cookies, exercising the
// cookie-based auth path the browser uses (the httpOnly access cookie).
func (h *harness) doCookies(t *testing.T, method, path string, body any, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

// cookieByName returns the named cookie set on the response, or nil if absent.
func cookieByName(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

// lastOTPCode returns the plaintext code from the most recent message the Fake
// channel recorded.
func (h *harness) lastOTPCode(t *testing.T) string {
	t.Helper()
	msg, ok := h.fake.Last()
	if !ok {
		t.Fatal("no OTP message was dispatched")
	}
	payload, ok := msg.Payload.(notification.OTPPayload)
	if !ok {
		t.Fatalf("last message payload is %T, want notification.OTPPayload", msg.Payload)
	}
	return payload.Code
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestFullLifecycle is the acceptance scenario: a new phone requests a code,
// verifies it, receives a session, and uses that session to reach a protected
// route — while an unauthenticated call to the same route is rejected.
func TestFullLifecycle(t *testing.T) {
	h := newHarness(t)
	const phone = "+962790000001"

	// 1. Request a code (with consent).
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": phone, "opt_in": true}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("request-otp: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if h.fake.Count() != 1 {
		t.Fatalf("expected exactly one dispatched message, got %d", h.fake.Count())
	}

	// 2. Verify the code we just "received".
	code := h.lastOTPCode(t)
	rec = h.do(t, http.MethodPost, "/auth/verify-otp",
		map[string]any{"phone": phone, "code": code}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify-otp: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// The session now arrives as httpOnly cookies, NOT in the body. The body
	// carries only the (non-sensitive) user profile and expiry.
	var verifyResp struct {
		Data struct {
			ExpiresIn int `json:"expires_in_seconds"`
			User      struct {
				ID    int    `json:"id"`
				Phone string `json:"phone"`
				Role  string `json:"role"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &verifyResp); err != nil {
		t.Fatalf("decode verify response: %v", err)
	}
	if verifyResp.Data.User.Phone != phone || verifyResp.Data.User.Role != "player" {
		t.Fatalf("unexpected user in response: %+v", verifyResp.Data.User)
	}

	// The access + refresh tokens must be delivered as httpOnly cookies and must
	// NOT leak anywhere into the response body.
	access := cookieByName(rec, "malaab_access")
	refresh := cookieByName(rec, "malaab_refresh")
	if access == nil || access.Value == "" || !access.HttpOnly {
		t.Fatalf("expected an httpOnly malaab_access cookie, got %+v", access)
	}
	if refresh == nil || refresh.Value == "" || !refresh.HttpOnly {
		t.Fatalf("expected an httpOnly malaab_refresh cookie, got %+v", refresh)
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(access.Value)) {
		t.Fatal("access token leaked into the response body")
	}

	// 3. Protected route accepts the session presented via cookie.
	sessionCookies := rec.Result().Cookies()
	rec = h.doCookies(t, http.MethodGet, "/me", nil, sessionCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("protected route with session cookie: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// 4. Protected route rejects an unauthenticated call.
	rec = h.do(t, http.MethodGet, "/me", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected route without token: status = %d, want 401", rec.Code)
	}

	// 5. ...and a garbage bearer token.
	rec = h.do(t, http.MethodGet, "/me", nil, "not-a-real-jwt")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("protected route with bad token: status = %d, want 401", rec.Code)
	}
}

// TestOTPIsOneTimeUse ensures a code cannot be replayed after a successful
// verification.
func TestOTPIsOneTimeUse(t *testing.T) {
	h := newHarness(t)
	const phone = "+962790000002"

	h.do(t, http.MethodPost, "/auth/request-otp", map[string]any{"phone": phone, "opt_in": true}, "")
	code := h.lastOTPCode(t)

	if rec := h.do(t, http.MethodPost, "/auth/verify-otp", map[string]any{"phone": phone, "code": code}, ""); rec.Code != http.StatusOK {
		t.Fatalf("first verify: status = %d, want 200", rec.Code)
	}
	rec := h.do(t, http.MethodPost, "/auth/verify-otp", map[string]any{"phone": phone, "code": code}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed verify: status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRequestOTPWithoutConsentIsRejected verifies the opt-in gate: an explicit
// opt_in=false is refused (403) and no code is dispatched.
func TestRequestOTPWithoutConsentIsRejected(t *testing.T) {
	h := newHarness(t)
	const phone = "+962790000003"

	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": phone, "opt_in": false}, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
	if h.fake.Count() != 0 {
		t.Fatalf("no message should be dispatched without consent, got %d", h.fake.Count())
	}
}

// TestRequestOTPMissingOptInField rejects a request that omits opt_in entirely.
func TestRequestOTPMissingOptInField(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "+962790000004"}, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

// TestVerifyWithWrongCode rejects an incorrect code without issuing a session.
func TestVerifyWithWrongCode(t *testing.T) {
	h := newHarness(t)
	const phone = "+962790000005"

	h.do(t, http.MethodPost, "/auth/request-otp", map[string]any{"phone": phone, "opt_in": true}, "")
	rec := h.do(t, http.MethodPost, "/auth/verify-otp", map[string]any{"phone": phone, "code": "000000"}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
}

// TestRequestOTPInvalidPhone rejects an unparseable phone number.
func TestRequestOTPInvalidPhone(t *testing.T) {
	h := newHarness(t)
	rec := h.do(t, http.MethodPost, "/auth/request-otp",
		map[string]any{"phone": "abc", "opt_in": true}, "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestNormalizePhone covers the canonicalisation rules feeding the OTP store and
// the DB E.164 constraint.
func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "+962790123456", want: "+962790123456"},
		{in: "0790123456", want: "+962790123456"},       // local trunk prefix
		{in: "00962790123456", want: "+962790123456"},   // 00 international prefix
		{in: "790123456", want: "+962790123456"},        // bare national number
		{in: "+962 79 012 3456", want: "+962790123456"}, // separators stripped
		{in: "+1-555-0100", want: "+15550100"},          // dashes stripped, other country
		{in: "", wantErr: true},
		{in: "abc", wantErr: true},
		{in: "+0123", wantErr: true}, // E.164 forbids a leading zero country code
	}
	for _, tc := range cases {
		got, err := normalizePhone(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizePhone(%q): expected error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizePhone(%q): unexpected error: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizePhone(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
