package handlers

// Tests for the pitch `maps_url` validation that need no database: UpdatePitch
// rejects a non-empty maps_url that is not an https URL with 400, BEFORE any DB
// access (the handler's Model has a nil DB here). The valid / empty-clear / absent
// cases pass validation and proceed to the DB layer (which panics on the nil DB),
// so they are exercised in the DB-gated integration tests, not here.
//
// Reuses newPitchWriteRouter from pitch_description_test.go.

import (
	"net/http"
	"strings"
	"testing"
)

func TestUpdatePitch_NonHTTPSMapsURL400(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{"plain text", "not-a-url"},
		{"http scheme rejected", "http://maps.app.goo.gl/abc"},
		{"scheme-relative rejected", "//maps.app.goo.gl/abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"maps_url": tc.url}
			rec := doJSON(t, newPitchWriteRouter("owner"), http.MethodPatch, "/pitches/59", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "invalid_maps_url") {
				t.Fatalf("expected invalid_maps_url error, got %s", rec.Body.String())
			}
		})
	}
}
