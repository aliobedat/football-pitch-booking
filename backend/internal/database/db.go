package database

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ali/football-pitch-api/internal/config"
)

// NewPool creates and validates a pgxpool connection pool.
//
// pgxpool is used over a single *pgx.Conn because it is safe for concurrent
// use across goroutines — essential for a web server handling parallel requests.
//
// The pool is configured with explicit min/max connections and a connection
// lifetime to prevent stale connections against PostgreSQL's idle timeout.
func NewPool(cfg *config.DBConfig) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("database: failed to parse DSN: %w", err)
	}

	// Connection pool tuning
	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = 1 * time.Hour
	poolConfig.MaxConnIdleTime = 30 * time.Minute
	poolConfig.HealthCheckPeriod = 1 * time.Minute

	// Attempt to connect with a bounded timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("database: failed to create pool: %w", err)
	}

	// Verify the connection is actually alive
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("database: ping failed — is PostgreSQL running? %w", err)
	}

	log.Printf("[DB] Connected — pool ready (min: %d, max: %d)\n",
		cfg.MinConns, cfg.MaxConns)

	return pool, nil
}