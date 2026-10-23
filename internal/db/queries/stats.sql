-- name: CountSongs :one
select count(*) from songs;

-- name: CountHits :one
select count(*) from songs where title is not null;

-- name: CountSongsSince :one
select count(*) from songs where recognized_at >= $1;

-- name: ProviderShare :many
select provider, count(*)::bigint as hits from songs
where title is not null
group by provider
order by hits desc, provider;

-- name: SongsPerWeek :many
select date_trunc('week', recognized_at)::timestamptz as week, count(*)::bigint as songs
from songs
where recognized_at >= $1
group by 1
order by 1;

-- name: TopSongs :many
select min(title)::text as title, min(artist)::text as artist, count(*)::bigint as plays
from songs
where title is not null
group by lower(artist), lower(title)
order by plays desc, lower(title), lower(artist)
limit $1;

-- name: TopRequesters :many
select requester, count(*)::bigint as requests from requests
where status = 'done'
group by requester
order by requests desc, requester
limit $1;
