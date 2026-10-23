import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { GET as login } from "@/app/api/auth/login/route";
import { GET as callback } from "@/app/api/auth/callback/pr0/route";
import { POST as logout } from "@/app/api/auth/logout/route";
import { signOAuthCookie } from "./oauth";

const SECRET = "test-secret";
const INTERNAL = "test-internal-token";
const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

/** Parses one Set-Cookie line into name, value and lower-cased attributes. */
function parseCookie(line: string) {
  const [pair, ...attrs] = line.split(";").map((s) => s.trim());
  const i = pair.indexOf("=");
  const a: Record<string, string | true> = {};
  for (const attr of attrs) {
    const j = attr.indexOf("=");
    if (j < 0) a[attr.toLowerCase()] = true;
    else a[attr.slice(0, j).toLowerCase()] = attr.slice(j + 1);
  }
  return { name: pair.slice(0, i), value: pair.slice(i + 1), attrs: a };
}
const cookies = (res: Response) => res.headers.getSetCookie().map(parseCookie);
const cookie = (res: Response, name: string) => cookies(res).find((c) => c.name === name);

const fetchMock = vi.fn();
beforeEach(() => {
  vi.stubGlobal("fetch", fetchMock);
  vi.stubEnv("PR0_OAUTH_CLIENT_ID", "644");
  vi.stubEnv("PR0_OAUTH_CLIENT_SECRET", SECRET);
  vi.stubEnv("PUBLIC_URL", "https://pr0sauce.info");
  vi.stubEnv("INTERNAL_API_TOKEN", INTERNAL);
  vi.stubEnv("API_URL", "http://api.test:8080");
  vi.spyOn(console, "error").mockImplementation(() => {});
  vi.spyOn(console, "warn").mockImplementation(() => {});
});
afterEach(() => {
  fetchMock.mockReset();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  vi.restoreAllMocks();
});

describe("GET /api/auth/login", () => {
  it("redirects to pr0gramm with state and sets a signed, locked-down oauth cookie", async () => {
    const res = await login(new Request("http://internal:3000/api/auth/login?next=/6796145"));
    expect(res.status).toBe(302);
    const loc = new URL(res.headers.get("location")!);
    expect(loc.origin + loc.pathname).toBe("https://pr0gramm.com/oauth/authorize");
    expect(loc.searchParams.get("client_id")).toBe("644");
    expect(loc.searchParams.get("redirect_uri")).toBe("https://pr0sauce.info/api/auth/callback/pr0");
    const state = loc.searchParams.get("state")!;
    expect(state).toMatch(/^[A-Za-z0-9_-]{43}$/);

    const c = cookie(res, "pr0sauce_oauth")!;
    expect(c).toBeDefined();
    expect(c.attrs.httponly).toBe(true);
    expect(c.attrs.secure).toBe(true);
    expect(c.attrs.samesite).toBe("Lax");
    expect(c.attrs.path).toBe("/api/auth");
    expect(c.attrs["max-age"]).toBe("600");
    expect(res.headers.get("location")).not.toContain(SECRET);
  });

  it("drops Secure only for http://localhost", async () => {
    vi.stubEnv("PUBLIC_URL", "http://localhost:3000");
    const res = await login(new Request("http://localhost:3000/api/auth/login"));
    expect(cookie(res, "pr0sauce_oauth")!.attrs.secure).toBeUndefined();
  });

  it("fails closed without the client secret configured", async () => {
    vi.stubEnv("PR0_OAUTH_CLIENT_SECRET", "");
    const res = await login(new Request("http://internal:3000/api/auth/login"));
    expect(res.status).toBe(500);
    expect(res.headers.getSetCookie()).toEqual([]);
  });
});

/** Runs /login to get a real state and oauth cookie for the callback. */
async function startLogin(next = "/6796145") {
  const res = await login(new Request(`http://internal:3000/api/auth/login?next=${encodeURIComponent(next)}`));
  const state = new URL(res.headers.get("location")!).searchParams.get("state")!;
  const c = cookie(res, "pr0sauce_oauth")!;
  return { state, cookieHeader: `pr0sauce_oauth=${c.value}` };
}

function callbackReq(query: string, cookieHeader?: string) {
  return new Request(`http://internal:3000/api/auth/callback/pr0?${query}`, {
    headers: cookieHeader ? { cookie: cookieHeader } : {},
  });
}

function happyFetch() {
  fetchMock.mockImplementation(async (url: string) => {
    if (url === "https://pr0gramm.com/api/oauth/createAccessToken")
      return json({ access_token: "tok-1", token_type: "Bearer", scope: "user.name profile.info" });
    if (url === "https://pr0gramm.com/api/user/name") return json({ name: "Pacerino" });
    if (url.startsWith("https://pr0gramm.com/api/profile/info"))
      return json({ user: { id: 1234, name: "Pacerino" } });
    if (url === "http://api.test:8080/api/internal/sessions")
      return json({ token: "sess-token-0123456789abcdef", user: { id: 1, name: "Pacerino", role: "user" } });
    return json({ error: "unexpected" }, 500);
  });
}

describe("GET /api/auth/callback/pr0", () => {
  it("rejects a state mismatch with 400 and no Set-Cookie", async () => {
    const { cookieHeader } = await startLogin();
    const res = await callback(callbackReq("code=c&state=wrong", cookieHeader));
    expect(res.status).toBe(400);
    expect(res.headers.getSetCookie()).toEqual([]);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects a state of different length without throwing", async () => {
    const { state, cookieHeader } = await startLogin();
    const res = await callback(callbackReq(`code=c&state=${state}x`, cookieHeader));
    expect(res.status).toBe(400);
  });

  it("rejects a missing oauth cookie", async () => {
    const { state } = await startLogin();
    const res = await callback(callbackReq(`code=c&state=${state}`));
    expect(res.status).toBe(400);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("rejects a forged (unsigned) oauth cookie", async () => {
    const forged = signOAuthCookie({ state: "s1", next: "/" }, "wrong-key");
    const res = await callback(callbackReq("code=c&state=s1", `pr0sauce_oauth=${forged}`));
    expect(res.status).toBe(400);
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("happy path: sets the session cookie, clears the oauth cookie, redirects to next", async () => {
    happyFetch();
    const { state, cookieHeader } = await startLogin("/6796145");
    const res = await callback(callbackReq(`code=the-code&state=${state}`, cookieHeader));
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe("https://pr0sauce.info/6796145");

    const s = cookie(res, "pr0sauce_session")!;
    expect(s.value).toBe("sess-token-0123456789abcdef");
    expect(s.attrs.httponly).toBe(true);
    expect(s.attrs.secure).toBe(true);
    expect(s.attrs.samesite).toBe("Lax");
    expect(s.attrs.path).toBe("/");
    expect(s.attrs["max-age"]).toBe(String(30 * 24 * 60 * 60));

    const o = cookie(res, "pr0sauce_oauth")!;
    expect(o.attrs["max-age"]).toBe("0");
    expect(o.attrs.path).toBe("/api/auth");

    // token exchange carried the secret
    const tokenCall = fetchMock.mock.calls.find(([u]) => u === "https://pr0gramm.com/api/oauth/createAccessToken")!;
    expect(new URLSearchParams(String(tokenCall[1].body)).get("client_secret")).toBe(SECRET);
    // internal session call carried the internal token and the identity
    const sessCall = fetchMock.mock.calls.find(([u]) => u === "http://api.test:8080/api/internal/sessions")!;
    expect(sessCall[1].method).toBe("POST");
    expect(new Headers(sessCall[1].headers).get("x-internal-token")).toBe(INTERNAL);
    expect(JSON.parse(sessCall[1].body)).toEqual({ pr0UserId: 1234, name: "Pacerino" });

    // nothing secret leaks into the response
    const everything = [...res.headers.entries()].map(([k, v]) => `${k}: ${v}`).join("\n") + (await res.text());
    expect(everything).not.toContain(SECRET);
    expect(everything).not.toContain("tok-1");
    expect(everything).not.toContain(INTERNAL);
  });

  it("error=access_denied redirects to next without a session", async () => {
    const { state, cookieHeader } = await startLogin("/42");
    const res = await callback(callbackReq(`error=access_denied&state=${state}`, cookieHeader));
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe("https://pr0sauce.info/42");
    expect(cookie(res, "pr0sauce_session")).toBeUndefined();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("error=access_denied without a cookie redirects to /", async () => {
    const res = await callback(callbackReq("error=access_denied"));
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe("https://pr0sauce.info/");
    expect(cookie(res, "pr0sauce_session")).toBeUndefined();
  });

  it.each([
    ["token exchange", "https://pr0gramm.com/api/oauth/createAccessToken"],
    ["user name", "https://pr0gramm.com/api/user/name"],
    ["profile", "https://pr0gramm.com/api/profile/info"],
    ["session", "http://api.test:8080/api/internal/sessions"],
  ])("a %s failure redirects to /?login=fehler without a session and without leaking", async (step, failing) => {
    happyFetch();
    const inner = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (url: string, init: RequestInit) =>
      url.startsWith(failing) ? json({ error: `boom ${SECRET}` }, 500) : inner(url, init),
    );
    const errSpy = vi.mocked(console.error);
    const { state, cookieHeader } = await startLogin();
    const res = await callback(callbackReq(`code=c&state=${state}`, cookieHeader));
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe("https://pr0sauce.info/?login=fehler");
    expect(cookie(res, "pr0sauce_session")).toBeUndefined();
    const logged = errSpy.mock.calls.flat().map(String).join("\n");
    expect(logged).toContain("login failed:");
    expect(logged).not.toContain(SECRET);
    expect(logged).not.toContain("tok-1");
    void step;
  });

  it("a network error is handled without a 500", async () => {
    fetchMock.mockRejectedValue(new TypeError("fetch failed"));
    const { state, cookieHeader } = await startLogin();
    const res = await callback(callbackReq(`code=c&state=${state}`, cookieHeader));
    expect(res.status).toBe(302);
    expect(res.headers.get("location")).toBe("https://pr0sauce.info/?login=fehler");
  });
});

describe("POST /api/auth/logout", () => {
  const logoutReq = (origin?: string, cookieHeader = "pr0sauce_session=sess-token-0123456789abcdef") =>
    new Request("http://internal:3000/api/auth/logout", {
      method: "POST",
      headers: { ...(origin ? { origin } : {}), cookie: cookieHeader },
    });

  it("rejects a missing or foreign Origin with 403", async () => {
    for (const o of [undefined, "https://evil.com"]) {
      const res = await logout(logoutReq(o));
      expect(res.status).toBe(403);
      expect(res.headers.getSetCookie()).toEqual([]);
    }
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("deletes the session, clears the cookie and 303s to /", async () => {
    fetchMock.mockResolvedValue(new Response(null, { status: 204 }));
    const res = await logout(logoutReq("https://pr0sauce.info"));
    expect(res.status).toBe(303);
    expect(res.headers.get("location")).toBe("https://pr0sauce.info/");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://api.test:8080/api/internal/sessions");
    expect(init.method).toBe("DELETE");
    const h = new Headers(init.headers);
    expect(h.get("x-internal-token")).toBe(INTERNAL);
    expect(h.get("x-session-token")).toBe("sess-token-0123456789abcdef");
    const c = cookie(res, "pr0sauce_session")!;
    expect(c.value).toBe("");
    expect(c.attrs["max-age"]).toBe("0");
    expect(c.attrs.path).toBe("/");
    expect(c.attrs.httponly).toBe(true);
    expect(c.attrs.secure).toBe(true);
  });

  it("still clears the cookie when the API is down", async () => {
    fetchMock.mockRejectedValue(new TypeError("fetch failed"));
    const res = await logout(logoutReq("https://pr0sauce.info"));
    expect(res.status).toBe(303);
    expect(cookie(res, "pr0sauce_session")!.attrs["max-age"]).toBe("0");
  });
});
