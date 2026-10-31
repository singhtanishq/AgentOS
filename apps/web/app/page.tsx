export default function Home() {
  return (
    <main className="flex min-h-dvh flex-col items-center justify-center gap-6 p-8">
      <div className="flex items-center gap-3">
        <div className="flex size-9 items-center justify-center rounded-md bg-primary font-mono text-lg font-bold text-primary-foreground">
          A
        </div>
        <span className="text-2xl font-semibold tracking-tight">AgentOS</span>
      </div>
      <p className="max-w-md text-center text-sm text-muted-foreground">
        The enterprise control plane for AI agents. The dashboard is under
        active construction — authentication lands with milestone 04.
      </p>
      <code className="rounded-md border bg-muted px-3 py-1.5 font-mono text-xs text-muted-foreground">
        Milestone 01 — foundation scaffold
      </code>
    </main>
  );
}
