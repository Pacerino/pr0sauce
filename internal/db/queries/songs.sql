-- name: GetSong :one
select * from songs where item_id = $1;

-- name: UpsertSong :exec
insert into songs (item_id, title, artist, album, provider, spotify_url, spotify_id, youtube_url, youtube_id, cover_url, recognized_at, apple_music_url, isrc, deezer_url)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
on conflict (item_id) do update set
  title = excluded.title, artist = excluded.artist, album = excluded.album,
  provider = excluded.provider, spotify_url = excluded.spotify_url, spotify_id = excluded.spotify_id,
  youtube_url = excluded.youtube_url, youtube_id = excluded.youtube_id,
  cover_url = excluded.cover_url, recognized_at = excluded.recognized_at,
  apple_music_url = excluded.apple_music_url, isrc = excluded.isrc, deezer_url = excluded.deezer_url
where songs.corrected_at is null;

-- name: LockSong :one
select item_id from songs where item_id = $1 for update;

-- name: SongSnapshot :one
select to_jsonb(s) as snapshot from songs s where s.item_id = $1;

-- name: ApplyCorrection :exec
-- Clears every field the bot's match produced (deezer_url and cover_url are
-- cleared by being overwritten from the params), then writes the corrected ones.
update songs set
  apple_music_url = null, isrc = null, spotify_url = null,
  spotify_id = null, youtube_url = null, youtube_id = null,
  title = $2, artist = $3, album = $4, provider = $5, deezer_url = $6, cover_url = $7,
  corrected_at = now()
where item_id = $1;

-- name: RestoreSnapshot :exec
-- Writes every column back from a SongSnapshot jsonb, corrected_at included.
update songs s set
  title = r.title, artist = r.artist, album = r.album, provider = r.provider,
  spotify_url = r.spotify_url, spotify_id = r.spotify_id,
  youtube_url = r.youtube_url, youtube_id = r.youtube_id,
  cover_url = r.cover_url, recognized_at = r.recognized_at,
  apple_music_url = r.apple_music_url, isrc = r.isrc, deezer_url = r.deezer_url,
  corrected_at = r.corrected_at
from jsonb_populate_record(null::songs, sqlc.arg(snapshot)::jsonb) r
where s.item_id = $1;

-- name: ListRecentSongs :many
-- Hits only, newest first. Keyset: rows strictly older than (before_recognized_at, before_item_id).
select * from songs
where title is not null
  and (recognized_at, item_id) < (sqlc.arg(before_recognized_at)::timestamptz, sqlc.arg(before_item_id)::bigint)
order by recognized_at desc, item_id desc
limit sqlc.arg(lim)::int;

-- name: SearchSongs :many
-- pattern is an already LIKE-escaped, %-wrapped string; q is the raw query used for ranking.
select * from songs
where title is not null
  and (title ilike sqlc.arg(pattern)::text escape '\' or artist ilike sqlc.arg(pattern)::text escape '\')
order by similarity(coalesce(title,'') || ' ' || coalesce(artist,''), sqlc.arg(q)::text) desc, recognized_at desc, item_id desc
limit sqlc.arg(lim)::int offset sqlc.arg(off)::int;
