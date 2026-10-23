-- name: UpsertBotComment :exec
insert into bot_comments (comment_id, item_id, content, up, down, thumb, created_at)
values ($1, $2, $3, $4, $5, $6, $7)
on conflict (comment_id) do update set
  item_id = excluded.item_id, content = excluded.content, up = excluded.up,
  down = excluded.down, thumb = excluded.thumb, created_at = excluded.created_at;

-- name: LatestBotCommentTime :one
-- Null when the table is empty.
select b.created_at as latest
from (select 1) as one
left join bot_comments as b on true
order by b.created_at desc nulls last
limit 1;

-- name: GetBotCommentForItem :one
select * from bot_comments where item_id = $1 order by created_at desc, comment_id desc limit 1;

-- name: GetState :one
select value from bot_state where key = $1;

-- name: SetState :exec
insert into bot_state (key, value) values ($1, $2)
on conflict (key) do update set value = excluded.value;
