import "server-only";
import { cookies } from "next/headers";
import { cache } from "react";
import { internalFetch, type SessionUser } from "./internal";
import { isPlausibleSessionToken, SESSION_COOKIE } from "./oauth";

/** The raw session token from the request cookie, if it looks like one. */
export async function getSessionToken(): Promise<string | null> {
  const token = (await cookies()).get(SESSION_COOKIE)?.value;
  return isPlausibleSessionToken(token) ? token : null;
}

/**
 * The logged-in user, or null. Deduplicated per request. A stale cookie is not
 * cleared here (server components may not set cookies); it just reads as logged out.
 */
export const getCurrentUser = cache(async (): Promise<SessionUser | null> => {
  const token = await getSessionToken();
  if (!token) return null;
  let res: Response;
  try {
    res = await internalFetch("/api/internal/me", { sessionToken: token });
  } catch {
    console.error("session lookup failed: network");
    return null;
  }
  if (res.status === 401) return null;
  if (!res.ok) {
    console.error(`session lookup failed: HTTP ${res.status}`);
    return null;
  }
  let u: Partial<SessionUser>;
  try {
    u = (await res.json()) as Partial<SessionUser>;
  } catch {
    return null;
  }
  if (!u || typeof u.id !== "number" || typeof u.name !== "string" || typeof u.role !== "string") return null;
  return { id: u.id, name: u.name, role: u.role, banned: u.banned === true };
});
