"use client";

// MCP connections management.

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

interface Connection {
  id: string; name: string; transport: string; url: string;
  status: string; health_status: string; last_checked_at?: string; created_at: string;
}

export default function McpPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const connections = useQuery({
    queryKey: ["mcp"],
    queryFn: () => api<{ items: Connection[] }>(path("mcp-connections")),
  });

  const create = useMutation({
    mutationFn: (body: { name: string; url: string }) =>
      api(path("mcp-connections"), { method: "POST", body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["mcp"] });
      setOpen(false);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to register"),
  });

  const toggle = useMutation({
    mutationFn: ({ id, status }: { id: string; status: string }) =>
      api(path("mcp-connections", id, "status"), { method: "PUT", body: { status } }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["mcp"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Update failed"),
  });

  const remove = useMutation({
    mutationFn: (id: string) => api(path("mcp-connections", id), { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["mcp"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Delete failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="MCP Connections"
        description="Model Context Protocol servers available to agents"
        actions={<Button onClick={() => setOpen(true)}>Register server</Button>}
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        {connections.isError ? (
          <ErrorState message="Failed to load connections." />
        ) : connections.isLoading ? (
          <LoadingState />
        ) : (connections.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No MCP connections"
            description="Register a streamable-HTTP MCP server to expose its tools to agents."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Transport</TableHead>
                  <TableHead>URL</TableHead>
                  <TableHead>Health</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Registered</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(connections.data?.items ?? []).map((connection) => (
                  <TableRow key={connection.id}>
                    <TableCell className="font-medium">{connection.name}</TableCell>
                    <TableCell className="text-xs">{connection.transport}</TableCell>
                    <TableCell className="max-w-64 truncate font-mono text-xs">{connection.url}</TableCell>
                    <TableCell className="text-xs">{connection.health_status}</TableCell>
                    <TableCell><StatusBadge status={connection.status} /></TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(connection.created_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      <Button
                        size="sm"
                        variant="secondary"
                        onClick={() =>
                          toggle.mutate({
                            id: connection.id,
                            status: connection.status === "active" ? "disabled" : "active",
                          })
                        }
                      >
                        {connection.status === "active" ? "Disable" : "Enable"}
                      </Button>{" "}
                      <Button
                        size="sm"
                        variant="destructive"
                        onClick={() => {
                          if (window.confirm(`Delete connection "${connection.name}"?`)) {
                            remove.mutate(connection.id);
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
        <DialogTitle>Register MCP server</DialogTitle>
        <DialogContent>
          <form
            id="mcp-create"
            className="space-y-4"
            onSubmit={async (event) => {
              event.preventDefault();
              setError(null);
              const form = new FormData(event.currentTarget);
              await create
                .mutateAsync({
                  name: String(form.get("name")),
                  url: String(form.get("url")),
                })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Failed"));
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="mcp-name">Name</Label>
              <Input id="mcp-name" name="name" required />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="mcp-url">Streamable HTTP endpoint</Label>
              <Input id="mcp-url" name="url" type="url" required placeholder="https://mcp.example.com/mcp" />
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
          <Button type="submit" form="mcp-create">Register</Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
