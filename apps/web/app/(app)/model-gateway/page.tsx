"use client";

// Model gateway: providers, credential status and the model registry.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { StatusBadge } from "@/components/ui/badge";
import { Input, Label, Select } from "@/components/ui/input";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
  EmptyState, ErrorState, LoadingState,
} from "@/components/ui/table";
import { Dialog, DialogActions, DialogContent, DialogTitle } from "@/components/ui/dialog";
import { useOrg } from "@/hooks/use-org";
import { api, ApiError } from "@/lib/api";
import { formatCost, formatDateTime } from "@/lib/format";

interface Provider {
  id: string; provider_type: string; display_name: string;
  base_url?: string; status: string; created_at: string;
}
interface Credential {
  id: string; label: string; fingerprint: string; status: string; created_at: string;
}
interface Model {
  id: string; provider_type: string; model_id: string; display_name: string;
  input_price_per_mtok_minor: number; output_price_per_mtok_minor: number;
  pricing_version: number; status: string;
}

export default function ModelGatewayPage() {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [providerOpen, setProviderOpen] = useState(false);
  const [modelOpen, setModelOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api<{ items: Provider[] }>(path("model-providers")),
  });
  const models = useQuery({
    queryKey: ["models"],
    queryFn: () => api<{ items: Model[] }>(path("models")),
  });

  const revoke = useMutation({
    mutationFn: ({ providerId, credentialId }: { providerId: string; credentialId: string }) =>
      api(path("model-providers", providerId, "credentials", credentialId), { method: "DELETE" }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["providers"] }),
    onError: (err) => setError(err instanceof ApiError ? err.message : "Revoke failed"),
  });

  return (
    <AppShell>
      <PageHeader
        title="Model Gateway"
        description="Providers, credentials and versioned pricing"
        actions={
          <>
            <Button variant="outline" onClick={() => setModelOpen(true)}>Register model</Button>
            <Button onClick={() => setProviderOpen(true)}>Add provider</Button>
          </>
        }
      />
      <PageBody className="space-y-6">
        {error ? <ErrorState message={error} /> : null}

        <Card>
          <CardHeader>
            <CardTitle>Providers</CardTitle>
            <CardDescription>
              Credentials are AES-256-GCM encrypted; only fingerprints are shown.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4 px-4">
            {providers.isLoading ? (
              <LoadingState />
            ) : (providers.data?.items ?? []).length === 0 ? (
              <EmptyState title="No providers registered" description="Add a provider to configure models." />
            ) : (
              (providers.data?.items ?? []).map((provider) => (
                <ProviderCredentials
                  key={provider.id}
                  provider={provider}
                  onRevoke={revoke.mutate}
                />
              ))
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Model registry</CardTitle>
            <CardDescription>Pricing in minor units per 1M tokens (estimates)</CardDescription>
          </CardHeader>
          <CardContent className="px-0 pb-0">
            {models.isLoading ? (
              <LoadingState />
            ) : (models.data?.items ?? []).length === 0 ? (
              <div className="px-4 pb-4">
                <EmptyState title="No models registered" />
              </div>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Model</TableHead>
                    <TableHead>Provider</TableHead>
                    <TableHead className="text-right">Input / 1M</TableHead>
                    <TableHead className="text-right">Output / 1M</TableHead>
                    <TableHead>Pricing v</TableHead>
                    <TableHead>Status</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(models.data?.items ?? []).map((model) => (
                    <TableRow key={model.id}>
                      <TableCell className="font-mono text-xs">{model.model_id}</TableCell>
                      <TableCell>{model.provider_type}</TableCell>
                      <TableCell className="text-right tabular">
                        {formatCost(model.input_price_per_mtok_minor)}
                      </TableCell>
                      <TableCell className="text-right tabular">
                        {formatCost(model.output_price_per_mtok_minor)}
                      </TableCell>
                      <TableCell className="tabular">v{model.pricing_version}</TableCell>
                      <TableCell><StatusBadge status={model.status === "available" ? "active" : model.status} /></TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </CardContent>
        </Card>
      </PageBody>

      <AddProviderDialog
        open={providerOpen}
        onClose={() => setProviderOpen(false)}
        onDone={() => {
          queryClient.invalidateQueries({ queryKey: ["providers"] });
          setProviderOpen(false);
        }}
      />
      <AddModelDialog
        open={modelOpen}
        providers={providers.data?.items ?? []}
        onClose={() => setModelOpen(false)}
        onDone={() => {
          queryClient.invalidateQueries({ queryKey: ["models"] });
          setModelOpen(false);
        }}
      />
    </AppShell>
  );
}

function ProviderCredentials({
  provider, onRevoke,
}: {
  provider: Provider;
  onRevoke: (ids: { providerId: string; credentialId: string }) => void;
}) {
  const { path } = useOrg();
  const queryClient = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [apiKey, setApiKey] = useState("");
  const [label, setLabel] = useState("primary");
  const [revealed, setRevealed] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const credentials = useQuery({
    queryKey: ["credentials", provider.id],
    queryFn: () => api<{ items: Credential[] }>(path("model-providers", provider.id, "credentials")),
  });

  const create = useMutation({
    mutationFn: () =>
      api<{ id: string }>(path("model-providers", provider.id, "credentials"), {
        method: "POST",
        body: { label, api_key: apiKey },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["credentials", provider.id] });
      setAdding(false);
      setApiKey("");
    },
    onError: (err) => setError(err instanceof ApiError ? err.message : "Failed to store credential"),
  });

  return (
    <div className="rounded-md border p-3">
      <div className="flex items-center justify-between">
        <div>
          <p className="text-sm font-medium">{provider.display_name}</p>
          <p className="text-xs text-muted-foreground">
            {provider.provider_type}
            {provider.base_url ? ` · ${provider.base_url}` : ""}
          </p>
        </div>
        <Button size="sm" variant="secondary" onClick={() => setAdding((v) => !v)}>
          {adding ? "Cancel" : "Add credential"}
        </Button>
      </div>

      {adding ? (
        <form
          className="mt-3 flex flex-wrap items-end gap-2"
          onSubmit={async (event) => {
            event.preventDefault();
            setError(null);
            const created = await create.mutateAsync().catch(() => null);
            if (created) {
              setRevealed(apiKey);
              setApiKey("");
            }
          }}
        >
          <div className="space-y-1">
            <Label htmlFor={`label-${provider.id}`}>Label</Label>
            <Input id={`label-${provider.id}`} className="w-40" value={label} onChange={(e) => setLabel(e.target.value)} />
          </div>
          <div className="space-y-1">
            <Label htmlFor={`key-${provider.id}`}>API key</Label>
            <Input
              id={`key-${provider.id}`}
              type="password"
              className="w-72 font-mono"
              required
              minLength={8}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </div>
          <Button type="submit" size="sm" disabled={create.isPending}>
            {create.isPending ? "Storing…" : "Store"}
          </Button>
          {error ? <p className="w-full text-xs text-destructive">{error}</p> : null}
        </form>
      ) : null}

      {revealed ? (
        <p className="mt-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs">
          Copy this key now — it is shown once: <span className="font-mono">{revealed}</span>
          <button className="ml-2 text-primary underline" onClick={() => setRevealed(null)}>dismiss</button>
        </p>
      ) : null}

      <div className="mt-3 space-y-1.5">
        {credentials.isLoading ? (
          <LoadingState label="Loading credentials…" />
        ) : (credentials.data?.items ?? []).length === 0 ? (
          <p className="text-xs text-muted-foreground">No active credentials.</p>
        ) : (
          (credentials.data?.items ?? []).map((credential) => (
            <div key={credential.id} className="flex items-center justify-between rounded-md border px-3 py-1.5">
              <div className="flex items-center gap-2 text-xs">
                <span>{credential.label || "credential"}</span>
                <span className="font-mono text-muted-foreground">{credential.fingerprint}</span>
                <span className="text-muted-foreground">{formatDateTime(credential.created_at)}</span>
              </div>
              <Button
                size="sm"
                variant="destructive"
                onClick={() => onRevoke({ providerId: provider.id, credentialId: credential.id })}
              >
                Revoke
              </Button>
            </div>
          ))
        )}
      </div>
    </div>
  );
}

function AddProviderDialog({
  open, onClose, onDone,
}: {
  open: boolean; onClose: () => void; onDone: () => void;
}) {
  const { path } = useOrg();
  const [type, setType] = useState("mock");
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [error, setError] = useState<string | null>(null);

  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>Add provider</DialogTitle>
      <DialogContent>
        <form
          id="add-provider"
          className="space-y-4"
          onSubmit={async (event) => {
            event.preventDefault();
            setError(null);
            try {
              await api(path("model-providers"), {
                method: "POST",
                body: { provider_type: type, display_name: name || undefined, base_url: baseUrl || undefined },
              });
              onDone();
            } catch (err) {
              setError(err instanceof ApiError ? err.message : "Failed to add provider");
            }
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor="provider-type">Type</Label>
            <Select id="provider-type" value={type} onChange={(e) => setType(e.target.value)}>
              <option value="mock">mock (deterministic)</option>
              <option value="openai">openai</option>
              <option value="anthropic">anthropic</option>
              <option value="google">google</option>
              <option value="compatible">openai-compatible</option>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="provider-name">Display name</Label>
            <Input id="provider-name" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          {type === "compatible" ? (
            <div className="space-y-1.5">
              <Label htmlFor="provider-url">Base URL</Label>
              <Input id="provider-url" required value={baseUrl} onChange={(e) => setBaseUrl(e.target.value)} />
            </div>
          ) : null}
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </form>
      </DialogContent>
      <DialogActions>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button type="submit" form="add-provider">Add</Button>
      </DialogActions>
    </Dialog>
  );
}

function AddModelDialog({
  open, providers, onClose, onDone,
}: {
  open: boolean;
  providers: Provider[];
  onClose: () => void;
  onDone: () => void;
}) {
  const { path } = useOrg();
  const [providerId, setProviderId] = useState("");
  const [modelId, setModelId] = useState("");
  const [inputPrice, setInputPrice] = useState("0");
  const [outputPrice, setOutputPrice] = useState("0");
  const [error, setError] = useState<string | null>(null);

  return (
    <Dialog open={open} onClose={onClose}>
      <DialogTitle>Register model</DialogTitle>
      <DialogContent>
        <form
          id="add-model"
          className="space-y-4"
          onSubmit={async (event) => {
            event.preventDefault();
            setError(null);
            try {
              await api(path("models"), {
                method: "POST",
                body: {
                  provider_id: providerId,
                  model_id: modelId,
                  input_price_per_mtok_minor: Number(inputPrice),
                  output_price_per_mtok_minor: Number(outputPrice),
                },
              });
              onDone();
            } catch (err) {
              setError(err instanceof ApiError ? err.message : "Failed to register model");
            }
          }}
        >
          <div className="space-y-1.5">
            <Label htmlFor="model-provider">Provider</Label>
            <Select id="model-provider" required value={providerId} onChange={(e) => setProviderId(e.target.value)}>
              <option value="">Select provider…</option>
              {providers.map((p) => (
                <option key={p.id} value={p.id}>{p.display_name}</option>
              ))}
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="model-id">Model identifier</Label>
            <Input id="model-id" required value={modelId} onChange={(e) => setModelId(e.target.value)} />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-1.5">
              <Label htmlFor="input-price">Input price / 1M (minor)</Label>
              <Input id="input-price" type="number" min="0" value={inputPrice} onChange={(e) => setInputPrice(e.target.value)} />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="output-price">Output price / 1M (minor)</Label>
              <Input id="output-price" type="number" min="0" value={outputPrice} onChange={(e) => setOutputPrice(e.target.value)} />
            </div>
          </div>
          {error ? <p className="text-xs text-destructive">{error}</p> : null}
        </form>
      </DialogContent>
      <DialogActions>
        <Button variant="outline" onClick={onClose}>Cancel</Button>
        <Button type="submit" form="add-model">Register</Button>
      </DialogActions>
    </Dialog>
  );
}
