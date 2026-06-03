package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// csrfRouter mounts RequireCSRF in front of a trivial handler on both a safe and
// an unsafe method so each scenario can be exercised end to end.
func csrfRouter() *gin.Engine {
	r := gin.New()
	r.Use(RequireCSRF())
	ok := func(c *gin.Context) { c.String(http.StatusOK, "ok") }
	r.GET("/x", ok)
	r.POST("/x", ok)
	return r
}

const csrfToken = "0123456789abcdef0123456789abcdef"

// request builds a request with the given cookie/header/bearer wiring.
func csrfRequest(method string, cookie, header, bearer string) *http.Request {
	req := httptest.NewRequest(method, "/x", nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookie})
	}
	if header != "" {
		req.Header.Set(csrfHeaderName, header)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return req
}

func TestCSRF_SafeMethodBypassesCheck(t *testing.T) {
	rec := httptest.NewRecorder()
	csrfRouter().ServeHTTP(rec, csrfRequest(http.MethodGet, "", "", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET without token: status = %d, want 200", rec.Code)
	}
}

func TestCSRF_MatchingTokenPasses(t *testing.T) {
	rec := httptest.NewRecorder()
	csrfRouter().ServeHTTP(rec, csrfRequest(http.MethodPost, csrfToken, csrfToken, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST with matching token: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCSRF_MismatchedTokenRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	csrfRouter().ServeHTTP(rec, csrfRequest(http.MethodPost, csrfToken, "wrong-token", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with mismatched token: status = %d, want 403", rec.Code)
	}
}

func TestCSRF_MissingHeaderRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	csrfRouter().ServeHTTP(rec, csrfRequest(http.MethodPost, csrfToken, "", ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with cookie but no header: status = %d, want 403", rec.Code)
	}
}

func TestCSRF_MissingCookieRejected(t *testing.T) {
	rec := httptest.NewRecorder()
	csrfRouter().ServeHTTP(rec, csrfRequest(http.MethodPost, "", csrfToken, ""))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with header but no cookie: status = %d, want 403", rec.Code)
	}
}

// A Bearer-authenticated caller is exempt: it cannot be driven by ambient
// browser cookies, so no CSRF token is required.
func TestCSRF_BearerAuthBypassesCheck(t *testing.T) {
	rec := httptest.NewRecorder()
	csrfRouter().ServeHTTP(rec, csrfRequest(http.MethodPost, "", "", "some-jwt"))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST with bearer token: status = %d, want 200", rec.Code)
	}
}
