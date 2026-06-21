package handlers

// §5.10 — CORS allow-list near-miss rejection. The matcher in cmd/api/main.go is
// `allowedOrigins[normalizeOrigin(origin)]` where
// `normalizeOrigin = TrimRight(TrimSpace(o), "/")`. This test REPLICATES that
// exact logic (the production matcher lives in package main and is not importable)
// and asserts gin-contrib/cors echoes Access-Control-Allow-Origin ONLY for an
// allowed origin — never for a near-miss.
//
// ⚠️ DIVERGENCE FROM THE WORK-ORDER ASSUMPTION, asserted truthfully here:
// normalizeOrigin DELIBERATELY trims a trailing slash and surrounding whitespace,
// so "http://localhost:3000/" and "http://localhost:3000 " normalise to the
// allowed base and are ACCEPTED (harmless — a browser Origin never carries a path
// or whitespace; this only collapses equivalent spellings of the SAME origin).
// The security-meaningful near-misses — case, scheme, host, port, subdomain — do
// NOT normalise away and are REJECTED. Pure middleware test; no DB.
//
//	go test ./internal/handlers/ -run CORSOriginMatching -v

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func TestCORSOriginMatching(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// ── Replicated verbatim from cmd/api/main.go ──
	normalizeOrigin := func(o string) string { return strings.TrimRight(strings.TrimSpace(o), "/") }
	allowedOrigins := map[string]bool{
		"http://localhost:3000":                           true,
		"http://localhost:3001":                           true,
		"https://football-pitch-booking-liart.vercel.app": true,
	}
	r := gin.New()
	r.Use(cors.New(cors.Config{
		AllowOriginFunc:  func(origin string) bool { return allowedOrigins[normalizeOrigin(origin)] },
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-CSRF-Token", "Idempotency-Key"},
		AllowCredentials: true,
	}))
	r.GET("/ping", func(c *gin.Context) { c.Status(http.StatusOK) })

	// acao issues a GET with the given Origin and returns the echoed
	// Access-Control-Allow-Origin header (empty == rejected, no echo).
	acao := func(origin string) string {
		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Header().Get("Access-Control-Allow-Origin")
	}

	cases := []struct {
		name    string
		origin  string
		allowed bool
	}{
		// Positive controls — exact allow-list entries.
		{"exact_localhost_3000", "http://localhost:3000", true},
		{"exact_localhost_3001", "http://localhost:3001", true},
		{"exact_vercel_prod", "https://football-pitch-booking-liart.vercel.app", true},

		// Deliberately-normalised equivalences → ACCEPTED (documented design).
		{"trailing_slash_normalised", "http://localhost:3000/", true},
		{"trailing_space_normalised", "http://localhost:3000 ", true},

		// Security-meaningful near-misses → REJECTED (no ACAO echo).
		{"case_variant_host", "http://LocalHost:3000", false},
		{"uppercase_scheme", "HTTP://localhost:3000", false},
		{"scheme_https_for_http_entry", "https://localhost:3000", false},
		{"wrong_port", "http://localhost:3002", false},
		{"different_host", "http://evil.com", false},
		{"subdomain_of_prod", "https://evil.football-pitch-booking-liart.vercel.app", false},
		{"prefix_attack", "http://localhost:3000.evil.com", false},
		{"empty_origin", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := acao(tc.origin)
			if tc.allowed {
				if got == "" {
					t.Fatalf("origin %q: no ACAO echo, want accepted", tc.origin)
				}
			} else {
				if got != "" {
					t.Fatalf("CRITICAL: near-miss origin %q was ACCEPTED (ACAO=%q), want rejected", tc.origin, got)
				}
			}
		})
	}
}
