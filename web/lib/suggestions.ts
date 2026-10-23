import { safeHttpUrl } from "./format";

export const APPROVE_THRESHOLD = 5;
export const MAX_FIELD = 200;
export const MAX_PROOF = 500;

/** Votes still missing until auto-approval: both up and net must reach the threshold. */
export function remainingToApprove(up: number, net: number): number {
  return Math.max(0, APPROVE_THRESHOLD - up, APPROVE_THRESHOLD - net);
}

/** Label of the suggestion trigger: a miss asks for the song, a hit for a correction. */
export function suggestTriggerLabel(found: boolean): string {
  return found ? "Falsch erkannt? Korrektur vorschlagen" : "Kennst du den Song? Jetzt vorschlagen";
}

export type SuggestionInput = { title: string; artist: string; album?: string; proofUrl?: string };
export type SuggestionErrors = Partial<Record<"title" | "artist" | "album" | "proofUrl", string>>;

const len = (s: string) => [...s].length;

export function validateSuggestion(input: SuggestionInput): { ok: boolean; errors: SuggestionErrors } {
  const errors: SuggestionErrors = {};
  const title = input.title.trim();
  const artist = input.artist.trim();
  const album = (input.album ?? "").trim();
  const proof = (input.proofUrl ?? "").trim();
  if (title.length === 0 || len(title) > MAX_FIELD) errors.title = "Titel: 1 bis 200 Zeichen";
  if (artist.length === 0 || len(artist) > MAX_FIELD) errors.artist = "Interpret: 1 bis 200 Zeichen";
  if (len(album) > MAX_FIELD) errors.album = "Album: höchstens 200 Zeichen";
  if (proof && (len(proof) > MAX_PROOF || safeHttpUrl(proof) === null)) {
    errors.proofUrl = "Beleg: gültiger http(s)-Link, höchstens 500 Zeichen";
  }
  return { ok: Object.keys(errors).length === 0, errors };
}

/** Maps an internal API error to a German user message. Go errors are lowercase English; German ones start uppercase. */
export function mapApiError(status: number, apiError: string | undefined, kind: "suggest" | "vote"): string {
  switch (status) {
    case 401:
      return "Bitte anmelden";
    case 403:
      return apiError === "Gesperrt" ? "Gesperrt" : "Nicht erlaubt";
    case 409:
      return kind === "suggest" ? "Du hast hier schon einen offenen Vorschlag" : "Vorschlag ist nicht mehr offen";
    case 429:
      return "Zu viele Vorschläge heute";
    case 400:
      return apiError && /^\p{Lu}/u.test(apiError) ? apiError : "Ungültige Eingabe";
    default:
      return "Das hat leider nicht geklappt";
  }
}
