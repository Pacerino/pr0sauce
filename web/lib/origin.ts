import "server-only";

export class OriginError extends Error {
  constructor() {
    super("cross-origin request rejected");
    this.name = "OriginError";
  }
}

function expectedOrigin(): string | null {
  const raw = process.env.PUBLIC_URL;
  if (!raw) return null;
  try {
    return new URL(raw).origin;
  } catch {
    return null;
  }
}

/** True only when the request's Origin header equals the origin of PUBLIC_URL. */
export function isSameOrigin(request: Request): boolean {
  const want = expectedOrigin();
  const got = request.headers.get("origin");
  return want !== null && !!got && got !== "null" && got === want;
}

/** Throws OriginError unless the request comes from our own origin. A missing Origin is rejected. */
export function assertSameOrigin(request: Request): void {
  if (!isSameOrigin(request)) throw new OriginError();
}
