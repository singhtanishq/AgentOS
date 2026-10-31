-- AgentOS 000013 — execution plane approval requests.
-- The runtime creates approval requests when policy requires human
-- authorization; the control plane decides them.

GRANT INSERT ON approvals TO agentos_worker;
