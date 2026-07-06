package handlers

// DB-backed money test for the financial report (ratified amendment): fixtures
// 15.250 + 20.125 + 10.125 JOD must come back as gross_revenue EXACTLY 45.5 in
// the endpoint's JSON — asserted on the raw JSON token via json.Number, never a
// Go-side re-sum. The sum happens in SQL over NUMERIC(10,3); the single ::float8
// cast + handler round3 must not perturb it.
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL is set (repository integration
// convention — never run against production).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/data"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/timeutil"
)

func TestReports_MoneyExactThroughEndpointJSON(t *testing.T) {
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping money endpoint test")
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

	// ── fixtures: one owner, one pitch, three confirmed walk-ins ─────────────
	suffix := time.Now().UnixNano() % 1_000_000
	var ownerID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (full_name, phone, role, opt_in) VALUES ('RPT Money Owner',$1,'owner',TRUE) RETURNING id
	`, fmt.Sprintf("+96286%06d", suffix)).Scan(&ownerID); err != nil {
		pool.Close()
		t.Fatalf("seed owner: %v", err)
	}
	model := &data.PitchModel{DB: pool}
	p, err := model.CreatePitch(ctx, data.CreatePitchRequest{
		Name: "RPT Money Pitch", Neighborhood: "Amman", Surface: "artificial_grass",
		Format: "خماسي", PricePerHour: 30, OwnerID: int(ownerID),
	})
	if err != nil {
		pool.Close()
		t.Fatalf("seed pitch: %v", err)
	}
	pitchID := int64(p.ID)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer ccancel()
		_, _ = pool.Exec(cctx, `DELETE FROM bookings WHERE pitch_id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitch_audit_log WHERE pitch_id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM pitches WHERE id = $1`, pitchID)
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, ownerID)
		pool.Close()
	})

	// A fixed future Amman day keeps the fixtures clear of any real data.
	const civilDay = "2031-06-15"
	anchor, _ := time.Parse("2006-01-02", civilDay)
	dayStart, _ := timeutil.AmmanDayBoundsUTC(anchor)
	for i, price := range []string{"15.250", "20.125", "10.125"} {
		start := dayStart.Add(time.Duration(9+2*i) * time.Hour)
		if _, err := pool.Exec(ctx, `
			INSERT INTO bookings (pitch_id, player_id, booking_range, status, source, total_price, guest_name)
			VALUES ($1, NULL, tstzrange($2::timestamptz, $3::timestamptz, '[)'), 'confirmed', 'manual', $4::numeric, 'RPT Money Guest')
		`, pitchID, start, start.Add(time.Hour), price); err != nil {
			t.Fatalf("seed booking %s: %v", price, err)
		}
	}

	// ── the real handler + real repository over the production route shape ───
	h := NewReportsHandler(repository.NewReportsRepository(pool))
	r := newReportsRouter(h, int(ownerID), "owner")
	rec := doJSON(t, r, http.MethodGet,
		fmt.Sprintf("/owner/reports/financial?from=%s&to=%s&pitch_id=%d", civilDay, civilDay, pitchID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// Decode preserving the raw number token: the assertion is on the JSON the
	// client receives, not on any Go-side arithmetic.
	dec := json.NewDecoder(rec.Body)
	dec.UseNumber()
	var body struct {
		Data struct {
			Summary struct {
				GrossRevenue json.Number `json:"gross_revenue"`
				Collected    json.Number `json:"collected"`
				Outstanding  json.Number `json:"outstanding"`
				BookingCount int         `json:"booking_count"`
			} `json:"summary"`
		} `json:"data"`
	}
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := body.Data.Summary.GrossRevenue.String(); got != "45.5" {
		t.Errorf("gross_revenue JSON token = %q, want exactly \"45.5\" (15.250+20.125+10.125 summed in SQL)", got)
	}
	if body.Data.Summary.BookingCount != 3 {
		t.Errorf("booking_count = %d, want 3", body.Data.Summary.BookingCount)
	}
	// Nothing marked paid_cash → collected 0, outstanding equals gross.
	if got := body.Data.Summary.Collected.String(); got != "0" {
		t.Errorf("collected JSON token = %q, want \"0\"", got)
	}
	if got := body.Data.Summary.Outstanding.String(); got != "45.5" {
		t.Errorf("outstanding JSON token = %q, want \"45.5\"", got)
	}
}
