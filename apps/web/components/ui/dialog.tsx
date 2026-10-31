"use client";

import * as React from "react";
import { cn } from "@/lib/utils";

export function Dialog({
  open, onClose, children, className,
}: {
  open: boolean;
  onClose: () => void;
  children: React.ReactNode;
  className?: string;
}) {
  React.useEffect(() => {
    if (!open) return;
    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        role="dialog"
        aria-modal="true"
        className={cn(
          "w-full max-w-lg rounded-lg border bg-popover text-popover-foreground shadow-xl",
          className,
        )}
      >
        {children}
      </div>
    </div>
  );
}

export function DialogTitle({ children }: { children: React.ReactNode }) {
  return <div className="border-b px-5 py-3.5 text-sm font-semibold">{children}</div>;
}

export function DialogContent({ children }: { children: React.ReactNode }) {
  return <div className="max-h-[70vh] overflow-y-auto px-5 py-4">{children}</div>;
}

export function DialogActions({ children }: { children: React.ReactNode }) {
  return <div className="flex justify-end gap-2 border-t px-5 py-3">{children}</div>;
}
