// Pure helpers for the moderation dashboard (no server-only imports; unit tested).

export type DashRole = "user" | "moderator" | "admin";

/** Strict role parse: anything unknown is null and therefore means no access. */
export function parseRole(x: unknown): DashRole | null {
  return x === "user" || x === "moderator" || x === "admin" ? x : null;
}

export function canSeeAdmin(role: DashRole | null): boolean {
  return role === "admin";
}

export function canModerate(role: DashRole | null): boolean {
  return role === "moderator" || role === "admin";
}

const MAX_POLL_AGE_MS = 2 * 60_000;

/** German one-line status for the bot's /healthz body (the `health` field of GET /admin/bot). */
export function formatHealth(body: unknown, now: Date = new Date()): string {
  if (typeof body !== "object" || body === null || Array.isArray(body)) return "Ungültige Antwort vom Bot";
  const b = body as Record<string, unknown>;
  if (b.error === "unreachable") return "Bot nicht erreichbar";
  if (typeof b.error === "string") return "Ungültige Antwort vom Bot";
  if (b.db !== "ok" && b.db !== "error") return "Ungültige Antwort vom Bot";
  if (b.db === "error") return "Bot: Datenbankfehler";
  if (b.session === "invalid") return "Session ungültig – bot-login nötig";
  if (b.session !== "ok") return b.session === "unknown" ? "Session unbekannt" : "Ungültige Antwort vom Bot";
  const last = typeof b.last_poll === "string" ? Date.parse(b.last_poll) : NaN;
  if (Number.isNaN(last) || now.getTime() - last > MAX_POLL_AGE_MS) return "Bot hängt – letzter Poll zu alt";
  return "Bot läuft";
}

const NAME_RE = /^[A-Za-z0-9_-]+$/;

export function validatePr0Name(raw: string): { ok: true; name: string } | { ok: false; error: string } {
  const name = raw.trim();
  if (name.length < 1 || name.length > 32 || !NAME_RE.test(name)) {
    return { ok: false, error: "Name: 1 bis 32 Zeichen (Buchstaben, Ziffern, _ und -)" };
  }
  return { ok: true, name };
}

export const MAX_REASON = 300;

export function validateReason(raw: string): { ok: true; reason: string } | { ok: false; error: string } {
  const reason = raw.trim();
  if ([...reason].length > MAX_REASON) return { ok: false, error: "Grund: höchstens 300 Zeichen" };
  return { ok: true, reason };
}

type Triple = { title: string | null; artist: string | null; album: string | null };

const norm = (s: string | null | undefined) => (s ?? "").trim();

/** Which of title/artist/album differ between the current song values and a suggestion. */
export function changedFields(cur: Triple, sug: Triple): Record<"title" | "artist" | "album", boolean> {
  return {
    title: norm(cur.title) !== norm(sug.title),
    artist: norm(cur.artist) !== norm(sug.artist),
    album: norm(cur.album) !== norm(sug.album),
  };
}

/** German message for a failed moderation/admin call. Go errors are lowercase English; German ones start uppercase. */
export function mapModError(status: number, apiError: string | undefined): string {
  const german = apiError && /^\p{Lu}/u.test(apiError) ? apiError : null;
  switch (status) {
    case 401:
      return "Bitte anmelden";
    case 403:
      return german ?? "Nicht erlaubt";
    case 404:
      return "Nicht gefunden (pr0gramm-Name oder Eintrag existiert nicht)";
    case 409:
      return german ?? "Das hat sich inzwischen geändert – bitte neu laden";
    case 400:
      return german ?? "Ungültige Eingabe";
    default:
      return "Das hat leider nicht geklappt";
  }
}
