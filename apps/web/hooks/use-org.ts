"use client";

// Organization-scoped route helpers.

import { useSession } from "@/components/session-context";
import { apiPath } from "@/lib/api";

export function useOrg() {
  const { activeOrg } = useSession();
  return {
    orgId: activeOrg.id,
    org: activeOrg,
    path: (...segments: string[]) => apiPath(activeOrg.id, ...segments),
  };
}
