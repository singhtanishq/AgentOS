package agentos.authz

# AgentOS baseline authorization policy (single source of truth).
# Inputs:
#   action          e.g. "tool.execute"
#   organization_id
#   agent           {id, status}
#   tool            {name, kind, required_role, status, approval_required}
#   identity        {status} (optional)
#   approval        {state: "none"|"pending"|"approved"}

default allow := false

default requires_approval := false

# Tools execute only for active agents on active-registry bindings.
allow if {
	input.action == "tool.execute"
	input.agent.status == "active"
	input.tool.status == "active"
	input.tool.required_role != "admin"
	not input.tool.approval_required
}

# Approval-configured tools execute once an approver has approved them
# for this execution.
allow if {
	input.action == "tool.execute"
	input.agent.status == "active"
	input.tool.status == "active"
	input.tool.approval_required
	input.approval.state == "approved"
}

# Admin-gated and approval-configured tools require human approval.
requires_approval if {
	input.action == "tool.execute"
	input.tool.approval_required
	input.approval.state != "approved"
}

requires_approval if {
	input.action == "tool.execute"
	input.tool.required_role == "admin"
	input.approval.state != "approved"
}

# Suspended or revoked identities block every action.
allow if {
	input.identity.status != ""
	input.identity.status == "active"
}
