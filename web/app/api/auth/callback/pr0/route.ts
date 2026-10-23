import { redirectWithCookies, textError } from "@/lib/auth-response";
import { internalFetch } from "@/lib/internal";
import {
  exchangeCode,
  fetchIdentity,
  isPlausibleSessionToken,
  LoginError,
  OAUTH_COOKIE,
  OAUTH_COOKIE_PATH,
  oauthConfig,
  readCookie,
  serializeCookie,
  SESSION_COOKIE,
  SESSION_COOKIE_MAX_AGE,
  statesEqual,
  verifyOAuthCookie,
} from "@/lib/oauth";

export const dynamic = "force-dynamic";

const FAILED = "/?login=fehler";

async function createSession(identity: { id: number; name: string }): Promise<string> {
  let res: Response;
  try {
    res = await internalFetch("/api/internal/sessions", {
      method: "POST",
      body: { pr0UserId: identity.id, name: identity.name },
    });
  } catch {
    throw new LoginError("session (network)");
  }
  if (!res.ok) throw new LoginError(`session (HTTP ${res.status})`);
  let token: unknown;
  try {
    token = ((await res.json()) as { token?: unknown }).token;
  } catch {
    throw new LoginError("session (bad json)");
  }
  if (typeof token !== "string" || !isPlausibleSessionToken(token)) throw new LoginError("session (no token)");
  return token;
}

export async function GET(request: Request): Promise<Response> {
  let cfg;
  try {
    cfg = oauthConfig();
  } catch {
    console.error("login failed: config");
    return textError(500, "Anmeldung ist gerade nicht verfügbar.");
  }
  const to = (path: string) => new URL(path, cfg.publicUrl + "/").toString();
  const clearOAuth = serializeCookie(OAUTH_COOKIE, "", {
    maxAge: 0,
    path: OAUTH_COOKIE_PATH,
    secure: cfg.secureCookies,
  });

  const params = new URL(request.url).searchParams;
  const stored = verifyOAuthCookie(readCookie(request, OAUTH_COOKIE), cfg.signingKey);

  // The user declined on pr0gramm (or pr0gramm reported another error): no session.
  const error = params.get("error");
  if (error !== null) {
    if (error === "access_denied") return redirectWithCookies(to(stored?.next ?? "/"), 302, [clearOAuth]);
    console.error("login failed: provider error");
    return redirectWithCookies(to(FAILED), 302, [clearOAuth]);
  }

  const state = params.get("state");
  const code = params.get("code");
  if (!stored || !state || !statesEqual(state, stored.state) || !code || code.length > 1024) {
    return textError(400, "Ungültige oder abgelaufene Anmeldung. Bitte erneut anmelden.");
  }

  try {
    const accessToken = await exchangeCode({
      code,
      redirectUri: cfg.redirectUri,
      clientId: cfg.clientId,
      clientSecret: cfg.clientSecret,
    });
    // The access token is used for the two identity calls only and then dropped.
    const identity = await fetchIdentity(accessToken);
    const sessionToken = await createSession(identity);
    const session = serializeCookie(SESSION_COOKIE, sessionToken, {
      maxAge: SESSION_COOKIE_MAX_AGE,
      path: "/",
      secure: cfg.secureCookies,
    });
    return redirectWithCookies(to(stored.next), 302, [clearOAuth, session]);
  } catch (e) {
    console.error(e instanceof LoginError ? e.message : "login failed: unexpected");
    return redirectWithCookies(to(FAILED), 302, [clearOAuth]);
  }
}
