// Command seed populates the local demo environment with the fictional
// "Acme Technologies" organization and realistic, clearly-demo data.
//
// The seed is idempotent: it looks up entities by natural keys and skips
// what already exists. Run: make seed (after make migrate, infra-up).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/secrets"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/toolcatalog"
)

const (
	orgName = "Acme Technologies"
	orgSlug = "acme-technologies"

	demoPassword   = "DemoPass123!"
	demoMasterKey  = "3q2+7wIEo6rdSmdBnY5wTnCXBlPlLLaCtL9TZnSYz2o=" // matches the documented dev key
	demoAPIKey     = "agos_demo_local_key_0000000000000000"
)

type logger = slog.Logger

func main() {
	if err := run(); err != nil {
		slog.Error("seed failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	databaseURL := os.Getenv("AGENTOS_DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://agentos:agentos_dev@localhost:5432/agentos?sslmode=disable"
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	// The seed runs as the owner role and establishes tenant context per
	// transaction, exactly like the application.
	orgTx := func(fn func(pgx.Tx) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
		if err := fn(tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	setCtx := func(tx pgx.Tx, orgID string) error {
		_, err := tx.Exec(ctx, "SELECT set_config('agentos.org_id', $1, true)", orgID)
		return err
	}

	// ---- Organization -----------------------------------------------------
	var orgID string
	err = pool.QueryRow(ctx, `SELECT id FROM organizations WHERE slug = $1`, orgSlug).Scan(&orgID)
	if errorsIsNoRows(err) {
		err = orgTx(func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `
				INSERT INTO organizations (name, slug) VALUES ($1, $2)
				RETURNING id`, orgName, orgSlug).Scan(&orgID)
			if err != nil {
				return err
			}
			if err := setCtx(tx, orgID); err != nil {
				return err
			}
			return toolcatalog.SeedForOrganization(ctx, tx, orgID)
		})
		if err != nil {
			return fmt.Errorf("create org: %w", err)
		}
	} else if err != nil {
		return err
	}
	slog.Info("organization ready", slog.String("id", orgID))

	// ---- Users & memberships ----------------------------------------------
	type demoUser struct {
		name, email, role string
	}
	users := []demoUser{
		{"Avery Chen", "owner@acme.test", "owner"},
		{"Riley Nakamura", "admin@acme.test", "admin"},
		{"Jordan Reyes", "developer@acme.test", "developer"},
		{"Sam Oakley", "operator@acme.test", "operator"},
		{"Priya Nair", "auditor@acme.test", "auditor"},
	}
	userIDs := map[string]string{}
	for _, u := range users {
		hash, err := auth.HashPassword(demoPassword)
		if err != nil {
			return err
		}
		var userID string
		err = pool.QueryRow(ctx, `
			INSERT INTO users (name, email, password_hash)
			VALUES ($1, $2, $3)
			ON CONFLICT (email) WHERE deleted_at IS NULL DO UPDATE SET updated_at = now()
			RETURNING id`, u.name, u.email, hash).Scan(&userID)
		if err != nil {
			return fmt.Errorf("create user %s: %w", u.email, err)
		}
		userIDs[u.role] = userID
	}
	err = orgTx(func(tx pgx.Tx) error {
		if err := setCtx(tx, orgID); err != nil {
			return err
		}
		for _, u := range users {
			if _, err := tx.Exec(ctx, `
				INSERT INTO memberships (organization_id, user_id, role)
				VALUES ($1, $2, $3)
				ON CONFLICT (organization_id, user_id) DO NOTHING`,
				orgID, userIDs[u.role], u.role); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	// All further writes use the owner as the acting user and set context.
	act := func(fn func(tx pgx.Tx) error) error {
		return orgTx(func(tx pgx.Tx) error {
			if err := setCtx(tx, orgID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx,
				"SELECT set_config('agentos.user_id', $1, true)", userIDs["owner"]); err != nil {
				return err
			}
			return fn(tx)
		})
	}

	// ---- Model provider, credential, models --------------------------------
	var providerID string
	err = act(func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `
			INSERT INTO model_providers (organization_id, provider_type, display_name)
			VALUES ($1, 'mock', 'Acme Mock Provider')
			ON CONFLICT (organization_id, provider_type, base_url) DO UPDATE SET display_name = EXCLUDED.display_name
			RETURNING id`, orgID).Scan(&providerID)
		if err != nil {
			return err
		}
		manager, err := secrets.NewManager(map[int]string{1: demoMasterKey})
		if err != nil {
			return err
		}
		envelope, err := manager.Encrypt([]byte("sk-mock-acme-demo-key"))
		if err != nil {
			return err
		}
		var credExists bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM model_credentials WHERE organization_id = $1)", orgID,
		).Scan(&credExists); err != nil {
			return err
		}
		if !credExists {
			if _, err := tx.Exec(ctx, `
				INSERT INTO model_credentials
					(organization_id, provider_id, label, ciphertext, nonce, key_version, key_fingerprint)
				VALUES ($1, $2, 'demo-primary', $3, $4, 1, $5)`,
				orgID, providerID, envelope[13:], envelope[1:13],
				secrets.Fingerprint([]byte("sk-mock-acme-demo-key"))); err != nil {
				return err
			}
		}
		for _, m := range []struct {
			modelID, name string
			in, out       int64
		}{
			{"mock-mini", "Mock Mini", 15, 60},
			{"mock-standard", "Mock Standard", 60, 240},
		} {
			if _, err := tx.Exec(ctx, `
				INSERT INTO models (organization_id, provider_id, model_id, display_name,
					capabilities, input_price_per_mtok_minor, output_price_per_mtok_minor)
				VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
				ON CONFLICT (provider_id, model_id) DO NOTHING`,
				orgID, providerID, m.modelID, m.name,
				`{"context_window":32000,"tools":true,"streaming":true}`, m.in, m.out); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("model gateway: %w", err)
	}

	// ---- Agents + versions + deployments ------------------------------------
	config := func(instructions string, tools []string) string {
		bound := make([]map[string]string, 0, len(tools))
		for _, t := range tools {
			bound = append(bound, map[string]string{"name": t})
		}
		b, _ := json.Marshal(map[string]any{
			"instructions": instructions,
			"model":        map[string]any{"provider_type": "mock", "model_id": "mock-mini"},
			"tools":        bound,
			"limits":       map[string]any{"max_steps": 10, "timeout_ms": 120000},
		})
		return string(b)
	}

	type agentSpec struct {
		name, description, v1Instructions, v2Instructions string
		tools                                             []string
		status                                            string
		deploy                                            bool
	}
	agentSpecs := []agentSpec{
		{
			name: "Research Assistant", description: "Searches permitted sources and summarizes findings with references.",
			v1Instructions: "You are a research assistant. Summarize findings and cite sources.",
			v2Instructions: "You are a precise research assistant. Use tools when asked to compute, cite sources and mark estimates.",
			tools:          []string{"web.search", "document.read", "calculator"},
			status:         "active", deploy: true,
		},
		{
			name: "Support Assistant", description: "Answers from the knowledge base and drafts customer replies.",
			v1Instructions: "You are a support assistant. Ground answers in the knowledge base and never promise refunds.",
			tools:          []string{"database.query", "crm.lookup", "email.draft"},
			status:         "active", deploy: true,
		},
		{
			name: "Finance Assistant", description: "Reads approved financial samples and produces summaries (draft).",
			v1Instructions: "You are a finance assistant. Only use approved sample data.",
			tools:          []string{"calculator"},
			status:         "draft", deploy: false,
		},
	}

	agentIDs := map[string]string{}
	versionIDs := map[string]string{} // agent name → published version id
	for _, spec := range agentSpecs {
		var agentID string
		var v1ID int
		err = act(func(tx pgx.Tx) error {
			err := tx.QueryRow(ctx, `
				INSERT INTO agents (organization_id, name, description, status, created_by)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (organization_id, name) WHERE deleted_at IS NULL
				DO UPDATE SET description = EXCLUDED.description
				RETURNING id, (SELECT COALESCE(MAX(version), 0) FROM agent_versions av WHERE av.agent_id = agents.id)`,
				orgID, spec.name, spec.description, spec.status, userIDs["owner"],
			).Scan(&agentID, &v1ID)
			if err != nil {
				return err
			}
			if v1ID == 0 {
				if _, err := tx.Exec(ctx, `
					INSERT INTO agent_versions (organization_id, agent_id, version, configuration, changelog, created_by)
					VALUES ($1, $2, 1, $3::jsonb, 'initial version', $4)`,
					orgID, agentID, config(spec.v1Instructions, spec.tools), userIDs["owner"]); err != nil {
					return err
				}
				v1ID = 1
			}
			if spec.v2Instructions != "" {
				var hasV2 bool
				if err := tx.QueryRow(ctx,
					"SELECT EXISTS (SELECT 1 FROM agent_versions WHERE agent_id = $1 AND version = 2)",
					agentID).Scan(&hasV2); err != nil {
					return err
				}
				if !hasV2 {
					if _, err := tx.Exec(ctx, `
						INSERT INTO agent_versions (organization_id, agent_id, version, configuration, changelog, created_by)
						VALUES ($1, $2, 2, $3::jsonb, 'stricter sourcing and tool usage', $4)`,
						orgID, agentID, config(spec.v2Instructions, spec.tools), userIDs["developer"]); err != nil {
						return err
					}
				}
			}
			// Publish both versions.
			if _, err := tx.Exec(ctx, `
				UPDATE agent_versions SET status = 'published', published_at = now()
				WHERE agent_id = $1 AND status = 'draft' AND version <= 2`, agentID); err != nil {
				return err
			}
			if spec.deploy {
				var deployed bool
				if err := tx.QueryRow(ctx,
					"SELECT EXISTS (SELECT 1 FROM deployments WHERE agent_id = $1 AND environment = 'production' AND status = 'active')",
					agentID).Scan(&deployed); err != nil {
					return err
				}
				if !deployed {
					var versionID string
					if err := tx.QueryRow(ctx, `
						SELECT id FROM agent_versions WHERE agent_id = $1 AND status = 'published'
						ORDER BY version DESC LIMIT 1`, agentID).Scan(&versionID); err != nil {
						return err
					}
					if _, err := tx.Exec(ctx, `
						INSERT INTO deployments (organization_id, agent_id, agent_version_id, environment, status, created_by)
						VALUES ($1, $2, $3, 'production', 'active', $4)`,
						orgID, agentID, versionID, userIDs["operator"]); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("agent %s: %w", spec.name, err)
		}
		agentIDs[spec.name] = agentID
		var publishedVersionID string
		if err := pool.QueryRow(ctx,
			"SELECT id FROM agent_versions WHERE agent_id = $1 AND status = 'published' ORDER BY version DESC LIMIT 1",
			agentID).Scan(&publishedVersionID); err != nil {
			return err
		}
		versionIDs[spec.name] = publishedVersionID
	}

	// ---- Identity for the research agent ------------------------------------
	err = act(func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM agent_identities WHERE agent_id = $1)",
			agentIDs["Research Assistant"]).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		secret, hash, prefix, err := auth.NewAPIKey()
		if err != nil {
			return err
		}
		_ = secret // demo credential hash is stored; the raw value is intentionally discarded
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_identities
				(organization_id, agent_id, name, credential_hash, credential_prefix, credential_expires_at)
			VALUES ($1, $2, 'research-production', $3, $4, now() + interval '30 days')`,
			orgID, agentIDs["Research Assistant"], hash, prefix); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// ---- Executions with events, spans, usage -------------------------------
	if err := seedExecutions(act, orgID, agentIDs, versionIDs); err != nil {
		return err
	}

	// ---- Governance: policy, budgets, approvals, alerts ----------------------
	err = act(func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM policies WHERE organization_id = $1 AND name = 'restrict-finance-tools')",
			orgID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			if _, err := tx.Exec(ctx,
				"INSERT INTO policies (organization_id, name, kind, content, version, created_by) "+
					"VALUES ($1, 'restrict-finance-tools', 'authorization', $2, 1, $3)",
				orgID, demoPolicyContent, userIDs["admin"]); err != nil {
				return err
			}
		}
		var budgetCount int
		if err := tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM budgets WHERE organization_id = $1", orgID).Scan(&budgetCount); err != nil {
			return err
		}
		if budgetCount == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO budgets (organization_id, name, period, limit_minor, enforcement_action, thresholds)
				VALUES ($1, 'org-monthly-spend', 'monthly', 500000, 'alert_only',
				        '{"50": "alert", "80": "alert", "100": "enforce"}'::jsonb)`, orgID); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO budgets (organization_id, agent_id, name, period, limit_minor, enforcement_action, thresholds)
				VALUES ($1, $2, 'research-agent-cap', 'monthly', 100000, 'block',
				        '{"50": "alert", "80": "alert", "100": "enforce"}'::jsonb)`,
				orgID, agentIDs["Research Assistant"]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("governance: %w", err)
	}

	if err := seedApprovalsAndAlerts(act, orgID, agentIDs); err != nil {
		return err
	}

	// ---- Evaluations ---------------------------------------------------------
	err = act(func(tx pgx.Tx) error {
		var datasetID string
		err := tx.QueryRow(ctx, `
			INSERT INTO evaluation_datasets (organization_id, name, description, created_by)
			VALUES ($1, 'research-quality', 'Deterministic checks on research answers', $2)
			ON CONFLICT (organization_id, name) WHERE deleted_at IS NULL
			DO UPDATE SET description = EXCLUDED.description
			RETURNING id`, orgID, userIDs["developer"]).Scan(&datasetID)
		if err != nil {
			return err
		}
		var caseCount int
		if err := tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM evaluation_cases WHERE dataset_id = $1", datasetID).Scan(&caseCount); err != nil {
			return err
		}
		if caseCount == 0 {
			cases := []struct {
				name, query, rules string
			}{
				{"structured answer", "Summarize the Q3 report",
					`[{"type": "contains_all", "values": ["Analysis of", "Recommended action plan"]}]`},
				{"references plan", "What changed in the platform?",
					`[{"type": "contains_any", "values": ["Recommended action plan", "considerations"]}]`},
				{"no leaks", "Tell me about pricing",
					`[{"type": "not_contains", "values": ["confidential"]}]`},
				{"length bound", "hello",
					`[{"type": "min_length", "value": 20}]`},
			}
			for i, c := range cases {
				if _, err := tx.Exec(ctx, `
					INSERT INTO evaluation_cases (organization_id, dataset_id, name, input, rules, position)
					VALUES ($1, $2, $3, $4::jsonb, $5::jsonb, $6)`,
					orgID, datasetID, c.name,
					fmt.Sprintf(`{"query": %q}`, c.query), c.rules, i+1); err != nil {
					return err
				}
			}
			// A completed run with results (deterministic demo values).
			var runID string
			if err := tx.QueryRow(ctx, `
				INSERT INTO evaluation_runs
					(organization_id, dataset_id, agent_id, agent_version_id, status, pass_rate,
					 total_cases, passed_cases, completed_at)
				VALUES ($1, $2, $3, $4, 'completed', 0.75, 4, 3, now())
				RETURNING id`,
				orgID, datasetID, agentIDs["Research Assistant"], versionIDs["Research Assistant"],
			).Scan(&runID); err != nil {
				return err
			}
			results := []struct {
				passed bool
				score  string
				output string
				rules  string
			}{
				{true, "1.0000", "1. Analysis of: Summarize the Q3 report\n2. Key considerations identified from the available context.\n3. Recommended action plan (reference 4f2a91bc).",
					`[{"type": "contains_all", "passed": true, "detail": "all present"}]`},
				{true, "1.0000", "1. Analysis of: What changed in the platform?\n2. Recommended action plan (reference 91bc4f2a).",
					`[{"type": "contains_any", "passed": true, "detail": "matched 'Recommended action plan'"}]`},
				{true, "1.0000", "1. Analysis of: Tell me about pricing\n2. Key considerations identified.",
					`[{"type": "not_contains", "passed": true, "detail": "none present"}]`},
				{false, "0.0000", "Short answer.",
					`[{"type": "min_length", "passed": false, "detail": "length 13 < 20"}]`},
			}
			for i, r := range results {
				if _, err := tx.Exec(ctx, `
					INSERT INTO evaluation_results
						(organization_id, run_id, case_id, passed, score, output, rule_results)
					VALUES ($1, $2,
					        (SELECT id FROM evaluation_cases WHERE dataset_id = $3 ORDER BY position LIMIT 1 OFFSET $4),
					        $5, $6, $7, $8::jsonb)`,
					orgID, runID, datasetID, i, r.passed, r.score, r.output, r.rules); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("evaluations: %w", err)
	}

	// ---- Developer platform: API key, webhook, MCP connection ----------------
	err = act(func(tx pgx.Tx) error {
		var keyExists bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM api_keys WHERE organization_id = $1 AND name = 'demo-local')",
			orgID).Scan(&keyExists); err != nil {
			return err
		}
		if !keyExists {
			if _, err := tx.Exec(ctx, `
				INSERT INTO api_keys (organization_id, name, key_hash, key_prefix, scopes, created_by)
				VALUES ($1, 'demo-local', $2, $3, $4, $5)`,
				orgID, auth.TokenHash(demoAPIKey), demoAPIKey[:14],
				[]string{"executions.submit", "executions.read", "agents.read", "agents.edit", "executions.cancel"},
				userIDs["developer"]); err != nil {
				return err
			}
		}
		var whExists bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM webhooks WHERE organization_id = $1 AND url = 'http://host.docker.internal:9999/demo-hook')",
			orgID).Scan(&whExists); err != nil {
			return err
		}
		if !whExists {
			manager, err := secrets.NewManager(map[int]string{1: demoMasterKey})
			if err != nil {
				return err
			}
			envelope, err := manager.Encrypt([]byte("whsec_demo_local_signing_key_0000"))
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO webhooks
					(organization_id, url, description, events, secret_encrypted, secret_nonce,
					 key_version, status, created_by)
				VALUES ($1, 'http://host.docker.internal:9999/demo-hook',
				        'Demo receiver (disabled — no listener in the local demo)',
				        $2::text[], $3, $4, 1, 'disabled', $5)`,
				orgID,
				[]string{"execution.completed", "approval.requested"},
				envelope[13:], envelope[1:13], userIDs["developer"]); err != nil {
				return err
			}
		}
		var mcpExists bool
		if err := tx.QueryRow(ctx,
			"SELECT EXISTS (SELECT 1 FROM mcp_connections WHERE organization_id = $1 AND name = 'acme-demo-mcp')",
			orgID).Scan(&mcpExists); err != nil {
			return err
		}
		if !mcpExists {
			if _, err := tx.Exec(ctx, `
				INSERT INTO mcp_connections (organization_id, name, transport, url, status, health_status)
				VALUES ($1, 'acme-demo-mcp', 'streamable_http',
				        'http://host.docker.internal:9910/mcp', 'disabled', 'unknown')`, orgID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("developer platform: %w", err)
	}

	// ---- Audit record for the seed itself ------------------------------------
	err = act(func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (organization_id, actor_type, actor_id, action, result)
			VALUES ($1, 'system', NULL, 'demo.seeded', 'success')`, orgID)
		return err
	})
	if err != nil {
		return err
	}

	fmt.Println("seed complete:")
	fmt.Println("  organization : Acme Technologies")
	fmt.Println("  users        : owner@acme.test, admin@acme.test, developer@acme.test, operator@acme.test, auditor@acme.test")
	fmt.Println("  password     : " + demoPassword + "  (local demo only)")
	fmt.Println("  api key      : " + demoAPIKey)
	return nil
}

func errorsIsNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}

// demoPolicyContent requires approval before any email draft leaves the
// building (demo policy; unioned with the platform baseline in OPA).
const demoPolicyContent = "package agentos.authz\n" +
	"# Demo policy: require approval before any email draft goes out.\n" +
	"default allow := false\n" +
	"allow if {\n" +
	"\tinput.action == \"tool.execute\"\n" +
	"\tinput.tool.name != \"email.draft\"\n" +
	"}\n" +
	"allow if {\n" +
	"\tinput.action == \"tool.execute\"\n" +
	"\tinput.tool.name == \"email.draft\"\n" +
	"\tinput.approval.state == \"approved\"\n" +
	"}\n"

var _ = time.Now
