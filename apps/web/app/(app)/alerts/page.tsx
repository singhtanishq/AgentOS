"use client";

// Alerts inbox with acknowledgement.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface AlertRow {
  id: string; type: string; severity: string; title: string;
  message: string; created_at: string; acknowledged_at?: string;
}

export default function AlertsPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [openOnly, setOpenOnly] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const alerts = useQuery({
    queryKey: ["alerts", openOnly],
    queryFn: () => api<{ items: AlertRow[] }>(path("alerts"), { params: { open: openOnly ? "true" : "false" } }),
    refetchInterval: 15_000,
  });

  const ack = useMutation({
    mutationFn: (id: string) => api(path("alerts", id, "ack"), { method: "POST" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["alerts"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Ack failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Alerts"
        description="Budget thresholds, failures and operational events"
        actions={
          <Button variant="outline" onClick={() => setOpenOnly((v) => !v)}>
            {openOnly ? "Show all" : "Show open only"}
          </Button>
        }
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        {alerts.isError ? (
          <ErrorState message="Failed to load alerts." />
        ) : alerts.isLoading ? (
          <LoadingState />
        ) : (alerts.data?.items ?? []).length === 0 ? (
          <EmptyState title={openOnly ? "No open alerts" : "No alerts recorded"} />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Severity</TableHead>
                  <TableHead>Title</TableHead>
                  <TableHead>Type</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(alerts.data?.items ?? []).map((alert) => (
                  <TableRow key={alert.id}>
                    <TableCell>
                      <Badge tone={alert.severity === "critical" ? "danger" : alert.severity === "warning" ? "warning" : "info"}>
                        {alert.severity}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <p>{alert.title}</p>
                      <p className="text-xs text-muted-foreground">{alert.message}</p>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{alert.type}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(alert.created_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      {alert.acknowledged_at ? (
                        <span className="text-xs text-muted-foreground">
                          ack {formatDateTime(alert.acknowledged_at)}
                        </span>
                      ) : (
                        <Button size="sm" variant="secondary" onClick={() => ack.mutate(alert.id)}>
                          Acknowledge
                        </Button>
                      )}
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
