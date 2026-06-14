// Command backfill-coords resolves pitch coordinates from each pitch's Google Maps
// share URL and (optionally) writes them back. It targets pitches that have NO
// usable coordinates yet (HasUsableCoords() == false) but DO have a maps_url.
//
// Usage:
//
//	DATABASE_URL=postgres://… go run ./cmd/backfill-coords --dry-run
//	DATABASE_URL=postgres://… go run ./cmd/backfill-coords           # live mutation
//
// It is idempotent (already-resolved pitches are skipped by the target query) and
// polite (a delay between fetches). Coordinates are validated against the Jordan
// bounding box + the (0,0) sentinel before being accepted.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/geo"
)

const politenessDelay = 500 * time.Millisecond

type target struct {
	id      int
	name    string
	mapsURL string
}

func main() {
	dryRun := flag.Bool("dry-run", false, "resolve and report only; do NOT write coordinates")
	flag.Parse()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL not set")
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	// Target: visible pitches with NO usable coordinates (NULL lat, or the (0,0)
	// sentinel) but a non-empty maps_url to resolve from.
	rows, err := pool.Query(ctx, `
		SELECT id, name, maps_url
		FROM pitches
		WHERE deleted_at IS NULL
		  AND maps_url IS NOT NULL AND maps_url <> ''
		  AND (latitude IS NULL OR (latitude = 0 AND longitude = 0))
		ORDER BY id
	`)
	if err != nil {
		fmt.Fprintf(os.Stderr, "query targets: %v\n", err)
		os.Exit(1)
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.name, &t.mapsURL); err != nil {
			fmt.Fprintf(os.Stderr, "scan: %v\n", err)
			os.Exit(1)
		}
		targets = append(targets, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "rows: %v\n", err)
		os.Exit(1)
	}

	mode := "LIVE"
	if *dryRun {
		mode = "DRY-RUN"
	}
	fmt.Printf("backfill-coords [%s] — %d pitch(es) need coordinates\n\n", mode, len(targets))

	var resolved int
	var flagged []target

	for i, t := range targets {
		if i > 0 {
			time.Sleep(politenessDelay) // be polite to Google between fetches
		}
		fctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		lat, lng, ok := geo.ResolvePitchCoordinates(fctx, t.mapsURL)
		cancel()

		if !ok {
			flagged = append(flagged, t)
			fmt.Printf("  ✗ #%-4d %-28s  FAILED  %s\n", t.id, trunc(t.name, 28), t.mapsURL)
			continue
		}
		resolved++
		fmt.Printf("  ✓ #%-4d %-28s  → (%.5f, %.5f)\n", t.id, trunc(t.name, 28), lat, lng)

		if !*dryRun {
			if _, err := pool.Exec(ctx,
				`UPDATE pitches SET latitude = $1, longitude = $2 WHERE id = $3`, lat, lng, t.id); err != nil {
				fmt.Fprintf(os.Stderr, "    UPDATE #%d failed: %v\n", t.id, err)
			}
		}
	}

	fmt.Printf("\n── summary [%s] ─────────────────────────────\n", mode)
	fmt.Printf("resolved: %d / %d\n", resolved, len(targets))
	fmt.Printf("flagged (could not resolve): %d\n", len(flagged))
	for _, t := range flagged {
		fmt.Printf("  - #%d  %s  |  %s\n", t.id, t.name, t.mapsURL)
	}
	if *dryRun {
		fmt.Printf("\n(dry-run: no rows were written)\n")
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
