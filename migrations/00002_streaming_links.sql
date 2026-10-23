-- +goose Up
alter table songs
  add column apple_music_url text,
  add column isrc text,
  add column deezer_url text;

-- +goose Down
alter table songs
  drop column deezer_url,
  drop column isrc,
  drop column apple_music_url;
