package db_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/testdb"
	"github.com/Pacerino/pr0sauce/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func tablesPresent(t *testing.T, pool *pgxpool.Pool) map[string]bool {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`select table_name from information_schema.tables where table_schema = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got[n] = true
	}
	return got
}

func TestMigrate_UpAndDown(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	want := []string{"songs", "requests", "bot_comments", "bot_state"}

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
	if err := goose.DownToContext(ctx, sqlDB, ".", 0); err != nil {
		t.Fatal(err)
	}
	got = tablesPresent(t, pool)
	for _, n := range want {
		if got[n] {
			t.Fatalf("table %s still present after down", n)
		}
	}
	if err := goose.UpContext(ctx, sqlDB, "."); err != nil {
		t.Fatal(err)
	}
	got = tablesPresent(t, pool)
	for _, n := range want {
		if !got[n] {
			t.Fatalf("table %s missing after second up", n)
		}
	}
}

var _ = db.Open

func ptr[T any](v T) *T { return &v }

func hit(id int64, title, artist string, at time.Time) db.UpsertSongParams {
	return db.UpsertSongParams{ItemID: id, Title: ptr(title), Artist: ptr(artist), Provider: "shazam", RecognizedAt: at}
}

func TestInsertRequestIgnore_Idempotent(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	p := db.InsertRequestIgnoreParams{MessageID: 42, ItemID: 7, Requester: "bob", Thumb: "t.jpg", MentionedAt: time.Now()}

	first, err := q.InsertRequestIgnore(ctx, p)
	if err != nil || !first {
		t.Fatalf("first insert = %v, %v; want true", first, err)
	}
	second, err := q.InsertRequestIgnore(ctx, p)
	if err != nil || second {
		t.Fatalf("second insert = %v, %v; want false", second, err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select count(*) from requests`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows = %d, %v; want 1", n, err)
	}
}

func TestListRecentSongs_Keyset(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := int64(1); i <= 5; i++ {
		if err := q.UpsertSong(ctx, hit(i, "t", "a", base.Add(time.Duration(i)*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	// a miss must never appear
	if err := q.UpsertSong(ctx, db.UpsertSongParams{ItemID: 99, Provider: "", RecognizedAt: base.Add(100 * time.Hour)}); err != nil {
		t.Fatal(err)
	}

	cur := db.ListRecentSongsParams{BeforeRecognizedAt: base.Add(1000 * time.Hour), BeforeItemID: 1 << 62, Lim: 2}
	var seen []int64
	var sizes []int
	for {
		page, err := q.ListRecentSongs(ctx, cur)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		sizes = append(sizes, len(page))
		for _, s := range page {
			seen = append(seen, s.ItemID)
		}
		last := page[len(page)-1]
		cur.BeforeRecognizedAt, cur.BeforeItemID = last.RecognizedAt, last.ItemID
	}
	if fmt.Sprint(sizes) != "[2 2 1]" {
		t.Fatalf("page sizes = %v; want [2 2 1]", sizes)
	}
	if fmt.Sprint(seen) != "[5 4 3 2 1]" {
		t.Fatalf("seen = %v; want [5 4 3 2 1]", seen)
	}
}

func TestTopSongs_GroupsCaseInsensitive(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	now := time.Now()
	for i, s := range [][2]string{{"Sandstorm", "Darude"}, {"Sandstorm", "Darude"}, {"sandstorm", "darude"}, {"Other", "Someone"}} {
		if err := q.UpsertSong(ctx, hit(int64(i+1), s[0], s[1], now)); err != nil {
			t.Fatal(err)
		}
	}
	top, err := q.TopSongs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 2 || top[0].Plays != 3 {
		t.Fatalf("top = %+v; want 2 groups, first with 3 plays", top)
	}
}

func TestSearchSongs_HitsOnlyEscapedPattern(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	now := time.Now()
	_ = q.UpsertSong(ctx, hit(1, "100% Pure Love", "Crystal Waters", now))
	_ = q.UpsertSong(ctx, hit(2, "Sandstorm", "Darude", now))
	_ = q.UpsertSong(ctx, db.UpsertSongParams{ItemID: 3, Provider: "", RecognizedAt: now})

	got, err := q.SearchSongs(ctx, db.SearchSongsParams{Pattern: `%100\%%`, Q: "100% pure", Lim: 10, Off: 0})
	if err != nil || len(got) != 1 || got[0].ItemID != 1 {
		t.Fatalf("got %+v, %v; want only item 1", got, err)
	}
	got, err = q.SearchSongs(ctx, db.SearchSongsParams{Pattern: "%DARUDE%", Q: "darude", Lim: 10})
	if err != nil || len(got) != 1 || got[0].ItemID != 2 {
		t.Fatalf("got %+v, %v; want only item 2", got, err)
	}
}

func TestRequestLifecycle(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	now := time.Now()
	for i := int64(1); i <= 2; i++ {
		if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: i, ItemID: i, Requester: "bob", MentionedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	due, err := q.DueRequests(ctx, db.DueRequestsParams{Now: now.Add(time.Minute), Lim: 10})
	if err != nil || len(due) != 2 || due[0].ID > due[1].ID {
		t.Fatalf("due = %+v, %v", due, err)
	}
	if err := q.MarkReplying(ctx, due[0].ID); err != nil {
		t.Fatal(err)
	}
	stale, err := q.FailStaleReplying(ctx)
	if err != nil || len(stale) != 1 || stale[0].Status != "failed" || *stale[0].LastError != "reply state unknown after restart" {
		t.Fatalf("stale = %+v, %v", stale, err)
	}
	unread, err := q.UnreadHandledRequests(ctx)
	if err != nil || len(unread) != 1 {
		t.Fatalf("unread = %+v, %v", unread, err)
	}
	if err := q.SetMarkedRead(ctx, unread[0].ID); err != nil {
		t.Fatal(err)
	}
	if unread, _ = q.UnreadHandledRequests(ctx); len(unread) != 0 {
		t.Fatalf("unread after mark = %+v", unread)
	}
	if err := q.SetRequestStatus(ctx, db.SetRequestStatusParams{ID: due[1].ID, Status: "done", ReplyKind: ptr("comment")}); err != nil {
		t.Fatal(err)
	}
	reqs, err := q.TopRequesters(ctx, 5)
	if err != nil || len(reqs) != 1 || reqs[0].Requests != 1 {
		t.Fatalf("top requesters = %+v, %v", reqs, err)
	}
}
