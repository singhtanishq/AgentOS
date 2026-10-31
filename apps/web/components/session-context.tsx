"use client";

// Session context: the authenticated user and their organizations.

import { useQuery } from "@tanstack/react-query";
import { createContext, useContext } from "react";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api";

export interface SessionUser {
  id: string;
  name: string;
  email: string;
}

export interface SessionOrganization {
  id: string;
  name: string;
  slug: string;
  role: string;
}

interface SessionData {
  user: SessionUser;
  organizations: SessionOrganization[];
}

interface SessionContextValue {
  user: SessionUser;
  organizations: SessionOrganization[];
  activeOrg: SessionOrganization;
}

const SessionContext = createContext<SessionContextValue | null>(null);

const ACTIVE_ORG_KEY = "agentos.activeOrgId";

export function getStoredOrgId(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(ACTIVE_ORG_KEY);
}

export function setStoredOrgId(orgId: string): void {
  window.localStorage.setItem(ACTIVE_ORG_KEY, orgId);
}

async function fetchSession(): Promise<SessionData> {
  return api<SessionData>("/v1/auth/session");
}

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const query = useQuery({
    queryKey: ["session"],
    queryFn: fetchSession,
    retry: false,
    staleTime: 60_000,
  });

  return <SessionGate query={query}>{children}</SessionGate>;
}

function SessionGate({
  query,
  children,
}: {
  query: ReturnType<typeof useQuery<SessionData>>;
  children: React.ReactNode;
}) {
  const router = useRouter();
  if (query.isError) {
    router.replace("/login");
    return null;
  }
  if (!query.data) {
    return (
      <div className="flex min-h-dvh items-center justify-center text-sm text-muted-foreground">
        Loading workspace…
      </div>
    );
  }

  const organizations = query.data.organizations ?? [];
  if (organizations.length === 0) {
    return (
      <div className="flex min-h-dvh items-center justify-center p-8 text-sm text-muted-foreground">
        No organization available — create one to continue.
      </div>
    );
  }

  const stored = getStoredOrgId();
  const activeOrg: SessionOrganization =
    organizations.find((o) => o.id === stored) ?? organizations[0]!;

  return (
    <SessionContext.Provider
      value={{ user: query.data.user, organizations, activeOrg }}
    >
      {children}
    </SessionContext.Provider>
  );
}

export function useSession(): SessionContextValue {
  const value = useContext(SessionContext);
  if (!value) {
    throw new Error("useSession must be used inside SessionProvider");
  }
  return value;
}
