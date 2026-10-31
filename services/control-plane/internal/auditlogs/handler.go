// Package auditlogs exposes the append-only audit trail to authorized
// readers (auditors and above).
package auditlogs

import (
	"log/slog"
	"net/http"

	"github.com/singhtanishq/AgentOS/services/control-plane/internal/auth"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/httpx"
	"github.com/singhtanishq/AgentOS/services/control-plane/internal/store"
)

// Handler implements audit log endpoints.
type Handler struct {
	Store  *store.Store
	Logger *slog.Logger
}

type auditResponse struct {
	ID           string `json:"id"`
	ActorType    string `json:"actor_type"`
	ActorID      string `json:"actor_id,omitempty"`
	Action       string `json:"action"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Result       string `json:"result"`
	RequestID    string `json:"request_id,omitempty"`
	ExecutionID  string `json:"execution_id,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// List returns recent audit entries (newest first).
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	orgID := auth.OrgID(r.Context())
	action := r.URL.Query().Get("action")

	where := " WHERE organization_id = $1"
	args := []any{orgID}
	if action != "" {
		args = append(args, action+"%")
		where += " AND action LIKE $" + itoa(len(args))
	}

	var items []auditResponse
	err := h.Store.OrgTx(r.Context(), orgID, auth.UserID(r.Context()), func(tx store.Tx) error {
		rows, err := tx.Query(r.Context(), `
			SELECT id, actor_type, COALESCE(actor_id::text, ''), action,
			       resource_type, resource_id, result,
			       COALESCE(request_id, ''), COALESCE(execution_id::text, ''), created_at::text
			FROM audit_logs`+where+`
			ORDER BY created_at DESC LIMIT 200`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		items = []auditResponse{}
		for rows.Next() {
			var a auditResponse
			if err := rows.Scan(&a.ID, &a.ActorType, &a.ActorID, &a.Action,
				&a.ResourceType, &a.ResourceID, &a.Result, &a.RequestID,
				&a.ExecutionID, &a.CreatedAt); err != nil {
				return err
			}
			items = append(items, a)
		}
		return rows.Err()
	})
	if err != nil {
		h.Logger.Error("audit list failed", slog.Any("error", err))
		httpx.Error(w, r, http.StatusInternalServerError, "internal_error", "failed to load audit log")
		return
	}
	httpx.JSON(w, r, http.StatusOK, map[string]any{"items": items})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
