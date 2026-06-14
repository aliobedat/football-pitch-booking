package handlers

// Tests for PR 4.2 mandatory pitch location. The create-validation cases run with
// no database (the 422 fires before the model). The create/update flow + manual-pin
// queue cases run against a live DB, SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run PitchLocation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/middleware"
)

func newPitchRouter(h *PitchHandler, userID int, role string) *gin.Engine {
	r := gin.New()
	inject := func(c *gin.Context) {
		c.Set(middleware.ContextKeyUserID, userID)
		c.Set(middleware.ContextKeyRole, role)
		c.Next()
	}
	r.POST("/pitches", inject, h.CreatePitch)
	r.PATCH("/pitches/:id", inject, h.UpdatePitch)
	return r
}

func validCreatePitchBody(mapsURL string) map[string]any {
	return map[string]any{
		"name": "Test Pitch", "neighborhood": "Khalda",
		"surface": "artificial_grass", "format": "خماسي",
		"price_per_hour": 30, "maps_url": mapsURL,
	}
}

// ── Test 1: create with empty or non-Google maps_url → 422 (no DB needed) ─────

func TestPitchLocation_CreateRejectsBadMapsURL(t *testing.T) {
	r := newPitchRouter(&PitchHandler{}, 1, "owner")
	for _, bad := range []string{"", "https://evil.com/maps", "http://maps.app.goo.gl/x", "ftp://x"} {
		rec := doJSON(t, r, http.MethodPost, "/pitches", validCreatePitchBody(bad))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("maps_url=%q: status = %d, want 422 (body: %s)", bad, rec.Code, rec.Body.String())
		}
		var resp map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp["field"] != "maps_url" {
			t.Errorf("maps_url=%q: field = %v, want \"maps_url\" (field-level error)", bad, resp["field"])
		}
	}
}

// ── Live env for the create/update flow ──────────────────────────────────────

type pitchLocEnv struct {
	pool    *pgxpool.Pool
	model   *data.PitchModel
	r       *gin.Engine
	ownerID int
	pitches []int
}

func newPitchLocEnv(t *testing.T) *pitchLocEnv {
	t.Helper()
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping live pitch-location test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping: %v", err)
	}
	var ownerID int
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ('Loc Owner', $1, 'owner', TRUE) RETURNING id
	`, fmt.Sprintf("+96277%06d", time.Now().UnixNano()%1_000_000)).Scan(&ownerID); err != nil {
		pool.Close()
		t.Fatalf("seed owner: %v", err)
	}
	model := &data.PitchModel{DB: pool}
	e := &pitchLocEnv{pool: pool, model: model, ownerID: ownerID,
		r: newPitchRouter(&PitchHandler{Model: model}, ownerID, "owner")}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		for _, pid := range e.pitches {
			_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pid)
		}
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, ownerID)
		pool.Close()
	})
	return e
}

// seedPitch creates a pitch via the model (bypassing handler validation) so a test
// can stage legacy/sentinel states. mapsURL may be "" for a legacy pitch.
func (e *pitchLocEnv) seedPitch(t *testing.T, mapsURL string) int {
	t.Helper()
	p, err := e.model.CreatePitch(context.Background(), data.CreatePitchRequest{
		Name: "Seed", Neighborhood: "Khalda", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: e.ownerID, MapsURL: mapsURL,
	})
	if err != nil {
		t.Fatalf("seedPitch: %v", err)
	}
	e.pitches = append(e.pitches, p.ID)
	return p.ID
}

func (e *pitchLocEnv) setCoords(t *testing.T, pitchID int, lat, lng float64) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE pitches SET latitude=$1, longitude=$2 WHERE id=$3`, lat, lng, pitchID); err != nil {
		t.Fatalf("setCoords: %v", err)
	}
}

func (e *pitchLocEnv) inManualPinQueue(t *testing.T, pitchID int) bool {
	t.Helper()
	var present bool
	if err := e.pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM pitches
			WHERE id = $1 AND deleted_at IS NULL
			  AND maps_url IS NOT NULL AND maps_url <> ''
			  AND (latitude IS NULL OR (latitude = 0 AND longitude = 0))
		)`, pitchID).Scan(&present); err != nil {
		t.Fatalf("inManualPinQueue: %v", err)
	}
	return present
}

// ── Test 2: create with a valid link → 201; pitch persisted with the link ────

func TestPitchLocation_CreateValidLinkSucceeds(t *testing.T) {
	e := newPitchLocEnv(t)
	// A well-formed Google host that won't actually resolve — create still succeeds;
	// coordinates either resolve async or stay at the (0,0) sentinel.
	rec := doJSON(t, e.r, http.MethodPost, "/pitches", validCreatePitchBody("https://maps.app.goo.gl/zzNonExistent123"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Data struct {
			ID      int    `json:"id"`
			MapsURL string `json:"maps_url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	e.pitches = append(e.pitches, resp.Data.ID)
	if resp.Data.MapsURL == "" {
		t.Fatalf("created pitch has no maps_url persisted")
	}
}

// ── Test 3: update a legacy pitch (coords, no URL), change only name → passes ─

func TestPitchLocation_UpdateLegacyPitchNameOnly(t *testing.T) {
	e := newPitchLocEnv(t)
	id := e.seedPitch(t, "https://maps.app.goo.gl/seed")
	// Make it "legacy": real coordinates, NO maps_url.
	e.setCoords(t, id, 32.0150, 35.8500)
	if _, err := e.pool.Exec(context.Background(), `UPDATE pitches SET maps_url='' WHERE id=$1`, id); err != nil {
		t.Fatalf("clear maps_url: %v", err)
	}

	rec := doJSON(t, e.r, http.MethodPatch, fmt.Sprintf("/pitches/%d", id), map[string]any{"name": "Renamed"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — legacy pitch with coords must pass (body: %s)", rec.Code, rec.Body.String())
	}
}

// ── Test 4: update blanking maps_url on a no-coords pitch → 422 ───────────────

func TestPitchLocation_UpdateBlankURLNoCoordsRejected(t *testing.T) {
	e := newPitchLocEnv(t)
	id := e.seedPitch(t, "https://maps.app.goo.gl/seed") // (0,0) coords by default
	blank := ""
	rec := doJSON(t, e.r, http.MethodPatch, fmt.Sprintf("/pitches/%d", id),
		map[string]any{"maps_url": blank})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 — blanking the only location source (body: %s)", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp["field"] != "maps_url" {
		t.Errorf("field = %v, want \"maps_url\"", resp["field"])
	}
}

// ── Test 5: create whose URL won't resolve still creates + enters manual-pin ──

func TestPitchLocation_UnresolvableCreateEntersManualPinQueue(t *testing.T) {
	e := newPitchLocEnv(t)
	rec := doJSON(t, e.r, http.MethodPost, "/pitches",
		validCreatePitchBody("https://maps.app.goo.gl/zzWillNotResolve999"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var resp struct{ Data struct{ ID int `json:"id"` } `json:"data"` }
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	e.pitches = append(e.pitches, resp.Data.ID)

	// Give the best-effort async resolver a moment; an unresolvable URL writes
	// nothing, so the pitch must remain in the manual-pin queue.
	time.Sleep(2 * time.Second)
	if !e.inManualPinQueue(t, resp.Data.ID) {
		t.Fatalf("pitch #%d should be in the manual-pin queue (maps_url set, no usable coords)", resp.Data.ID)
	}
}
