import * as React from "react";
import { cn } from "@/lib/utils";

type Tone = "neutral" | "success" | "warning" | "danger" | "info" | "primary";

const toneClasses: Record<Tone, string> = {
  neutral: "bg-secondary text-secondary-foreground",
  success: "bg-emerald-500/15 text-emerald-500",
  warning: "bg-amber-500/15 text-amber-500",
  danger: "bg-red-500/15 text-red-500",
  info: "bg-sky-500/15 text-sky-500",
  primary: "bg-primary/15 text-primary",
};

export function Badge({
  tone = "neutral",
  className,
  ...props
}: React.HTMLAttributes<HTMLSpanElement> & { tone?: Tone }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium",
        toneClasses[tone],
        className,
      )}
      {...props}
    />
  );
}

const STATUS_TONES: Record<string, Tone> = {
  // executions
  queued: "neutral", running: "info", waiting_approval: "warning",
  succeeded: "success", failed: "danger", cancelled: "neutral", timed_out: "danger",
  // agents
  draft: "neutral", active: "success", paused: "warning", suspended: "danger", archived: "neutral",
  // versions
  published: "success",
  // deployments
  inactive: "neutral", rolled_back: "warning",
  // identities / keys
  revoked: "danger",
  // memberships
  owner: "primary", admin: "info", developer: "info", operator: "info",
  auditor: "neutral", member: "neutral",
  // approvals
  pending: "warning", approved: "success", rejected: "danger", expired: "neutral",
  disabled: "neutral",
};

export function StatusBadge({ status }: { status: string }) {
  const tone = STATUS_TONES[status] ?? "neutral";
  return <Badge tone={tone}>{status.replace(/_/g, " ")}</Badge>;
}
