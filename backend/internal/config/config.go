package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	AppEnv     string
	ServerPort string
	DB         DBConfig
	JWT        JWTConfig  // ← NEW
	BcryptCost int        // ← NEW
}

type DBConfig struct {
	// URL takes full precedence when set. Passed directly to pgx so that
	// URL-encoded passwords and connection parameters are handled natively.
	// Falls back to the individual fields below when empty.
	URL      string
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	MaxConns int32
	MinConns int32
}

// JWTConfig holds all JWT-related configuration.
type JWTConfig struct {
	Secret        string
	AccessExpiry  time.Duration
	RefreshExpiry time.Duration
}

func (d DBConfig) DSN() string {
	if d.URL != "" {
		return d.URL
	}
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		d.Host, d.Port, d.User, d.Password, d.Name,
	)
}

func Load() *Config {
	maxConns, _ := strconv.Atoi(getEnv("DB_MAX_CONNS", "20"))
	minConns, _ := strconv.Atoi(getEnv("DB_MIN_CONNS", "5"))
	bcryptCost, _ := strconv.Atoi(getEnv("BCRYPT_COST", "12"))

	accessExpiry, err := time.ParseDuration(getEnv("JWT_ACCESS_EXPIRY", "15m"))
	if err != nil {
		panic("CONFIG: JWT_ACCESS_EXPIRY is not a valid duration (e.g. '15m', '1h')")
	}

	refreshExpiry, err := time.ParseDuration(getEnv("JWT_REFRESH_EXPIRY", "168h"))
	if err != nil {
		panic("CONFIG: JWT_REFRESH_EXPIRY is not a valid duration (e.g. '168h')")
	}

	jwtSecret := mustGetEnv("JWT_SECRET")
	if len(jwtSecret) < 32 {
		panic("CONFIG: JWT_SECRET must be at least 32 characters long")
	}

	if bcryptCost < 10 || bcryptCost > 31 {
		panic("CONFIG: BCRYPT_COST must be between 10 and 31")
	}

	return &Config{
		AppEnv:     getEnv("APP_ENV", "development"),
		ServerPort: getEnv("PORT", getEnv("SERVER_PORT", "8080")),
		BcryptCost: bcryptCost,
		JWT: JWTConfig{
			Secret:        jwtSecret,
			AccessExpiry:  accessExpiry,
			RefreshExpiry: refreshExpiry,
		},
		DB: loadDBConfig(int32(maxConns), int32(minConns)),
	}
}

func loadDBConfig(maxConns, minConns int32) DBConfig {
	if url := getEnv("DATABASE_URL", ""); url != "" {
		return DBConfig{URL: url, MaxConns: maxConns, MinConns: minConns}
	}
	return DBConfig{
		Host:     mustGetEnv("DB_HOST"),
		Port:     getEnv("DB_PORT", "5432"),
		User:     mustGetEnv("DB_USER"),
		Password: mustGetEnv("DB_PASSWORD"),
		Name:     mustGetEnv("DB_NAME"),
		MaxConns: maxConns,
		MinConns: minConns,
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func mustGetEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		panic(fmt.Sprintf("FATAL: required environment variable '%s' is not set", key))
	}
	return val
}