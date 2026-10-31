package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("AGENTOS_ENV", "")
	t.Setenv("AGENTOS_HTTP_ADDR", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want development", cfg.Env)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.SessionTTL != 12*time.Hour {
		t.Errorf("SessionTTL = %v, want 12h", cfg.SessionTTL)
	}
	if cfg.DatabaseURL == "" || cfg.NATSURL == "" || cfg.OPAURL == "" {
		t.Error("infrastructure defaults must be populated")
	}
}

func TestLoadOverridesAndValidation(t *testing.T) {
	t.Setenv("AGENTOS_ENV", "production")
	t.Setenv("AGENTOS_HTTP_ADDR", ":9090")
	t.Setenv("AGENTOS_SESSION_TTL", "30m")
	t.Setenv("AGENTOS_SECRETS_MASTER_KEY", "3q2+7wIEo6rdSmdBnY5wTnCXBlPlLLaCtL9TZnSYz2o=")
	t.Setenv("AGENTOS_INTERNAL_API_TOKEN", "prod-internal-token")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Env != "production" || cfg.Addr != ":9090" {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.SessionTTL != 30*time.Minute {
		t.Errorf("SessionTTL = %v, want 30m", cfg.SessionTTL)
	}
}

func TestLoadRejectsUnknownEnv(t *testing.T) {
	t.Setenv("AGENTOS_ENV", "staging-wrong")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for unknown AGENTOS_ENV")
	}
}
