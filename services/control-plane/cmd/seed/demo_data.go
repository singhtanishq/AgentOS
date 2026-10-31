// Demo execution data: deterministic, clearly-demo rows with events,
// spans and usage records so dashboards show realistic history.
package main

import (
	"context"
	"encoding/json"
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

// seedExecutions inserts three executions per interesting state with
// full event streams and span timelines.
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
	if existing > 0 {
		return nil // already seeded
	}

	specs := []seedExec{
		{agent: "Research Assistant", status: "succeeded", query: "Summarize the Q3 quarterly report and compute the growth rate.", output: "1. Analysis of: Summarize the Q3 quarterly report\n2. Tool results incorporated: {\"expression\": \"(1.14 - 1) * 100\", \"result\": 14}\n3. Key considerations identified from the available context.\n4. Recommended action plan (reference 4f2a91bc).", steps: 3, tools: 1, prompt: 182, compl: 96, cost: 4},
		{agent: "Research Assistant", status: "succeeded", query: "What sources are approved for research tasks?", output: "1. Analysis of: What sources are approved for research tasks?\n2. Key considerations identified from the available context.\n3. Recommended action plan (reference 91bc4f2a).", steps: 1, tools: 0, prompt: 121, compl: 64, cost: 2},
		{agent: "Research Assistant", status: "failed", query: "Look up customer Emberly Analytics", errCode: "tool_unavailable", output: "", steps: 1, tools: 0, prompt: 0, compl: 0, cost: 0},
		{agent: "Support Assistant", status: "succeeded", query: "Look up the contact for Bluepeak Systems and draft a follow-up.", output: "1. Analysis of: Look up the contact for Bluepeak Systems and draft a follow-up.\n2. Key considerations identified from the available context.\n3. Recommended action plan (reference 77aa23ff).", steps: 2, tools: 1, prompt: 204, compl: 88, cost: 3},
		{agent: "Support Assistant", status: "waiting_approval", query: "Draft a refund promise email to the customer.", errCode: "", output: "", steps: 1, tools: 0, prompt: 44, compl: 0, cost: 0},
		{agent: "Support Assistant", status: "cancelled", query: "Send the Q4 survey to all enterprise contacts.", errCode: "", output: "", steps: 1, tools: 0, prompt: 12, compl: 0, cost: 0},
		{agent: "Research Assistant", status: "failed", query: "Compute 2 ** 999999 without the calculator", errCode: "step_limit_exceeded", output: "", steps: 10, tools: 4, prompt: 240, compl: 120, cost: 1},
		{agent: "Research Assistant", status: "timed_out", query: "Explore every document exhaustively.", errCode: "execution_timeout", output: "", steps: 4, tools: 2, prompt: 150, compl: 80, cost: 1},
	}

	for i, spec := range specs {
		spec := spec
		err := act(func(tx pgx.Tx) error {
			ctx := context.Background()
			executionID := fmt.Sprintf("d1000000-0000-4000-8000-0000000000%02d", i+1)
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
			var completedAt any
			if terminal {
				completedAt = "now() - interval '1 hour'"
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO executions
					(id, organization_id, agent_id, agent_version_id, deployment_id, status,
					 input, output, error, worker_id, step_count, tool_call_count,
					 prompt_tokens, completion_tokens, total_tokens, cost_estimate_minor,
					 pricing_version, started_at, completed_at, created_at)
				VALUES ($1, $2, $3, $4,
				        (SELECT id FROM deployments WHERE agent_id = $3 AND environment = 'production' AND status = 'active'),
				        $5, $6::jsonb, $7::jsonb, $8::jsonb, 'demo-worker', $9, $10, $11, $12, $13, $14, 1,
				        now() - interval '2 hours', $15, now() - interval '2 hours')`,
				executionID, orgID, agentIDs[spec.agent], versionIDs[spec.agent],
				spec.status,
				fmt.Sprintf(`{"query": %q}`, spec.query), output, errJSON,
				spec.steps, spec.tools, spec.prompt, spec.compl, spec.prompt+spec.compl, spec.cost,
				completedAt,
			); err != nil {
				return err
			}

			// Events
			type event struct{ seq int; typ, payload string }
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
			events = append(events, event{seq, map[bool]string{true: "execution.completed", false: "execution.failed"}[spec.status == "succeeded"],
				fmt.Sprintf(`{"status": %q, "steps": %d, "tool_calls": %d, "total_tokens": %d}`,
					spec.status, spec.steps, spec.tools, spec.prompt+spec.compl)})
			for _, e := range events {
				if _, err := tx.Exec(ctx, `
					INSERT INTO execution_events (execution_id, organization_id, seq, type, payload)
					VALUES ($1, $2, $3, $4, $5::jsonb)`,
					executionID, orgID, e.seq, e.typ, e.payload); err != nil {
					return err
				}
			}

			// Trace + spans
			var traceID string
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
			var rootSpan string
			if err := tx.QueryRow(ctx, `
				INSERT INTO spans (trace_id, organization_id, execution_id, name, kind, status, started_at, ended_at, duration_ms, attributes)
				VALUES ($1, $2, $3, 'invoke_agent', 'internal', 'ok', now() - interval '2 hours', now() - interval '1 hour', 4200, '{}')
				RETURNING id`, traceID, orgID, executionID).Scan(&rootSpan); err != nil {
				return err
			}
			if spec.tools > 0 {
				var modelSpan, toolSpan string
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
				_ = toolSpan
			}

			// Usage
			if spec.status == "succeeded" {
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
			return nil
		})
		if err != nil {
			return fmt.Errorf("seed execution %d: %w", i+1, err)
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
		if err := tx.QueryRow(ctx, `
			SELECT id FROM executions WHERE organization_id = $1 AND status = 'waiting_approval' LIMIT 1`,
			orgID).Scan(&waitingExec); err != nil {
			if errorsIsNoRowsSeed(err) {
				return nil // no waiting execution — skip approval seeding
			}
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
		if err := tx.QueryRow(ctx, `
			SELECT id, agent_id FROM executions
			WHERE organization_id = $1 AND status = 'succeeded' AND agent_id = $2 LIMIT 1`,
			orgID, agentIDs["Research Assistant"]).Scan(&doneExec, &doneAgent); err != nil {
			if errorsIsNoRowsSeed(err) {
				return nil
			}
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

func errorsIsNoRowsSeed(err error) bool {
	return err.Error() == "no rows in result set"
}
