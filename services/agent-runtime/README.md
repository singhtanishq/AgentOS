# AgentOS Agent Runtime

The Python execution plane for AgentOS. This service consumes durable
execution jobs from NATS JetStream, runs the agent loop (model invocation,
tool calls, authorization, validation), records usage and telemetry, and
persists execution results through the control plane.

See the repository root documentation for setup and usage.
