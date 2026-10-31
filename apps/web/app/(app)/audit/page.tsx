"use client";

// Audit log: append-only record of sensitive operations.

import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { useOrg } from "@/hooks/use-org";
import { api } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface AuditRow {
  id: string; actor_type: string; actor_id?: string; action: string;
  resource_type: string; resource_id: string; result: string;
  request_id: string; created_at: string;
}

export default function AuditPage() {
  // The audit endpoint is served from the same organization surface; the
  // backend currently exposes it via the policies/audit read path.
  const { path } = useOrg();
  const [actionFilter, setActionFilter] = useState("");

  // NOTE: audit listing is exposed via the same /audit-logs route family.
  const audit = useQuery({
    queryKey: ["audit", actionFilter],
    queryFn: () =>
      api<{ items: AuditRow[] }>(path("audit-logs"), {
        params: { action: actionFilter || undefined, limit: 100 },
      }),
    refetchInterval: 15_000,
  });

  return (
    <AppShell>
      <PageHeader
        title="Audit Logs"
        description="Append-only record of security-relevant operations"
      />
      <PageBody className="space-y-4">
        <input
          className="flex h-9 w-full max-w-xs rounded-md border border-input bg-transparent px-3 py-1 text-sm placeholder:text-muted-foreground"
          placeholder="Filter by action…"
          value={actionFilter}
          onChange={(e) => setActionFilter(e.target.value)}
        />
        {audit.isError ? (
          <ErrorState message="Failed to load audit logs." />
        ) : audit.isLoading ? (
          <LoadingState />
        ) : (audit.data?.items ?? []).length === 0 ? (
          <EmptyState title="No audit entries" description="Sensitive operations will appear here." />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Time</TableHead>
                  <TableHead>Actor</TableHead>
                  <TableHead>Action</TableHead>
                  <TableHead>Resource</TableHead>
                  <TableHead>Result</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(audit.data?.items ?? []).map((entry) => (
                  <TableRow key={entry.id}>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(entry.created_at)}
                    </TableCell>
                    <TableCell className="text-xs">
                      {entry.actor_type}
                      {entry.actor_id ? (
                        <span className="ml-1 font-mono text-muted-foreground">
                          {entry.actor_id.slice(0, 8)}…
                        </span>
                      ) : null}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{entry.action}</TableCell>
                    <TableCell className="text-xs">
                      {entry.resource_type}
                      {entry.resource_id ? (
                        <span className="ml-1 font-mono text-muted-foreground">
                          {entry.resource_id.slice(0, 8)}…
                        </span>
                      ) : null}
                    </TableCell>
                    <TableCell className="text-xs">{entry.result}</TableCell>
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
