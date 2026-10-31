// Demo execution data: deterministic, clearly-demo rows with events,
// spans and usage records so dashboards show realistic history.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type seedExec struct {
	agent   string
	status  string
	query   string
	output  string
	errCode string
	steps   int
	tools   int
	prompt  int64
	compl   int64
	cost    int64
}

// seedExecutions inserts one execution per interesting state with full
// event streams and span timelines. Idempotent: rows are upserted by
// fixed ids, and telemetry is only written when absent.
func seedExecutions(
	act func(func(pgx.Tx) error) error,
	orgID string,
	agentIDs map[string]string,
	versionIDs map[string]string,
) error {
	var existing int
	err := act(func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(),
			"SELECT COUNT(*) FROM executions WHERE organization_id = $1", orgID).Scan(&existing)
	})
	if err != nil {
		return err
	}
	if existing >= len(seedExecSpecs()) {
		return nil // already seeded
	}

	for i, spec := range seedExecSpecs() {
		spec := spec
		err := act(func(tx pgx.Tx) error {
			return upsertDemoExecution(tx, orgID, agentIDs, versionIDs, i+1, spec)
		})
		if err != nil {
			return fmt.Errorf("seed execution %d: %w", i+1, err)
		}
	}
	return nil
}

func seedExecSpecs() []seedExec {
	return []seedExec{
		{agent: "Research Assistant", status: "succeeded", query: "Summarize the Q3 quarterly report and compute the growth rate.", output: "1. Analysis of: Summarize the Q3 quarterly report\n2. Tool results incorporated: {\"expression\": \"(1.14 - 1) * 100\", \"result\": 14}\n3. Key considerations identified from the available context.\n4. Recommended action plan (reference 4f2a91bc).", steps: 3, tools: 1, prompt: 182, compl: 96, cost: 4},
		{agent: "Research Assistant", status: "succeeded", query: "What sources are approved for research tasks?", output: "1. Analysis of: What sources are approved for research tasks?\n2. Key considerations identified from the available context.\n3. Recommended action plan (reference 91bc4f2a).", steps: 1, tools: 0, prompt: 121, compl: 64, cost: 2},
		{agent: "Research Assistant", status: "failed", query: "Look up customer Emberly Analytics", errCode: "tool_unavailable", steps: 1, tools: 0},
		{agent: "Support Assistant", status: "succeeded", query: "Look up the contact for Bluepeak Systems and draft a follow-up.", output: "1. Analysis of: Look up the contact for Bluepeak Systems and draft a follow-up.\n2. Key considerations identified from the available context.\n3. Recommended action plan (reference 77aa23ff).", steps: 2, tools: 1, prompt: 204, compl: 88, cost: 3},
		{agent: "Support Assistant", status: "waiting_approval", query: "Draft a refund promise email to the customer.", steps: 1, tools: 0, prompt: 44},
		{agent: "Support Assistant", status: "cancelled", query: "Send the Q4 survey to all enterprise contacts.", steps: 1, tools: 0, prompt: 12},
		{agent: "Research Assistant", status: "failed", query: "Compute 2 ** 999999 without the calculator", errCode: "step_limit_exceeded", steps: 10, tools: 4, prompt: 240, compl: 120, cost: 1},
		{agent: "Research Assistant", status: "timed_out", query: "Explore every document exhaustively.", errCode: "execution_timeout", steps: 4, tools: 2, prompt: 150, compl: 80, cost: 1},
	}
}

func upsertDemoExecution(
	tx pgx.Tx,
	orgID string,
	agentIDs map[string]string,
	versionIDs map[string]string,
	index int,
	spec seedExec,
) error {
	ctx := context.Background()
	executionID := fmt.Sprintf("d1000000-0000-4000-8000-0000000000%02d", index)

	var errJSON any
	if spec.errCode != "" {
		b, _ := json.Marshal(map[string]string{
			"code": spec.errCode, "message": "demo failure", "classification": "terminal",
		})
		errJSON = string(b)
	}
	var output any
	if spec.output != "" {
		b, _ := json.Marshal(map[string]any{"content": spec.output, "steps": spec.steps})
		output = string(b)
	}
	terminal := spec.status == "succeeded" || spec.status == "failed" ||
		spec.status == "cancelled" || spec.status == "timed_out"
	completedSQL := "NULL"
	if terminal {
		completedSQL = "now() - interval '1 hour'"
	}
	// completed_at is a fixed SQL expression chosen by the seed (never input).
	query := `
		INSERT INTO executions
			(id, organization_id, agent_id, agent_version_id, deployment_id, status,
			 input, output, error, worker_id, step_count, tool_call_count,
			 prompt_tokens, completion_tokens, total_tokens, cost_estimate_minor,
			 pricing_version, started_at, completed_at, created_at)
		VALUES ($1, $2, $3, $4,
		        (SELECT id FROM deployments WHERE agent_id = $3 AND environment = 'production' AND status = 'active'),
		        $5, $6::jsonb, $7::jsonb, $8::jsonb, 'demo-worker', $9, $10, $11, $12, $13, $14, 1,
		        now() - interval '2 hours', ` + completedSQL + `, now() - interval '2 hours')
		ON CONFLICT (id) DO UPDATE SET
			status = EXCLUDED.status, output = EXCLUDED.output, error = EXCLUDED.error,
			step_count = EXCLUDED.step_count, tool_call_count = EXCLUDED.tool_call_count,
			prompt_tokens = EXCLUDED.prompt_tokens, completion_tokens = EXCLUDED.completion_tokens,
			total_tokens = EXCLUDED.total_tokens, cost_estimate_minor = EXCLUDED.cost_estimate_minor,
			completed_at = EXCLUDED.completed_at, updated_at = now()`
	if _, err := tx.Exec(ctx, query,
		executionID, orgID, agentIDs[spec.agent], versionIDs[spec.agent],
		spec.status,
		fmt.Sprintf(`{"query": %q}`, spec.query), output, errJSON,
		spec.steps, spec.tools, spec.prompt, spec.compl, spec.prompt+spec.compl, spec.cost,
	); err != nil {
		return err
	}

	// Events (upserted by (execution, seq))
	type event struct {
		seq          int
		typ, payload string
	}
	events := []event{
		{0, "execution.started", `{"worker_id": "demo-worker"}`},
	}
	seq := 1
	if spec.steps > 1 && spec.output != "" {
		events = append(events, event{seq, "model.response",
			`{"step": 1, "provider": "mock", "finish_reason": "tool_calls", "input_tokens": 91, "output_tokens": 18}`})
		seq++
		events = append(events, event{seq, "tool.requested",
			`{"tool": "calculator", "arguments": {"expression": "(1.14 - 1) * 100"}}`})
		seq++
		events = append(events, event{seq, "tool.completed",
			`{"tool": "calculator", "ok": true}`})
		seq++
	}
	if spec.status == "waiting_approval" {
		events = append(events, event{seq, "approval.requested",
			`{"tool": "email.draft"}`})
		seq++
	}
	events = append(events, event{seq,
		map[bool]string{true: "execution.completed", false: "execution.failed"}[spec.status == "succeeded"],
		fmt.Sprintf(`{"status": %q, "steps": %d, "tool_calls": %d, "total_tokens": %d}`,
			spec.status, spec.steps, spec.tools, spec.prompt+spec.compl)})
	for _, e := range events {
		if _, err := tx.Exec(ctx, `
			INSERT INTO execution_events (execution_id, organization_id, seq, type, payload)
			VALUES ($1, $2, $3, $4, $5::jsonb)
			ON CONFLICT (execution_id, seq) DO UPDATE SET type = EXCLUDED.type, payload = EXCLUDED.payload`,
			executionID, orgID, e.seq, e.typ, e.payload); err != nil {
			return err
		}
	}

	// Trace (reuse when present)
	var traceID string
	var traceCount int
	if err := tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM traces WHERE execution_id = $1", executionID).Scan(&traceCount); err != nil {
		return err
	}
	if traceCount > 0 {
		if err := tx.QueryRow(ctx,
			"SELECT id FROM traces WHERE execution_id = $1", executionID).Scan(&traceID); err != nil {
			return err
		}
	} else {
		if err := tx.QueryRow(ctx, `
			INSERT INTO traces (organization_id, execution_id, root_span_name, status, started_at, ended_at, duration_ms)
			VALUES ($1, $2, 'invoke_agent', $3, now() - interval '2 hours',
			        now() - interval '1 hour', 4200)
			RETURNING id`,
			orgID, executionID,
			map[bool]string{true: "ok", false: "error"}[spec.status == "succeeded"],
		).Scan(&traceID); err != nil {
			return err
		}
	}

	// Spans (only when the trace has none yet)
	var spanCount int
	if err := tx.QueryRow(ctx,
		"SELECT COUNT(*) FROM spans WHERE trace_id = $1", traceID).Scan(&spanCount); err != nil {
		return err
	}
	if spanCount == 0 {
		var rootSpan string
		if err := tx.QueryRow(ctx, `
			INSERT INTO spans (trace_id, organization_id, execution_id, name, kind, status, started_at, ended_at, duration_ms, attributes)
			VALUES ($1, $2, $3, 'invoke_agent', 'internal', 'ok', now() - interval '2 hours', now() - interval '1 hour', 4200, '{}')
			RETURNING id`, traceID, orgID, executionID).Scan(&rootSpan); err != nil {
			return err
		}
		if spec.tools > 0 {
			var modelSpan string
			if err := tx.QueryRow(ctx, `
				INSERT INTO spans (trace_id, parent_span_id, organization_id, execution_id, name, kind, status, started_at, ended_at, duration_ms, attributes)
				VALUES ($1, $2, $3, $4, 'model_request', 'client', 'ok', now() - interval '2 hours', now() - interval '2 hours' + interval '800 ms', 800, '{"model": "mock-mini", "step": 1}')
				RETURNING id`, traceID, rootSpan, orgID, executionID).Scan(&modelSpan); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO spans (trace_id, parent_span_id, organization_id, execution_id, name, kind, status, started_at, ended_at, duration_ms, attributes)
				VALUES ($1, $2, $3, $4, 'tool_execution', 'internal', 'ok', now() - interval '2 hours' + interval '900 ms', now() - interval '2 hours' + interval '950 ms', 50, '{"tool": "calculator"}')`,
				traceID, modelSpan, orgID, executionID); err != nil {
				return err
			}
		}
	}

	// Usage (append-only rows with random ids — guard on the execution ref)
	if spec.status == "succeeded" {
		var usageCount int
		if err := tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM usage_records WHERE execution_id = $1", executionID).Scan(&usageCount); err != nil {
			return err
		}
		if usageCount == 0 {
			if _, err := tx.Exec(ctx, `
				INSERT INTO usage_records
					(organization_id, execution_id, agent_id, provider, model,
					 input_tokens, output_tokens, total_tokens, tool_calls, duration_ms,
					 cost_estimate_minor, currency, pricing_version)
				VALUES ($1, $2, $3, 'mock', 'mock-mini', $4, $5, $6, $7, 4200, $8, 'USD', 1)`,
				orgID, executionID, agentIDs[spec.agent],
				spec.prompt, spec.compl, spec.prompt+spec.compl, spec.tools, spec.cost); err != nil {
				return err
			}
		}
	}
	return nil
}

// seedApprovalsAndAlerts inserts one pending approval tied to the waiting
// execution, one decided approval and two open alerts.
func seedApprovalsAndAlerts(
	act func(func(pgx.Tx) error) error,
	orgID string,
	agentIDs map[string]string,
) error {
	return act(func(tx pgx.Tx) error {
		ctx := context.Background()
		var approvalCount int
		if err := tx.QueryRow(ctx,
			"SELECT COUNT(*) FROM approvals WHERE organization_id = $1", orgID).Scan(&approvalCount); err != nil {
			return err
		}
		if approvalCount > 0 {
			return nil
		}
		var waitingExec string
		err := tx.QueryRow(ctx, `
			SELECT id FROM executions WHERE organization_id = $1 AND status = 'waiting_approval' LIMIT 1`,
			orgID).Scan(&waitingExec)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // no waiting execution — skip approval seeding
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO approvals
				(organization_id, execution_id, agent_id, action, resource, justification, status, assigned_role)
			VALUES ($1, $2, $3, 'tool.execute:email.draft',
			        '{"tool": "email.draft", "arguments": {"to": "customer@example.test", "subject": "Your refund", "body": "…"}}'::jsonb,
			        'requested automatically by policy during execution', 'pending', 'operator')`,
			orgID, waitingExec, agentIDs["Support Assistant"]); err != nil {
			return err
		}
		// A decided approval on a succeeded execution.
		var doneExec, doneAgent string
		err = tx.QueryRow(ctx, `
			SELECT id, agent_id FROM executions
			WHERE organization_id = $1 AND status = 'succeeded' AND agent_id = $2 LIMIT 1`,
			orgID, agentIDs["Research Assistant"]).Scan(&doneExec, &doneAgent)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO approvals
				(organization_id, execution_id, agent_id, action, resource, status, assigned_role,
				 decided_at, expires_at)
			VALUES ($1, $2, $3, 'tool.execute:calculator',
			        '{"tool": "calculator", "arguments": {"expression": "(1.14 - 1) * 100"}}'::jsonb,
			        'approved', 'operator', now() - interval '90 minutes', now() - interval '30 minutes')`,
			orgID, doneExec, doneAgent); err != nil {
			return err
		}
		// Alerts
		if _, err := tx.Exec(ctx, `
			INSERT INTO alerts (organization_id, type, severity, title, message, resource_type)
			VALUES
				($1, 'budget_threshold', 'warning', 'Budget ''org-monthly-spend'' at 52%',
				 'spend 260000 of 500000 minor units in the current period', 'budget'),
				($1, 'execution_failed', 'warning', 'Execution failed: tool unavailable',
				 'Research Assistant run could not resolve a bound tool', 'execution')`, orgID); err != nil {
			return err
		}
		return nil
	})
}
