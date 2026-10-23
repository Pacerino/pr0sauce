const berlinYmd = new Intl.DateTimeFormat("en-CA", {
  timeZone: "Europe/Berlin",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});

const DAY = 86_400_000;

function iso(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10);
}

/** Berlin calendar date of `d` as a UTC-midnight timestamp (pure date arithmetic from here on, DST safe). */
function berlinDayMs(d: Date): number {
  const p = berlinYmd.formatToParts(d);
  const get = (t: string) => Number(p.find((x) => x.type === t)?.value);
  return Date.UTC(get("year"), get("month") - 1, get("day"));
}

/**
 * Exactly n consecutive Monday-start weeks (YYYY-MM-DD) ending with the current Europe/Berlin week.
 * Missing weeks get count 0; input weeks outside the window are dropped.
 */
export function fillWeeks(
  perWeek: { week: string; count: number }[],
  now: Date,
  n = 26,
): { week: string; count: number }[] {
  const today = berlinDayMs(now);
  const sinceMonday = (new Date(today).getUTCDay() + 6) % 7;
  const current = today - sinceMonday * DAY;
  const counts = new Map(perWeek.map((w) => [w.week, w.count]));
  return Array.from({ length: n }, (_, i) => {
    const week = iso(current - (n - 1 - i) * 7 * DAY);
    return { week, count: counts.get(week) ?? 0 };
  });
}

/** ISO 8601 week number of a YYYY-MM-DD date. */
export function isoWeekNumber(ymd: string): number {
  const t = Date.parse(ymd);
  const thursday = t + (3 - ((new Date(t).getUTCDay() + 6) % 7)) * DAY;
  const yearStart = Date.UTC(new Date(thursday).getUTCFullYear(), 0, 1);
  return Math.floor((thursday - yearStart) / (7 * DAY)) + 1;
}

/** Share of each provider in percent (count / sum, whole numbers), largest first. */
export function providerShares(
  providers: { provider: string; count: number }[],
): { provider: string; count: number; percent: number }[] {
  const total = providers.reduce((s, p) => s + p.count, 0);
  if (total <= 0) return [];
  return [...providers]
    .sort((a, b) => b.count - a.count)
    .map((p) => ({ ...p, percent: Math.round((p.count / total) * 100) }));
}
