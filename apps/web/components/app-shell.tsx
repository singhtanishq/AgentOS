// App shell: sidebar navigation, org switcher, user menu.

"use client";

import { useRouter } from "next/navigation";
import { useQueryClient } from "@tanstack/react-query";
import {
  Activity,
  BadgeCheck,
  Bot,
  Boxes,
  ClipboardCheck,
  Coins,
  Database,
  FileClock,
  Gauge,
  Globe2,
  KeyRound,
  LayoutDashboard,
  LogOut,
  Plug,
  Rocket,
  Settings,
  ShieldCheck,
  Users,
} from "lucide-react";
import { api } from "@/lib/api";
import { useSession, setStoredOrgId } from "@/components/session-context";
import { cn } from "@/lib/utils";

interface NavSection {
  label: string;
  items: { href: string; label: string; icon: React.ComponentType<{ className?: string }> }[];
}

const NAV: NavSection[] = [
  {
    label: "Workspace",
    items: [
      { href: "/", label: "Overview", icon: LayoutDashboard },
      { href: "/agents", label: "Agents", icon: Bot },
      { href: "/deployments", label: "Deployments", icon: Rocket },
    ],
  },
  {
    label: "Execution",
    items: [
      { href: "/executions", label: "Executions", icon: Activity },
      { href: "/traces", label: "Traces", icon: Gauge },
      { href: "/approvals", label: "Approvals", icon: ClipboardCheck },
    ],
  },
  {
    label: "AI Infrastructure",
    items: [
      { href: "/model-gateway", label: "Model Gateway", icon: Boxes },
      { href: "/tools", label: "Tool Registry", icon: Database },
      { href: "/mcp", label: "MCP Connections", icon: Plug },
    ],
  },
  {
    label: "Security & Governance",
    items: [
      { href: "/identities", label: "Agent Identity", icon: BadgeCheck },
      { href: "/policies", label: "Policies", icon: ShieldCheck },
      { href: "/audit", label: "Audit Logs", icon: FileClock },
    ],
  },
  {
    label: "Intelligence",
    items: [
      { href: "/evaluations", label: "Evaluations", icon: Gauge },
      { href: "/usage", label: "Usage & Costs", icon: Coins },
      { href: "/alerts", label: "Alerts", icon: Activity },
    ],
  },
  {
    label: "Organization",
    items: [
      { href: "/members", label: "Members", icon: Users },
      { href: "/api-keys", label: "API Keys", icon: KeyRound },
      { href: "/webhooks", label: "Webhooks", icon: Globe2 },
      { href: "/settings", label: "Settings", icon: Settings },
    ],
  },
];

export function AppShell({ children }: { children: React.ReactNode }) {
  const { user, organizations, activeOrg } = useSession();
  const router = useRouter();
  const queryClient = useQueryClient();

  async function logout() {
    await api("/v1/auth/logout", { method: "POST" });
    queryClient.clear();
    router.push("/login");
  }

  function switchOrg(orgId: string) {
    setStoredOrgId(orgId);
    // Reload so all org-scoped queries refetch with the new context.
    window.location.reload();
  }

  return (
    <div className="flex min-h-dvh">
      <aside className="hidden w-60 shrink-0 flex-col border-r bg-sidebar text-sidebar-foreground md:flex">
        <div className="flex h-14 items-center gap-2 border-b px-4">
          <div className="flex size-7 items-center justify-center rounded-md bg-primary font-mono text-sm font-bold text-primary-foreground">
            A
          </div>
          <span className="text-sm font-semibold tracking-tight">AgentOS</span>
        </div>

        <div className="border-b px-3 py-2.5">
          <label className="text-[10px] uppercase tracking-wider text-muted-foreground">
            Organization
          </label>
          <select
            className="mt-1 w-full rounded-md border border-input bg-transparent px-2 py-1.5 text-xs"
            value={activeOrg.id}
            onChange={(e) => switchOrg(e.target.value)}
          >
            {organizations.map((org) => (
              <option key={org.id} value={org.id} className="bg-popover">
                {org.name}
              </option>
            ))}
          </select>
        </div>

        <nav className="flex-1 overflow-y-auto px-2 py-3">
          {NAV.map((section) => (
            <div key={section.label} className="mb-4">
              <p className="mb-1 px-2 text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
                {section.label}
              </p>
              {section.items.map((item) => (
                <a
                  key={item.href}
                  href={item.href}
                  className="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
                >
                  <item.icon className="size-4" />
                  {item.label}
                </a>
              ))}
            </div>
          ))}
        </nav>

        <div className="border-t p-3">
          <div className="flex items-center gap-2">
            <div className="flex size-7 items-center justify-center rounded-full bg-secondary text-xs font-medium">
              {user.name.slice(0, 1).toUpperCase()}
            </div>
            <div className="min-w-0 flex-1">
              <p className="truncate text-xs font-medium">{user.name}</p>
              <p className="truncate text-[10px] text-muted-foreground">{user.email}</p>
            </div>
            <button
              onClick={logout}
              title="Log out"
              className="rounded-md p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              <LogOut className="size-4" />
            </button>
          </div>
        </div>
      </aside>

      <main className={cn("min-w-0 flex-1 overflow-x-hidden")}>{children}</main>
    </div>
  );
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: string;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b px-6 py-4">
      <div>
        <h1 className="text-lg font-semibold tracking-tight">{title}</h1>
        {description ? (
          <p className="mt-0.5 text-xs text-muted-foreground">{description}</p>
        ) : null}
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </div>
  );
}

export function PageBody({
  children, className,
}: {
  children: React.ReactNode; className?: string;
}) {
  return <div className={"p-6 " + (className ?? "")}>{children}</div>;
}
