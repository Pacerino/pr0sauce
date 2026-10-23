-- name: InsertRequestIgnore :one
-- Returns true when a row was inserted, false when message_id already existed.
-- status and next_attempt_at come from the app (its clock), so skipped rows are
-- written atomically and due times never depend on the DB clock.
with ins as (
  insert into requests (message_id, item_id, requester, thumb, mentioned_at, status, next_attempt_at)
  values ($1, $2, $3, $4, $5, sqlc.arg(status), sqlc.arg(next_attempt_at))
  on conflict (message_id) do nothing
  returning 1
)
select exists (select 1 from ins) as inserted;

-- name: DueRequests :many
select * from requests
where status = 'pending' and next_attempt_at <= sqlc.arg(now)::timestamptz
order by id
limit sqlc.arg(lim)::int;

-- name: SetRequestStatus :execrows
-- Only live (pending/replying) rows may change status; terminal rows are never resurrected.
update requests
set status = $2, reply_kind = $3, last_error = $4, updated_at = now()
where id = $1 and status in ('pending', 'replying');

-- name: BumpAttempt :execrows
update requests
set attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
where id = $1 and status = 'pending';

-- name: MarkReplying :execrows
-- Claims a request; 0 rows affected means someone else already moved it.
update requests set status = 'replying', updated_at = now() where id = $1 and status = 'pending';

-- name: RetryFromReplying :execrows
-- Releases a claimed request back to pending, only when pr0gramm explicitly
-- rejected the reply (it was definitely not posted).
update requests
set status = 'pending', attempts = attempts + 1, next_attempt_at = $2, last_error = $3, updated_at = now()
where id = $1 and status = 'replying';

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
