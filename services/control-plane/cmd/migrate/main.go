// Command migrate applies AgentOS database schema migrations.
//
// Usage:
//
//	migrate [flags]
//
// The database URL comes from AGENTOS_DATABASE_URL or --url.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/migrate"
)

func main() {
	url := flag.String("url", "", "PostgreSQL connection URL (default $AGENTOS_DATABASE_URL)")
	timeout := flag.Duration("timeout", 2*time.Minute, "migration run timeout")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	dbURL := *url
	if dbURL == "" {
		dbURL = os.Getenv("AGENTOS_DATABASE_URL")
	}
	if dbURL == "" {
		fatal("no database URL: set AGENTOS_DATABASE_URL or pass --url")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	runner, err := migrate.New(ctx, dbURL, logger)
	if err != nil {
		fatal("connect: %v", err)
	}
	defer runner.Close()

	before, err := runner.Version(ctx)
	if err != nil {
		fatal("read current version: %v", err)
	}

	result, err := runner.Up(ctx)
	if err != nil {
		fatal("apply migrations: %v", err)
	}

	after, _ := runner.Version(ctx)
	logger.Info("migrations complete",
		slog.Int64("schema_version_before", before),
		slog.Int64("schema_version_after", after),
		slog.Int("applied", len(result.Applied)),
		slog.Int("already_applied", result.Already),
	)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "migrate: "+format+"\n", args...)
	os.Exit(1)
}
