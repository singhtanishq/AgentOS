"use client";

// Evaluations: datasets, cases and run results with regression tracking.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
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
import { formatDateTime, formatPercent } from "@/lib/format";

interface Dataset { id: string; name: string; description: string; case_count: number }
interface CaseRow { id: string; name: string; input: Record<string, unknown>; rules: unknown[]; position: number }
interface RunRow {
  id: string; status: string; pass_rate?: number; total_cases: number;
  passed_cases: number; started_at: string; completed_at?: string;
}

export default function EvaluationsPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [selected, setSelected] = useState<string | null>(null);
  const [datasetOpen, setDatasetOpen] = useState(false);
  const [caseOpen, setCaseOpen] = useState(false);
  const [runTarget, setRunTarget] = useState<{ datasetId: string } | null>(null);
  const [lastRun, setLastRun] = useState<Record<string, unknown> | null>(null);
  const [error, setError] = useState<string | null>(null);

  const datasets = useQuery({
    queryKey: ["eval-datasets"],
    queryFn: () => api<{ items: Dataset[] }>(path("evaluations", "datasets")),
  });
  const cases = useQuery({
    queryKey: ["eval-cases", selected],
    queryFn: () => api<{ items: CaseRow[] }>(path("evaluations", "datasets", selected!, "cases")),
    enabled: !!selected,
  });
  const runs = useQuery({
    queryKey: ["eval-runs", selected],
    queryFn: () => api<{ items: RunRow[] }>(path("evaluations", "datasets", selected!, "runs")),
    enabled: !!selected,
  });

  const agents = useQuery({
    queryKey: ["agents", "eval"],
    queryFn: () => api<{ items: { id: string; name: string; latest_version: number }[] }>(path("agents")),
  });

  const run = useMutation({
    mutationFn: ({ datasetId, agentId }: { datasetId: string; agentId: string }) => {
      // Resolve the latest published version via the agent detail.
      return api<{ versions: { id: string; status: string }[] }>(path("agents", agentId)).then(
        (detail) => {
          const published = detail.versions.find((v) => v.status === "published");
          if (!published) throw new ApiError(0, "no_published_version", "the agent has no published version");
          return api<Record<string, unknown>>(
            path("evaluations", "datasets", datasetId, "runs"),
            { method: "POST", body: { agent_id: agentId, agent_version_id: published.id } },
          );
        },
      );
    },
    onSuccess: (data) => {
      setLastRun(data);
      queryClient.invalidateQueries({ queryKey: ["eval-runs"] });
      setRunTarget(null);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Run failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Evaluations"
        description="Dataset-driven scoring with deterministic rules and regression tracking"
        actions={<Button onClick={() => setDatasetOpen(true)}>New dataset</Button>}
      />
      <PageBody className="space-y-6">
        {error ? <ErrorState message={error} /> : null}

        <div className="grid gap-6 xl:grid-cols-3">
          <div className="space-y-2">
            <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">Datasets</p>
            {datasets.isLoading ? (
              <LoadingState />
            ) : (datasets.data?.items ?? []).length === 0 ? (
              <EmptyState title="No datasets" description="Create a dataset with test cases." />
            ) : (
              (datasets.data?.items ?? []).map((dataset) => (
                <button
                  key={dataset.id}
                  onClick={() => setSelected(dataset.id)}
                  className={
                    "w-full rounded-md border px-3 py-2 text-left text-sm hover:bg-accent " +
                    (selected === dataset.id ? "border-primary bg-accent" : "")
                  }
                >
                  <p className="font-medium">{dataset.name}</p>
                  <p className="text-xs text-muted-foreground">
                    {dataset.case_count} cases · {dataset.description || "no description"}
                  </p>
                </button>
              ))
            )}
          </div>

          <div className="space-y-2 xl:col-span-2">
            {selected ? (
              <>
                <div className="flex items-center justify-between">
                  <p className="text-xs font-medium uppercase tracking-wider text-muted-foreground">Cases</p>
                  <div className="flex gap-2">
                    <Button size="sm" variant="outline" onClick={() => setCaseOpen(true)}>Add case</Button>
                    <Button size="sm" onClick={() => setRunTarget({ datasetId: selected })}>Run dataset</Button>
                  </div>
                </div>
                {cases.isLoading ? (
                  <LoadingState />
                ) : (cases.data?.items ?? []).length === 0 ? (
                  <EmptyState title="No cases in this dataset" />
                ) : (
                  <div className="rounded-lg border">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>#</TableHead>
                          <TableHead>Case</TableHead>
                          <TableHead>Rules</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {(cases.data?.items ?? []).map((row) => (
                          <TableRow key={row.id}>
                            <TableCell className="tabular">{row.position}</TableCell>
                            <TableCell>
                              <p className="text-sm">{row.name}</p>
                              <p className="font-mono text-xs text-muted-foreground">
                                {String(row.input?.query ?? "")}
                              </p>
                            </TableCell>
                            <TableCell className="font-mono text-xs">
                              {(row.rules ?? []).map((r) => (r as { type: string }).type).join(", ")}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}

                <p className="pt-2 text-xs font-medium uppercase tracking-wider text-muted-foreground">Runs</p>
                {(runs.data?.items ?? []).length === 0 ? (
                  <EmptyState title="No runs yet" />
                ) : (
                  <div className="rounded-lg border">
                    <Table>
                      <TableHeader>
                        <TableRow>
                          <TableHead>Status</TableHead>
                          <TableHead className="text-right">Pass rate</TableHead>
                          <TableHead className="text-right">Cases</TableHead>
                          <TableHead>Started</TableHead>
                        </TableRow>
                      </TableHeader>
                      <TableBody>
                        {(runs.data?.items ?? []).map((entry) => (
                          <TableRow key={entry.id}>
                            <TableCell><StatusBadge status={entry.status} /></TableCell>
                            <TableCell className="text-right tabular">{formatPercent(entry.pass_rate)}</TableCell>
                            <TableCell className="text-right tabular">
                              {entry.passed_cases}/{entry.total_cases}
                            </TableCell>
                            <TableCell className="text-xs text-muted-foreground">
                              {formatDateTime(entry.started_at)}
                            </TableCell>
                          </TableRow>
                        ))}
                      </TableBody>
                    </Table>
                  </div>
                )}

                {lastRun ? (
                  <Card>
                    <CardHeader>
                      <CardTitle>Last run result</CardTitle>
                      <CardDescription>
                        Pass rate {formatPercent((lastRun as { pass_rate?: number }).pass_rate)}
                        {(lastRun as { regression?: { regressed: boolean; delta: number } }).regression
                          ? ` · delta ${(lastRun as { regression?: { delta: number } }).regression!.delta}`
                          : ""}
                      </CardDescription>
                    </CardHeader>
                    <CardContent className="space-y-1 text-xs">
                      {(lastRun as { cases?: { name: string; passed: boolean }[] }).cases?.map((c) => (
                        <div key={c.name} className="flex items-center justify-between">
                          <span>{c.name}</span>
                          <StatusBadge status={c.passed ? "succeeded" : "failed"} />
                        </div>
                      ))}
                    </CardContent>
                  </Card>
                ) : null}
              </>
            ) : (
              <EmptyState title="Select a dataset" description="Choose a dataset to manage cases and runs." />
            )}
          </div>
        </div>
      </PageBody>

      <DatasetDialog open={datasetOpen} onClose={() => setDatasetOpen(false)} />
      <CaseDialog
        open={caseOpen}
        datasetId={selected}
        onClose={() => setCaseOpen(false)}
      />
      <Dialog open={!!runTarget} onClose={() => setRunTarget(null)}>
        <DialogTitle>Run evaluation</DialogTitle>
        <DialogContent>
          <form
            id="run-eval"
            className="space-y-4"
            onSubmit={async (event) => {
              event.preventDefault();
              setError(null);
              const form = new FormData(event.currentTarget);
              await run
                .mutateAsync({ datasetId: runTarget!.datasetId, agentId: String(form.get("agent")) })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Run failed"));
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="eval-agent">Agent (latest published version)</Label>
              <select
                id="eval-agent"
                name="agent"
                required
                className="w-full rounded-md border border-input bg-transparent px-2 py-1.5 text-sm"
              >
                {(agents.data?.items ?? []).map((agent) => (
                  <option key={agent.id} value={agent.id}>{agent.name}</option>
                ))}
              </select>
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setRunTarget(null)}>Cancel</Button>
          <Button type="submit" form="run-eval" disabled={run.isPending}>
            {run.isPending ? "Running…" : "Run"}
          </Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}

function DatasetDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>New dataset</DialogTitle>
      <DialogContent>
        <form
          id="dataset-create"
          className="space-y-4"
          onSubmit={async (event) => {
            event.preventDefault();
            setError(null);
            const form = new FormData(event.currentTarget);
            await api(path("evaluations", "datasets"), {
              method: "POST",
              body: { name: String(form.get("name")), description: String(form.get("description")) },
            })
              .then(() => {
                queryClient.invalidateQueries({ queryKey: ["eval-datasets"] });
                onClose();
              })
              .catch((err) => setError(err instanceof ApiError ? err.message : "Failed"));
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor="dataset-name">Name</Label>
            <Input id="dataset-name" name="name" required />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="dataset-desc">Description</Label>
            <Input id="dataset-desc" name="description" />
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </form>
      </DialogContent>
      <DialogActions>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button type="submit" form="dataset-create">Create</Button>
      </DialogActions>
    </Dialog>
  );
}

function CaseDialog({
  open, datasetId, onClose,
}: {
  open: boolean; datasetId: string | null; onClose: () => void;
}) {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>Add case</DialogTitle>
      <DialogContent>
        <form
          id="case-create"
          className="space-y-4"
          onSubmit={async (event) => {
            event.preventDefault();
            if (!datasetId) return;
            setError(null);
            const form = new FormData(event.currentTarget);
            await api(path("evaluations", "datasets", datasetId, "cases"), {
              method: "POST",
              body: {
                name: String(form.get("name")),
                input: { query: String(form.get("query")) },
                rules: JSON.parse(String(form.get("rules"))),
              },
            })
              .then(() => {
                queryClient.invalidateQueries({ queryKey: ["eval-cases", datasetId] });
                onClose();
              })
              .catch((err) =>
                setError(
                  err instanceof ApiError
                    ? err.message
                    : "Invalid rules JSON — use [{\"type\": \"contains_any\", \"values\": [\"...\"]}]",
                ),
              );
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor="case-name">Case name</Label>
            <Input id="case-name" name="name" required />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="case-query">Input query</Label>
            <Textarea id="case-query" name="query" required rows={2} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="case-rules">Rules (JSON array)</Label>
            <Textarea
              id="case-rules"
              name="rules"
              required
              rows={4}
              className="font-mono text-xs"
              defaultValue={'[{"type": "contains_any", "values": ["expected phrase"]}]'}
            />
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </form>
      </DialogContent>
      <DialogActions>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button type="submit" form="case-create">Add case</Button>
      </DialogActions>
    </Dialog>
  );
}
