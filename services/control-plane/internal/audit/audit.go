package audit

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
)

// Entry describes one auditable action.
type Entry struct {
	OrganizationID string
	ActorType      string // user | api_key | agent | system
	ActorID        string // optional
	Action         string // e.g. auth.login, member.role_changed
	ResourceType   string // optional
	ResourceID     string // optional
	Result         string // success | denied | error
	RequestID      string // optional
	ExecutionID    string // optional
	Metadata       map[string]any
	IPAddress      string // optional
}

// Append writes an audit entry inside the given transaction. The caller is
// responsible for the tenant context; audit rows are append-only by grants.
func Append(ctx context.Context, tx pgx.Tx, e Entry) error {
	meta := e.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	payload, err := json.Marshal(meta)
	if err != nil {
		payload = []byte("{}")
	}
	// Columns declared NOT NULL DEFAULT '' receive empty strings; only
	// genuinely nullable columns use pointers.
	var actorID, executionID, ip *string
	if e.ActorID != "" {
		v := e.ActorID
		actorID = &v
	}
	if e.ExecutionID != "" {
		v := e.ExecutionID
		executionID = &v
	}
	if e.IPAddress != "" {
		v := e.IPAddress
		ip = &v
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs
			(organization_id, actor_type, actor_id, action, resource_type, resource_id,
			 result, request_id, execution_id, metadata, ip_address)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb, $11)`,
		e.OrganizationID, e.ActorType, actorID, e.Action, e.ResourceType, e.ResourceID,
		e.Result, e.RequestID, executionID, string(payload), ip,
	)
	return err
}
