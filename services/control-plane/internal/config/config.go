// Package config loads control-plane configuration from the environment.
//
// Configuration is environment-driven with safe local defaults so that
// `go run ./cmd/server` works out of the box against Docker Compose infra.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all control-plane runtime configuration.
type Config struct {
	Env  string // development | test | production
	Addr string // HTTP listen address, e.g. ":8080"

	// Infrastructure endpoints.
	DatabaseURL     string
	RedisAddr       string
	NATSURL         string
	OPAURL          string
	RuntimeInternal string // agent-runtime internal endpoint (execution plane)

	// Security.
	SessionCookieName string
	SessionTTL        time.Duration
	SecretsMasterKey  string // base64 AES-256 key for credential encryption (32 bytes decoded)

	// Observability.
	LogLevel    string
	ServiceName string
	OTLPEndpoint string
}

// Load reads configuration from the process environment, applying defaults
// appropriate for local Docker Compose development.
func Load() (Config, error) {
	cfg := Config{
		Env:               envOr("AGENTOS_ENV", "development"),
		Addr:              envOr("AGENTOS_HTTP_ADDR", ":8080"),
		DatabaseURL:       envOr("AGENTOS_DATABASE_URL", "postgres://agentos:agentos_dev@localhost:5432/agentos?sslmode=disable"),
		RedisAddr:         envOr("AGENTOS_REDIS_ADDR", "localhost:6379"),
		NATSURL:           envOr("AGENTOS_NATS_URL", "nats://localhost:4222"),
		OPAURL:            envOr("AGENTOS_OPA_URL", "http://localhost:8181"),
		RuntimeInternal:   envOr("AGENTOS_RUNTIME_INTERNAL_URL", "http://localhost:8090"),
		SessionCookieName: envOr("AGENTOS_SESSION_COOKIE", "agentos_session"),
		SessionTTL:        envDurationOr("AGENTOS_SESSION_TTL", 12*time.Hour),
		SecretsMasterKey:  os.Getenv("AGENTOS_SECRETS_MASTER_KEY"),
		LogLevel:          envOr("AGENTOS_LOG_LEVEL", "info"),
		ServiceName:       envOr("AGENTOS_SERVICE_NAME", "agentos-control-plane"),
		OTLPEndpoint:      envOr("AGENTOS_OTLP_ENDPOINT", ""),
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	switch c.Env {
	case "development", "test", "production":
	default:
		return fmt.Errorf("config: unknown AGENTOS_ENV %q", c.Env)
	}
	if strings.TrimSpace(c.Addr) == "" {
		return fmt.Errorf("config: AGENTOS_HTTP_ADDR must not be empty")
	}
	if c.SessionTTL <= 0 {
		return fmt.Errorf("config: AGENTOS_SESSION_TTL must be positive")
	}
	return nil
}

func envOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

// IntFromEnv is a small helper for callers that need numeric configuration.
func IntFromEnv(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
