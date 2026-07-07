package main

// PR-1 (fix/trusted-proxy-clientip): prove gin's ClientIP() resolves the real
// client from Railway's un-spoofable X-Real-IP header and ignores a forged
// X-Forwarded-For prefix. The router here is configured identically to the
// production engine setup in main.go (TrustedPlatform="X-Real-IP" +
// SetTrustedProxies(nil)); if that config regresses, these tests fail.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// newIPRouter mirrors the production client-IP trust config from main.go.
func newIPRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.TrustedPlatform = "X-Real-IP"
	if err := r.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	return r
}

func callIP(t *testing.T, r *gin.Engine, remoteAddr string, headers map[string]string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Body.String()
}

// 1. Real client returned: X-Real-IP (as Railway's edge sets it) is what ClientIP resolves.
func TestClientIP_ReturnsRealClientFromXRealIP(t *testing.T) {
	r := newIPRouter(t)
	got := callIP(t, r, "10.0.0.1:5000", map[string]string{"X-Real-IP": "203.0.113.9"})
	if got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q, want 203.0.113.9 (from X-Real-IP)", got)
	}
}

// 2. Forged X-Forwarded-For prefix is ignored when X-Real-IP is present.
func TestClientIP_IgnoresForgedXFFWhenRealIPPresent(t *testing.T) {
	r := newIPRouter(t)
	got := callIP(t, r, "10.0.0.1:5000", map[string]string{
		"X-Forwarded-For": "1.2.3.4", // attacker-supplied
		"X-Real-IP":       "203.0.113.9",
	})
	if got == "1.2.3.4" {
		t.Fatalf("ClientIP = %q — forged X-Forwarded-For was trusted", got)
	}
	if got != "203.0.113.9" {
		t.Fatalf("ClientIP = %q, want 203.0.113.9 (X-Real-IP), not the forged XFF", got)
	}
}

// 3. No trusted-platform header + forged XFF only → RemoteAddr, never the forged XFF.
func TestClientIP_NoRealIPHeader_FallsBackToRemoteAddr_NotForgedXFF(t *testing.T) {
	r := newIPRouter(t)
	got := callIP(t, r, "198.51.100.7:5000", map[string]string{
		"X-Forwarded-For": "1.2.3.4", // attacker-supplied, no X-Real-IP
	})
	if got == "1.2.3.4" {
		t.Fatalf("ClientIP = %q — forged X-Forwarded-For was trusted in the fallback path", got)
	}
	if got != "198.51.100.7" {
		t.Fatalf("ClientIP = %q, want 198.51.100.7 (RemoteAddr fallback)", got)
	}
}
