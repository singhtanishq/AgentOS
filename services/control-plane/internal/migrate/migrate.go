// Package migrate applies versioned SQL migrations to PostgreSQL.
//
// Each migration runs inside a single transaction together with its
// schema_migrations bookkeeping row, so partial application is impossible.
// A PostgreSQL advisory lock serializes concurrent migration attempts.
package migrate

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/singhtanishq/AgentOS/services/control-plane/migrations"
)

const advisoryLockKey = 0x6167656e // "agen"

// Runner applies embedded migrations to a database pool.
type Runner struct {
	pool     *pgxpool.Pool
	logger   *slog.Logger
	database string // target database name (informational)
}

// New opens a connection pool for the migration runner.
func New(ctx context.Context, databaseURL string, logger *slog.Logger) (*Runner, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	// Migrations should not linger: bounded lifetime for the pool.
	cfg.MaxConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	var dbName string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&dbName); err != nil {
		pool.Close()
		return nil, fmt.Errorf("resolve database name: %w", err)
	}
	return &Runner{pool: pool, logger: logger, database: dbName}, nil
}

// Close releases the connection pool.
func (r *Runner) Close() { r.pool.Close() }

// Result summarizes an Up run.
type Result struct {
	Applied []migrations.Migration
	Already int
}

// Up applies all pending migrations in order.
func (r *Runner) Up(ctx context.Context) (Result, error) {
	all, err := migrations.All()
	if err != nil {
		return Result{}, err
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Release()

	// Serialize migration runs across processes.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return Result{}, fmt.Errorf("acquire advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey)
	}()

	if err := ensureSchemaMigrations(ctx, conn.Conn()); err != nil {
		return Result{}, err
	}

	appliedVersions, err := appliedVersions(ctx, conn.Conn())
	if err != nil {
		return Result{}, err
	}

	// Detect conflicting history: an already-applied version whose recorded
	// name differs from the embedded file indicates edited history.
	for _, m := range all {
		if name, ok := appliedVersions[m.Version]; ok && name != m.Name {
			return Result{}, fmt.Errorf(
				"migration %06d was applied as %q but is now %q — applied migrations must not be renamed or edited",
				m.Version, name, m.Name)
		}
	}

	var result Result
	for _, m := range all {
		if _, done := appliedVersions[m.Version]; done {
			result.Already++
			continue
		}
		if err := applyOne(ctx, conn.Conn(), m); err != nil {
			return result, fmt.Errorf("apply migration %06d_%s: %w", m.Version, m.Name, err)
		}
		result.Applied = append(result.Applied, m)
		r.logger.Info("migration applied",
			slog.Int64("version", m.Version),
			slog.String("name", m.Name),
			slog.String("database", r.database),
		)
	}
	return result, nil
}

// Version returns the current applied schema version (0 when none or when
// no migrations table exists yet).
func (r *Runner) Version(ctx context.Context) (int64, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema = 'public' AND table_name = 'schema_migrations')",
	).Scan(&exists); err != nil {
		return 0, err
	}
	if !exists {
		return 0, nil
	}
	var v int64
	err := r.pool.QueryRow(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&v)
	return v, err
}

func ensureSchemaMigrations(ctx context.Context, conn *pgx.Conn) error {
	_, err := conn.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    BIGINT PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[int64]string, error) {
	rows, err := conn.Query(ctx, "SELECT version, name FROM schema_migrations")
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var v int64
		var name string
		if err := rows.Scan(&v, &name); err != nil {
			return nil, err
		}
		out[v] = name
	}
	return out, rows.Err()
}

func applyOne(ctx context.Context, conn *pgx.Conn, m migrations.Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	// Migration scripts contain multiple statements (including
	// dollar-quoted function bodies), so they are executed with the
	// simple protocol, which supports multi-statement strings. The
	// surrounding transaction still applies.
	mrr := tx.Conn().PgConn().Exec(ctx, m.SQL)
	if _, err := mrr.ReadAll(); err != nil {
		return fmt.Errorf("execute script: %w", err)
	}

	if _, err := tx.Exec(ctx,
		"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)", m.Version, m.Name); err != nil {
		return fmt.Errorf("record version: %w", err)
	}
	return tx.Commit(ctx)
}
