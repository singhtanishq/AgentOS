"use client";

// Root providers: TanStack Query + session gate with client-side redirect.

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { SessionProvider } from "@/components/session-context";

export function Providers({ children }: { children: React.ReactNode }) {
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            staleTime: 10_000,
            retry: (failureCount, error) => {
              const status = (error as { status?: number }).status;
              if (status === 401 || status === 403 || status === 404) return false;
              return failureCount < 2;
            },
          },
        },
      }),
  );
  const router = useRouter();

  return (
    <QueryClientProvider client={queryClient}>
      <SessionProvider onUnauthorized={() => router.replace("/login")}>
        {children}
      </SessionProvider>
    </QueryClientProvider>
  );
}
