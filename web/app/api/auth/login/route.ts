import { redirectWithCookies, textError } from "@/lib/auth-response";
import {
  buildAuthorizeUrl,
  newState,
  OAUTH_COOKIE,
  OAUTH_COOKIE_MAX_AGE,
  OAUTH_COOKIE_PATH,
  oauthConfig,
  safeNext,
  serializeCookie,
  signOAuthCookie,
} from "@/lib/oauth";

export const dynamic = "force-dynamic";

export async function GET(request: Request): Promise<Response> {
  let cfg;
  try {
    cfg = oauthConfig();
  } catch {
    console.error("login failed: config");
    return textError(500, "Anmeldung ist gerade nicht verfügbar.");
  }
  const next = safeNext(new URL(request.url).searchParams.get("next"));
  const state = newState();
  const cookie = serializeCookie(OAUTH_COOKIE, signOAuthCookie({ state, next }, cfg.signingKey), {
    maxAge: OAUTH_COOKIE_MAX_AGE,
    path: OAUTH_COOKIE_PATH,
    secure: cfg.secureCookies,
  });
  const location = buildAuthorizeUrl({ clientId: cfg.clientId, redirectUri: cfg.redirectUri, state });
  return redirectWithCookies(location, 302, [cookie]);
}
