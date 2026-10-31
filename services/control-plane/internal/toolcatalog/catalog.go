// Package toolcatalog defines the platform builtin tool catalog and seeds
// it into new organizations. Runtime implementations live in the Python
// execution plane; the catalog rows carry schemas and operational metadata.
package toolcatalog

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// Builtin is one platform-provided tool.
type Builtin struct {
	Name         string
	Kind         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	RequiredRole string
	TimeoutMs    int
	Config       json.RawMessage
	Status       string
}

// Catalog is the platform builtin set (Part 15). The HTTP tool ships
// disabled: operators enable it after reviewing its outbound allowlist.
var Catalog = []Builtin{
	{
		Name:         "calculator",
		Kind:         "builtin",
		Description:  "Evaluate arithmetic expressions safely.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"}},"required":["expression"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"},"result":{}},"required":["expression","result"]}`),
		RequiredRole: "developer",
		TimeoutMs:    5000,
		Config:       json.RawMessage(`{}`),
		Status:       "active",
	},
	{
		Name:         "web.search",
		Kind:         "builtin",
		Description:  "Search configured sources (deterministic demo fixtures in local mode).",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}},"required":["query"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"results":{"type":"array"}},"required":["results"]}`),
		RequiredRole: "developer",
		TimeoutMs:    10000,
		Config:       json.RawMessage(`{"mode":"demo"}`),
		Status:       "active",
	},
	{
		Name:         "document.read",
		Kind:         "builtin",
		Description:  "Read a permitted sample document by id.",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"document_id":{"type":"string"}},"required":["document_id"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"document_id":{"type":"string"},"title":{"type":"string"},"content":{"type":"string"}},"required":["document_id","title","content"]}`),
		RequiredRole: "developer",
		TimeoutMs:    5000,
		Config:       json.RawMessage(`{}`),
		Status:       "active",
	},
	{
		Name:         "database.query",
		Kind:         "database",
		Description:  "Query the read-only sample customer dataset (demo).",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"limit":{"type":"integer"}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"records":{"type":"array"}},"required":["records"]}`),
		RequiredRole: "operator",
		TimeoutMs:    10000,
		Config:       json.RawMessage(`{"dataset":"demo_customers"}`),
		Status:       "active",
	},
	{
		Name:         "crm.lookup",
		Kind:         "crm",
		Description:  "Look up a contact in the sample CRM (demo data).",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"email":{"type":"string"}},"required":["email"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object"}`),
		RequiredRole: "operator",
		TimeoutMs:    5000,
		Config:       json.RawMessage(`{"dataset":"demo_crm"}`),
		Status:       "active",
	},
	{
		Name:         "email.draft",
		Kind:         "email",
		Description:  "Draft an email (mock adapter — drafts are returned, never sent).",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"to":{"type":"string"},"subject":{"type":"string"},"body":{"type":"string"}},"required":["to","subject","body"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"draft_id":{"type":"string"},"status":{"type":"string"}},"required":["draft_id","status"]}`),
		RequiredRole: "operator",
		TimeoutMs:    5000,
		Config:       json.RawMessage(`{"mode":"mock"}`),
		Status:       "active",
	},
	{
		Name:         "http.request",
		Kind:         "http",
		Description:  "Perform an HTTP GET against an allowlisted host (disabled until configured).",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"],"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"status":{"type":"integer"},"body":{}},"required":["status"]}`),
		RequiredRole: "admin",
		TimeoutMs:    15000,
		Config:       json.RawMessage(`{"allowed_hosts":[],"max_response_bytes":65536}`),
		Status:       "disabled",
	},
}

// SeedForOrganization inserts the catalog rows for a new organization.
// Existing rows are left untouched (idempotent).
func SeedForOrganization(ctx context.Context, tx pgx.Tx, orgID string) error {
	for _, t := range Catalog {
		if _, err := tx.Exec(ctx, `
			INSERT INTO tools
				(organization_id, name, kind, description, input_schema, output_schema,
				 config, required_role, status, timeout_ms)
			VALUES ($1, $2, $3, $4, $5::jsonb, $6::jsonb, $7::jsonb, $8, $9, $10)
			ON CONFLICT (organization_id, name) DO NOTHING`,
			orgID, t.Name, t.Kind, t.Description, string(t.InputSchema),
			string(t.OutputSchema), string(t.Config), t.RequiredRole, t.Status, t.TimeoutMs,
		); err != nil {
			return err
		}
	}
	return nil
}
