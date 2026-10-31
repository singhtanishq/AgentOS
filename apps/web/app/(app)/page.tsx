"use client";

// Overview dashboard: live operational metrics from the control plane.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useOrg } from "@/hooks/use-org";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Badge, StatusBadge } from "@/components/ui/badge";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { api } from "@/lib/api";
import { formatCost, formatDateTime, formatDuration, formatNumber, formatPercent } from "@/lib/format";

interface AgentSummary {
  id: string; name: string; description: string; status: string;
  latest_version: number; version_count: number;
}
interface ExecutionSummary {
  id: string; agent_id: string; status: string;
  total_tokens: number; cost_estimate_minor: number;
  started_at?: string; completed_at?: string; created_at: string;
}

export default function OverviewPage() {
  const { path } = useOrg();

  const agents = useQuery({
    queryKey: ["agents", "overview"],
    queryFn: () => api<{ items: AgentSummary[] }>(path("agents")),
  });
  const executions = useQuery({
    queryKey: ["executions", "overview"],
    queryFn: () =>
      api<{ items: ExecutionSummary[]; total: number }>(path("executions") + "?limit=20"),
  });
  const deployments = useQuery({
    queryKey: ["deployments", "overview"],
    queryFn: () =>
      api<{ items: { id: string; agent_id: string; environment: string; status: string; version: number }[] }>(
        path("deployments"),
      ),
  });
  const alerts = useQuery({
    queryKey: ["alerts", "overview"],
    queryFn: () => api<{ items: { id: string; title: string; severity: string; created_at: string }[] }>(
      path("alerts"),
    ),
  });

  const agentItems = agents.data?.items ?? [];
  const execItems = executions.data?.items ?? [];
  const activeDeployments = (deployments.data?.items ?? []).filter((d) => d.status === "active");
  const succeeded = execItems.filter((e) => e.status === "succeeded").length;
  const failed = execItems.filter((e) => e.status === "failed" || e.status === "timed_out").length;
  const finished = execItems.filter((e) => e.status === "succeeded" || e.status === "failed" || e.status === "timed_out");
  const successRate = finished.length > 0 ? succeeded / finished.length : null;
  const totalTokens = execItems.reduce((sum, e) => sum + e.total_tokens, 0);
  const totalCost = execItems.reduce((sum, e) => sum + e.cost_estimate_minor, 0);
  const avgDuration = (() => {
    const durations = finished
      .map((e) => {
        if (!e.started_at || !e.completed_at) return null;
        const ms = new Date(e.completed_at).getTime() - new Date(e.started_at).getTime();
        return Number.isFinite(ms) && ms >= 0 ? ms : null;
      })
      .filter((v): v is number => v !== null);
    return durations.length ? Math.round(durations.reduce((a, b) => a + b, 0) / durations.length) : null;
  })();

  const metrics: { label: string; value: string; hint?: string }[] = [
    { label: "Agents", value: formatNumber(agentItems.length), hint: `${agentItems.filter((a) => a.status === "active").length} active` },
    { label: "Active deployments", value: formatNumber(activeDeployments.length), hint: "production + staging" },
    { label: "Executions (recent)", value: formatNumber(execItems.length), hint: `${formatNumber(executions.data?.total ?? 0)} total` },
    { label: "Success rate", value: formatPercent(successRate), hint: `${failed} failed` },
    { label: "Avg duration", value: formatDuration(avgDuration) },
    { label: "Model spend", value: formatCost(totalCost), hint: `${formatNumber(totalTokens)} tokens`, },
  ];

  return (
    <AppShell>
      <PageHeader
        title="Overview"
        description="Operational state of your agent fleet"
      />
      <PageBody className="space-y-6">
        {(agents.isError || executions.isError) ? (
          <ErrorState message="Failed to load dashboard data — is the control plane running?" />
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-3 xl:grid-cols-6">
              {metrics.map((metric) => (
                <Card key={metric.label}>
                  <CardHeader>
                    <CardDescription>{metric.label}</CardDescription>
                    <CardTitle className="text-xl tabular">{metric.value}</CardTitle>
                  </CardHeader>
                  {metric.hint ? (
                    <CardContent className="text-[10px] text-muted-foreground">{metric.hint}</CardContent>
                  ) : null}
                </Card>
              ))}
            </div>

            <div className="grid gap-6 xl:grid-cols-3">
              <Card className="xl:col-span-2">
                <CardHeader>
                  <CardTitle>Recent executions</CardTitle>
                  <CardDescription>Newest runs across all agents</CardDescription>
                </CardHeader>
                <CardContent className="px-0 pb-0">
                  {executions.isLoading ? (
                    <LoadingState />
                  ) : execItems.length === 0 ? (
                    <div className="px-4 pb-4">
                      <EmptyState
                        title="No executions yet"
                        description="Submit a run from an agent's detail page."
                      />
                    </div>
                  ) : (
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Status</TableHead>
                          <TableHead>Created</TableHead>
                          <TableHead className="text-right">Tokens</TableHead>
                          <TableHead className="text-right">Cost</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {execItems.slice(0, 10).map((execution) => (
                          <TableRow key={execution.id}>
                            <TableCell>
                              <Link href={`/executions/${execution.id}`} className="flex items-center gap-2 hover:underline">
                                <StatusBadge status={execution.status} />
                              </Link>
                            </TableCell>
                            <TableCell className="text-xs text-muted-foreground">
                              {formatDateTime(execution.created_at)}
                            </TableCell>
                            <TableCell className="text-right tabular">{formatNumber(execution.total_tokens)}</TableCell>
                            <TableCell className="text-right tabular">{formatCost(execution.cost_estimate_minor)}</TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  )}
                </CardContent>
              </Card>

              <div className="space-y-6">
                <Card>
                  <CardHeader>
                    <CardTitle>Top agents</CardTitle>
                    <CardDescription>Registry entries by recency</CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-2 px-4">
                    {agents.isLoading ? (
                      <LoadingState label="Loading agents…" />
                    ) : agentItems.length === 0 ? (
                      <EmptyState title="No agents registered" description="Create your first agent to get started." />
                    ) : (
                      agentItems.slice(0, 5).map((agent) => (
                        <Link
                          key={agent.id}
                          href={`/agents/${agent.id}`}
                          className="flex items-center justify-between rounded-md border px-3 py-2 text-sm hover:bg-accent"
                        >
                          <span className="truncate">{agent.name}</span>
                          <StatusBadge status={agent.status} />
                        </Link>
                      ))
                    )}
                  </CardContent>
                </Card>

                <Card>
                  <CardHeader>
                    <CardTitle>Open alerts</CardTitle>
                    <CardDescription>Unacknowledged operational alerts</CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-2 px-4">
                    {alerts.isLoading ? (
                      <LoadingState label="Loading alerts…" />
                    ) : (alerts.data?.items ?? []).length === 0 ? (
                      <p className="text-xs text-muted-foreground">No open alerts — all clear.</p>
                    ) : (
                      (alerts.data?.items ?? []).slice(0, 5).map((alert) => (
                        <div key={alert.id} className="flex items-center justify-between rounded-md border px-3 py-2">
                          <span className="truncate text-xs">{alert.title}</span>
                          <Badge tone={alert.severity === "critical" ? "danger" : "warning"}>
                            {alert.severity}
                          </Badge>
                        </div>
                      ))
                    )}
                  </CardContent>
                </Card>
              </div>
            </div>
          </>
        )}
      </PageBody>
    </AppShell>
  );
}
