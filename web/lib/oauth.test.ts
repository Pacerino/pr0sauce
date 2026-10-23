import { describe, expect, it, vi } from "vitest";
import {
  buildAuthorizeUrl,
  exchangeCode,
  fetchIdentity,
  newState,
  safeNext,
  signOAuthCookie,
  statesEqual,
  USER_AGENT,
  verifyOAuthCookie,
} from "./oauth";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

describe("buildAuthorizeUrl", () => {
  it("contains the exact params with an encoded scope", () => {
    const url = new URL(
      buildAuthorizeUrl({
        clientId: "644",
        redirectUri: "https://pr0sauce.info/api/auth/callback/pr0",
        state: "abc_-123",
      }),
    );
    expect(url.origin + url.pathname).toBe("https://pr0gramm.com/oauth/authorize");
    expect([...url.searchParams.keys()].sort()).toEqual(
      ["client_id", "redirect_uri", "response_type", "scope", "state"].sort(),
    );
    expect(url.searchParams.get("response_type")).toBe("code");
    expect(url.searchParams.get("client_id")).toBe("644");
    expect(url.searchParams.get("redirect_uri")).toBe("https://pr0sauce.info/api/auth/callback/pr0");
    expect(url.searchParams.get("scope")).toBe("user.name profile.info");
    expect(url.searchParams.get("state")).toBe("abc_-123");
    // the space in the scope is encoded, never raw
    expect(url.search).not.toContain(" ");
    expect(url.search).toMatch(/scope=user\.name(\+|%20)profile\.info/);
  });
});

describe("safeNext", () => {
  it.each([["/6796145"], ["/"], ["/statistik?x=1"], ["/a/b"]])("keeps %s", (p) => {
    expect(safeNext(p)).toBe(p);
  });
  it.each([
    ["//evil.com"],
    ["https://evil.com"],
    ["javascript:alert(1)"],
    ["/\\evil.com"],
    ["\\\\evil.com"],
    ["/a//b"],
    ["/foo\nbar"],
    ["/foo\tbar"],
    ["/foo\u007fbar"],
    ["evil.com"],
    [""],
    [null],
    [undefined],
    ["/" + "a".repeat(200)],
  ])("rejects %j", (p) => {
    expect(safeNext(p as string | null | undefined)).toBe("/");
  });
  it("accepts exactly 200 chars", () => {
    const p = "/" + "a".repeat(199);
    expect(safeNext(p)).toBe(p);
  });
});

describe("state", () => {
  it("newState is 32 random bytes as base64url", () => {
    const s = newState();
    expect(s).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(newState()).not.toBe(s);
  });
  it("statesEqual compares in constant time and handles length mismatch", () => {
    expect(statesEqual("abc", "abc")).toBe(true);
    expect(statesEqual("abc", "abd")).toBe(false);
    expect(statesEqual("abc", "abcd")).toBe(false);
    expect(statesEqual("", "")).toBe(false);
  });
});

describe("signed oauth cookie", () => {
  const key = "test-secret";
  it("round-trips", () => {
    const v = signOAuthCookie({ state: "s1", next: "/42" }, key);
    expect(verifyOAuthCookie(v, key)).toEqual({ state: "s1", next: "/42" });
  });
  it("rejects a tampered payload", () => {
    const v = signOAuthCookie({ state: "s1", next: "/42" }, key);
    const [, sig] = v.split(".");
    const forged = Buffer.from(JSON.stringify({ state: "s1", next: "//evil.com" })).toString("base64url");
    expect(verifyOAuthCookie(`${forged}.${sig}`, key)).toBeNull();
  });
  it("rejects a tampered signature, a wrong key and garbage", () => {
    const v = signOAuthCookie({ state: "s1", next: "/42" }, key);
    expect(verifyOAuthCookie(v.slice(0, -2) + "AA", key)).toBeNull();
    expect(verifyOAuthCookie(v, "other-secret")).toBeNull();
    expect(verifyOAuthCookie("nodot", key)).toBeNull();
    expect(verifyOAuthCookie("", key)).toBeNull();
    expect(verifyOAuthCookie(undefined, key)).toBeNull();
  });
  it("re-sanitizes next even when signed", () => {
    const v = signOAuthCookie({ state: "s1", next: "//evil.com" }, key);
    expect(verifyOAuthCookie(v, key)).toEqual({ state: "s1", next: "/" });
  });
});

describe("exchangeCode", () => {
  const params = {
    code: "the-code",
    redirectUri: "https://pr0sauce.info/api/auth/callback/pr0",
    clientId: "644",
    clientSecret: "test-secret",
  };

  it("posts the form with a User-Agent and returns the access token", async () => {
    const f = vi.fn().mockResolvedValue(json({ access_token: "tok-1", token_type: "Bearer", scope: "x" }));
    expect(await exchangeCode(params, f)).toBe("tok-1");
    const [url, init] = f.mock.calls[0];
    expect(url).toBe("https://pr0gramm.com/api/oauth/createAccessToken");
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("user-agent")).toBe(USER_AGENT);
    const body = new URLSearchParams(String(init.body));
    expect(Object.fromEntries(body)).toEqual({
      grant_type: "authorization_code",
      code: "the-code",
      redirect_uri: "https://pr0sauce.info/api/auth/callback/pr0",
      client_id: "644",
      client_secret: "test-secret",
    });
  });

  it("throws without echoing the secret on a non-2xx or a missing token", async () => {
    for (const res of [json({ error: "invalid_grant" }, 400), json({ token_type: "Bearer" })]) {
      const f = vi.fn().mockResolvedValue(res);
      const err = await exchangeCode(params, f).catch((e: Error) => e);
      expect(err).toBeInstanceOf(Error);
      expect(String((err as Error).message)).not.toContain("test-secret");
    }
  });
});

describe("fetchIdentity", () => {
  it("resolves name then id with Bearer and User-Agent", async () => {
    const f = vi
      .fn()
      .mockResolvedValueOnce(json({ name: "Pacerino" }))
      .mockResolvedValueOnce(json({ user: { id: 1234, name: "Pacerino", mark: 1 } }));
    expect(await fetchIdentity("tok-1", f)).toEqual({ id: 1234, name: "Pacerino" });
    expect(f.mock.calls[0][0]).toBe("https://pr0gramm.com/api/user/name");
    expect(f.mock.calls[1][0]).toBe("https://pr0gramm.com/api/profile/info?name=Pacerino&flags=1");
    for (const [, init] of f.mock.calls) {
      const h = new Headers(init.headers);
      expect(h.get("authorization")).toBe("Bearer tok-1");
      expect(h.get("user-agent")).toBe(USER_AGENT);
    }
  });

  it("rejects a bad id, a missing name or a non-2xx", async () => {
    const cases = [
      [json({ name: "" })],
      [json({ name: "x" }, 401)],
      [json({ name: "x" }), json({ user: { id: 0, name: "x" } })],
      [json({ name: "x" }), json({ user: { id: "7", name: "x" } })],
      [json({ name: "x" }), json({ user: { id: 7, name: "y" } })],
      [json({ name: "x" }), json({}, 404)],
    ];
    for (const responses of cases) {
      const f = vi.fn();
      for (const r of responses) f.mockResolvedValueOnce(r);
      await expect(fetchIdentity("tok-1", f)).rejects.toThrow();
    }
  });
});
