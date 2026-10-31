"use client";

// Agent detail: configuration, versions, lifecycle and operations.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams, useRouter } from "next/navigation";
import React, { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Input, Label, Textarea } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface AgentDetail {
  id: string; name: string; description: string; status: string;
  latest_version: number; version_count: number; created_at: string;
}
interface VersionRow {
  id: string; version: number; status: string; changelog: string;
  configuration: {
    instructions?: string;
    model?: { provider_type?: string; model_id?: string; temperature?: number; max_tokens?: number };
    tools?: { name: string }[];
    limits?: { max_steps?: number; max_tool_calls?: number; max_total_tokens?: number; timeout_ms?: number };
  };
  published_at?: string; created_at: string;
}

export default function AgentDetailPage() {
  const { agentId } = useParams<{ agentId: string }>();
  const { path } = useOrg();
  const router = useRouter();
  const queryClient = useQueryClient();
  const [newVersionOpen, setNewVersionOpen] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const agent = useQuery({
    queryKey: ["agent", agentId],
    queryFn: () => api<{ agent: AgentDetail; versions: VersionRow[] }>(path("agents", agentId)),
  });

  const statusMutation = useMutation({
    mutationFn: (status: string) =>
      api(path("agents", agentId, "status"), { method: "PUT", body: { status } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["agent", agentId] }),
    onError: (err) => setActionError(err instanceof ApiError ? err.message : "Action failed"),
  });

  const deleteMutation = useMutation({
    mutationFn: () => api(path("agents", agentId), { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["agents"] });
      router.push("/agents");
    },
    onError: (err) => setActionError(err instanceof ApiError ? err.message : "Delete failed"),
  });

  if (agent.isError) {
    return (
      <AppShell>
        <PageHeader title="Agent" />
        <PageBody><ErrorState message="Agent not found or failed to load." /></PageBody>
      </AppShell>
    );
  }
  if (agent.isLoading || !agent.data) {
    return (
      <AppShell>
        <PageHeader title="Agent" />
        <PageBody><LoadingState /></PageBody>
      </AppShell>
    );
  }

  const { agent: detail, versions } = agent.data;
  const nextStatuses = NEXT_STATUSES[detail.status] ?? [];
  const publishedVersions = versions.filter((v) => v.status === "published");

  return (
    <AppShell>
      <PageHeader
        title={detail.name}
        description={detail.description || undefined}
        actions={
          <>
            <Button variant="outline" onClick={() => setNewVersionOpen(true)}>New version</Button>
            {nextStatuses.map((status) => (
              <Button
                key={status}
                variant={status === "archived" ? "destructive" : "secondary"}
                disabled={statusMutation.isPending}
                onClick={() => statusMutation.mutate(status)}
              >
                {status === "archived" ? "Archive" : `Mark ${status}`}
              </Button>
            ))}
          </>
        }
      />
      <PageBody className="space-y-6">
        {actionError ? <ErrorState message={actionError} /> : null}

        <div className="flex flex-wrap items-center gap-3">
          <StatusBadge status={detail.status} />
          <span className="text-xs text-muted-foreground">
            v{detail.latest_version} · {detail.version_count} versions · created {formatDateTime(detail.created_at)}
          </span>
          {detail.status === "active" ? (
            <SubmitExecutionButton agentId={detail.id} publishedVersionId={publishedVersions[0]?.id} />
          ) : null}
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Version history</CardTitle>
            <CardDescription>Published versions are immutable; executions pin the exact version used.</CardDescription>
          </CardHeader>
          <CardContent className="px-0 pb-0">
            {versions.length === 0 ? (
              <div className="px-4 pb-4"><EmptyState title="No versions" /></div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Version</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Changelog</TableHead>
                    <TableHead>Published</TableHead>
                    <TableHead />
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {versions.map((version) => (
                    <TableRow key={version.id}>
                      <TableCell className="font-medium tabular">v{version.version}</TableCell>
                      <TableCell><StatusBadge status={version.status} /></TableCell>
                      <TableCell className="text-xs text-muted-foreground">{version.changelog || "—"}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {formatDateTime(version.published_at)}
                      </TableCell>
                      <TableCell className="text-right">
                        <VersionConfigDialog version={version} />
                        {version.status === "draft" ? (
                          <Button
                            size="sm"
                            variant="secondary"
                            onClick={() =>
                              api(path("agents", agentId, "versions", String(version.version), "publish"), {
                                method: "POST",
                              })
                                .then(() => queryClient.invalidateQueries({ queryKey: ["agent", agentId] }))
                                .catch((err) =>
                                  setActionError(err instanceof ApiError ? err.message : "Publish failed"),
                                )
                            }
                          >
                            Publish
                          </Button>
                        ) : null}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>

        <div className="flex justify-end">
          <Button
            variant="destructive"
            onClick={() => {
              if (window.confirm(`Delete agent "${detail.name}"? This cannot be undone.`)) {
                deleteMutation.mutate();
              }
            }}
          >
            Delete agent
          </Button>
        </div>
      </PageBody>

      <NewVersionDialog
        agentId={detail.id}
        open={newVersionOpen}
        onClose={() => setNewVersionOpen(false)}
        base={versions[0]?.configuration}
      />
    </AppShell>
  );
}

const NEXT_STATUSES: Record<string, string[]> = {
  draft: ["active", "archived"],
  active: ["paused", "suspended", "archived"],
  paused: ["active", "suspended", "archived"],
  suspended: ["active", "archived"],
  archived: [],
};

function VersionConfigDialog({ version }: { version: VersionRow }) {
  const [open, setOpen] = useState(false);
  const config = version.configuration;
  return (
    <>
      <Button size="sm" variant="ghost" onClick={() => setOpen(true)}>View</Button>
      <Dialog open={open} onClose={() => setOpen(false)}>
        <DialogTitle>Version v{version.version} configuration</DialogTitle>
        <DialogContent>
          <dl className="space-y-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">Instructions</dt>
              <dd className="whitespace-pre-wrap rounded-md border bg-muted/40 p-2 text-xs">
                {config.instructions}
              </dd>
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div>
                <dt className="text-xs text-muted-foreground">Provider</dt>
                <dd>{config.model?.provider_type ?? "—"}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Model</dt>
                <dd className="font-mono text-xs">{config.model?.model_id ?? "—"}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Temperature</dt>
                <dd className="tabular">{config.model?.temperature ?? "default"}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Max steps</dt>
                <dd className="tabular">{config.limits?.max_steps ?? "default"}</dd>
              </div>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">Tools</dt>
              <dd>{(config.tools ?? []).map((t) => t.name).join(", ") || "none"}</dd>
            </div>
          </dl>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setOpen(false)}>Close</Button>
        </DialogActions>
      </Dialog>
    </>
  );
}

function NewVersionDialog({
  agentId, open, onClose, base,
}: {
  agentId: string;
  open: boolean;
  onClose: () => void;
  base?: VersionRow["configuration"];
}) {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [instructions, setInstructions] = useState(base?.instructions ?? "");
  const [changelog, setChangelog] = useState("");
  const [error, setError] = useState<string | null>(null);

  // Reset the draft instructions whenever the dialog opens against a new
  // base version (derived state instead of an effect).
  const [lastBase, setLastBase] = useState<string | undefined>(base?.instructions);
  if (open && base?.instructions !== lastBase) {
    setLastBase(base?.instructions);
    setInstructions(base?.instructions ?? "");
  }

  const create = useMutation({
    mutationFn: () =>
      api(path("agents", agentId, "versions"), {
        method: "POST",
        body: {
          configuration: { ...(base ?? {}), instructions },
          changelog,
        },
      }),
  });

  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>New version</DialogTitle>
      <DialogContent>
        <form
          id="new-version"
          className="space-y-4"
          onSubmit={async (event) => {
            event.preventDefault();
            setError(null);
            try {
              await create.mutateAsync();
              queryClient.invalidateQueries({ queryKey: ["agent", agentId] });
              onClose();
            } catch (err) {
              setError(err instanceof ApiError ? err.message : "Failed to create the version");
            }
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor="nv-instructions">Instructions</Label>
            <Textarea
              id="nv-instructions"
              required
              rows={6}
              value={instructions}
              onChange={(e) => setInstructions(e.target.value)}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="nv-changelog">Changelog</Label>
            <Input
              id="nv-changelog"
              value={changelog}
              onChange={(e) => setChangelog(e.target.value)}
              placeholder="What changed and why"
            />
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </form>
      </DialogContent>
      <DialogActions>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button type="submit" form="new-version" disabled={create.isPending}>
          {create.isPending ? "Creating…" : "Create draft version"}
        </Button>
      </DialogActions>
    </Dialog>
  );
}

function SubmitExecutionButton({
  agentId, publishedVersionId,
}: {
  agentId: string;
  publishedVersionId?: string;
}) {
  const { path } = useOrg();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const created = await api<{ id: string }>(path("executions"), {
        method: "POST",
        body: {
          agent_id: agentId,
          ...(publishedVersionId ? { agent_version_id: publishedVersionId } : {}),
          input: { query },
        },
      });
      setOpen(false);
      router.push(`/executions/${created.id}`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Submission failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Button onClick={() => setOpen(true)}>Run agent</Button>
      <Dialog open={open} onClose={() => setOpen(false)}>
        <DialogTitle>Run agent</DialogTitle>
        <DialogContent>
          <form id="run-agent" onSubmit={submit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="run-query">Input</Label>
              <Textarea
                id="run-query"
                required
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="What should the agent do?"
              />
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
          <Button type="submit" form="run-agent" disabled={busy}>
            {busy ? "Submitting…" : "Submit execution"}
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
