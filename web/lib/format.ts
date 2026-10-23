const berlinDate = new Intl.DateTimeFormat("de-DE", {
  timeZone: "Europe/Berlin",
  day: "2-digit",
  month: "2-digit",
  year: "numeric",
});

const numberDe = new Intl.NumberFormat("de-DE");

function berlinParts(d: Date): { y: number; m: number; d: number; text: string } {
  const parts = berlinDate.formatToParts(d);
  const get = (t: string) => parts.find((p) => p.type === t)?.value ?? "";
  const day = get("day");
  const month = get("month");
  const year = get("year");
  return { y: Number(year), m: Number(month), d: Number(day), text: `${day}.${month}.${year}` };
}

/** Calendar-day index in Europe/Berlin, DST safe (pure date arithmetic on the Berlin Y-M-D). */
function berlinDayIndex(p: { y: number; m: number; d: number }): number {
  return Math.round(Date.UTC(p.y, p.m - 1, p.d) / 86_400_000);
}

export function relativeTimeDe(iso: string, now: Date): string {
  const then = new Date(iso);
  if (Number.isNaN(then.getTime())) return "";
  const diffMin = Math.floor((now.getTime() - then.getTime()) / 60_000);
  const tp = berlinParts(then);
  const dayDiff = berlinDayIndex(berlinParts(now)) - berlinDayIndex(tp);
  if (dayDiff === 1) return "gestern";
  if (dayDiff === 0 || diffMin < 0) {
    if (diffMin < 1) return "gerade eben";
    if (diffMin < 60) return `vor ${diffMin} Min`;
    return `vor ${Math.floor(diffMin / 60)} Std`;
  }
  return tp.text;
}

export function formatDateDe(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : berlinParts(d).text;
}

export function providerLabel(p: string): string {
  switch (p) {
    case "shazam":
      return "Shazam";
    case "acrcloud":
      return "ACRCloud";
    case "auddio":
      return "audd.io";
    default:
      return "unbekannt";
  }
}

export function formatNumberDe(n: number): string {
  return numberDe.format(n);
}

export function formatPercentDe(fraction: number): string {
  return `${Math.round(fraction * 100)} %`;
}

/** Returns the URL only if it is an absolute http(s) URL; guards href/src against javascript: etc. */
export function safeHttpUrl(u: string | null): string | null {
  if (!u) return null;
  try {
    const { protocol } = new URL(u);
    return protocol === "http:" || protocol === "https:" ? u : null;
  } catch {
    return null;
  }
}
