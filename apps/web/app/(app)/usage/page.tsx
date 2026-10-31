"use client";

// Usage & costs: analytics, budgets and cost breakdown.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { Input, Label, Select } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatCost, formatNumber } from "@/lib/format";

interface UsageSummary {
  by_day: { day: string; input_tokens: number; output_tokens: number; cost_estimate_minor: number }[];
  by_agent: { agent_id: string; executions: number; total_tokens: number; cost_estimate_minor: number }[];
}
interface BudgetRow {
  id: string; agent_id?: string; name: string; period: string;
  limit_minor: number; spent_minor: number; enforcement_action: string; status: string;
}

export default function UsagePage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [days, setDays] = useState(30);
  const [budgetOpen, setBudgetOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const summary = useQuery({
    queryKey: ["usage", days],
    queryFn: () => api<UsageSummary>(path("usage"), { params: { days } }),
  });
  const budgets = useQuery({
    queryKey: ["budgets"],
    queryFn: () => api<{ items: BudgetRow[] }>(path("budgets")),
  });

  const createBudget = useMutation({
    mutationFn: (body: Record<string, unknown>) => api(path("budgets"), { method: "POST", body }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["budgets"] });
      setBudgetOpen(false);
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to create budget"),
  });

  const deleteBudget = useMutation({
    mutationFn: (id: string) => api(path("budgets", id), { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["budgets"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to delete budget"),
  });

  const totalCost = (summary.data?.by_day ?? []).reduce((sum, d) => sum + d.cost_estimate_minor, 0);
  const totalTokens = (summary.data?.by_day ?? []).reduce(
    (sum, d) => sum + d.input_tokens + d.output_tokens,
    0,
  );
  const maxDayCost = Math.max(1, ...(summary.data?.by_day ?? []).map((d) => d.cost_estimate_minor));

  return (
    <AppShell>
      <PageHeader
        title="Usage & Costs"
        description="Token analytics, spend and budgets (estimates from versioned pricing)"
        actions={<Button onClick={() => setBudgetOpen(true)}>New budget</Button>}
      />
      <PageBody className="space-y-6">
        {error ? <ErrorState message={error} /> : null}

        <div className="flex items-center gap-2">
          <Select
            className="max-w-32"
            value={String(days)}
            onChange={(e) => setDays(Number(e.target.value))}
          >
            <option value="7">Last 7 days</option>
            <option value="30">Last 30 days</option>
            <option value="90">Last 90 days</option>
          </Select>
        </div>

        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <Card>
            <CardHeader><CardDescription>Total spend</CardDescription></CardHeader>
            <CardContent className="text-xl tabular">{formatCost(totalCost)}</CardContent>
          </Card>
          <Card>
            <CardHeader><CardDescription>Total tokens</CardDescription></CardHeader>
            <CardContent className="text-xl tabular">{formatNumber(totalTokens)}</CardContent>
          </Card>
          <Card>
            <CardHeader><CardDescription>Active budgets</CardDescription></CardHeader>
            <CardContent className="text-xl tabular">
              {(budgets.data?.items ?? []).filter((b) => b.status === "active").length}
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardDescription>Blocking budgets</CardDescription></CardHeader>
            <CardContent className="text-xl tabular">
              {(budgets.data?.items ?? []).filter((b) => b.enforcement_action === "block").length}
            </CardContent>
          </Card>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Daily spend</CardTitle>
            <CardDescription>Estimated cost per day (minor units)</CardDescription>
          </CardHeader>
          <CardContent>
            {summary.isLoading ? (
              <LoadingState />
            ) : (summary.data?.by_day ?? []).length === 0 ? (
              <EmptyState title="No usage recorded in this window" />
            ) : (
              <div className="flex h-40 items-end gap-1.5">
                {(summary.data?.by_day ?? []).map((day) => (
                  <div key={day.day} className="group relative flex-1">
                    <div
                      className="w-full rounded-t bg-primary/70"
                      style={{ height: `${Math.max(2, (day.cost_estimate_minor / maxDayCost) * 150)}px` }}
                      title={`${day.day}: ${formatCost(day.cost_estimate_minor)}`}
                    />
                  </div>
                ))}
              </div>
            )}
          </CardContent>
        </Card>

        <div className="grid gap-6 xl:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>Cost by agent</CardTitle>
              <CardDescription>Top consumers in the window</CardDescription>
            </CardHeader>
            <CardContent className="px-0 pb-0">
              {(summary.data?.by_agent ?? []).length === 0 ? (
                <div className="px-4 pb-4"><EmptyState title="No agent usage" /></div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Agent</TableHead>
                      <TableHead className="text-right">Executions</TableHead>
                      <TableHead className="text-right">Tokens</TableHead>
                      <TableHead className="text-right">Cost</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(summary.data?.by_agent ?? []).map((agent) => (
                      <TableRow key={agent.agent_id}>
                        <TableCell>
                          <a href={`/agents/${agent.agent_id}`} className="font-mono text-xs hover:underline">
                            {agent.agent_id.slice(0, 8)}…
                          </a>
                        </TableCell>
                        <TableCell className="text-right tabular">{formatNumber(agent.executions)}</TableCell>
                        <TableCell className="text-right tabular">{formatNumber(agent.total_tokens)}</TableCell>
                        <TableCell className="text-right tabular">{formatCost(agent.cost_estimate_minor)}</TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Budgets</CardTitle>
              <CardDescription>Blocking budgets refuse new executions once exhausted</CardDescription>
            </CardHeader>
            <CardContent className="px-0 pb-0">
              {(budgets.data?.items ?? []).length === 0 ? (
                <div className="px-4 pb-4">
                  <EmptyState title="No budgets configured" />
                </div>
              ) : (
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Name</TableHead>
                      <TableHead>Period</TableHead>
                      <TableHead className="text-right">Spent</TableHead>
                      <TableHead>Enforcement</TableHead>
                      <TableHead />
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(budgets.data?.items ?? []).map((budget) => {
                      const pct = Math.min(100, Math.round((budget.spent_minor / budget.limit_minor) * 100));
                      return (
                        <TableRow key={budget.id}>
                          <TableCell>{budget.name}</TableCell>
                          <TableCell>{budget.period}</TableCell>
                          <TableCell className="text-right tabular">
                            {formatCost(budget.spent_minor)} / {formatCost(budget.limit_minor)}
                            <div className="mt-1 h-1.5 w-24 rounded bg-muted">
                              <div
                                className={pct >= 100 ? "h-1.5 rounded bg-red-500" : pct >= 80 ? "h-1.5 rounded bg-amber-500" : "h-1.5 rounded bg-emerald-500"}
                                style={{ width: `${pct}%` }}
                              />
                            </div>
                          </TableCell>
                          <TableCell className="text-xs">{budget.enforcement_action}</TableCell>
                          <TableCell className="text-right">
                            <Button
                              size="sm"
                              variant="destructive"
                              onClick={() => {
                                if (window.confirm(`Delete budget "${budget.name}"?`)) {
                                  deleteBudget.mutate(budget.id);
                                }
                              }}
                            >
                              Delete
                            </Button>
                          </TableCell>
                        </TableRow>
                      );
                    })}
                  </TableBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </div>
      </PageBody>

      <Dialog open={budgetOpen} onClose={() => setBudgetOpen(false)}>
        <DialogTitle>New budget</DialogTitle>
        <DialogContent>
          <form
            id="budget-create"
            className="space-y-4"
            onSubmit={async (event) => {
              event.preventDefault();
              setError(null);
              const form = new FormData(event.currentTarget);
              await createBudget
                .mutateAsync({
                  name: String(form.get("name")),
                  period: String(form.get("period")),
                  limit_minor: Number(form.get("limit")),
                  enforcement_action: String(form.get("enforcement")),
                })
                .catch((err) => setError(err instanceof ApiError ? err.message : "Failed"));
            }}
          >
            <div className="space-y-1.5">
              <Label htmlFor="budget-name">Name</Label>
              <Input id="budget-name" name="name" required />
            </div>
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="budget-period">Period</Label>
                <Select id="budget-period" name="period" defaultValue="monthly">
                  <option value="daily">daily</option>
                  <option value="weekly">weekly</option>
                  <option value="monthly">monthly</option>
                  <option value="total">total</option>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="budget-limit">Limit (minor units)</Label>
                <Input id="budget-limit" name="limit" type="number" min="1" required defaultValue="1000" />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="budget-enforcement">Enforcement</Label>
              <Select id="budget-enforcement" name="enforcement" defaultValue="alert_only">
                <option value="alert_only">alert only</option>
                <option value="block">block executions</option>
              </Select>
            </div>
            {error ? <p className="text-xs text-destructive">{error}</p> : null}
          </form>
        </DialogContent>
        <DialogActions>
          <Button variant="outline" onClick={() => setBudgetOpen(false)}>Cancel</Button>
          <Button type="submit" form="budget-create">Create</Button>
        </DialogActions>
      </Dialog>
    </AppShell>
  );
}
