-- name: InsertRequestIgnore :one
-- Returns true when a row was inserted, false when message_id already existed.
with ins as (
  insert into requests (message_id, item_id, requester, thumb, mentioned_at, status)
  values ($1, $2, $3, $4, $5, 'pending')
  on conflict (message_id) do nothing
  returning 1
)
select exists (select 1 from ins) as inserted;

-- name: DueRequests :many
select * from requests
where status = 'pending' and next_attempt_at <= sqlc.arg(now)::timestamptz
order by id
limit sqlc.arg(lim)::int;

-- name: SetRequestStatus :exec
update requests
set status = $2, reply_kind = $3, last_error = $4, updated_at = now()
where id = $1;

-- name: BumpAttempt :exec
update requests
set attempts = attempts + 1, next_attempt_at = $2, last_error = $3, status = 'pending', updated_at = now()
where id = $1;

-- name: MarkReplying :exec
update requests set status = 'replying', updated_at = now() where id = $1;

-- name: FailStaleReplying :many
update requests
set status = 'failed', last_error = 'reply state unknown after restart', updated_at = now()
where status = 'replying'
returning *;

-- name: UnreadHandledRequests :many
select * from requests
where status in ('done', 'skipped', 'failed') and marked_read = false
order by id;

-- name: SetMarkedRead :exec
update requests set marked_read = true, updated_at = now() where id = $1;
