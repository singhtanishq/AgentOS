"use client";

// Webhooks: endpoints, subscribed events and delivery status.

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

interface WebhookRow {
  id: string; url: string; description: string; events: string[];
  status: string; created_at: string; last_delivery_at?: string;
}

export default function WebhooksPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [created, setCreated] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const webhooks = useQuery({
    queryKey: ["webhooks"],
    queryFn: () => api<{ items: WebhookRow[] }>(path("webhooks")),
  });

  const create = useMutation({
    mutationFn: (body: { url: string; description: string }) =>
      api<{ secret: string }>(path("webhooks"), { method: "POST", body }),
    onSuccess: (data) => {
      queryClient.invalidateQueries({ queryKey: ["webhooks"] });
      setCreated(data.secret);
      setOpen(false);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to register webhook"),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api(path("webhooks", id), { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["webhooks"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Delete failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Webhooks"
        description="HMAC-signed delivery of platform events"
        actions={<Button onClick={() => setOpen(true)}>Add webhook</Button>}
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        {created ? (
          <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-xs">
            Signing secret (shown once — verify X-AgentOS-Signature with it):
            <span className="ml-1 font-mono">{created}</span>
            <button className="ml-2 text-primary underline" onClick={() => setCreated(null)}>dismiss</button>
          </div>
        ) : null}

        {webhooks.isError ? (
          <ErrorState message="Failed to load webhooks." />
        ) : webhooks.isLoading ? (
          <LoadingState />
        ) : (webhooks.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No webhooks"
            description="Register an HTTPS endpoint to receive execution and approval events."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>URL</TableHead>
                  <TableHead>Events</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Last delivery</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(webhooks.data?.items ?? []).map((webhook) => (
                  <TableRow key={webhook.id}>
                    <TableCell className="max-w-64 truncate font-mono text-xs">{webhook.url}</TableCell>
                    <TableCell className="text-xs">{webhook.events.join(", ")}</TableCell>
                    <TableCell><StatusBadge status={webhook.status} /></TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {webhook.last_delivery_at ? formatDateTime(webhook.last_delivery_at) : "never"}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="destructive"
                        onClick={() => {
                          if (window.confirm("Delete this webhook?")) {
                            remove.mutate(webhook.id);
                          }
                        }}
                      >
                        Delete
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </PageBody>

      <Dialog open={open} onClose={() => setOpen(false)}>
        <DialogTitle>Add webhook</DialogTitle>
        <DialogContent>
          <form
            id="webhook-create"
            className="space-y-4"
            onSubmit={async (event) => {
              event.preventDefault();
              setError(null);
              const form = new FormData(event.currentTarget);
              await create
                .mutateAsync({
                  url: String(form.get("url")),
                  description: String(form.get("description") || ""),
                })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Failed"));
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="webhook-url">HTTPS endpoint</Label>
              <Input id="webhook-url" name="url" type="url" required placeholder="https://ops.example.com/hooks/agentos" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="webhook-desc">Description</Label>
              <Input id="webhook-desc" name="description" />
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
          <Button type="submit" form="webhook-create">Register</Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
