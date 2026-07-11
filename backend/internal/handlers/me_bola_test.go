package handlers

// §5.7 — PATCH /me must be BOLA-safe: the target user id comes from the SESSION
// only. A mass-assignment attempt — a body carrying another user's `id` — must be
// inert: the victim row stays byte-unchanged and the change applies ONLY to the
// session user. Tested at the HTTP layer because the JSON binding is exactly what
// could leak (a struct field for `id` would be the hole).
//
// SKIPPED unless PITCH_SCOPING_TEST_DATABASE_URL.
//
//	PITCH_SCOPING_TEST_DATABASE_URL=postgres://... go test ./internal/handlers/ -run MeBOLA -v

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/auth"
	"github.com/ali/football-pitch-api/internal/config"
	"github.com/ali/football-pitch-api/internal/middleware"
	"github.com/ali/football-pitch-api/internal/repository"
	"github.com/ali/football-pitch-api/internal/testutil"
)

func TestMeBOLA(t *testing.T) {
	dsn := os.Getenv("PITCH_SCOPING_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PITCH_SCOPING_TEST_DATABASE_URL not set; skipping PATCH /me BOLA test")
	}
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	suffix := testutil.UniqueSuffix() % 1_000_000
	const victimOriginal = "VICTIM ORIGINAL NAME"
	mkUser := func(name, prefix string) int {
		var id int
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (full_name, phone, role, opt_in) VALUES ($1,$2,'player',TRUE) RETURNING id
		`, name, fmt.Sprintf("+962%s%06d", prefix, suffix)).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		return id
	}
	sessionUser := mkUser("Session Original", "40")
	victim := mkUser(victimOriginal, "41")
	t.Cleanup(func() {
		cctx, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1)`, []int{sessionUser, victim})
	})

	nameOf := func(id int) string {
		var n string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(full_name,'') FROM users WHERE id=$1`, id).Scan(&n); err != nil {
			t.Fatalf("read name %d: %v", id, err)
		}
		return n
	}

	jwtManager := auth.NewJWTManager("integration-test-secret-key-min-32-chars-long", 15*time.Minute, 168*time.Hour)
	cfg := &config.Config{AppEnv: "test"}
	h := NewPhoneAuthHandler(nil, repository.NewAuthRepository(pool), jwtManager, cfg)

	r := gin.New()
	grp := r.Group("/")
	grp.Use(middleware.RequireAuth(jwtManager))
	grp.Use(middleware.RequireCSRF()) // Bearer-exempt
	grp.PATCH("/me", h.PatchMe)

	token, _ := jwtManager.GenerateAccessToken(sessionUser, auth.RolePlayer)

	// Mass-assignment attempt: body carries the VICTIM's id plus a name change.
	body, _ := json.Marshal(map[string]any{
		"id":        victim,
		"user_id":   victim,
		"full_name": "Session New Name",
	})
	req := httptest.NewRequest(http.MethodPatch, "/me", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH /me = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}

	// The victim row must be byte-unchanged — the body id is inert.
	if got := nameOf(victim); got != victimOriginal {
		t.Fatalf("CRITICAL BOLA: victim's name changed via body id: %q → %q", victimOriginal, got)
	}
	// The change applied only to the session user.
	if got := nameOf(sessionUser); got != "Session New Name" {
		t.Fatalf("session user's name = %q, want %q (the update must hit the session user)", got, "Session New Name")
	}
}
