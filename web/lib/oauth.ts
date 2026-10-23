import "server-only";
import { createHmac, randomBytes, timingSafeEqual } from "node:crypto";

export const AUTHORIZE_URL = "https://pr0gramm.com/oauth/authorize";
export const TOKEN_URL = "https://pr0gramm.com/api/oauth/createAccessToken";
export const USER_NAME_URL = "https://pr0gramm.com/api/user/name";
export const PROFILE_INFO_URL = "https://pr0gramm.com/api/profile/info";
export const SCOPE = "user.name profile.info";
/** Cloudflare in front of pr0gramm blocks requests without a User-Agent. */
export const USER_AGENT = "pr0sauce.info (+https://pr0sauce.info)";

export const OAUTH_COOKIE = "pr0sauce_oauth";
export const OAUTH_COOKIE_PATH = "/api/auth";
export const OAUTH_COOKIE_MAX_AGE = 600;
export const SESSION_COOKIE = "pr0sauce_session";
export const SESSION_COOKIE_MAX_AGE = 30 * 24 * 60 * 60;

const TIMEOUT_MS = 10_000;

type FetchFn = typeof fetch;

export interface OAuthConfig {
  clientId: string;
  clientSecret: string;
  publicUrl: string;
  redirectUri: string;
  signingKey: string;
  secureCookies: boolean;
}

/** Reads the server-only OAuth env; throws (without values) when anything is missing. */
export function oauthConfig(env: NodeJS.ProcessEnv = process.env): OAuthConfig {
  const clientId = env.PR0_OAUTH_CLIENT_ID || "";
  const clientSecret = env.PR0_OAUTH_CLIENT_SECRET || "";
  const signingKey = env.INTERNAL_API_TOKEN || "";
  const publicUrl = (env.PUBLIC_URL || "").replace(/\/+$/, "");
  const missing = [
    !clientId && "PR0_OAUTH_CLIENT_ID",
    !clientSecret && "PR0_OAUTH_CLIENT_SECRET",
    !signingKey && "INTERNAL_API_TOKEN",
    !publicUrl && "PUBLIC_URL",
  ].filter(Boolean);
  if (missing.length) throw new Error(`oauth not configured: ${missing.join(", ")} missing`);
  return {
    clientId,
    clientSecret,
    publicUrl,
    redirectUri: `${publicUrl}/api/auth/callback/pr0`,
    signingKey,
    secureCookies: secureCookies(publicUrl),
  };
}

/** Secure cookies everywhere except plain-http local development. */
export function secureCookies(publicUrl: string | undefined): boolean {
  return !(publicUrl || "").startsWith("http://localhost");
}

export function buildAuthorizeUrl(p: { clientId: string; redirectUri: string; state: string }): string {
  const qs = new URLSearchParams({
    response_type: "code",
    client_id: p.clientId,
    redirect_uri: p.redirectUri,
    scope: SCOPE,
    state: p.state,
  });
  return `${AUTHORIZE_URL}?${qs.toString()}`;
}

/**
 * Only a same-site path: exactly one leading "/", no "//" or "/\" anywhere,
 * no backslashes or control characters, at most 200 chars. Anything else is "/".
 */
export function safeNext(raw: string | null | undefined): string {
  if (typeof raw !== "string" || raw.length === 0 || raw.length > 200) return "/";
  if (raw[0] !== "/") return "/";
  if (raw.includes("//") || raw.includes("\\")) return "/";
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f]/.test(raw)) return "/";
  return raw;
}

export function newState(): string {
  return randomBytes(32).toString("base64url");
}

/** Constant-time string comparison; unequal lengths and empty strings are false. */
export function statesEqual(a: string, b: string): boolean {
  const x = Buffer.from(a, "utf8");
  const y = Buffer.from(b, "utf8");
  if (x.length === 0 || x.length !== y.length) return false;
  return timingSafeEqual(x, y);
}

function hmac(payload: string, key: string): Buffer {
  return createHmac("sha256", key).update(payload).digest();
}

/** `base64url(JSON{state,next}).base64url(HMAC-SHA256)` */
export function signOAuthCookie(v: { state: string; next: string }, key: string): string {
  const payload = Buffer.from(JSON.stringify({ state: v.state, next: v.next }), "utf8").toString("base64url");
  return `${payload}.${hmac(payload, key).toString("base64url")}`;
}

export function verifyOAuthCookie(
  value: string | null | undefined,
  key: string,
): { state: string; next: string } | null {
  if (!value || !key || value.length > 1024) return null;
  const dot = value.indexOf(".");
  if (dot <= 0 || dot !== value.lastIndexOf(".")) return null;
  const payload = value.slice(0, dot);
  const sig = Buffer.from(value.slice(dot + 1), "base64url");
  const want = hmac(payload, key);
  if (sig.length !== want.length || !timingSafeEqual(sig, want)) return null;
  try {
    const v = JSON.parse(Buffer.from(payload, "base64url").toString("utf8")) as unknown;
    if (!v || typeof v !== "object") return null;
    const { state, next } = v as { state?: unknown; next?: unknown };
    if (typeof state !== "string" || state.length === 0) return null;
    return { state, next: safeNext(typeof next === "string" ? next : "/") };
  } catch {
    return null;
  }
}

/** Errors carry only the failing step, never a value. */
export class LoginError extends Error {
  constructor(public readonly step: string) {
    super(`login failed: ${step}`);
    this.name = "LoginError";
  }
}

async function readJson(res: Response, step: string): Promise<unknown> {
  if (!res.ok) throw new LoginError(`${step} (HTTP ${res.status})`);
  try {
    return await res.json();
  } catch {
    throw new LoginError(`${step} (bad json)`);
  }
}

async function call(f: FetchFn, url: string, init: RequestInit, step: string): Promise<Response> {
  try {
    return await f(url, { ...init, cache: "no-store", signal: AbortSignal.timeout(TIMEOUT_MS) });
  } catch {
    throw new LoginError(`${step} (network)`);
  }
}

export async function exchangeCode(
  p: { code: string; redirectUri: string; clientId: string; clientSecret: string },
  f: FetchFn = fetch,
): Promise<string> {
  const body = new URLSearchParams({
    grant_type: "authorization_code",
    code: p.code,
    redirect_uri: p.redirectUri,
    client_id: p.clientId,
    client_secret: p.clientSecret,
  });
  const res = await call(
    f,
    TOKEN_URL,
    {
      method: "POST",
      headers: {
        "content-type": "application/x-www-form-urlencoded",
        accept: "application/json",
        "user-agent": USER_AGENT,
      },
      body: body.toString(),
    },
    "token exchange",
  );
  const data = (await readJson(res, "token exchange")) as { access_token?: unknown };
  if (typeof data?.access_token !== "string" || data.access_token.length === 0)
    throw new LoginError("token exchange (no token)");
  return data.access_token;
}

export async function fetchIdentity(accessToken: string, f: FetchFn = fetch): Promise<{ id: number; name: string }> {
  const headers = {
    authorization: `Bearer ${accessToken}`,
    accept: "application/json",
    "user-agent": USER_AGENT,
  };
  const nameRes = await call(f, USER_NAME_URL, { headers }, "user name");
  const nameData = (await readJson(nameRes, "user name")) as { name?: unknown };
  const name = nameData?.name;
  if (typeof name !== "string" || name.trim() === "" || name.length > 64) throw new LoginError("user name (invalid)");

  const qs = new URLSearchParams({ name, flags: "1" });
  const infoRes = await call(f, `${PROFILE_INFO_URL}?${qs.toString()}`, { headers }, "profile");
  const info = (await readJson(infoRes, "profile")) as { user?: { id?: unknown; name?: unknown } };
  const id = info?.user?.id;
  const profileName = info?.user?.name;
  if (typeof id !== "number" || !Number.isSafeInteger(id) || id <= 0) throw new LoginError("profile (invalid id)");
  if (typeof profileName !== "string" || profileName.toLowerCase() !== name.toLowerCase())
    throw new LoginError("profile (name mismatch)");
  return { id, name: profileName };
}

export interface CookieOptions {
  maxAge: number;
  path: string;
  secure: boolean;
}

/** Set-Cookie value: always HttpOnly and SameSite=Lax. */
export function serializeCookie(name: string, value: string, o: CookieOptions): string {
  const parts = [`${name}=${value}`, `Path=${o.path}`, `Max-Age=${o.maxAge}`, "HttpOnly", "SameSite=Lax"];
  if (o.secure) parts.push("Secure");
  return parts.join("; ");
}

export function readCookie(request: Request, name: string): string | undefined {
  const header = request.headers.get("cookie");
  if (!header) return undefined;
  for (const part of header.split(";")) {
    const i = part.indexOf("=");
    if (i < 0) continue;
    if (part.slice(0, i).trim() === name) return part.slice(i + 1).trim();
  }
  return undefined;
}

/** Session tokens are opaque base64url strings from the API; anything else is ignored. */
export function isPlausibleSessionToken(v: string | undefined): v is string {
  return typeof v === "string" && /^[A-Za-z0-9_-]{16,128}$/.test(v);
}
