"use client";

// Developer platform: API keys with single-disclosure secrets.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Input, Label } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface KeyRow {
  id: string; name: string; key_prefix: string; scopes: string[];
  status: string; created_at: string; last_used_at?: string; revoked_at?: string;
}

export default function ApiKeysPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [created, setCreated] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const keys = useQuery({
    queryKey: ["api-keys"],
    queryFn: () => api<{ items: KeyRow[] }>(path("api-keys")),
  });

  const create = useMutation({
    mutationFn: (body: { name: string; scopes: string[] }) =>
      api<{ secret: string }>(path("api-keys"), { method: "POST", body }),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["api-keys"] });
      setCreated(data.secret);
      setOpen(false);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to create key"),
  });

  const revoke = useMutation({
    mutationFn: (id: string) => api(path("api-keys", id), { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["api-keys"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Revoke failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="API Keys"
        description="Scoped bearer keys for the REST API and Python SDK"
        actions={<Button onClick={() => setOpen(true)}>Create key</Button>}
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        {created ? (
          <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs">
            Copy this key now — it is shown once:
            <span className="ml-1 font-mono">{created}</span>
            <button className="ml-2 text-primary underline" onClick={() => setCreated(null)}>dismiss</button>
          </div>
        ) : null}

        {keys.isError ? (
          <ErrorState message="Failed to load API keys." />
        ) : keys.isLoading ? (
          <LoadingState />
        ) : (keys.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No API keys"
            description="Create a key to authenticate the Python SDK or machines."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Prefix</TableHead>
                  <TableHead>Scopes</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(keys.data?.items ?? []).map((key) => (
                  <TableRow key={key.id}>
                    <TableCell>{key.name}</TableCell>
                    <TableCell className="font-mono text-xs">{key.key_prefix}…</TableCell>
                    <TableCell className="text-xs">{key.scopes.join(", ")}</TableCell>
                    <TableCell><StatusBadge status={key.status} /></TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {key.last_used_at ? formatDateTime(key.last_used_at) : "never"}
                    </TableCell>
                    <TableCell className="text-right">
                      {key.status === "active" ? (
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={() => {
                            if (window.confirm(`Revoke key "${key.name}"? Clients using it stop working.`)) {
                              revoke.mutate(key.id);
                            }
                          }}
                        >
                          Revoke
                        </Button>
                      ) : "—"}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PageBody>

      <Dialog open={open} onClose={() => setOpen(false)}>
        <DialogTitle>Create API key</DialogTitle>
        <DialogContent>
          <form
            id="key-create"
            className="space-y-4"
            onSubmit={async (event) => {
              event.preventDefault();
              setError(null);
              const form = new FormData(event.currentTarget);
              const scopes = ["executions.submit", "executions.read", "agents.read"];
              if (form.get("edit")) scopes.push("agents.edit");
              if (form.get("cancel")) scopes.push("executions.cancel");
              await create
                .mutateAsync({ name: String(form.get("name")), scopes })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Failed"));
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="key-name">Name</Label>
              <Input id="key-name" name="name" required placeholder="ci-pipeline" />
            </div>
            <p className="text-xs text-muted-foreground">
              Base scopes: executions.submit, executions.read, agents.read
            </p>
            <label className="flex items-center gap-2 text-xs">
              <input type="checkbox" name="edit" className="accent-primary" /> agents.edit
            </label>
            <label className="flex items-center gap-2 text-xs">
              <input type="checkbox" name="cancel" className="accent-primary" /> executions.cancel
            </label>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
          <Button type="submit" form="key-create">Create</Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
