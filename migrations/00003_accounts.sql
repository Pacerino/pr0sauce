-- +goose Up
alter table songs add column corrected_at timestamptz;

create table users (
  id            bigserial primary key,
  pr0_user_id   bigint not null unique,
  name          text not null,
  role          text not null default 'user' check (role in ('user','moderator','admin')),
  banned_at     timestamptz,
  ban_reason    text,
  created_at    timestamptz not null default now(),
  last_login_at timestamptz not null default now()
);

create table sessions (
  token_hash bytea primary key,
  user_id    bigint not null references users (id) on delete cascade,
  created_at timestamptz not null default now(),
  expires_at timestamptz not null
);
create index sessions_user on sessions (user_id);

create table suggestions (
  id          bigserial primary key,
  item_id     bigint not null references songs (item_id),
  user_id     bigint not null references users (id),
  title       text not null,
  artist      text not null,
  album       text,
  proof_url   text,
  status      text not null check (status in ('open','approved','auto_approved','rejected','superseded')),
  reviewer_id bigint references users (id),
  reason      text,
  created_at  timestamptz not null default now(),
  decided_at  timestamptz
);
create unique index suggestions_one_open on suggestions (item_id, user_id) where status = 'open';
create index suggestions_item on suggestions (item_id);
create index suggestions_open_queue on suggestions (created_at, id) where status = 'open';

create table votes (
  suggestion_id bigint not null references suggestions (id) on delete cascade,
  user_id       bigint not null references users (id),
  value         smallint not null check (value in (-1, 1)),
  created_at    timestamptz not null default now(),
  primary key (suggestion_id, user_id)
);

create table song_history (
  id            bigserial primary key,
  item_id       bigint not null,
  snapshot      jsonb not null,
  source        text not null check (source in ('suggestion','vote','moderator','revert')),
  actor_id      bigint references users (id),
  suggestion_id bigint references suggestions (id),
  created_at    timestamptz not null default now()
);
create index song_history_item on song_history (item_id, id desc);

-- +goose Down
drop table song_history;
drop table votes;
drop table suggestions;
drop table sessions;
drop table users;
alter table songs drop column corrected_at;
