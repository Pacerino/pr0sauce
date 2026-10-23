import { redirectWithCookies, textError } from "@/lib/auth-response";
import { internalFetch } from "@/lib/internal";
import { isPlausibleSessionToken, readCookie, secureCookies, serializeCookie, SESSION_COOKIE } from "@/lib/oauth";
import { assertSameOrigin } from "@/lib/origin";

export const dynamic = "force-dynamic";

export async function POST(request: Request): Promise<Response> {
  try {
    assertSameOrigin(request);
  } catch {
    return textError(403, "Verboten.");
  }
  const publicUrl = (process.env.PUBLIC_URL || "").replace(/\/+$/, "");

  const token = readCookie(request, SESSION_COOKIE);
  if (isPlausibleSessionToken(token)) {
    try {
      const res = await internalFetch("/api/internal/sessions", { method: "DELETE", sessionToken: token });
      // 401 means the session was already gone; anything else is worth a note.
      if (!res.ok && res.status !== 401) console.error(`logout failed: session (HTTP ${res.status})`);
    } catch {
      console.error("logout failed: session (network)");
    }
  }

  const clear = serializeCookie(SESSION_COOKIE, "", { maxAge: 0, path: "/", secure: secureCookies(publicUrl) });
  return redirectWithCookies(`${publicUrl}/`, 303, [clear]);
}
