-- name: GetSong :one
select * from songs where item_id = $1;

-- name: UpsertSong :exec
insert into songs (item_id, title, artist, album, provider, spotify_url, spotify_id, youtube_url, youtube_id, cover_url, recognized_at)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
on conflict (item_id) do update set
  title = excluded.title, artist = excluded.artist, album = excluded.album,
  provider = excluded.provider, spotify_url = excluded.spotify_url, spotify_id = excluded.spotify_id,
  youtube_url = excluded.youtube_url, youtube_id = excluded.youtube_id,
  cover_url = excluded.cover_url, recognized_at = excluded.recognized_at;

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
order by similarity(coalesce(title,'') || ' ' || coalesce(artist,''), sqlc.arg(q)::text) desc, recognized_at desc
limit sqlc.arg(lim)::int offset sqlc.arg(off)::int;
