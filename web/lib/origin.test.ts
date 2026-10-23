import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { assertSameOrigin, isSameOrigin } from "./origin";

const req = (origin?: string) =>
  new Request("http://internal:3000/api/auth/logout", {
    method: "POST",
    headers: origin === undefined ? {} : { origin },
  });

describe("assertSameOrigin", () => {
  beforeEach(() => vi.stubEnv("PUBLIC_URL", "https://pr0sauce.info"));
  afterEach(() => vi.unstubAllEnvs());

  it("accepts the PUBLIC_URL origin", () => {
    expect(() => assertSameOrigin(req("https://pr0sauce.info"))).not.toThrow();
    expect(isSameOrigin(req("https://pr0sauce.info"))).toBe(true);
  });

  it.each([
    [undefined],
    ["null"],
    [""],
    ["https://evil.com"],
    ["http://pr0sauce.info"],
    ["https://pr0sauce.info.evil.com"],
    ["https://pr0sauce.info:444"],
  ])("rejects Origin %j", (o) => {
    expect(() => assertSameOrigin(req(o))).toThrow();
    expect(isSameOrigin(req(o))).toBe(false);
  });

  it("fails closed without PUBLIC_URL", () => {
    vi.stubEnv("PUBLIC_URL", "");
    expect(isSameOrigin(req("https://pr0sauce.info"))).toBe(false);
  });
});
