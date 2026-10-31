"use client";

// Execution explorer: filterable list of runs.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { StatusBadge } from "@/components/ui/badge";
import { Select } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { useOrg } from "@/hooks/use-org";
import { api } from "@/lib/api";
import { formatCost, formatDateTime, formatDuration, formatNumber } from "@/lib/format";

interface ExecutionRow {
  id: string; agent_id: string; status: string;
  step_count: number; tool_call_count: number;
  total_tokens: number; cost_estimate_minor: number;
  started_at?: string; completed_at?: string; created_at: string;
}

export default function ExecutionsPage() {
  const { path } = useOrg();
  const [status, setStatus] = useState("");
  const [limit, setLimit] = useState(50);

  const executions = useQuery({
    queryKey: ["executions", status, limit],
    queryFn: () =>
      api<{ items: ExecutionRow[]; total: number }>(path("executions"), {
        params: { status, limit },
      }),
    refetchInterval: 5000, // live view of in-flight runs
  });

  const durationOf = (execution: ExecutionRow) => {
    if (!execution.started_at || !execution.completed_at) return null;
    const ms = new Date(execution.completed_at).getTime() - new Date(execution.started_at).getTime();
    return Number.isFinite(ms) && ms >= 0 ? ms : null;
  };

  return (
    <AppShell>
      <PageHeader
        title="Executions"
        description="Agent runs across the organization (auto-refreshing)"
      />
      <PageBody className="space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <Select className="max-w-44" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">All statuses</option>
            <option value="queued">Queued</option>
            <option value="running">Running</option>
            <option value="waiting_approval">Waiting approval</option>
            <option value="succeeded">Succeeded</option>
            <option value="failed">Failed</option>
            <option value="cancelled">Cancelled</option>
            <option value="timed_out">Timed out</option>
          </Select>
          <Select
            className="max-w-32"
            value={String(limit)}
            onChange={(e) => setLimit(Number(e.target.value))}
          >
            <option value="20">20 rows</option>
            <option value="50">50 rows</option>
            <option value="100">100 rows</option>
          </Select>
        </div>

        {executions.isError ? (
          <ErrorState message="Failed to load executions." />
        ) : executions.isLoading ? (
          <LoadingState />
        ) : (executions.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No executions"
            description="Submit a run from an agent's detail page."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Execution</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Steps</TableHead>
                  <TableHead className="text-right">Tools</TableHead>
                  <TableHead className="text-right">Tokens</TableHead>
                  <TableHead className="text-right">Cost</TableHead>
                  <TableHead className="text-right">Duration</TableHead>
                  <TableHead>Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(executions.data?.items ?? []).map((execution) => (
                  <TableRow key={execution.id}>
                    <TableCell>
                      <Link
                        href={`/executions/${execution.id}`}
                        className="font-mono text-xs hover:underline"
                      >
                        {execution.id.slice(0, 8)}…
                      </Link>
                    </TableCell>
                    <TableCell><StatusBadge status={execution.status} /></TableCell>
                    <TableCell className="text-right tabular">{formatNumber(execution.step_count)}</TableCell>
                    <TableCell className="text-right tabular">{formatNumber(execution.tool_call_count)}</TableCell>
                    <TableCell className="text-right tabular">{formatNumber(execution.total_tokens)}</TableCell>
                    <TableCell className="text-right tabular">{formatCost(execution.cost_estimate_minor)}</TableCell>
                    <TableCell className="text-right tabular">{formatDuration(durationOf(execution))}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(execution.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PageBody>
    </AppShell>
  );
}
