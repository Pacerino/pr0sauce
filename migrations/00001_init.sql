-- +goose Up
create extension if not exists pg_trgm;

create table songs (
  item_id       bigint primary key,
  title         text,              -- NULL = looked up, nothing found
  artist        text,
  album         text,
  provider      text not null,     -- 'shazam' | 'acrcloud' (legacy) | 'audd' (legacy) | '' (legacy miss)
  spotify_url   text, spotify_id text,
  youtube_url   text, youtube_id text,
  cover_url     text,
  recognized_at timestamptz not null
);
create index songs_recognized_at on songs (recognized_at desc);
create index songs_search_trgm on songs using gin ((coalesce(title,'') || ' ' || coalesce(artist,'')) gin_trgm_ops);

create table requests (
  id             bigserial primary key,
  message_id     bigint not null unique,
  item_id        bigint not null,
  requester      text not null,
  thumb          text not null default '',
  mentioned_at   timestamptz not null,
  status         text not null check (status in ('pending','replying','done','failed','skipped')),
  reply_kind     text check (reply_kind in ('comment','pm')),
  attempts       int not null default 0,
  next_attempt_at timestamptz not null default now(),
  last_error     text,
  marked_read    boolean not null default false,
  created_at     timestamptz not null default now(),
  updated_at     timestamptz not null default now()
);
create index requests_due on requests (next_attempt_at) where status = 'pending';

create table bot_comments (
  comment_id bigint primary key,
  item_id    bigint not null,
  content    text not null,
  up int not null, down int not null,
  thumb      text,
  created_at timestamptz not null
);
create index bot_comments_item on bot_comments (item_id);

create table bot_state (key text primary key, value text not null);   -- 'live_since', 'session_cookies'

-- +goose Down
drop table bot_state;
drop table bot_comments;
drop table requests;
drop table songs;
-- pg_trgm is left installed on purpose: the extension may be shared.
