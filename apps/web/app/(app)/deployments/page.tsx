"use client";

// Deployment management: environments, history and rollback.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Select } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface DeploymentRow {
  id: string; agent_id: string; agent_version_id: string; version: number;
  environment: string; status: string; created_by_email?: string;
  deactivated_at?: string; created_at: string;
}

export default function DeploymentsPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [environment, setEnvironment] = useState("");
  const [error, setError] = useState<string | null>(null);

  const deployments = useQuery({
    queryKey: ["deployments", environment],
    queryFn: () => api<{ items: DeploymentRow[] }>(path("deployments"), { params: { environment } }),
    refetchInterval: 10_000,
  });

  const rollback = useMutation({
    mutationFn: (id: string) => api(path("deployments", id, "rollback"), { method: "POST" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["deployments"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Rollback failed"),
  });

  const deactivate = useMutation({
    mutationFn: (id: string) => api(path("deployments", id, "deactivate"), { method: "POST" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["deployments"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Deactivate failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Deployments"
        description="Published agent versions per environment with rollback"
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        <Select className="max-w-44" value={environment} onChange={(e) => setEnvironment(e.target.value)}>
          <option value="">All environments</option>
          <option value="production">Production</option>
          <option value="staging">Staging</option>
          <option value="development">Development</option>
        </Select>

        {deployments.isError ? (
          <ErrorState message="Failed to load deployments." />
        ) : deployments.isLoading ? (
          <LoadingState />
        ) : (deployments.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No deployments"
            description="Deploy a published version from an agent's detail page."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Agent</TableHead>
                  <TableHead>Version</TableHead>
                  <TableHead>Environment</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>By</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(deployments.data?.items ?? []).map((deployment) => (
                  <TableRow key={deployment.id}>
                    <TableCell className="font-mono text-xs">
                      <a href={`/agents/${deployment.agent_id}`} className="hover:underline">
                        {deployment.agent_id.slice(0, 8)}…
                      </a>
                    </TableCell>
                    <TableCell className="tabular">v{deployment.version}</TableCell>
                    <TableCell>{deployment.environment}</TableCell>
                    <TableCell><StatusBadge status={deployment.status} /></TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {deployment.created_by_email || "—"}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(deployment.created_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      {deployment.status === "active" ? (
                        <>
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() => rollback.mutate(deployment.id)}
                          >
                            Rollback
                          </Button>{" "}
                          <Button
                            size="sm"
                            variant="destructive"
                            onClick={() => deactivate.mutate(deployment.id)}
                          >
                            Deactivate
                          </Button>
                        </>
                      ) : "—"}
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
