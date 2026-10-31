"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { api, ApiError } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input, Label } from "@/components/ui/input";

export default function SignupPage() {
  const router = useRouter();
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [organizationName, setOrganizationName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api("/v1/auth/signup", {
        method: "POST",
        body: { name, email, password, organization_name: organizationName },
      });
      router.push("/");
      router.refresh();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Unable to create the account");
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
            <h1 className="text-xl font-semibold tracking-tight">Create your workspace</h1>
            <p className="mt-1 text-xs text-muted-foreground">
              Start governing your AI agents
            </p>
          </div>
        </div>

        <form onSubmit={submit} className="space-y-4 rounded-lg border bg-card p-6">
          <div className="space-y-1.5">
            <Label htmlFor="name">Your name</Label>
            <Input id="name" required value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="email">Work email</Label>
            <Input id="email" type="email" required value={email} onChange={(e) => setEmail(e.target.value)} />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              required
              minLength={10}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <p className="text-[10px] text-muted-foreground">
              At least 10 characters with letters and digits.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="org">Organization name</Label>
            <Input
              id="org"
              required
              minLength={2}
              value={organizationName}
              onChange={(e) => setOrganizationName(e.target.value)}
              placeholder="Acme Technologies"
            />
          </div>
          {error ? (
            <p className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {error}
            </p>
          ) : null}
          <Button type="submit" className="w-full" disabled={busy}>
            {busy ? "Creating…" : "Create account"}
          </Button>
          <p className="text-center text-xs text-muted-foreground">
            Already registered?{" "}
            <a href="/login" className="text-primary underline-offset-4 hover:underline">
              Sign in
            </a>
          </p>
        </form>
      </div>
    </main>
  );
}
