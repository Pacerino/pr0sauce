package db_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/testdb"
	"github.com/Pacerino/pr0sauce/migrations"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestMigrate_00003_UpDown(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	want := []string{"users", "sessions", "suggestions", "votes", "song_history"}
	got := tablesPresent(t, pool)
	for _, n := range want {
		if !got[n] {
			t.Fatalf("table %s missing after up", n)
		}
	}
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetBaseFS(migrations.FS)
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	if err := goose.DownToContext(ctx, sqlDB, ".", 2); err != nil {
		t.Fatal(err)
	}
	got = tablesPresent(t, pool)
	for _, n := range want {
		if got[n] {
			t.Fatalf("table %s still present after down", n)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from information_schema.columns where table_name='songs' and column_name='corrected_at'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("corrected_at still present after down: %d, %v", n, err)
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		t.Fatal(err)
	}
	if got = tablesPresent(t, pool); !got["users"] {
		t.Fatal("users missing after second up")
	}
}

func TestUpsertSong_SkipsCorrected(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	now := time.Now()
	if err := q.UpsertSong(ctx, hit(1, "Original", "A", now)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update songs set corrected_at = now() where item_id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertSong(ctx, hit(1, "Other", "B", now)); err != nil {
		t.Fatal(err)
	}
	s, err := q.GetSong(ctx, 1)
	if err != nil || *s.Title != "Original" {
		t.Fatalf("title = %v, %v; want Original", s.Title, err)
	}
	// uncorrected rows still update
	if err := q.UpsertSong(ctx, hit(2, "X", "A", now)); err != nil {
		t.Fatal(err)
	}
	if err := q.UpsertSong(ctx, hit(2, "Y", "A", now)); err != nil {
		t.Fatal(err)
	}
	if s, _ := q.GetSong(ctx, 2); *s.Title != "Y" {
		t.Fatalf("uncorrected title = %s; want Y", *s.Title)
	}
}

func mkUser(t *testing.T, q *db.Queries, pr0ID int64, name string) db.User {
	t.Helper()
	u, err := q.UpsertUserLogin(context.Background(), db.UpsertUserLoginParams{Pr0UserID: pr0ID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func mkSuggestion(q *db.Queries, item, user int64, title string) (db.Suggestion, error) {
	return q.CreateSuggestion(context.Background(), db.CreateSuggestionParams{ItemID: item, UserID: user, Title: title, Artist: "Art"})
}

func TestOneOpenSuggestionPerUserItem(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	if err := q.UpsertSong(ctx, hit(1, "T", "A", time.Now())); err != nil {
		t.Fatal(err)
	}
	u := mkUser(t, q, 100, "alice")
	first, err := mkSuggestion(q, 1, u.ID, "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mkSuggestion(q, 1, u.ID, "two"); err == nil {
		t.Fatal("second open suggestion succeeded; want unique violation")
	}
	if _, err := q.SetSuggestionStatus(ctx, db.SetSuggestionStatusParams{ID: first.ID, Status: "rejected", ReviewerID: &u.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := mkSuggestion(q, 1, u.ID, "two"); err != nil {
		t.Fatalf("suggestion after rejection: %v", err)
	}
}

func TestTally_ExcludesBannedVoters(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	if err := q.UpsertSong(ctx, hit(1, "T", "A", time.Now())); err != nil {
		t.Fatal(err)
	}
	author := mkUser(t, q, 1, "author")
	s, err := mkSuggestion(q, 1, author.ID, "x")
	if err != nil {
		t.Fatal(err)
	}
	vote := func(u db.User, v int16) {
		if err := q.UpsertVote(ctx, db.UpsertVoteParams{SuggestionID: s.ID, UserID: u.ID, Value: v}); err != nil {
			t.Fatal(err)
		}
	}
	v1, v2, v3, bad := mkUser(t, q, 2, "v1"), mkUser(t, q, 3, "v2"), mkUser(t, q, 4, "v3"), mkUser(t, q, 5, "bad")
	vote(v1, 1)
	vote(v2, 1)
	vote(v3, -1)
	vote(bad, 1)
	vote(bad, 1) // upsert, not duplicate
	if err := q.SetBan(ctx, db.SetBanParams{ID: bad.ID, BanReason: ptr("spam")}); err != nil {
		t.Fatal(err)
	}
	tl, err := q.TallySuggestion(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tl.Up != 2 || tl.Down != 1 || tl.Net != 1 {
		t.Fatalf("tally = %+v; want up 2 down 1 net 1", tl)
	}
}

func TestApplyCorrection_ClearsMatchFields(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	if _, err := pool.Exec(ctx, `insert into songs (item_id,title,artist,album,provider,spotify_url,spotify_id,youtube_url,youtube_id,cover_url,recognized_at,apple_music_url,isrc,deezer_url)
		values (1,'Old','OldA','OldAlb','shazam','sp','spid','yt','ytid','cov',now(),'am','isrc1','dz')`); err != nil {
		t.Fatal(err)
	}
	err := q.ApplyCorrection(ctx, db.ApplyCorrectionParams{ItemID: 1, Title: ptr("New"), Artist: ptr("NewA"), Album: ptr("NewAlb"), Provider: "community", DeezerUrl: ptr("dz2"), CoverUrl: ptr("cov2")})
	if err != nil {
		t.Fatal(err)
	}
	s, err := q.GetSong(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if *s.Title != "New" || *s.Artist != "NewA" || *s.Album != "NewAlb" || s.Provider != "community" || *s.DeezerUrl != "dz2" || *s.CoverUrl != "cov2" {
		t.Fatalf("set fields wrong: %+v", s)
	}
	if s.AppleMusicUrl != nil || s.Isrc != nil || s.SpotifyUrl != nil || s.SpotifyID != nil || s.YoutubeUrl != nil || s.YoutubeID != nil {
		t.Fatalf("match fields not cleared: %+v", s)
	}
	var corrected bool
	if err := pool.QueryRow(ctx, `select corrected_at is not null from songs where item_id=1`).Scan(&corrected); err != nil || !corrected {
		t.Fatalf("corrected_at set = %v, %v", corrected, err)
	}
}

func TestSnapshotRestore_RoundTrip(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	if err := q.UpsertSong(ctx, db.UpsertSongParams{ItemID: 1, Title: ptr("Orig"), Artist: ptr("A"), Provider: "shazam", SpotifyUrl: ptr("sp"), RecognizedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	snap, err := q.SongSnapshot(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.ApplyCorrection(ctx, db.ApplyCorrectionParams{ItemID: 1, Title: ptr("Fixed"), Artist: ptr("B"), Provider: "community"}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.RestoreSnapshot(ctx, db.RestoreSnapshotParams{ItemID: 1, Snapshot: snap}); err != nil {
		t.Fatal(err)
	}
	s, err := q.GetSong(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if *s.Title != "Orig" || *s.Artist != "A" || s.Provider != "shazam" || s.SpotifyUrl == nil || *s.SpotifyUrl != "sp" {
		t.Fatalf("not restored: %+v", s)
	}
	var corrected bool
	if err := pool.QueryRow(ctx, `select corrected_at is not null from songs where item_id=1`).Scan(&corrected); err != nil || corrected {
		t.Fatalf("corrected_at still set after revert to original: %v, %v", corrected, err)
	}
	if _, err := q.LockSong(ctx, 1); err != nil {
		t.Fatal(err)
	}
}

func TestPendingRetryStats_Empty(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	st, err := q.PendingRetryStats(ctx)
	if err != nil || st.NextAttemptAt != nil || st.Pending != 0 {
		t.Fatalf("empty stats = %+v, %v; want nil next, 0 pending", st, err)
	}
	now := time.Now().Truncate(time.Microsecond)
	if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: 1, ItemID: 1, Requester: "b", MentionedAt: now, Status: "pending", NextAttemptAt: now}); err != nil {
		t.Fatal(err)
	}
	due := now.Add(time.Hour)
	if _, err := pool.Exec(ctx, `update requests set attempts = 3, next_attempt_at = $1`, due); err != nil {
		t.Fatal(err)
	}
	st, err = q.PendingRetryStats(ctx)
	if err != nil || st.Pending != 1 || st.MaxAttempts != 3 || st.NextAttemptAt == nil || !st.NextAttemptAt.Equal(due) {
		t.Fatalf("stats = %+v, %v", st, err)
	}
}

func TestLastDoneAt_Empty(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	got, err := q.LastDoneAt(ctx)
	if err != nil || got != nil {
		t.Fatalf("empty LastDoneAt = %v, %v; want nil", got, err)
	}
	now := time.Now()
	if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: 1, ItemID: 1, Requester: "b", MentionedAt: now, Status: "done", NextAttemptAt: now}); err != nil {
		t.Fatal(err)
	}
	got, err = q.LastDoneAt(ctx)
	if err != nil || got == nil {
		t.Fatalf("LastDoneAt = %v, %v; want non-nil", got, err)
	}
}

func TestRestoreSnapshot_RejectsForeignItem(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	for i, ti := range []string{"One", "Two"} {
		if err := q.UpsertSong(ctx, hit(int64(i+1), ti, "A", time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := q.SongSnapshot(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	n, err := q.RestoreSnapshot(ctx, db.RestoreSnapshotParams{ItemID: 2, Snapshot: snap})
	if err != nil || n != 0 {
		t.Fatalf("foreign restore = %d, %v; want 0 rows", n, err)
	}
	if s, _ := q.GetSong(ctx, 2); *s.Title != "Two" {
		t.Fatalf("item 2 overwritten: %s", *s.Title)
	}
	if n, err := q.RestoreSnapshot(ctx, db.RestoreSnapshotParams{ItemID: 1, Snapshot: snap}); err != nil || n != 1 {
		t.Fatalf("own restore = %d, %v; want 1", n, err)
	}
}

func TestSetSuggestionStatus_OnlyFromOpen(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	if err := q.UpsertSong(ctx, hit(1, "T", "A", time.Now())); err != nil {
		t.Fatal(err)
	}
	u, u2 := mkUser(t, q, 1, "a"), mkUser(t, q, 2, "b")
	s, err := mkSuggestion(q, 1, u.ID, "x")
	if err != nil {
		t.Fatal(err)
	}
	other, err := mkSuggestion(q, 1, u2.ID, "y")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := q.SetSuggestionStatus(ctx, db.SetSuggestionStatusParams{ID: s.ID, Status: "approved", ReviewerID: &u.ID}); err != nil || n != 1 {
		t.Fatalf("first decide = %d, %v; want 1", n, err)
	}
	if n, err := q.SetSuggestionStatus(ctx, db.SetSuggestionStatusParams{ID: s.ID, Status: "rejected", ReviewerID: &u.ID}); err != nil || n != 0 {
		t.Fatalf("second decide = %d, %v; want 0", n, err)
	}
	if err := q.SupersedeOthers(ctx, db.SupersedeOthersParams{ItemID: 1, ID: other.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ := q.GetSuggestion(ctx, s.ID)
	if got.Status != "approved" {
		t.Fatalf("approved suggestion changed to %s by SupersedeOthers", got.Status)
	}
}

func TestRestoreSnapshot_CoversAllColumns(t *testing.T) {
	pool := testdb.New(t)
	rows, err := pool.Query(context.Background(), `select column_name from information_schema.columns where table_schema='public' and table_name='songs'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols := map[string]bool{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols[c] = true
	}
	src, err := os.ReadFile("queries/songs.sql")
	if err != nil {
		t.Fatal(err)
	}
	block := string(src)
	i := strings.Index(block, "-- name: RestoreSnapshot")
	j := strings.Index(block[i:], "where s.item_id")
	restored := map[string]bool{"item_id": true}
	for _, m := range regexp.MustCompile(`(\w+) = r\.(\w+)`).FindAllStringSubmatch(block[i:i+j], -1) {
		restored[m[1]] = true
	}
	if len(restored) != len(cols) {
		t.Fatalf("RestoreSnapshot restores %d columns (incl. item_id), songs has %d: %v", len(restored), len(cols), cols)
	}
	for c := range cols {
		if !restored[c] {
			t.Fatalf("column %s not restored", c)
		}
	}
}
