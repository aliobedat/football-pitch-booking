package handlers

// Tests for the pitch `description` field that need no database:
//   - normalizeDescription: trims, allows empty, caps length (runes), stores raw
//     (no HTML-encoding at rest — the XSS guard is render-time escaping)
//   - CreatePitch / UpdatePitch return 400 on an over-length description, BEFORE
//     any DB access (the handler's Model has a nil DB here).
//
// Round-trip persistence (create→GET, scoped update, non-owner 404) and the raw
// XSS string surviving a real INSERT/SELECT live in the DB-gated integration test
// internal/data/pitches_scoping_test.go.

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
)

func TestNormalizeDescription(t *testing.T) {
	xss := `<script>alert(1)</script><img src=x onerror=alert(1)>`

	cases := []struct {
		name    string
		in      string
		wantOut string
		wantOK  bool
	}{
		{"empty allowed", "", "", true},
		{"whitespace trims to empty", "   \n\t ", "", true},
		{"trims surrounding space", "  ملعب الحسين  ", "ملعب الحسين", true},
		{"at the cap allowed", strings.Repeat("a", maxDescriptionLen), strings.Repeat("a", maxDescriptionLen), true},
		{"over the cap rejected", strings.Repeat("a", maxDescriptionLen+1), "", false},
		// Arabic runes are counted as single characters, not bytes.
		{"arabic counted by rune", strings.Repeat("م", maxDescriptionLen), strings.Repeat("م", maxDescriptionLen), true},
		{"arabic over cap rejected", strings.Repeat("م", maxDescriptionLen+1), "", false},
		// Storage stays RAW: markup is preserved verbatim, never encoded at rest.
		{"xss stored raw", xss, xss, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, ok := normalizeDescription(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && out != tc.wantOut {
				t.Fatalf("out = %q, want %q", out, tc.wantOut)
			}
		})
	}
}

// newPitchWriteRouter mounts create/update behind the SAME role middleware as
// production, injecting an owner identity in place of RequireAuth. The handler's
// Model has a nil DB: the over-length tests return 400 BEFORE any DB access.
func newPitchWriteRouter(role string) *gin.Engine {
	h := &PitchHandler{Model: &data.PitchModel{}}
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, 7)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/pitches", inject, middleware.RequireRole("owner", "admin"), h.CreatePitch)
	r.PATCH("/pitches/:id", inject, middleware.RequireRole("owner", "admin"), h.UpdatePitch)
	return r
}

func TestCreatePitch_OverLongDescription400(t *testing.T) {
	body := map[string]any{
		"name":           "ملعب",
		"neighborhood":   "خلدا",
		"surface":        "artificial_grass",
		"format":         "خماسي",
		"price_per_hour": 25,
		"description":    strings.Repeat("x", maxDescriptionLen+1),
	}
	rec := doJSON(t, newPitchWriteRouter("owner"), http.MethodPost, "/pitches", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_description") {
		t.Fatalf("expected invalid_description error, got %s", rec.Body.String())
	}
}

func TestUpdatePitch_OverLongDescription400(t *testing.T) {
	body := map[string]any{"description": strings.Repeat("x", maxDescriptionLen+1)}
	rec := doJSON(t, newPitchWriteRouter("owner"), http.MethodPatch, "/pitches/5", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_description") {
		t.Fatalf("expected invalid_description error, got %s", rec.Body.String())
	}
}
