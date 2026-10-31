//go:build integration

// Package tests contains integration tests that run against a real
// PostgreSQL instance (Docker Compose). They verify tenant isolation
// (row-level security), immutability guards, state constraints and the
// atomic budget enforcement path.
//
// Run with:
//
//	go test -tags=integration ./tests/...
package tests

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	neturl "net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/migrate"
)

const (
	adminURLDefault = "postgres://agentos:agentos_dev@localhost:5432/postgres?sslmode=disable"
	appURLDefault   = "postgres://agentos_api:agentos_app_dev@localhost:5432/agentos_test?sslmode=disable"
	testDB          = "agentos_test"
)

var (
	pool     *pgxpool.Pool
	poolOnce sync.Once
	poolErr  error
)

// testEnv returns admin (owner/superuser) and app (RLS-bound) URLs.
func testEnv() (admin, app string) {
	admin = os.Getenv("TEST_DATABASE_ADMIN_URL")
	if admin == "" {
		admin = adminURLDefault
	}
	app = os.Getenv("TEST_DATABASE_APP_URL")
	if app == "" {
		app = appURLDefault
	}
	return admin, app
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	poolOnce.Do(func() {
		admin, app := testEnv()
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		adminConn, err := pgx.Connect(ctx, admin)
		if err != nil {
			poolErr = fmt.Errorf("connect admin database (is the Docker stack up?): %w", err)
			return
		}
		defer func() { _ = adminConn.Close(ctx) }()

		// Recreate the test database for a deterministic schema.
		if _, err := adminConn.Exec(ctx,
			"DROP DATABASE IF EXISTS "+testDB+" WITH (FORCE)"); err != nil {
			poolErr = fmt.Errorf("drop test database: %w", err)
			return
		}
		if _, err := adminConn.Exec(ctx, "CREATE DATABASE "+testDB); err != nil {
			poolErr = fmt.Errorf("create test database: %w", err)
			return
		}

		// Apply the full migration chain to the fresh database.
		testURL := replaceDBName(app, testDB)
		ownerURL := replaceDBName(admin, testDB)
		runner, err := migrate.New(ctx, ownerURL, slog.New(slog.NewTextHandler(os.Stderr, nil)))
		if err != nil {
			poolErr = fmt.Errorf("migration runner: %w", err)
			return
		}
		defer runner.Close()
		result, err := runner.Up(ctx)
		if err != nil {
			poolErr = fmt.Errorf("apply migrations: %w", err)
			return
		}
		if len(result.Applied) == 0 {
			poolErr = fmt.Errorf("no migrations applied — unexpected schema state")
			return
		}

		pool, poolErr = pgxpool.New(ctx, testURL)
		if poolErr != nil {
			poolErr = fmt.Errorf("connect app role: %w", poolErr)
		}
	})
	if poolErr != nil {
		// Setup failures must be loud: a silent skip would report the
		// integration suite as green while testing nothing.
		t.Fatalf("integration database setup failed: %v", poolErr)
	}
	return pool
}

func replaceDBName(rawURL, db string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Path = "/" + db
	return u.String()
}

// orgCtx sets the transaction organization/user context, mirroring how the
// application establishes tenancy for every request.
func orgCtx(ctx context.Context, tx pgx.Tx, org, user string) error {
	if _, err := tx.Exec(ctx, "SELECT set_config('agentos.org_id', $1, true)", org); err != nil {
		return err
	}
	if user == "" {
		return nil
	}
	_, err := tx.Exec(ctx, "SELECT set_config('agentos.user_id', $1, true)", user)
	return err
}

// runInTx runs fn in a fresh transaction (pgx v5 pools have no BeginFunc).
func runInTx(ctx context.Context, p *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func mustExec(t *testing.T, tx pgx.Tx, sql string, args ...any) commandTag {
	t.Helper()
	tag, err := tx.Exec(context.Background(), sql, args...)
	if err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
	return commandTag(tag)
}

type commandTag interface{ RowsAffected() int64 }

func TestRLSTenantIsolation(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()

	orgA := "11111111-1111-1111-1111-111111111111"
	orgB := "22222222-2222-2222-2222-222222222222"
	userA := "33333333-3333-3333-3333-333333333333"

	// Seed two organizations with one agent each (seed path runs as the app
	// role with proper context, exactly like production writes).
	seed := func(org, name, slug string) {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := orgCtx(ctx, tx, org, userA); err != nil {
			t.Fatal(err)
		}
		mustExec(t, tx,
			"INSERT INTO organizations (id, name, slug) VALUES ($1, $2, $3)", org, name, slug)
		mustExec(t, tx,
			"INSERT INTO agents (organization_id, name) VALUES ($1, $2)", org, "agent-"+slug)
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	seed(orgA, "Org A", "org-a")
	seed(orgB, "Org B", "org-b")

	t.Run("org A cannot see org B agents", func(t *testing.T) {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := orgCtx(ctx, tx, orgA, userA); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM agents").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("org A sees %d agents, want exactly its own 1", count)
		}
		var name string
		if err := tx.QueryRow(ctx, "SELECT name FROM agents").Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name != "agent-org-a" {
			t.Fatalf("org A sees agent %q, want agent-org-a", name)
		}
	})

	t.Run("cross-tenant update affects zero rows", func(t *testing.T) {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := orgCtx(ctx, tx, orgB, userA); err != nil {
			t.Fatal(err)
		}
		// Try to rename org A's agent from org B's context.
		tag := mustExec(t, tx,
			"UPDATE agents SET description = 'hijacked' WHERE name = 'agent-org-a'")
		if tag.RowsAffected() != 0 {
			t.Fatalf("cross-tenant UPDATE affected %d rows, want 0", tag.RowsAffected())
		}
	})

	t.Run("cross-tenant insert is rejected", func(t *testing.T) {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := orgCtx(ctx, tx, orgB, userA); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx,
			"INSERT INTO agents (organization_id, name) VALUES ($1, 'sneaky')", orgA); err == nil {
			t.Fatal("cross-tenant INSERT succeeded, want RLS rejection")
		}
	})

	t.Run("no context means default deny", func(t *testing.T) {
		tx, err := p.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM agents").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("unset context saw %d agents, want 0 (default deny)", count)
		}
	})

	t.Run("worker role cannot read users", func(t *testing.T) {
		_, appURL := testEnv()
		workerURL := replaceUser(replaceDBName(appURL, testDB), "agentos_worker")
		wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		wconn, err := pgx.Connect(wctx, workerURL)
		if err != nil {
			t.Fatalf("connect worker role: %v", err)
		}
		defer func() { _ = wconn.Close(wctx) }()
		if _, err := wconn.Exec(wctx, "SELECT count(*) FROM users"); err == nil {
			t.Fatal("worker role read users table, want permission denied")
		}
		// Worker can read the agents it must execute.
		if _, err := wconn.Exec(wctx, "SELECT count(*) FROM agents"); err != nil {
			t.Fatalf("worker role cannot read agents: %v", err)
		}
	})
}

func replaceUser(rawURL, user string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	if u.User != nil {
		pass, _ := u.User.Password()
		u.User = neturl.UserPassword(user, pass)
	}
	return u.String()
}

func TestAgentVersionImmutability(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()

	org := "44444444-4444-4444-4444-444444444444"
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := orgCtx(ctx, tx, org, ""); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, "INSERT INTO organizations (id, name, slug) VALUES ($1, 'Immut Org', 'immut-org')", org)
	var agentID, versionID string
	if err := tx.QueryRow(ctx,
		"INSERT INTO agents (organization_id, name) VALUES ($1, 'v-agent') RETURNING id", org,
	).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO agent_versions (organization_id, agent_id, version, configuration, status)
		VALUES ($1, $2, 1, '{"instructions": "v1"}', 'published') RETURNING id`,
		org, agentID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// A separate transaction attempts to mutate the published configuration.
	mtx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mtx.Rollback(ctx) }()
	if err := orgCtx(ctx, mtx, org, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := mtx.Exec(ctx,
		"UPDATE agent_versions SET configuration = '{\"instructions\": \"tampered\"}' WHERE id = $1",
		versionID); err == nil {
		t.Fatal("published agent version configuration was mutated, want immutability guard rejection")
	}
	// Reading it back (under org context — RLS applies) must show the
	// original configuration.
	var config []byte
	err = runInTx(ctx, p, func(tx pgx.Tx) error {
		if err := orgCtx(ctx, tx, org, ""); err != nil {
			return err
		}
		return tx.QueryRow(ctx,
			"SELECT configuration FROM agent_versions WHERE id = $1", versionID).Scan(&config)
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(config) != `{"instructions": "v1"}` {
		t.Fatalf("configuration was altered: %s", config)
	}
}

func TestExecutionTerminalConsistency(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()

	org := "55555555-5555-5555-5555-555555555555"
	setup, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = setup.Rollback(ctx) }()
	if err := orgCtx(ctx, setup, org, ""); err != nil {
		t.Fatal(err)
	}
	mustExec(t, setup, "INSERT INTO organizations (id, name, slug) VALUES ($1, 'Exec Org', 'exec-org')", org)
	var agentID, versionID string
	if err := setup.QueryRow(ctx,
		"INSERT INTO agents (organization_id, name, status) VALUES ($1, 'e-agent', 'active') RETURNING id", org,
	).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := setup.QueryRow(ctx, `
		INSERT INTO agent_versions (organization_id, agent_id, version, configuration, status)
		VALUES ($1, $2, 1, '{}', 'published') RETURNING id`, org, agentID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if err := setup.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// A terminal status without completed_at must be rejected.
	if _, err := p.Exec(ctx, `
		INSERT INTO executions (organization_id, agent_id, agent_version_id, status, completed_at)
		VALUES ($1, $2, $3, 'succeeded', NULL)`, org, agentID, versionID); err == nil {
		t.Fatal("terminal execution without completed_at accepted, want CHECK violation")
	}
}

func TestBudgetAtomicEnforcement(t *testing.T) {
	p := testPool(t)
	ctx := context.Background()

	org := "66666666-6666-6666-6666-666666666666"
	tx, err := p.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := orgCtx(ctx, tx, org, ""); err != nil {
		t.Fatal(err)
	}
	mustExec(t, tx, "INSERT INTO organizations (id, name, slug) VALUES ($1, 'Budget Org', 'budget-org')", org)
	var budgetID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO budgets (organization_id, name, period, limit_minor)
		VALUES ($1, 'tiny', 'total', 1000) RETURNING id`, org).Scan(&budgetID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// The enforcement statement used by the metering path: a conditional
	// atomic increment. Two concurrent spends of 600 against a 1000 limit
	// must yield exactly one success. Each attempt carries its own tenant
	// context on its own connection, mirroring concurrent executions.
	spend := func() bool {
		var allowed bool
		err := runInTx(ctx, p, func(tx pgx.Tx) error {
			if err := orgCtx(ctx, tx, org, ""); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `
				UPDATE budgets
				SET spent_minor = spent_minor + $2
				WHERE id = $1 AND spent_minor + $2 <= limit_minor`, budgetID, 600)
			if err != nil {
				return err
			}
			allowed = tag.RowsAffected() == 1
			return nil
		})
		if err != nil {
			t.Errorf("budget spend: %v", err)
			return false
		}
		return allowed
	}

	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = spend()
		}(i)
	}
	wg.Wait()

	successes := 0
	for _, ok := range results {
		if ok {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("budget allowed %d of 2 concurrent overspending attempts, want exactly 1", successes)
	}

	var spent int64
	err = runInTx(ctx, p, func(tx pgx.Tx) error {
		if err := orgCtx(ctx, tx, org, ""); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT spent_minor FROM budgets WHERE id = $1", budgetID).Scan(&spent)
	})
	if err != nil {
		t.Fatal(err)
	}
	if spent != 600 {
		t.Fatalf("budget spent = %d, want 600", spent)
	}
}
