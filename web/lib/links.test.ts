import { describe, expect, it } from "vitest";
import { streamingButtons } from "./links";
import type { Song } from "./types";

const base: Song = {
  itemId: 1,
  title: "t",
  artist: "a",
  album: null,
  provider: "shazam",
  spotifyUrl: null,
  youtubeUrl: null,
  appleMusicUrl: null,
  deezerUrl: null,
  songLinkUrl: null,
  spotifySearchUrl: null,
  youtubeSearchUrl: null,
  coverUrl: null,
  recognizedAt: "2026-01-01T00:00:00Z",
  comment: null,
  found: true,
  corrected: null,
};

describe("streamingButtons", () => {
  it("legacy song yields Spotify and YouTube only", () => {
    const s = { ...base, spotifyUrl: "https://open.spotify.com/track/1", youtubeUrl: "https://youtu.be/x" };
    expect(streamingButtons(s).map((b) => b.label)).toEqual(["Spotify", "YouTube"]);
  });

  it("legacy exact links win over search links", () => {
    const s = {
      ...base,
      spotifyUrl: "https://open.spotify.com/track/1",
      youtubeUrl: "https://youtu.be/x",
      spotifySearchUrl: "https://open.spotify.com/search/q",
      youtubeSearchUrl: "https://www.youtube.com/results?search_query=q",
    };
    expect(streamingButtons(s).map((b) => b.label)).toEqual(["Spotify", "YouTube"]);
  });

  it("new song yields search, apple, deezer, search, all in order", () => {
    const s = {
      ...base,
      appleMusicUrl: "https://music.apple.com/x",
      deezerUrl: "https://www.deezer.com/track/1",
      songLinkUrl: "https://song.link/x",
      spotifySearchUrl: "https://open.spotify.com/search/q",
      youtubeSearchUrl: "https://www.youtube.com/results?search_query=q",
    };
    const b = streamingButtons(s);
    expect(b.map((x) => x.label)).toEqual([
      "Auf Spotify suchen",
      "Apple Music",
      "Deezer",
      "Auf YouTube suchen",
      "Alle Plattformen",
    ]);
    expect(b.map((x) => x.kind)).toEqual(["spotify", "apple", "deezer", "youtube", "all"]);
  });

  it("drops unsafe urls", () => {
    const s = { ...base, deezerUrl: "javascript:alert(1)", appleMusicUrl: "https://music.apple.com/x" };
    expect(streamingButtons(s).map((b) => b.label)).toEqual(["Apple Music"]);
  });

  it("falls back to search when the legacy url is unsafe", () => {
    const s = { ...base, spotifyUrl: "javascript:1", spotifySearchUrl: "https://open.spotify.com/search/q" };
    expect(streamingButtons(s).map((b) => b.label)).toEqual(["Auf Spotify suchen"]);
  });
});
