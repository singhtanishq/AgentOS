// Command server runs the AgentOS control plane HTTP API.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/config"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/health"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/policies"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/queue"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/secrets"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/server"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("control plane exited with error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)
	logger.Info("starting control plane",
		slog.String("env", cfg.Env),
		slog.String("addr", cfg.Addr),
	)

	readiness := health.NewRegistry()

	// Database connection: required. Readiness tracks it continuously.
	st, err := store.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer st.Close()
	readiness.Register("postgres", func(ctx context.Context) error { return st.Ping(ctx) })

	secretsManager, err := buildSecretsManager(cfg, logger)
	if err != nil {
		return fmt.Errorf("build secrets manager: %w", err)
	}

	// NATS JetStream: the durable execution queue. Required to accept
	// submissions; degraded readiness is reported when unavailable.
	connectCtx, connectCancel := context.WithTimeout(context.Background(), 15*time.Second)
	publisher, err := queue.Connect(connectCtx, cfg.NATSURL)
	connectCancel()
	if err != nil {
		return fmt.Errorf("connect queue: %w", err)
	}
	defer publisher.Close()
	ensureCtx, ensureCancel := context.WithTimeout(context.Background(), 15*time.Second)
	err = queue.EnsureStream(ensureCtx, publisher.JetStream(), logger)
	ensureCancel()
	if err != nil {
		return fmt.Errorf("ensure jetstream topology: %w", err)
	}
	readiness.Register("nats", func(ctx context.Context) error { return publisher.Healthy(ctx) })

	// OpenTelemetry + Prometheus instrumentation.
	otelCtx, otelCancel := context.WithTimeout(context.Background(), 15*time.Second)
	providers, err := telemetry.Init(otelCtx, cfg.ServiceName, cfg.OTLPEndpoint, logger)
	otelCancel()
	if err != nil {
		logger.Warn("telemetry init failed", slog.Any("error", err))
	}
	defer func() {
		if providers != nil {
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer shutdownCancel()
			if err := providers.Shutdown(shutdownCtx); err != nil {
				logger.Warn("telemetry shutdown failed", slog.Any("error", err))
			}
		}
	}()

	// OPA policy deployer: pushes the baseline and organization policies.
	deployer := &policies.Deployer{BaseURL: cfg.OPAURL, Logger: logger}
	opaCtx, opaCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if err := deployer.EnsureBaseline(opaCtx); err != nil {
		logger.Warn("opa baseline deploy failed — policy evaluation will fail closed",
			slog.Any("error", err))
	}
	opaCancel()

	srv := &http.Server{
		Handler:           server.NewRouter(cfg, logger, readiness, &server.Deps{Store: st, Secrets: secretsManager, Publisher: publisher, PolicyDeployer: deployer}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	logger.Info("control plane listening", slog.String("addr", ln.Addr().String()))

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("control plane stopped cleanly")
	return nil
}

// buildSecretsManager assembles the versioned key set. AGENTOS_SECRETS_KEYS
// accepts "1:<base64>,2:<base64>" for rotation; AGENTOS_SECRETS_MASTER_KEY
// provides a single version-1 key. Development environments without either
// use a fixed, documented dev key — production refuses to start.
func buildSecretsManager(cfg config.Config, logger *slog.Logger) (*secrets.Manager, error) {
	if raw := os.Getenv("AGENTOS_SECRETS_KEYS"); raw != "" {
		keys := make(map[int]string)
		for _, part := range strings.Split(raw, ",") {
			version, key, ok := strings.Cut(strings.TrimSpace(part), ":")
			if !ok {
				return nil, fmt.Errorf("AGENTOS_SECRETS_KEYS entry %q is not <version>:<base64>", part)
			}
			v, err := strconv.Atoi(version)
			if err != nil {
				return nil, fmt.Errorf("AGENTOS_SECRETS_KEYS version %q is not numeric", version)
			}
			keys[v] = key
		}
		return secrets.NewManager(keys)
	}
	if cfg.SecretsMasterKey != "" {
		return secrets.NewManager(map[int]string{1: cfg.SecretsMasterKey})
	}
	if cfg.Env == "production" {
		return nil, fmt.Errorf("AGENTOS_SECRETS_MASTER_KEY is required in production")
	}
	// Deterministic local-only development key (documented, not a secret).
	sum := sha256.Sum256([]byte("agentos-local-development-master-key"))
	logger.Warn("using the fixed local development master key — never use outside development")
	return secrets.NewManager(map[int]string{
		1: base64.StdEncoding.EncodeToString(sum[:]),
	})
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
