-- name: CreateSuggestion :one
insert into suggestions (item_id, user_id, title, artist, album, proof_url, status)
values ($1, $2, $3, $4, $5, $6, 'open')
returning *;

-- name: GetSuggestion :one
select * from suggestions where id = $1;

-- name: ListOpenSuggestionsForItem :many
select s.*, u.name as author_name,
  (select count(*) from votes v join users vu on vu.id = v.user_id where v.suggestion_id = s.id and v.value = 1 and vu.banned_at is null)::bigint as up,
  (select count(*) from votes v join users vu on vu.id = v.user_id where v.suggestion_id = s.id and v.value = -1 and vu.banned_at is null)::bigint as down,
  (select coalesce(sum(v.value), 0) from votes v join users vu on vu.id = v.user_id where v.suggestion_id = s.id and vu.banned_at is null)::bigint as net
from suggestions s join users u on u.id = s.user_id
where s.item_id = $1 and s.status = 'open'
order by net desc, s.created_at, s.id;

-- name: ListOpenQueue :many
select s.*, u.name as author_name,
  (select count(*) from votes v join users vu on vu.id = v.user_id where v.suggestion_id = s.id and v.value = 1 and vu.banned_at is null)::bigint as up,
  (select count(*) from votes v join users vu on vu.id = v.user_id where v.suggestion_id = s.id and v.value = -1 and vu.banned_at is null)::bigint as down,
  (select coalesce(sum(v.value), 0) from votes v join users vu on vu.id = v.user_id where v.suggestion_id = s.id and vu.banned_at is null)::bigint as net
from suggestions s join users u on u.id = s.user_id
where s.status = 'open'
order by s.created_at, s.id
limit sqlc.arg(lim)::int offset sqlc.arg(off)::int;

-- name: CountUserSuggestionsSince :one
select count(*) from suggestions where user_id = $1 and created_at >= $2;

-- name: SetSuggestionStatus :exec
update suggestions set status = $2, reviewer_id = $3, reason = $4, decided_at = now() where id = $1;

-- name: SupersedeOthers :exec
update suggestions set status = 'superseded', decided_at = now()
where item_id = $1 and id <> $2 and status = 'open';

-- name: CloseOpenSuggestionsOfUser :exec
update suggestions set status = 'rejected', reason = $2, decided_at = now()
where user_id = $1 and status = 'open';

-- name: UpsertVote :exec
insert into votes (suggestion_id, user_id, value) values ($1, $2, $3)
on conflict (suggestion_id, user_id) do update set value = excluded.value;

-- name: DeleteVote :exec
delete from votes where suggestion_id = $1 and user_id = $2;

-- name: TallySuggestion :one
select
  (count(*) filter (where v.value = 1))::bigint as up,
  (count(*) filter (where v.value = -1))::bigint as down,
  (coalesce(sum(v.value), 0))::bigint as net
from votes v join users u on u.id = v.user_id
where v.suggestion_id = $1 and u.banned_at is null;

-- name: InsertHistory :one
insert into song_history (item_id, snapshot, source, actor_id, suggestion_id)
values ($1, $2, $3, $4, $5)
returning *;

-- name: ListHistory :many
select * from song_history where item_id = $1 order by id desc;

-- name: GetHistory :one
select * from song_history where id = $1;

-- name: CountRequestsByStatus :many
select status, count(*)::bigint as n from requests group by status order by status;

-- name: ListFailedRequests :many
select * from requests where status = 'failed' order by updated_at desc, id desc
limit sqlc.arg(lim)::int offset sqlc.arg(off)::int;

-- name: PendingRetryStats :one
select count(*)::bigint as pending, coalesce(max(attempts), 0)::int as max_attempts, min(next_attempt_at)::timestamptz as next_attempt_at
from requests where status = 'pending' and attempts > 0;

-- name: LastDoneAt :one
select max(updated_at)::timestamptz as last_done_at from requests where status = 'done';
