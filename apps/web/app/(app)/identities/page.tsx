"use client";

// Agent identities: inventory, credentials metadata, lifecycle actions.

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

interface AgentSummary { id: string; name: string; status: string }
interface Identity {
  id: string; agent_id: string; name: string; status: string;
  credential_prefix: string; credential_expires_at?: string;
  last_used_at?: string; revoked_at?: string; created_at: string;
}

export default function IdentitiesPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [agentId, setAgentId] = useState("");
  const [created, setCreated] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const agents = useQuery({
    queryKey: ["agents", "identities"],
    queryFn: () => api<{ items: AgentSummary[] }>(path("agents")),
  });

  const selectedAgent = agentId || agents.data?.items[0]?.id || "";
  const identities = useQuery({
    queryKey: ["identities", selectedAgent],
    queryFn: () => api<{ items: Identity[] }>(path("agents", selectedAgent, "identities")),
    enabled: !!selectedAgent,
  });

  const issue = useMutation({
    mutationFn: () => api<{ credential: string }>(path("agents", selectedAgent, "identities"), {
      method: "POST",
      body: { name: `identity-${Date.now().toString(36)}` },
    }),
    onSuccess: (data) => {
      setCreated(data.credential);
      queryClient.invalidateQueries({ queryKey: ["identities", selectedAgent] });
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Issuance failed"),
  });

  const lifecycle = useMutation({
    mutationFn: ({ id, action }: { id: string; action: "suspend" | "revoke" }) =>
      api(path("agents", selectedAgent, "identities", id, action), { method: "POST" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["identities", selectedAgent] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Action failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Agent Identity"
        description="Per-agent credentials with independent lifecycles"
        actions={
          <Button onClick={() => issue.mutate()} disabled={!selectedAgent || issue.isPending}>
            {issue.isPending ? "Issuing…" : "Issue credential"}
          </Button>
        }
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        {created ? (
          <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs">
            New credential (shown once — copy it now):
            <span className="ml-1 font-mono">{created}</span>
            <button className="ml-2 text-primary underline" onClick={() => setCreated(null)}>dismiss</button>
          </div>
        ) : null}

        <Select className="max-w-64" value={selectedAgent} onChange={(e) => setAgentId(e.target.value)}>
          {(agents.data?.items ?? []).map((agent) => (
            <option key={agent.id} value={agent.id}>{agent.name}</option>
          ))}
        </Select>

        {identities.isError ? (
          <ErrorState message="Failed to load identities." />
        ) : identities.isLoading || !selectedAgent ? (
          <LoadingState />
        ) : (identities.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No identities for this agent"
            description="Issue a credential to give the agent its own verifiable identity."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Credential</TableHead>
                  <TableHead>Expires</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(identities.data?.items ?? []).map((identity) => (
                  <TableRow key={identity.id}>
                    <TableCell>{identity.name}</TableCell>
                    <TableCell><StatusBadge status={identity.status} /></TableCell>
                    <TableCell className="font-mono text-xs">{identity.credential_prefix}…</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {identity.credential_expires_at ? formatDateTime(identity.credential_expires_at) : "—"}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {identity.last_used_at ? formatDateTime(identity.last_used_at) : "never"}
                    </TableCell>
                    <TableCell className="text-right">
                      {identity.status === "active" ? (
                        <>
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() => lifecycle.mutate({ id: identity.id, action: "suspend" })}
                          >
                            Suspend
                          </Button>{" "}
                          <Button
                            size="sm"
                            variant="destructive"
                            onClick={() => lifecycle.mutate({ id: identity.id, action: "revoke" })}
                          >
                            Revoke
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
