const ITEM_ID_RE = /^[1-9][0-9]{0,15}$/;

/** Returns the numeric item id only for canonical positive ids in the safe integer range. */
export function parseItemId(s: string): number | null {
  if (!ITEM_ID_RE.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) ? n : null;
}
