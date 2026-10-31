"use client";

// Policies: Rego rule management with OPA sync.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Input, Label, Select, Textarea } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";

interface PolicyRow {
  id: string; name: string; kind: string; version: number; status: string;
}

const DEFAULT_REGO = `package agentos.authz

# Custom organization policy. Rules in this package are unioned with the
# platform baseline; the baseline defaults to deny.

default allow := false
`;

export default function PoliciesPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [open, setOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const policies = useQuery({
    queryKey: ["policies"],
    queryFn: () => api<{ items: PolicyRow[] }>(path("policies")),
  });

  const create = useMutation({
    mutationFn: (body: { name: string; kind: string; content: string }) =>
      api(path("policies"), { method: "POST", body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["policies"] });
      setOpen(false);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to create policy"),
  });

  const disable = useMutation({
    mutationFn: (id: string) => api(path("policies", id), { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["policies"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to disable"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Policies"
        description="Authorization rules evaluated by OPA before every tool execution"
        actions={<Button onClick={() => setOpen(true)}>New policy</Button>}
      />
      <PageBody className="space-y-4">
        {error ? <ErrorState message={error} /> : null}
        {policies.isError ? (
          <ErrorState message="Failed to load policies." />
        ) : policies.isLoading ? (
          <LoadingState />
        ) : (policies.data?.items ?? []).length === 0 ? (
          <EmptyState
            title="No organization policies"
            description="The platform baseline policy is always active. Add custom Rego rules to extend it."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Kind</TableHead>
                  <TableHead>Version</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead className="text-right">Actions</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(policies.data?.items ?? []).map((policy) => (
                  <TableRow key={policy.id}>
                    <TableCell className="font-mono text-xs">{policy.name}</TableCell>
                    <TableCell>{policy.kind}</TableCell>
                    <TableCell className="tabular">v{policy.version}</TableCell>
                    <TableCell><StatusBadge status={policy.status} /></TableCell>
                    <TableCell className="text-right">
                      {policy.status === "active" ? (
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={() => {
                            if (window.confirm(`Disable policy "${policy.name}"?`)) {
                              disable.mutate(policy.id);
                            }
                          }}
                        >
                          Disable
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

      <Dialog open={open} onClose={() => setOpen(false)} className="max-w-2xl">
        <DialogTitle>New policy</DialogTitle>
        <DialogContent>
          <form
            id="policy-create"
            className="space-y-4"
            onSubmit={async (event) => {
              event.preventDefault();
              setError(null);
              const form = new FormData(event.currentTarget);
              await create
                .mutateAsync({
                  name: String(form.get("name")),
                  kind: String(form.get("kind")),
                  content: String(form.get("content")),
                })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Failed"));
            }}
          >
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="policy-name">Name</Label>
                <Input id="policy-name" name="name" required minLength={2} />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="policy-kind">Kind</Label>
                <Select id="policy-kind" name="kind" defaultValue="authorization">
                  <option value="authorization">authorization</option>
                  <option value="approval">approval</option>
                  <option value="budget">budget</option>
                  <option value="model_usage">model_usage</option>
                </Select>
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="policy-content">Rego content</Label>
              <Textarea
                id="policy-content"
                name="content"
                rows={12}
                className="font-mono text-xs"
                defaultValue={DEFAULT_REGO}
              />
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
          <Button type="submit" form="policy-create">Create & deploy</Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
