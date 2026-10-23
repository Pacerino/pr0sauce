"use server";

import { revalidatePath } from "next/cache";
import { headers } from "next/headers";
import { internalFetch } from "@/lib/internal";
import { assertSameOrigin } from "@/lib/origin";
import { getCurrentUser, getSessionToken } from "@/lib/session";
import { mapApiError, validateSuggestion } from "@/lib/suggestions";
import { parseItemId } from "@/lib/ids";

export type ActionResult = { ok: boolean; error?: string };

async function guard(): Promise<{ token: string } | ActionResult> {
  try {
    assertSameOrigin(new Request("http://internal/", { headers: await headers() }));
  } catch {
    return { ok: false, error: "Nicht erlaubt" };
  }
  const user = await getCurrentUser();
  const token = await getSessionToken();
  if (!user || !token) return { ok: false, error: "Bitte anmelden" };
  if (user.banned) return { ok: false, error: "Gesperrt" };
  return { token };
}

async function apiErrorText(res: Response): Promise<string | undefined> {
  try {
    const b = (await res.json()) as { error?: unknown };
    return typeof b.error === "string" ? b.error : undefined;
  } catch {
    return undefined;
  }
}

const text = (f: FormData, k: string) => {
  const v = f.get(k);
  return typeof v === "string" ? v : "";
};

export async function suggestAction(_prev: ActionResult | null, form: FormData): Promise<ActionResult> {
  const g = await guard();
  if (!("token" in g)) return g;
  const id = parseItemId(text(form, "itemId"));
  if (id === null) return { ok: false, error: "Ungültige Eingabe" };
  const input = {
    title: text(form, "title"),
    artist: text(form, "artist"),
    album: text(form, "album"),
    proofUrl: text(form, "proofUrl"),
  };
  const v = validateSuggestion(input);
  if (!v.ok) return { ok: false, error: Object.values(v.errors)[0] };
  const body: Record<string, string> = { title: input.title.trim(), artist: input.artist.trim() };
  if (input.album.trim()) body.album = input.album.trim();
  if (input.proofUrl.trim()) body.proofUrl = input.proofUrl.trim();
  let res: Response;
  try {
    res = await internalFetch(`/api/internal/songs/${id}/suggestions`, { method: "POST", body, sessionToken: g.token });
  } catch {
    return { ok: false, error: "Das hat leider nicht geklappt" };
  }
  if (!res.ok) return { ok: false, error: mapApiError(res.status, await apiErrorText(res), "suggest") };
  revalidatePath(`/${id}`);
  return { ok: true };
}

export async function voteAction(_prev: ActionResult | null, form: FormData): Promise<ActionResult> {
  const g = await guard();
  if (!("token" in g)) return g;
  const id = parseItemId(text(form, "itemId"));
  const sid = Number(text(form, "suggestionId"));
  const value = Number(text(form, "value"));
  if (id === null || !Number.isSafeInteger(sid) || sid <= 0 || ![1, -1, 0].includes(value)) {
    return { ok: false, error: "Ungültige Eingabe" };
  }
  let res: Response;
  try {
    res = await internalFetch(`/api/internal/suggestions/${sid}/vote`, {
      method: "PUT",
      body: { value },
      sessionToken: g.token,
    });
  } catch {
    return { ok: false, error: "Das hat leider nicht geklappt" };
  }
  if (!res.ok) {
    revalidatePath(`/${id}`);
    return { ok: false, error: mapApiError(res.status, await apiErrorText(res), "vote") };
  }
  revalidatePath(`/${id}`);
  return { ok: true };
}
