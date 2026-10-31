// Package store provides the PostgreSQL access layer with per-transaction
// tenant context, mirroring the row-level security contract in the schema.
package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Tx aliases the pgx transaction interface for application packages.
type Tx = pgx.Tx

// Store wraps the connection pool and exposes transaction helpers that
// always establish the agentos.org_id / agentos.user_id context the
// database row-level security policies depend on.
type Store struct {
	Pool *pgxpool.Pool
}

// New opens a connection pool for the application role.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	cfg.MaxConns = 12
	cfg.MinConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.Pool.Close() }

// OrgTx runs fn inside a transaction whose RLS context is bound to the
// given organization. User may be empty for system/agent flows.
func (s *Store) OrgTx(ctx context.Context, orgID, userID string, fn func(pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := setTxContext(ctx, tx, orgID, userID); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UserTx runs fn inside a transaction bound to a user but with no
// organization context (org creation, workspace selection). Only
// organization-scoped operations with explicit policies may run here.
func (s *Store) UserTx(ctx context.Context, userID string, fn func(pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if userID != "" {
		if _, err := tx.Exec(ctx,
			"SELECT set_config('agentos.user_id', $1, true)", userID); err != nil {
			return fmt.Errorf("set user context: %w", err)
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func setTxContext(ctx context.Context, tx pgx.Tx, orgID, userID string) error {
	if orgID != "" {
		if _, err := tx.Exec(ctx,
			"SELECT set_config('agentos.org_id', $1, true)", orgID); err != nil {
			return fmt.Errorf("set org context: %w", err)
		}
	}
	if userID != "" {
		if _, err := tx.Exec(ctx,
			"SELECT set_config('agentos.user_id', $1, true)", userID); err != nil {
			return fmt.Errorf("set user context: %w", err)
		}
	}
	return nil
}

// Ping checks database availability for readiness endpoints.
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }
