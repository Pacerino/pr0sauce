import { safeHttpUrl } from "./format";
import type { Song } from "./types";

export type ButtonKind = "spotify" | "apple" | "deezer" | "youtube" | "all";
export type StreamingButton = { label: string; href: string; kind: ButtonKind };

export function streamingButtons(song: Song): StreamingButton[] {
  const out: StreamingButton[] = [];
  const add = (label: string, url: string | null, kind: ButtonKind) => {
    const href = safeHttpUrl(url);
    if (href) out.push({ label, href, kind });
  };
  const pick = (exact: string | null, search: string | null, label: string, searchLabel: string, kind: ButtonKind) => {
    if (safeHttpUrl(exact)) add(label, exact, kind);
    else add(searchLabel, search, kind);
  };

  pick(song.spotifyUrl, song.spotifySearchUrl, "Spotify", "Auf Spotify suchen", "spotify");
  add("Apple Music", song.appleMusicUrl, "apple");
  add("Deezer", song.deezerUrl, "deezer");
  pick(song.youtubeUrl, song.youtubeSearchUrl, "YouTube", "Auf YouTube suchen", "youtube");
  add("Alle Plattformen", song.songLinkUrl, "all");
  return out;
}

export const buttonClass: Record<ButtonKind, string> = {
  spotify: "bg-[#1db954] text-white",
  apple: "bg-[#fa243c] text-white",
  deezer: "bg-[#a238ff] text-white",
  youtube: "bg-[#e62117] text-white",
  all: "bg-surface text-white border border-accent",
};
