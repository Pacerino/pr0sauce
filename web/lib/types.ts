// Mirrors internal/api/songs.go and internal/api/stats.go.

export type BotComment = {
  up: number;
  down: number;
  commentId: number;
};

export type Song = {
  itemId: number;
  title: string;
  artist: string;
  album: string | null;
  provider: string;
  spotifyUrl: string | null;
  youtubeUrl: string | null;
  appleMusicUrl: string | null;
  deezerUrl: string | null;
  songLinkUrl: string | null;
  spotifySearchUrl: string | null;
  youtubeSearchUrl: string | null;
  coverUrl: string | null;
  /** RFC 3339 (UTC) */
  recognizedAt: string;
  /** Only set by the single-song endpoint; null in lists. */
  comment: BotComment | null;
};

export type SongList = {
  items: Song[];
  nextCursor: string | null;
};

export type Stats = {
  totalLookups: number;
  hits: number;
  hitRate: number;
  today: number;
  providers: { provider: string; count: number }[];
  /** week is a YYYY-MM-DD date */
  perWeek: { week: string; count: number }[];
  topSongs: { title: string; artist: string; count: number }[];
  topRequesters: { name: string; count: number }[];
  /** RFC 3339 (UTC) or null */
  requestersSince: string | null;
};
