"use client";

// Organization settings: profile and security overview.

import { useQuery } from "@tanstack/react-query";
import { AppShell, PageBody, PageHeader } from "@/components/app-shell";
import { Card, CardContent, CardHeader, CardTitle, CardDescription } from "@/components/ui/card";
import { useSession } from "@/components/session-context";
import { Badge } from "@/components/ui/badge";
import { LoadingState } from "@/components/ui/table";

export default function SettingsPage() {
  const { activeOrg, user } = useSession();
  const session = useQuery({
    queryKey: ["session"],
    queryFn: () => fetch("/v1/auth/session", { credentials: "include" }).then((r) => r.json()),
    staleTime: 60_000,
  });

  return (
    <AppShell>
      <PageHeader title="Settings" description="Organization profile and security posture" />
      <PageBody className="grid gap-6 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>Organization</CardTitle>
            <CardDescription>Workspace identity</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Name</span>
              <span>{activeOrg.name}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Slug</span>
              <span className="font-mono text-xs">{activeOrg.slug}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Your role</span>
              <Badge tone="primary">{activeOrg.role}</Badge>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Signed in as</span>
              <span>{user.email}</span>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Security posture</CardTitle>
            <CardDescription>Active platform controls</CardDescription>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            {[
              "Argon2id password hashing",
              "HTTP-only session cookies with sliding renewal",
              "Row-level security on every tenant table",
              "AES-256-GCM provider credential encryption",
              "OPA fail-closed tool authorization",
              "Append-only audit and decision logs",
              "HMAC-signed webhook deliveries",
            ].map((control) => (
              <div key={control} className="flex items-center gap-2">
                <span className="size-1.5 rounded-full bg-emerald-500" />
                {control}
              </div>
            ))}
            {session.isLoading ? <LoadingState label="Loading…" /> : null}
          </CardContent>
        </Card>
      </PageBody>
    </AppShell>
  );
}
