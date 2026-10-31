"use client";

// Tool registry: schemas, configuration and status.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";

interface ToolRow {
  id: string; name: string; kind: string; description: string;
  input_schema: Record<string, unknown>; config: Record<string, unknown>;
  required_role: string; status: string; timeout_ms: number; version: number;
}

export default function ToolsPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<ToolRow | null>(null);
  const [error, setError] = useState<string | null>(null);

  const tools = useQuery({
    queryKey: ["tools"],
    queryFn: () => api<{ items: ToolRow[] }>(path("tools")),
  });

  const update = useMutation({
    mutationFn: ({ toolId, body }: { toolId: string; body: Record<string, unknown> }) =>
      api(path("tools", toolId), { method: "PATCH", body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tools"] });
      setSelected(null);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Update failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Tool Registry"
        description="Built-in and remote tools available to agents"
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        {tools.isError ? (
          <ErrorState message="Failed to load tools." />
        ) : tools.isLoading ? (
          <LoadingState />
        ) : (tools.data?.items ?? []).length === 0 ? (
          <EmptyState title="No tools registered" />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Tool</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Required role</TableHead>
                  <TableHead className="text-right">Timeout</TableHead>
                  <TableHead>Version</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead />
                </TableRow>
              </TableHeader>
              <TableBody>
                {(tools.data?.items ?? []).map((tool) => (
                  <TableRow key={tool.id}>
                    <TableCell>
                      <p className="font-mono text-xs">{tool.name}</p>
                      <p className="text-xs text-muted-foreground">{tool.description}</p>
                    </TableCell>
                    <TableCell>{tool.kind}</TableCell>
                    <TableCell>{tool.required_role}</TableCell>
                    <TableCell className="text-right tabular">{tool.timeout_ms}ms</TableCell>
                    <TableCell className="tabular">v{tool.version}</TableCell>
                    <TableCell><StatusBadge status={tool.status} /></TableCell>
                    <TableCell className="text-right">
                      <Button size="sm" variant="ghost" onClick={() => setSelected(tool)}>
                        Configure
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PageBody>

      <Dialog open={!!selected} onClose={() => setSelected(null)}>
        <DialogTitle>Configure {selected?.name}</DialogTitle>
        <DialogContent>
          {selected ? (
            <form
              id="tool-config"
              className="space-y-4"
              onSubmit={async (event) => {
                event.preventDefault();
                const form = new FormData(event.currentTarget);
                await update.mutateAsync({
                  toolId: selected.id,
                  body: {
                    required_role: String(form.get("required_role")),
                    status: String(form.get("status")),
                    timeout_ms: Number(form.get("timeout_ms")),
                    ...(String(form.get("config")).trim()
                      ? { config: JSON.parse(String(form.get("config"))) }
                      : {}),
                  },
                }).catch((err) =>
                  setError(err instanceof ApiError ? err.message : "Update failed"),
                );
              }}
            >
              <p className="text-xs text-muted-foreground">{selected.description}</p>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <label className="text-xs text-muted-foreground" htmlFor="tool-role">Required role</label>
                  <select
                    id="tool-role"
                    name="required_role"
                    defaultValue={selected.required_role}
                    className="w-full rounded-md border border-input bg-transparent px-2 py-1.5 text-sm"
                  >
                    <option value="operator">operator</option>
                    <option value="developer">developer</option>
                    <option value="admin">admin</option>
                  </select>
                </div>
                <div className="space-y-1.5">
                  <label className="text-xs text-muted-foreground" htmlFor="tool-status">Status</label>
                  <select
                    id="tool-status"
                    name="status"
                    defaultValue={selected.status}
                    className="w-full rounded-md border border-input bg-transparent px-2 py-1.5 text-sm"
                  >
                    <option value="active">active</option>
                    <option value="disabled">disabled</option>
                  </select>
                </div>
              </div>
              <div className="space-y-1.5">
                <label className="text-xs text-muted-foreground" htmlFor="tool-timeout">Timeout (ms)</label>
                <input
                  id="tool-timeout"
                  name="timeout_ms"
                  type="number"
                  defaultValue={selected.timeout_ms}
                  className="w-full rounded-md border border-input bg-transparent px-2 py-1.5 text-sm"
                />
              </div>
              <div className="space-y-1.5">
                <label className="text-xs text-muted-foreground" htmlFor="tool-config">Config (JSON)</label>
                <textarea
                  id="tool-config"
                  name="config"
                  rows={4}
                  defaultValue={JSON.stringify(selected.config, null, 2)}
                  className="w-full rounded-md border border-input bg-transparent px-2 py-1.5 font-mono text-xs"
                />
              </div>
              {error ? <p className="text-xs text-destructive">{error}</p> : null}
            </form>
          ) : null}
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setSelected(null)}>Cancel</Button>
          <Button type="submit" form="tool-config">Save</Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
