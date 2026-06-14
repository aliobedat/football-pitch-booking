package routes

// Routing guard: GET /api/v1/pitches/availability must reach SearchAvailability,
// NOT the /pitches/:id GetPitch handler. The static segment and the :id param sit
// under the same path node, and their precedence is registration-order sensitive —
// a reorder (or dropping the route) would silently send "availability" into
// strconv.Atoi and 400. This boots the REAL routes.Register (no hand-copied route
// list, so it can't drift) and asserts the request lands on the right handler.
//
// Both probe requests short-circuit before any DB access — SearchAvailability
// returns 422 (missing date/start) before touching the model, and GetPitch returns
// 400 on a non-numeric id before any query — so the live pool is never queried.
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set (a non-nil pool is all
// Register needs at construction).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
)

func newTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping availability routing guard")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	jwt := auth.NewJWTManager("test-secret-not-used-by-public-routes", time.Hour, 24*time.Hour)
	// Only the Cloudinary credentials are read at Register time (cloudinary.New);
	// dummy non-empty values satisfy the client constructor without any network.
	cfg := &config.Config{Cloudinary: config.CloudinaryConfig{
		CloudName: "test", APIKey: "test", APISecret: "test",
		UploadPreset: "test", Folder: "test",
	}}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	// The notification/store/service dependencies are not exercised by the pitches
	// GET routes, so nil interface values are sufficient for this routing guard.
	Register(r, pool, jwt, cfg, nil, nil, nil, nil, nil)
	return r
}

func TestRouting_AvailabilityReachesSearchHandler(t *testing.T) {
	r := newTestRouter(t)

	// No date/start → SearchAvailability returns 422 missing_param. If this were
	// mis-routed to GetPitch it would be 400 "رقم الملعب غير صحيح".
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/pitches/availability", nil))

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("GET /pitches/availability → %d, want 422 (SearchAvailability). body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "missing_param") {
		t.Fatalf("body=%s, want SearchAvailability's missing_param (not GetPitch)", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "رقم الملعب") {
		t.Fatalf("request was mis-routed to GetPitch (:id): %s", w.Body.String())
	}
}

func TestRouting_NonNumericIDStillHitsGetPitch(t *testing.T) {
	r := newTestRouter(t)

	// A different non-numeric segment must still fall to /pitches/:id → GetPitch
	// (400), proving the availability route did not over-capture the :id slot.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/pitches/not-a-number", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("GET /pitches/not-a-number → %d, want 400 (GetPitch). body=%s", w.Code, w.Body.String())
	}
}
