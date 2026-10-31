"use client";

// Approvals queue: decide pending authorization requests.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import Link from "next/link";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/ui/badge";
import { Select } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatDateTime } from "@/lib/format";

interface ApprovalRow {
  id: string; execution_id: string; agent_id: string; action: string;
  resource: { tool?: string; arguments?: Record<string, unknown> };
  justification: string; status: string; assigned_role: string;
  expires_at: string; created_at: string;
}

export default function ApprovalsPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [statusFilter, setStatusFilter] = useState("pending");
  const [deciding, setDeciding] = useState<ApprovalRow | null>(null);
  const [decisionError, setDecisionError] = useState<string | null>(null);

  const approvals = useQuery({
    queryKey: ["approvals", statusFilter],
    queryFn: () => api<{ items: ApprovalRow[] }>(path("approvals"), { params: { status: statusFilter } }),
    refetchInterval: statusFilter === "pending" ? 10_000 : false,
  });

  const decide = useMutation({
    mutationFn: ({ id, decision }: { id: string; decision: "approved" | "rejected" }) =>
      api(path("approvals", id, "decide"), { method: "POST", body: { decision, reason: "" } }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["approvals"] });
      setDeciding(null);
    },
    onError: (err) =>
      setDecisionError(err instanceof ApiError ? err.message : "Decision failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Approvals"
        description="Human authorization requests raised by executions"
      />
      <PageBody className="space-y-4">
        <Select
          className="max-w-44"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
        >
          <option value="pending">Pending</option>
          <option value="approved">Approved</option>
          <option value="rejected">Rejected</option>
        </Select>

        {approvals.isError ? (
          <ErrorState message="Failed to load approvals." />
        ) : approvals.isLoading ? (
          <LoadingState />
        ) : (approvals.data?.items ?? []).length === 0 ? (
          <EmptyState
            title={statusFilter === "pending" ? "No pending approvals" : `No ${statusFilter} approvals`}
            description="Approval requests appear when policy requires human authorization."
          />
        ) : (
          <div className="rounded-lg border">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Action</TableHead>
                  <TableHead>Resource</TableHead>
                  <TableHead>Execution</TableHead>
                  <TableHead>Status</TableHead>
                  <TableHead>Requested</TableHead>
                  <TableHead className="text-right">Decision</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {(approvals.data?.items ?? []).map((approval) => (
                  <TableRow key={approval.id}>
                    <TableCell className="font-mono text-xs">{approval.action}</TableCell>
                    <TableCell className="text-xs">
                      {approval.resource?.tool ?? "—"}
                    </TableCell>
                    <TableCell>
                      <Link
                        href={`/executions/${approval.execution_id}`}
                        className="font-mono text-xs hover:underline"
                      >
                        {approval.execution_id.slice(0, 8)}…
                      </Link>
                    </TableCell>
                    <TableCell><StatusBadge status={approval.status} /></TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatDateTime(approval.created_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      {approval.status === "pending" ? (
                        <Button size="sm" variant="secondary" onClick={() => setDeciding(approval)}>
                          Review
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

      <Dialog open={!!deciding} onClose={() => setDeciding(null)}>
        <DialogTitle>Review approval request</DialogTitle>
        <DialogContent>
          {deciding ? (
            <dl className="space-y-3 text-sm">
              <div>
                <dt className="text-xs text-muted-foreground">Requested action</dt>
                <dd className="font-mono text-xs">{deciding.action}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Tool</dt>
                <dd>{deciding.resource?.tool ?? "—"}</dd>
              </div>
              <div>
                <dt className="text-xs text-muted-foreground">Arguments</dt>
                <dd>
                  <pre className="max-h-40 overflow-auto rounded-md border bg-muted/40 p-2 text-xs">
                    {JSON.stringify(deciding.resource?.arguments ?? {}, null, 2)}
                  </pre>
                </dd>
              </div>
              {decisionError ? <p className="text-xs text-destructive">{decisionError}</p> : null}
            </dl>
          ) : null}
        </DialogContent>
        <DialogActions>
          <Button
            variant="destructive"
            disabled={decide.isPending}
            onClick={() => deciding && decide.mutate({ id: deciding.id, decision: "rejected" })}
          >
            Reject
          </Button>
          <Button
            disabled={decide.isPending}
            onClick={() => deciding && decide.mutate({ id: deciding.id, decision: "approved" })}
          >
            Approve
          </Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
