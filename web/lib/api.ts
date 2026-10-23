import "server-only";
import type { Song, SongList, Stats } from "./types";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export function apiBaseUrl(): string {
  return (process.env.API_URL || "http://api:8080").replace(/\/+$/, "");
}

async function request(path: string, init: RequestInit & { next?: { revalidate: number } }) {
  const res = await fetch(apiBaseUrl() + path, init);
  if (res.ok) return res;
  let message = res.statusText || `HTTP ${res.status}`;
  try {
    const body = (await res.json()) as { error?: unknown };
    if (typeof body.error === "string") message = body.error;
  } catch {
    // non-JSON error body: keep the status text
  }
  throw new ApiError(res.status, message);
}

export const api = {
  async songs(params: { q?: string; cursor?: string } = {}): Promise<SongList> {
    const qs = new URLSearchParams();
    if (params.q) qs.set("q", params.q);
    if (params.cursor) qs.set("cursor", params.cursor);
    const s = qs.toString();
    const res = await request(`/api/songs${s ? `?${s}` : ""}`, { cache: "no-store" });
    return (await res.json()) as SongList;
  },

  async song(id: number): Promise<Song | null> {
    try {
      const res = await request(`/api/songs/${id}`, { cache: "no-store" });
      return (await res.json()) as Song;
    } catch (e) {
      if (e instanceof ApiError && e.status === 404) return null;
      throw e;
    }
  },

  async stats(): Promise<Stats> {
    const res = await request("/api/stats", { next: { revalidate: 60 } });
    return (await res.json()) as Stats;
  },
};
