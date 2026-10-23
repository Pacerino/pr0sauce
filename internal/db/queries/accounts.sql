-- name: UpsertUserLogin :one
insert into users (pr0_user_id, name) values ($1, $2)
on conflict (pr0_user_id) do update set name = excluded.name, last_login_at = now()
returning *;

-- name: GetUserByPr0ID :one
select * from users where pr0_user_id = $1;

-- name: GetUser :one
select * from users where id = $1;

-- name: CountAdmins :one
select count(*) from users where role = 'admin';

-- name: SetRole :exec
update users set role = $2 where id = $1;

-- name: SetBan :exec
update users set banned_at = now(), ban_reason = $2 where id = $1;

-- name: ClearBan :exec
update users set banned_at = null, ban_reason = null where id = $1;

-- name: ListModerators :many
select * from users where role in ('moderator','admin') order by role, name;

-- name: ListBanned :many
select * from users where banned_at is not null order by banned_at desc;

-- name: CreateSession :exec
insert into sessions (token_hash, user_id, expires_at) values ($1, $2, $3);

-- name: GetSessionUser :one
select u.* from sessions s join users u on u.id = s.user_id
where s.token_hash = $1 and s.expires_at > sqlc.arg(now)::timestamptz;

-- name: TouchSession :exec
update sessions set expires_at = $2 where token_hash = $1;

-- name: DeleteSession :exec
delete from sessions where token_hash = $1;
