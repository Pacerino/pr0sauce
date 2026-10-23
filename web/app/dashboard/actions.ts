"use server";

import { revalidatePath } from "next/cache";
import { headers } from "next/headers";
import { redirect } from "next/navigation";
import { canModerate, canSeeAdmin, mapModError, parseRole, validatePr0Name, validateReason } from "@/lib/dashboard";
import { parseOffset } from "@/lib/dashboard-data";
import { parseItemId } from "@/lib/ids";
import { internalFetch } from "@/lib/internal";
import { assertSameOrigin } from "@/lib/origin";
import { getCurrentUser, getSessionToken } from "@/lib/session";

const text = (f: FormData, k: string) => {
  const v = f.get(k);
  return typeof v === "string" ? v : "";
};

const posInt = (s: string): number | null => (/^[1-9][0-9]{0,15}$/.test(s) && Number.isSafeInteger(Number(s)) ? Number(s) : null);

function back(path: string, msg?: string, ok?: string): never {
  const q = msg ? `?e=${encodeURIComponent(msg)}` : ok ? `?ok=${encodeURIComponent(ok)}` : "";
  redirect(path + q);
}

/** Every action re-checks origin, login, ban and role itself; the layout is not trusted. */
async function guard(need: "mod" | "admin", path: string): Promise<{ token: string }> {
  try {
    assertSameOrigin(new Request("http://internal/", { headers: await headers() }));
  } catch {
    back(path, "Nicht erlaubt");
  }
  const user = await getCurrentUser();
  const token = await getSessionToken();
  if (!user || !token) back(path, "Bitte anmelden");
  if (user.banned) back(path, "Gesperrt");
  const role = parseRole(user.role);
  if (!(need === "admin" ? canSeeAdmin(role) : canModerate(role))) back(path, "Nicht erlaubt");
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

/** Runs the internal call; on failure redirects back with a German message, on success revalidates and redirects. */
async function call(
  path: string,
  token: string,
  api: string,
  method: string,
  body: unknown,
  okMsg: string,
  revalidate: string[],
  backTo = path,
): Promise<never> {
  let res: Response;
  try {
    res = await internalFetch(api, { method, body, sessionToken: token });
  } catch {
    return back(backTo, "Das hat leider nicht geklappt");
  }
  if (!res.ok) {
    for (const p of revalidate) revalidatePath(p);
    return back(backTo, mapModError(res.status, await apiErrorText(res)));
  }
  for (const p of revalidate) revalidatePath(p);
  return back(backTo, undefined, okMsg);
}

function queuePath(form: FormData): string {
  const off = parseOffset(text(form, "offset"));
  return off > 0 ? `/dashboard?offset=${off}` : "/dashboard";
}

export async function approveAction(form: FormData): Promise<void> {
  const path = queuePath(form);
  const g = await guard("mod", path);
  const id = posInt(text(form, "suggestionId"));
  if (id === null) back(path, "Ungültige Eingabe");
  await call(path, g.token, `/api/internal/suggestions/${id}/approve`, "POST", undefined, "Übernommen", ["/dashboard"]);
}

export async function rejectAction(form: FormData): Promise<void> {
  const path = queuePath(form);
  const g = await guard("mod", path);
  const id = posInt(text(form, "suggestionId"));
  if (id === null) back(path, "Ungültige Eingabe");
  const r = validateReason(text(form, "reason"));
  if (!r.ok) back(path, r.error);
  const body = r.reason ? { reason: r.reason } : {};
  await call(path, g.token, `/api/internal/suggestions/${id}/reject`, "POST", body, "Abgelehnt", ["/dashboard"]);
}

export async function revertAction(form: FormData): Promise<void> {
  const itemId = parseItemId(text(form, "itemId"));
  if (itemId === null) back("/dashboard", "Ungültige Eingabe");
  const path = `/dashboard/songs/${itemId}`;
  const g = await guard("mod", path);
  const historyId = posInt(text(form, "historyId"));
  if (historyId === null) back(path, "Ungültige Eingabe");
  if (text(form, "confirm") !== "yes") back(path, "Bitte „Ja, wirklich zurücksetzen“ bestätigen");
  await call(path, g.token, `/api/internal/songs/${itemId}/revert`, "POST", { historyId }, "Zurückgesetzt", [path, `/${itemId}`]);
}

const ADMIN = "/dashboard/admin";

export async function addModeratorAction(form: FormData): Promise<void> {
  const g = await guard("admin", ADMIN);
  const n = validatePr0Name(text(form, "name"));
  if (!n.ok) back(ADMIN, n.error);
  await call(ADMIN, g.token, "/api/internal/admin/moderators", "POST", { name: n.name }, "Moderator hinzugefügt", [ADMIN]);
}

export async function removeModeratorAction(form: FormData): Promise<void> {
  const g = await guard("admin", ADMIN);
  const id = posInt(text(form, "userId"));
  if (id === null) back(ADMIN, "Ungültige Eingabe");
  await call(ADMIN, g.token, `/api/internal/admin/moderators/${id}`, "DELETE", undefined, "Moderator entfernt", [ADMIN]);
}

export async function banAction(form: FormData): Promise<void> {
  const g = await guard("admin", ADMIN);
  const n = validatePr0Name(text(form, "name"));
  if (!n.ok) back(ADMIN, n.error);
  const r = validateReason(text(form, "reason"));
  if (!r.ok) back(ADMIN, r.error);
  await call(ADMIN, g.token, "/api/internal/admin/bans", "POST", { name: n.name, reason: r.reason }, "Gesperrt", [ADMIN, "/dashboard"]);
}

export async function unbanAction(form: FormData): Promise<void> {
  const g = await guard("admin", ADMIN);
  const id = posInt(text(form, "userId"));
  if (id === null) back(ADMIN, "Ungültige Eingabe");
  await call(ADMIN, g.token, `/api/internal/admin/bans/${id}`, "DELETE", undefined, "Entsperrt", [ADMIN]);
}
