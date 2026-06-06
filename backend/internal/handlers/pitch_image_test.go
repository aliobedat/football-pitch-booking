package handlers

// HTTP-contract tests for the pitch-image upload flow that need no database:
//   - the signed-payload endpoint returns the non-secret fields and never the secret
//   - role enforcement bars players (403) at the route
//   - the persist trust-guard rejects a non-Cloudinary URL before any DB call
//
// Actor-scoping / not-owned-404 / soft-deleted-404 for the persist path exercise
// real SQL and live in internal/data/pitches_scoping_test.go (skipped w/o a DB).

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/ali/football-pitch-api/internal/cloudinary"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
)

func testPitchHandler(t *testing.T) *PitchHandler {
	t.Helper()
	cld, err := cloudinary.New(config.CloudinaryConfig{
		CloudName: "malaeb-cloud", APIKey: "123456789", APISecret: "topsecret",
		UploadPreset: "malaeb_pitches", Folder: "malaeb/pitches",
	})
	if err != nil {
		t.Fatalf("cloudinary.New: %v", err)
	}
	// Model has a nil DB: every test here returns BEFORE any DB access (signature
	// is pure; the guard rejects bad URLs pre-DB; role checks abort at middleware).
	return &PitchHandler{Model: &data.PitchModel{}, Cloudinary: cld}
}

// router mounting the image routes behind the SAME role middleware as production,
// with an injected identity in place of RequireAuth.
func newImageRouter(h *PitchHandler, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, 7)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/pitches/upload-signature", inject, middleware.RequireRole("owner", "admin"), h.UploadSignature)
	r.PATCH("/pitches/:id/image", inject, middleware.RequireRole("owner", "admin"), h.SetPitchImage)
	return r
}

func TestUploadSignature_OwnerGetsPayloadNoSecret(t *testing.T) {
	h := testPitchHandler(t)
	rec := doJSON(t, newImageRouter(h, "owner"), http.MethodPost, "/pitches/upload-signature", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "topsecret") {
		t.Fatalf("API secret leaked into signature response: %s", rec.Body.String())
	}

	var resp struct {
		Data cloudinary.SignedUpload `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := resp.Data
	if d.APIKey == "" || d.CloudName == "" || d.Signature == "" || d.Timestamp == 0 ||
		d.Folder != "malaeb/pitches" || d.UploadPreset != "malaeb_pitches" {
		t.Fatalf("incomplete signed payload: %+v", d)
	}
}

func TestUploadSignature_AdminAllowed(t *testing.T) {
	h := testPitchHandler(t)
	rec := doJSON(t, newImageRouter(h, "admin"), http.MethodPost, "/pitches/upload-signature", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin status = %d, want 200", rec.Code)
	}
}

func TestUploadSignature_PlayerForbidden(t *testing.T) {
	h := testPitchHandler(t)
	rec := doJSON(t, newImageRouter(h, "player"), http.MethodPost, "/pitches/upload-signature", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("player status = %d, want 403", rec.Code)
	}
}

func TestSetPitchImage_PlayerForbidden(t *testing.T) {
	h := testPitchHandler(t)
	body := map[string]string{"image_url": "https://res.cloudinary.com/malaeb-cloud/image/upload/v1/a.webp", "public_id": "malaeb/pitches/a"}
	rec := doJSON(t, newImageRouter(h, "player"), http.MethodPatch, "/pitches/5/image", body)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("player status = %d, want 403", rec.Code)
	}
}

func TestSetPitchImage_RejectsForeignURL(t *testing.T) {
	h := testPitchHandler(t)
	// A non-Cloudinary URL must be rejected by the trust guard BEFORE any DB call
	// (the Model has a nil DB; reaching it would panic).
	body := map[string]string{"image_url": "https://evil.example.com/x.png", "public_id": "x"}
	rec := doJSON(t, newImageRouter(h, "owner"), http.MethodPatch, "/pitches/5/image", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("foreign URL status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "invalid_image") {
		t.Fatalf("expected invalid_image error, got %s", rec.Body.String())
	}
}

func TestSetPitchImage_RejectsURLWithoutPublicID(t *testing.T) {
	h := testPitchHandler(t)
	// URL present but public_id missing is incoherent → 400, pre-DB.
	body := map[string]string{"image_url": "https://res.cloudinary.com/malaeb-cloud/image/upload/v1/a.webp"}
	rec := doJSON(t, newImageRouter(h, "owner"), http.MethodPatch, "/pitches/5/image", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}
