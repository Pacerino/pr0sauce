import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "./api";

const json = (body: unknown, status = 200) =>
  new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });

describe("api", () => {
  const fetchMock = vi.fn();
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    vi.stubEnv("API_URL", "http://example.test:9000/");
  });
  afterEach(() => {
    fetchMock.mockReset();
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
  });

  it("song() returns null on 404", async () => {
    fetchMock.mockResolvedValue(json({ error: "song not found" }, 404));
    expect(await api.song(42)).toBeNull();
    expect(fetchMock.mock.calls[0][0]).toBe("http://example.test:9000/api/songs/42");
  });

  it("song() throws on other non-2xx", async () => {
    fetchMock.mockResolvedValue(json({ error: "boom" }, 500));
    await expect(api.song(42)).rejects.toMatchObject({ status: 500, message: "boom" });
  });

  it("songs() throws ApiError with status on 400", async () => {
    fetchMock.mockResolvedValue(json({ error: "invalid cursor" }, 400));
    const err = await api.songs({ cursor: "bad" }).catch((e) => e);
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(400);
    expect(err.message).toBe("invalid cursor");
  });

  it("songs() builds the URL from API_URL with encoded params and no-store", async () => {
    fetchMock.mockResolvedValue(json({ items: [], nextCursor: null }));
    await api.songs({ q: "a b&c", cursor: "xyz" });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("http://example.test:9000/api/songs?q=a+b%26c&cursor=xyz");
    expect(init.cache).toBe("no-store");
  });

  it("songs() percent-encodes a Cyrillic query", async () => {
    fetchMock.mockResolvedValue(json({ items: [], nextCursor: null }));
    await api.songs({ q: "Новая" });
    expect(fetchMock.mock.calls[0][0]).toBe(
      "http://example.test:9000/api/songs?q=%D0%9D%D0%BE%D0%B2%D0%B0%D1%8F",
    );
  });

  it("stats() revalidates for 60s", async () => {
    fetchMock.mockResolvedValue(json({}));
    await api.stats();
    expect(fetchMock.mock.calls[0][1].next).toEqual({ revalidate: 60 });
  });
});
