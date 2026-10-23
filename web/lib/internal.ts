import "server-only";
import { apiBaseUrl } from "./api";

/**
 * Calls the internal Go API with the shared X-Internal-Token and, when given,
 * the user's X-Session-Token. Returns the raw Response; callers decide on status.
 */
export async function internalFetch(
  path: string,
  init: { method?: string; body?: unknown; sessionToken?: string } = {},
): Promise<Response> {
  const token = process.env.INTERNAL_API_TOKEN;
  if (!token) throw new Error("internal api not configured: INTERNAL_API_TOKEN missing");
  const headers: Record<string, string> = { "x-internal-token": token, accept: "application/json" };
  if (init.sessionToken) headers["x-session-token"] = init.sessionToken;
  let body: string | undefined;
  if (init.body !== undefined) {
    headers["content-type"] = "application/json";
    body = JSON.stringify(init.body);
  }
  return fetch(apiBaseUrl() + path, {
    method: init.method ?? "GET",
    headers,
    body,
    cache: "no-store",
    signal: AbortSignal.timeout(10_000),
  });
}

export type Role = "user" | "moderator" | "admin";
export interface SessionUser {
  id: number;
  name: string;
  role: Role;
  banned: boolean;
}
