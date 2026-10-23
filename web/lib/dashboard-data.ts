import "server-only";
import { internalFetch } from "./internal";
import { providerLabel } from "./format";
import { getSessionToken } from "./session";

/** GET an internal moderation/admin route with the caller's session. null on any failure. */
export async function modGet<T>(path: string): Promise<T | null> {
  try {
    const token = await getSessionToken();
    if (!token) return null;
    const res = await internalFetch(path, { sessionToken: token });
    if (!res.ok) return null;
    return (await res.json()) as T;
  } catch {
    return null;
  }
}

export type QueueItem = {
  id: number;
  itemId: number;
  title: string;
  artist: string;
  album: string | null;
  proofUrl: string | null;
  author: string;
  up: number;
  down: number;
  net: number;
  createdAt: string;
  current: { title: string | null; artist: string | null; album: string | null };
};
export type QueuePage = { items: QueueItem[]; nextOffset: number | null };
export type Vote = { userId: number; name: string; value: number; banned: boolean; createdAt: string };
export type HistoryEntry = {
  id: number;
  source: string;
  actorId: number | null;
  actor: string | null;
  suggestionId: number | null;
  createdAt: string;
  snapshot: unknown;
};
export type AdminUser = { id: number; pr0UserId: number; name: string; role: string; bannedAt: string | null; reason: string | null };
export type BotStatus = {
  health: unknown;
  httpStatus: number;
  requests: Record<string, number>;
  retry: { pending: number; maxAttempts: number; nextAttemptAt: string | null };
  markReadBacklog: number;
  lastDoneAt: string | null;
};
export type FailedRow = { id: number; itemId: number; requester: string; attempts: number; lastError: string | null; updatedAt: string };

/** Parses a ?offset= value: a non-negative safe integer, else 0. */
export function parseOffset(v: string | string[] | undefined): number {
  if (typeof v !== "string" || !/^[0-9]{1,9}$/.test(v)) return 0;
  return Number(v);
}

export function sourceLabel(s: string): string {
  switch (s) {
    case "suggestion":
      return "Vorschlag";
    case "vote":
      return "Community-Abstimmung";
    case "moderator":
      return "Moderator";
    case "revert":
      return "Zurücksetzen";
    default:
      return "Unbekannt";
  }
}

export function snapshotProviderLabel(p: string): string {
  if (p === "moderation") return "Moderation";
  if (p === "community") return "Community";
  return providerLabel(p);
}
