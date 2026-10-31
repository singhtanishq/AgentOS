"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";

export default function LoginPage() {
  const router = useRouter();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api("/v1/auth/login", { method: "POST", body: { email, password } });
      router.push("/");
      router.refresh();
    } catch (err) {
      setError(
        err instanceof ApiError ? err.message : "Unable to sign in — try again",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="flex min-h-dvh items-center justify-center p-6">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3">
          <div className="flex size-10 items-center justify-center rounded-lg bg-primary font-mono text-lg font-bold text-primary-foreground">
            A
          </div>
          <div className="text-center">
            <h1 className="text-xl font-semibold tracking-tight">Sign in to AgentOS</h1>
            <p className="mt-1 text-xs text-muted-foreground">
              The enterprise control plane for AI agents
            </p>
          </div>
        </div>

        <form onSubmit={submit} className="space-y-4 rounded-lg border bg-card p-6">
          <div className="space-y-1.5">
            <Label htmlFor="email">Email</Label>
            <Input
              id="email"
              type="email"
              required
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@company.com"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              required
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="••••••••••"
            />
          </div>
          {error ? (
            <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {error}
            </p>
          ) : null}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
          <p className="text-center text-xs text-muted-foreground">
            No account?{" "}
            <a href="/signup" className="text-primary underline-offset-4 hover:underline">
              Create one
            </a>
          </p>
        </form>
      </div>
    </main>
  );
}
