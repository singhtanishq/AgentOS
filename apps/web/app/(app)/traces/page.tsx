"use client";

// Trace explorer: recent traces and drill-down span timeline.

import { useQuery } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { useOrg } from "@/hooks/use-org";
import { api } from "@/lib/api";
import { formatDateTime, formatDuration } from "@/lib/format";

interface TraceSummary {
  id: string; execution_id: string; agent_id: string; status: string;
  duration_ms?: number; started_at: string; ended_at?: string;
}
interface SpanRow {
  id: string; name: string; status: string; duration_ms?: number;
  started_at: string; attributes: Record<string, unknown>;
}

export default function TracesPage() {
  const { path } = useOrg();
  const [selected, setSelected] = useState<string | null>(null);

  const traces = useQuery({
    queryKey: ["traces"],
    queryFn: () => api<{ items: TraceSummary[] }>(path("traces")),
    refetchInterval: 10_000,
  });
  const detail = useQuery({
    queryKey: ["trace", selected],
    queryFn: () => api<{ trace: TraceSummary; spans: SpanRow[] }>(path("traces", selected!)),
    enabled: !!selected,
  });

  // Build a simple Gantt-style layout over relative offsets.
  const spans = detail.data?.spans ?? [];
  const traceStart = spans.length
    ? Math.min(...spans.map((s) => new Date(s.started_at).getTime()))
    : 0;
  const totalMs = detail.data?.trace.duration_ms ?? spans.reduce((max, s) => Math.max(max, s.duration_ms ?? 0), 0);

  return (
    <AppShell>
      <PageHeader
        title="Traces"
        description="Execution timelines with model, tool and governance spans"
      />
      <PageBody className="space-y-6">
        {traces.isError ? (
          <ErrorState message="Failed to load traces." />
        ) : traces.isLoading ? (
          <LoadingState />
        ) : (traces.data?.items ?? []).length === 0 ? (
          <EmptyState title="No traces yet" description="Traces appear after the first execution." />
        ) : (
          <div className="grid gap-6 xl:grid-cols-5">
            <div className="rounded-lg border xl:col-span-2">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Trace</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="text-right">Duration</TableHead>
                    <TableHead>Started</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(traces.data?.items ?? []).map((trace) => (
                    <TableRow
                      key={trace.id}
                      className={selected === trace.id ? "bg-accent" : ""}
                      onClick={() => setSelected(trace.id)}
                    >
                      <TableCell>
                        <Link
                          href={`/executions/${trace.execution_id}`}
                          className="font-mono text-xs hover:underline"
                          onClick={(e) => e.stopPropagation()}
                        >
                          {trace.execution_id.slice(0, 8)}…
                        </Link>
                      </TableCell>
                      <TableCell><StatusBadge status={trace.status === "ok" ? "succeeded" : trace.status} /></TableCell>
                      <TableCell className="text-right tabular">{formatDuration(trace.duration_ms)}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {formatDateTime(trace.started_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>

            <Card className="xl:col-span-3">
              <CardHeader>
                <CardTitle>Span timeline</CardTitle>
                <CardDescription>
                  {selected
                    ? `Trace ${selected.slice(0, 8)}… · total ${formatDuration(totalMs || null)}`
                    : "Select a trace to inspect its spans"}
                </CardDescription>
              </CardHeader>
              <CardContent className="space-y-1.5">
                {!selected || spans.length === 0 ? (
                  <EmptyState title="No spans to display" />
                ) : (
                  spans.map((span) => {
                    const offset = new Date(span.started_at).getTime() - traceStart;
                    const leftPct = totalMs > 0 ? Math.min(100, (offset / totalMs) * 100) : 0;
                    const widthPct = totalMs > 0 && span.duration_ms
                      ? Math.max(1.5, Math.min(100, (span.duration_ms / totalMs) * 100))
                      : 1.5;
                    return (
                      <div key={span.id} className="grid grid-cols-[140px_1fr_70px] items-center gap-2 text-xs">
                        <span className="truncate font-mono">{span.name}</span>
                        <div className="relative h-5 rounded bg-muted/40">
                          <div
                            className={
                              "absolute h-5 rounded " +
                              (span.status === "error"
                                ? "bg-red-500/70"
                                : span.name.startsWith("tool")
                                  ? "bg-amber-500/70"
                                  : span.name.startsWith("model")
                                    ? "bg-sky-500/70"
                                    : "bg-primary/70")
                            }
                            style={{ left: `${leftPct}%`, width: `${widthPct}%` }}
                            title={`${span.name} · ${formatDuration(span.duration_ms)}`}
                          />
                        </div>
                        <span className="text-right tabular text-muted-foreground">
                          {formatDuration(span.duration_ms)}
                        </span>
                      </div>
                    );
                  })
                )}
              </CardContent>
            </Card>
          </div>
        )}
      </PageBody>
    </AppShell>
  );
}
