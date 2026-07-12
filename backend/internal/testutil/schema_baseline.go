package testutil

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Schema-baseline freshness gate (owner rule, 2026-07-12): a green DB suite
// against a stale scratch baseline is a FALSE green — migration 034's NOT NULL
// went untested exactly this way. The re-baseline procedure stamps the scratch
// database with the pg_dump generation token of the database/schema.sql it
// loaded (one-row table `schema_baseline`); every DB-backed fixture asserts
// that stamp matches the CURRENT working tree's schema.sql before running.
//
// Re-baseline recipe (adds the stamp):
//
//	psql $SCRATCH -f database/schema.sql
//	psql $SCRATCH -c "CREATE TABLE schema_baseline (token text NOT NULL);
//	                  INSERT INTO schema_baseline VALUES ('<\restrict token>');"

var (
	baselineOnce sync.Once
	baselineErr  error
)

// fileGenerationToken extracts the pg_dump `\restrict <token>` marker — unique
// per dump generation — from database/schema.sql, located by walking up from
// the test's working directory.
func fileGenerationToken() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for range [8]int{} {
		p := filepath.Join(dir, "database", "schema.sql")
		if f, err := os.Open(p); err == nil {
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if tok, ok := strings.CutPrefix(line, `\restrict `); ok {
					return strings.TrimSpace(tok), nil
				}
			}
			if err := sc.Err(); err != nil {
				return "", fmt.Errorf("read %s: %w", p, err)
			}
			return "", fmt.Errorf("no \\restrict generation token in %s", p)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("database/schema.sql not found above the test working directory")
}

// fataler is the subset of *testing.T the assertion needs (keeps testutil free
// of a testing import at call sites that pass t directly).
type fataler interface {
	Helper()
	Fatalf(format string, args ...any)
}

// AssertSchemaBaseline fails the calling test IMMEDIATELY when the scratch
// database's stamped generation token does not match the working tree's
// database/schema.sql (or when the stamp is missing — an unstamped scratch is
// treated as stale). Checked once per test process; subsequent calls reuse the
// verdict.
func AssertSchemaBaseline(t fataler, pool *pgxpool.Pool) {
	t.Helper()
	baselineOnce.Do(func() {
		want, err := fileGenerationToken()
		if err != nil {
			baselineErr = fmt.Errorf("schema-baseline gate: %w", err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var got string
		if err := pool.QueryRow(ctx, `SELECT token FROM schema_baseline LIMIT 1`).Scan(&got); err != nil {
			baselineErr = fmt.Errorf(
				"schema-baseline gate: scratch has no schema_baseline stamp (%v) — re-baseline from current main's database/schema.sql", err)
			return
		}
		if got != want {
			baselineErr = fmt.Errorf(
				"schema-baseline gate: scratch was loaded from a DIFFERENT schema.sql generation (stamp %.12s… != file %.12s…) — stale baseline, re-baseline before trusting any result",
				got, want)
		}
	})
	if baselineErr != nil {
		t.Fatalf("%v", baselineErr)
	}
}
