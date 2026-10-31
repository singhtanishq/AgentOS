"use client";

// Execution detail: summary, lifecycle events and the span timeline.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "next/navigation";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  ErrorState, LoadingState, EmptyState,
} from "@/components/ui/table";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatCost, formatDateTime, formatDuration, formatNumber } from "@/lib/format";

interface ExecutionDetail {
  id: string; agent_id: string; agent_version_id: string; status: string;
  input: { query?: string }; output?: { content?: string };
  error?: { code?: string; message?: string };
  step_count: number; tool_call_count: number;
  prompt_tokens: number; completion_tokens: number; total_tokens: number;
  cost_estimate_minor: number;
  started_at?: string; completed_at?: string; created_at: string;
}

interface EventRow {
  id: string; seq: number; type: string; payload: Record<string, unknown>; created_at: string;
}

interface SpanRow {
  id: string; name: string; status: string; duration_ms?: number;
  started_at: string; attributes: Record<string, unknown>;
}

export default function ExecutionDetailPage() {
  const { executionId } = useParams<{ executionId: string }>();
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [actionError, setActionError] = useState<string | null>(null);

  const execution = useQuery({
    queryKey: ["execution", executionId],
    queryFn: () => api<ExecutionDetail>(path("executions", executionId)),
    refetchInterval: (query) =>
      query.state.data && !TERMINAL.has(query.state.data.status) ? 3000 : false,
  });
  const events = useQuery({
    queryKey: ["execution-events", executionId],
    queryFn: () => api<{ items: EventRow[] }>(path("executions", executionId, "events")),
    enabled: !!execution.data,
  });

  const cancel = useMutation({
    mutationFn: () => api(path("executions", executionId, "cancel"), { method: "POST" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["execution", executionId] }),
    onError: (err) => setActionError(err instanceof ApiError ? err.message : "Cancel failed"),
  });

  const duration = (() => {
    const data = execution.data;
    if (!data?.started_at || !data?.completed_at) return null;
    const ms = new Date(data.completed_at).getTime() - new Date(data.started_at).getTime();
    return Number.isFinite(ms) && ms >= 0 ? ms : null;
  })();

  return (
    <AppShell>
      <PageHeader
        title={`Execution ${executionId.slice(0, 8)}…`}
        description={execution.data ? `Agent ${execution.data.agent_id.slice(0, 8)}…` : undefined}
        actions={
          execution.data && !TERMINAL.has(execution.data.status) ? (
            <Button
              variant="destructive"
              onClick={() => cancel.mutate()}
              disabled={cancel.isPending}
            >
              {cancel.isPending ? "Cancelling…" : "Cancel"}
            </Button>
          ) : null
        }
      />
      <PageBody className="space-y-6">
        {actionError ? <ErrorState message={actionError} /> : null}

        {execution.isError ? (
          <ErrorState message="Execution not found." />
        ) : execution.isLoading || !execution.data ? (
          <LoadingState />
        ) : (
          <>
            <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
              <Card>
                <CardHeader><CardDescription>Status</CardDescription></CardHeader>
                <CardContent><StatusBadge status={execution.data.status} /></CardContent>
              </Card>
              <Card>
                <CardHeader><CardDescription>Duration</CardDescription></CardHeader>
                <CardContent className="text-lg tabular">{formatDuration(duration)}</CardContent>
              </Card>
              <Card>
                <CardHeader><CardDescription>Tokens</CardDescription></CardHeader>
                <CardContent className="text-lg tabular">
                  {formatNumber(execution.data.total_tokens)}
                  <span className="ml-1 text-xs text-muted-foreground">
                    ({formatNumber(execution.data.prompt_tokens)} in / {formatNumber(execution.data.completion_tokens)} out)
                  </span>
                </CardContent>
              </Card>
              <Card>
                <CardHeader><CardDescription>Estimated cost</CardDescription></CardHeader>
                <CardContent className="text-lg tabular">
                  {formatCost(execution.data.cost_estimate_minor)}
                </CardContent>
              </Card>
            </div>

            <Card>
              <CardHeader>
                <CardTitle>Input / output</CardTitle>
              </CardHeader>
              <CardContent className="space-y-3 text-sm">
                <div>
                  <p className="text-xs text-muted-foreground">Input</p>
                  <pre className="whitespace-pre-wrap rounded-md border bg-muted/40 p-3 text-xs">
                    {JSON.stringify(execution.data.input, null, 2)}
                  </pre>
                </div>
                {execution.data.output ? (
                  <div>
                    <p className="text-xs text-muted-foreground">Output</p>
                    <pre className="whitespace-pre-wrap rounded-md border bg-muted/40 p-3 text-xs">
                      {execution.data.output.content ?? JSON.stringify(execution.data.output, null, 2)}
                    </pre>
                  </div>
                ) : null}
                {execution.data.error ? (
                  <div>
                    <p className="text-xs text-muted-foreground">Error</p>
                    <pre className="whitespace-pre-wrap rounded-md border border-destructive/40 bg-destructive/5 p-3 text-xs text-destructive">
                      {execution.data.error.code}: {execution.data.error.message}
                    </pre>
                  </div>
                ) : null}
              </CardContent>
            </Card>

            <Timeline executionId={executionId} events={events.data?.items ?? []} loading={events.isLoading} />
          </>
        )}
      </PageBody>
    </AppShell>
  );
}

const TERMINAL = new Set(["succeeded", "failed", "cancelled", "timed_out"]);

function Timeline({
  executionId, events, loading,
}: {
  executionId: string;
  events: EventRow[];
  loading: boolean;
}) {
  const { path } = useOrg();
  const spans = useQuery({
    queryKey: ["trace-by-execution", executionId],
    queryFn: async () => {
      const traces = await api<{ items: { id: string }[] }>(path("traces"));
      // Traces are keyed by execution; find the trace whose execution matches.
      for (const trace of traces.items.slice(0, 20)) {
        const detail = await api<{ trace: { execution_id: string }; spans: SpanRow[] }>(
          path("traces", trace.id),
        );
        if (detail.trace.execution_id === executionId) {
          return detail;
        }
      }
      return { trace: { execution_id: executionId }, spans: [] as SpanRow[] };
    },
    enabled: !!events.length,
  });

  return (
    <div className="grid gap-6 xl:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Execution events</CardTitle>
          <CardDescription>Structured lifecycle stream</CardDescription>
        </CardHeader>
        <CardContent className="px-0 pb-0">
          {loading ? (
            <LoadingState />
          ) : events.length === 0 ? (
            <div className="px-4 pb-4"><EmptyState title="No events recorded" /></div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>#</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Time</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {events.map((event) => (
                  <TableRow key={event.id}>
                    <TableCell className="tabular text-muted-foreground">{event.seq}</TableCell>
                    <TableCell className="font-mono text-xs">{event.type}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(event.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Span timeline</CardTitle>
          <CardDescription>OpenTelemetry spans recorded for this execution</CardDescription>
        </CardHeader>
        <CardContent className="px-0 pb-0">
          {spans.isLoading ? (
            <LoadingState />
          ) : (spans.data?.spans ?? []).length === 0 ? (
            <div className="px-4 pb-4"><EmptyState title="No spans recorded" /></div>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Span</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Duration</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(spans.data?.spans ?? []).map((span) => (
                  <TableRow key={span.id}>
                    <TableCell className="font-mono text-xs">
                      {span.name}
                      {span.attributes?.tool ? (
                        <span className="ml-2 text-muted-foreground">
                          {String(span.attributes.tool)}
                        </span>
                      ) : null}
                    </TableCell>
                    <TableCell><StatusBadge status={span.status === "ok" ? "succeeded" : span.status} /></TableCell>
                    <TableCell className="text-right tabular">{formatDuration(span.duration_ms)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
