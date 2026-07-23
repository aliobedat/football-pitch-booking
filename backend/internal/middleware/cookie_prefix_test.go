package middleware

import (
	"net/http/httptest"
	"testing"
)

// TestSetCookieNamePrefix_ProductionUnchanged proves the zero-config default
// (as Production sets it) resolves to the historical malaab_* names.
func TestSetCookieNamePrefix_ProductionUnchanged(t *testing.T) {
	t.Cleanup(func() { SetCookieNamePrefix("malaab_") })

	SetCookieNamePrefix("malaab_")
	if accessCookieName != "malaab_access" {
		t.Fatalf("accessCookieName = %q, want malaab_access", accessCookieName)
	}
	if csrfCookieName != "malaab_csrf" {
		t.Fatalf("csrfCookieName = %q, want malaab_csrf", csrfCookieName)
	}
}

// TestSetCookieNamePrefix_DemoResolvesDifferentNames proves Production and
// Demo resolve to distinct cookie names, so a browser holding both cannot
// confuse or overwrite one session with the other.
func TestSetCookieNamePrefix_DemoResolvesDifferentNames(t *testing.T) {
	t.Cleanup(func() { SetCookieNamePrefix("malaab_") })

	SetCookieNamePrefix("malaab_demo_")
	if accessCookieName != "malaab_demo_access" {
		t.Fatalf("accessCookieName = %q, want malaab_demo_access", accessCookieName)
	}
	if csrfCookieName != "malaab_demo_csrf" {
		t.Fatalf("csrfCookieName = %q, want malaab_demo_csrf", csrfCookieName)
	}
	if accessCookieName == "malaab_access" || csrfCookieName == "malaab_csrf" {
		t.Fatalf("Demo cookie names must differ from Production names")
	}
}

// TestSetCookieNamePrefix_EmptyIsIgnored proves a blank/whitespace prefix can
// never accidentally strip Production's prefix off its cookies.
func TestSetCookieNamePrefix_EmptyIsIgnored(t *testing.T) {
	t.Cleanup(func() { SetCookieNamePrefix("malaab_") })

	SetCookieNamePrefix("malaab_demo_")
	SetCookieNamePrefix("")
	if accessCookieName != "malaab_demo_access" {
		t.Fatalf("empty prefix must be a no-op; accessCookieName = %q", accessCookieName)
	}
	SetCookieNamePrefix("   ")
	if accessCookieName != "malaab_demo_access" {
		t.Fatalf("whitespace-only prefix must be a no-op; accessCookieName = %q", accessCookieName)
	}
}

// RequireCSRF must validate against whatever name is currently configured —
// proves the CSRF check is not hardcoded to the Production name.
func TestRequireCSRF_UsesConfiguredCookieName(t *testing.T) {
	SetCookieNamePrefix("malaab_demo_")
	t.Cleanup(func() { SetCookieNamePrefix("malaab_") })

	rec := httptest.NewRecorder()
	req := csrfRequest("POST", csrfToken, csrfToken, "")
	csrfRouter().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("POST with matching token under Demo prefix: status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
