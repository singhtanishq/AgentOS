// Typed API client for the AgentOS control plane.
//
// Browser requests use session cookies (credentials: include); the SDK
// path uses bearer API keys. All errors are normalized to ApiError.

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public requestId?: string,
  ) {
    super(message);
  }
}

const API_BASE = (globalThis as { NEXT_PUBLIC_API_URL?: string })
  .NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

interface RequestOptions {
  method?: "GET" | "POST" | "PATCH" | "PUT" | "DELETE";
  body?: unknown;
  params?: Record<string, string | number | undefined>;
}

export async function api<T = unknown>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const url = new URL(`${API_BASE}${path}`);
  if (options.params) {
    for (const [key, value] of Object.entries(options.params)) {
      if (value !== undefined && value !== "") {
        url.searchParams.set(key, String(value));
      }
    }
  }
  const response = await fetch(url.toString(), {
    method: options.method ?? "GET",
    headers: {
      "Content-Type": "application/json",
      ...(options.body !== undefined ? { "Content-Type": "application/json" } : {}),
    },
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
    credentials: "include",
  });

  if (response.status === 204) {
    return {} as T;
  }
  const raw = await response.text();
  let body: unknown = {};
  if (raw) {
    try {
      body = JSON.parse(raw);
    } catch {
      body = {};
    }
  }

  if (!response.ok) {
    const err = (body as { error?: { code?: string; message?: string; request_id?: string } })
      .error;
    throw new ApiError(
      response.status,
      err?.code ?? "unknown",
      err?.message ?? `Request failed with status ${response.status}`,
      err?.request_id,
    );
  }
  return body as T;
}

export function apiPath(orgId: string, ...segments: string[]): string {
  return `/v1/organizations/${orgId}/${segments.join("/")}`;
}
