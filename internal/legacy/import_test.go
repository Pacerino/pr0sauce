package legacy_test

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/legacy"
	"github.com/Pacerino/pr0sauce/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const legacyDDL = `
create schema pr0music;
create table pr0music.items (
  id bigserial primary key, created_at timestamptz, updated_at timestamptz, deleted_at timestamptz,
  item_id bigint, title text, album text, artist text, url text, acr_id text,
  spotify_url text, spotify_id text, youtube_url text, youtube_id text, provider text);
create table pr0music.comments (
  id bigserial primary key, created_at timestamptz, updated_at timestamptz, deleted_at timestamptz,
  comment_id bigint, up bigint, down bigint, content text, created timestamptz, item_id bigint, thumb text);
`

func newSource(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:13",
		tcpostgres.WithDatabase("pr0music"), tcpostgres.WithUsername("u"), tcpostgres.WithPassword("p"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
			WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(ctr) })
	url, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, legacyDDL); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestImport_DedupeAndMapping(t *testing.T) {
	ctx := context.Background()
	src := newSource(t)
	dst := testdb.New(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	_, err := src.Exec(ctx, `
insert into pr0music.items (created_at, item_id, title, album, artist, url, acr_id, spotify_url, spotify_id, youtube_url, youtube_id, provider) values
 ('2024-01-01T10:00:00Z', 1, 'A', 'Alb', 'Art', 'u1', 'acr', 'https://sp/1', 'sp1', '', '', 'acrcloud'),
 ('2024-01-02T10:00:00Z', 1, '', '', '', 'u1', '', '', '', '', '', 'acrcloud'),
 ('2024-02-01T10:00:00Z', 2, 'Old', '', 'X', '', '', '', '', '', '', 'shazam'),
 ('2024-02-02T10:00:00Z', 2, 'New', '', 'Y', '', '', '', '', 'https://yt/2', 'yt2', 'shazam'),
 ('2024-03-01T10:00:00Z', 3, '', '', '', '', '', '', '', '', '', 'shazam');
insert into pr0music.items (created_at, deleted_at, item_id, title, provider) values
 ('2024-04-01T10:00:00Z', now(), 4, 'Gone', 'shazam');
insert into pr0music.comments (created_at, deleted_at, comment_id, up, down, content, created, item_id)
 values ('2020-01-01T00:00:00Z', now(), 901, 0, 0, 'gone', '2024-01-01T10:00:00Z', 4);
insert into pr0music.comments (created_at, comment_id, up, down, content, created, item_id, thumb)
 values ('2020-01-01T00:00:00Z', 900, 5, 1, 'hello', '2024-01-01T10:00:00Z', 1, 'thumb.jpg')`)
	if err != nil {
		t.Fatal(err)
	}

	rep, err := legacy.Import(ctx, src, dst, log, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := (legacy.Report{Songs: 3, Hits: 2, DuplicatesDropped: 2, Comments: 1, DatesFromCreated: 3}); rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}

	q := db.New(dst)
	s1, err := q.GetSong(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if s1.Title == nil || *s1.Title != "A" || s1.Artist == nil || *s1.Artist != "Art" ||
		s1.YoutubeUrl != nil || s1.SpotifyUrl == nil || *s1.SpotifyUrl != "https://sp/1" ||
		s1.CoverUrl != nil || s1.Provider != "acrcloud" ||
		!s1.RecognizedAt.Equal(time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("song 1 = %+v", s1)
	}
	s2, _ := q.GetSong(ctx, 2)
	if s2.Title == nil || *s2.Title != "New" || s2.Album != nil || s2.YoutubeID == nil || *s2.YoutubeID != "yt2" {
		t.Fatalf("song 2 = %+v", s2)
	}
	s3, _ := q.GetSong(ctx, 3)
	if s3.Title != nil || s3.Artist != nil || s3.Provider != "shazam" {
		t.Fatalf("song 3 = %+v", s3)
	}
	var content, thumb string
	var created time.Time
	var up, down int
	if err := dst.QueryRow(ctx, `select content, thumb, up, down, created_at from bot_comments where comment_id=900`).Scan(&content, &thumb, &up, &down, &created); err != nil {
		t.Fatal(err)
	}
	if content != "hello" || !created.Equal(time.Unix(1704103200, 0)) || thumb != "thumb.jpg" || up != 5 || down != 1 {
		t.Fatalf("comment = %q %q %d %d", content, thumb, up, down)
	}

	// Guard: songs is non-empty, so a second run without force must refuse.
	if _, err := legacy.Import(ctx, src, dst, log, false); err == nil {
		t.Fatal("expected refusal on non-empty songs")
	}
	// With force it is idempotent.
	rep2, err := legacy.Import(ctx, src, dst, log, true)
	if err != nil || rep2 != rep {
		t.Fatalf("force rerun: %+v %v", rep2, err)
	}
}

func assertEmpty(t *testing.T, dst *pgxpool.Pool) {
	t.Helper()
	var songs, comments int
	if err := dst.QueryRow(context.Background(),
		`select (select count(*) from songs), (select count(*) from bot_comments)`).Scan(&songs, &comments); err != nil {
		t.Fatal(err)
	}
	if songs != 0 || comments != 0 {
		t.Fatalf("expected rollback, got songs=%d comments=%d", songs, comments)
	}
}

func TestImport_VoteOverflowAborts(t *testing.T) {
	ctx := context.Background()
	src := newSource(t)
	dst := testdb.New(t)
	_, err := src.Exec(ctx, `
insert into pr0music.items (created_at, item_id, title, provider) values ('2024-01-01T10:00:00Z', 1, 'A', 'x');
insert into pr0music.comments (created_at, comment_id, up, down, content, created, item_id)
 values ('2020-01-01T00:00:00Z', 777, 2147483648, 0, 'c', '2024-01-01T10:00:00Z', 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Import(ctx, src, dst, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err == nil || !strings.Contains(err.Error(), "777") || !strings.Contains(err.Error(), "2147483648") {
		t.Fatalf("err = %v", err)
	}
	assertEmpty(t, dst)
}

func TestImport_NullCommentCreatedAborts(t *testing.T) {
	ctx := context.Background()
	src := newSource(t)
	dst := testdb.New(t)
	_, err := src.Exec(ctx, `
insert into pr0music.items (created_at, item_id, title, provider) values (now(), 1, 'A', 'x');
insert into pr0music.comments (created_at, comment_id, up, down, content, created, item_id)
 values (now(), 555, 0, 0, 'c', null, 1)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Import(ctx, src, dst, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err == nil || !strings.Contains(err.Error(), "555") {
		t.Fatalf("err = %v", err)
	}
	assertEmpty(t, dst)
}

func TestImport_RecognizedAtFallback(t *testing.T) {
	ctx := context.Background()
	src := newSource(t)
	dst := testdb.New(t)
	// item 1: undated, nothing else -> leading, takes next dated (item 2)
	// item 2: created_at
	// item 3: no created_at, earliest comment (two comments, earlier wins)
	// item 4: no created_at, no comment, updated_at
	// item 5: nothing -> estimated from nearest lower resolved (item 4)
	// item 6: created_at (later than 4) ; item 7 nothing -> takes item 6
	_, err := src.Exec(ctx, `
insert into pr0music.items (created_at, updated_at, item_id, title, provider) values
 (null, null, 1, 'a', 'x'),
 ('2024-01-01T00:00:00Z', null, 2, 'b', 'x'),
 (null, '2025-01-01T00:00:00Z', 3, 'c', 'x'),
 (null, '2024-06-01T00:00:00Z', 4, 'd', 'x'),
 (null, null, 5, 'e', 'x'),
 ('2024-09-01T00:00:00Z', null, 6, 'f', 'x'),
 (null, null, 7, 'g', 'x');
insert into pr0music.comments (created_at, comment_id, up, down, content, created, item_id) values
 (now(), 10, 0, 0, 'c', '2024-03-02T00:00:00Z', 3),
 (now(), 11, 0, 0, 'c', '2024-03-01T00:00:00Z', 3)`)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := legacy.Import(ctx, src, dst, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	if err != nil {
		t.Fatal(err)
	}
	want := legacy.Report{Songs: 7, Hits: 7, Comments: 2, DatesFromCreated: 2, DatesFromComment: 1, DatesFromUpdated: 1, DatesEstimated: 3}
	if rep != want {
		t.Fatalf("report = %+v, want %+v", rep, want)
	}
	exp := map[int64]string{1: "2024-01-01", 2: "2024-01-01", 3: "2024-03-01", 4: "2024-06-01",
		5: "2024-06-01", 6: "2024-09-01", 7: "2024-09-01"}
	q := db.New(dst)
	for id, d := range exp {
		s, err := q.GetSong(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got := s.RecognizedAt.UTC().Format("2006-01-02"); got != d {
			t.Errorf("item %d recognized_at = %s, want %s", id, got, d)
		}
	}
}
