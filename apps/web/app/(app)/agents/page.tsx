"use client";

// Agent registry: searchable, filterable list.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Input, Label, Select, Textarea } from "@/components/ui/input";
import { StatusBadge } from "@/components/ui/badge";
import {
  Dialog, DialogActions, DialogContent, DialogTitle,
} from "@/components/ui/dialog";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface AgentSummary {
  id: string; name: string; description: string; status: string;
  latest_version: number; version_count: number; created_at: string;
}

export default function AgentsPage() {
  const { path } = useOrg();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [createOpen, setCreateOpen] = useState(false);

  const agents = useQuery({
    queryKey: ["agents", search, statusFilter],
    queryFn: () =>
      api<{ items: AgentSummary[] }>(path("agents"), { params: { q: search, status: statusFilter } }),
  });

  return (
    <AppShell>
      <PageHeader
        title="Agents"
        description="Your registered AI agents and their lifecycle"
        actions={<Button onClick={() => setCreateOpen(true)}>New agent</Button>}
      />
      <PageBody className="space-y-4">
        <div className="flex flex-wrap items-center gap-2">
          <Input
            placeholder="Search agents…"
            className="max-w-xs"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Select
            className="max-w-40"
            value={statusFilter}
            onChange={(e) => setStatusFilter(e.target.value)}
          >
            <option value="">All statuses</option>
            <option value="draft">Draft</option>
            <option value="active">Active</option>
            <option value="paused">Paused</option>
            <option value="suspended">Suspended</option>
            <option value="archived">Archived</option>
          </Select>
        </div>

        {agents.isError ? (
          <ErrorState message="Failed to load agents." />
        ) : agents.isLoading ? (
          <LoadingState />
        ) : (agents.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No agents found"
            description={search || statusFilter ? "Try adjusting the filters." : "Register your first agent to get started."}
            action={<Button variant="outline" onClick={() => setCreateOpen(true)}>New agent</Button>}
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Version</TableHead>
                  <TableHead>Created</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(agents.data?.items ?? []).map((agent) => (
                  <TableRow key={agent.id}>
                    <TableCell>
                      <Link href={`/agents/${agent.id}`} className="font-medium hover:underline">
                        {agent.name}
                      </Link>
                      {agent.description ? (
                        <p className="text-xs text-muted-foreground">{agent.description}</p>
                      ) : null}
                    </TableCell>
                    <TableCell><StatusBadge status={agent.status} /></TableCell>
                    <TableCell className="tabular">v{agent.latest_version}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(agent.created_at)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PageBody>

      <CreateAgentDialog
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(agentId) => {
          queryClient.invalidateQueries({ queryKey: ["agents"] });
          setCreateOpen(false);
          router.push(`/agents/${agentId}`);
        }}
      />
    </AppShell>
  );
}

function CreateAgentDialog({
  open, onClose, onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (agentId: string) => void;
}) {
  const { path } = useOrg();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");
  const [modelId, setModelId] = useState("mock-mini");
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      api<{ id: string }>(path("agents"), {
        method: "POST",
        body: {
          name,
          description,
          configuration: {
            instructions,
            model: { provider_type: "mock", model_id: modelId },
            tools: [],
            limits: {},
          },
        },
      }),
  });

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError(null);
    try {
      const created = await create.mutateAsync();
      onCreated(created.id);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create the agent");
    }
  }

  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>Create agent</DialogTitle>
      <DialogContent>
        <form id="create-agent" onSubmit={submit} className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="agent-name">Name</Label>
            <Input id="agent-name" required minLength={2} value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="agent-desc">Description</Label>
            <Input id="agent-desc" value={description} onChange={(e) => setDescription(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="agent-instructions">System instructions</Label>
            <Textarea
              id="agent-instructions"
              required
              value={instructions}
              onChange={(e) => setInstructions(e.target.value)}
              placeholder="You are a precise research assistant…"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="agent-model">Model (mock provider)</Label>
            <Select id="agent-model" value={modelId} onChange={(e) => setModelId(e.target.value)}>
              <option value="mock-mini">mock-mini</option>
            </Select>
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </form>
      </DialogContent>
      <DialogActions>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button type="submit" form="create-agent" disabled={create.isPending}>
          {create.isPending ? "Creating…" : "Create agent"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}
