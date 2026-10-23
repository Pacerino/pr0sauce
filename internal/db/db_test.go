package db_test

import (
	"context"
	"fmt"
	"strings"
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

func ptr[T any](v T) *T { return &v }

func hit(id int64, title, artist string, at time.Time) db.UpsertSongParams {
	return db.UpsertSongParams{ItemID: id, Title: ptr(title), Artist: ptr(artist), Provider: "shazam", RecognizedAt: at}
}

func TestInsertRequestIgnore_Idempotent(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	p := db.InsertRequestIgnoreParams{MessageID: 42, ItemID: 7, Requester: "bob", Thumb: "t.jpg", MentionedAt: time.Now(), Status: "pending", NextAttemptAt: time.Now()}

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
		if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: i, ItemID: i, Requester: "bob", MentionedAt: now, Status: "pending", NextAttemptAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	due, err := q.DueRequests(ctx, db.DueRequestsParams{Now: now.Add(time.Minute), Lim: 10})
	if err != nil || len(due) != 2 || due[0].ID > due[1].ID {
		t.Fatalf("due = %+v, %v", due, err)
	}
	if n, err := q.MarkReplying(ctx, due[0].ID); err != nil || n != 1 {
		t.Fatalf("MarkReplying = %d, %v; want 1", n, err)
	}
	stale, err := q.FailStaleReplying(ctx)
	if err != nil || len(stale) != 1 || stale[0].Status != "failed" || *stale[0].LastError != "reply state unknown after restart" {
		t.Fatalf("stale = %+v, %v", stale, err)
	}
	unread, err := q.UnreadHandledRequests(ctx, db.UnreadHandledRequestsParams{Now: time.Now().Add(time.Minute), Lim: 10})
	if err != nil || len(unread) != 1 {
		t.Fatalf("unread = %+v, %v", unread, err)
	}
	if err := q.SetMarkedRead(ctx, unread[0].ID); err != nil {
		t.Fatal(err)
	}
	if unread, _ = q.UnreadHandledRequests(ctx, db.UnreadHandledRequestsParams{Now: time.Now().Add(time.Minute), Lim: 10}); len(unread) != 0 {
		t.Fatalf("unread after mark = %+v", unread)
	}
	if n, err := q.SetRequestStatus(ctx, db.SetRequestStatusParams{ID: due[1].ID, Status: "done", ReplyKind: ptr("comment")}); err != nil || n != 1 {
		t.Fatalf("set status = %d, %v; want 1", n, err)
	}
	// a done row must not be resurrected by either guard
	if n, err := q.SetRequestStatus(ctx, db.SetRequestStatusParams{ID: due[1].ID, Status: "pending"}); err != nil || n != 0 {
		t.Fatalf("SetRequestStatus on done = %d, %v; want 0", n, err)
	}
	if n, err := q.BumpAttempt(ctx, db.BumpAttemptParams{ID: due[1].ID, NextAttemptAt: now}); err != nil || n != 0 {
		t.Fatalf("BumpAttempt on done = %d, %v; want 0", n, err)
	}
	var st string
	var attempts int
	if err := pool.QueryRow(ctx, `select status, attempts from requests where id = $1`, due[1].ID).Scan(&st, &attempts); err != nil || st != "done" || attempts != 0 {
		t.Fatalf("row = %s/%d, %v; want done/0", st, attempts, err)
	}
	reqs, err := q.TopRequesters(ctx, 5)
	if err != nil || len(reqs) != 1 || reqs[0].Requests != 1 {
		t.Fatalf("top requesters = %+v, %v", reqs, err)
	}
}

func TestMarkReplying_OnlyFromPending(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	now := time.Now()
	ids := map[string]int64{}
	for i, st := range []string{"pending", "replying", "done", "failed", "skipped"} {
		if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: int64(i + 1), ItemID: 1, Requester: "bob", MentionedAt: now, Status: "pending", NextAttemptAt: now}); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := pool.QueryRow(ctx, `update requests set status = $1 where message_id = $2 returning id`, st, i+1).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[st] = id
	}
	for _, st := range []string{"replying", "done", "failed", "skipped"} {
		n, err := q.MarkReplying(ctx, ids[st])
		if err != nil || n != 0 {
			t.Fatalf("MarkReplying(%s) = %d, %v; want 0", st, n, err)
		}
		var got string
		if err := pool.QueryRow(ctx, `select status from requests where id = $1`, ids[st]).Scan(&got); err != nil || got != st {
			t.Fatalf("%s row became %q, %v", st, got, err)
		}
	}
	if n, err := q.MarkReplying(ctx, ids["pending"]); err != nil || n != 1 {
		t.Fatalf("MarkReplying(pending) = %d, %v; want 1", n, err)
	}
}

func TestInsertRequestIgnore_StatusAndDueTime(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	// An app clock far in the past: the row must be due at that clock, not at the DB's now().
	clock := time.Date(2001, 1, 1, 12, 0, 0, 0, time.UTC)
	if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: 1, ItemID: 1, Requester: "a", MentionedAt: clock, Status: "pending", NextAttemptAt: clock}); err != nil {
		t.Fatal(err)
	}
	if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: 2, ItemID: 2, Requester: "b", MentionedAt: clock, Status: "skipped", NextAttemptAt: clock}); err != nil {
		t.Fatal(err)
	}
	due, err := q.DueRequests(ctx, db.DueRequestsParams{Now: clock, Lim: 10})
	if err != nil || len(due) != 1 || due[0].MessageID != 1 {
		t.Fatalf("due = %+v, %v; want only message 1", due, err)
	}
	var st string
	if err := pool.QueryRow(ctx, `select status from requests where message_id = 2`).Scan(&st); err != nil || st != "skipped" {
		t.Fatalf("message 2 status = %q, %v; want skipped", st, err)
	}
	// a second insert with another status must not touch the existing row
	if ins, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: 2, ItemID: 2, Requester: "b", MentionedAt: clock, Status: "pending", NextAttemptAt: clock}); err != nil || ins {
		t.Fatalf("re-insert = %v, %v; want false", ins, err)
	}
	if err := pool.QueryRow(ctx, `select status from requests where message_id = 2`).Scan(&st); err != nil || st != "skipped" {
		t.Fatalf("message 2 status after re-insert = %q, %v; want skipped", st, err)
	}
}

func TestRetryFromReplying_OnlyFromReplying(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	now := time.Now()
	ids := map[string]int64{}
	for i, st := range []string{"pending", "replying", "done", "failed", "skipped"} {
		if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{MessageID: int64(i + 1), ItemID: 1, Requester: "bob", MentionedAt: now, Status: st, NextAttemptAt: now}); err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := pool.QueryRow(ctx, `update requests set status = $1 where message_id = $2 returning id`, st, i+1).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids[st] = id
	}
	next := now.Add(time.Minute)
	for _, st := range []string{"pending", "done", "failed", "skipped"} {
		n, err := q.RetryFromReplying(ctx, db.RetryFromReplyingParams{ID: ids[st], NextAttemptAt: next, LastError: ptr("x")})
		if err != nil || n != 0 {
			t.Fatalf("RetryFromReplying(%s) = %d, %v; want 0", st, n, err)
		}
		var got string
		var attempts int
		if err := pool.QueryRow(ctx, `select status, attempts from requests where id = $1`, ids[st]).Scan(&got, &attempts); err != nil || got != st || attempts != 0 {
			t.Fatalf("%s row became %q/%d, %v", st, got, attempts, err)
		}
	}
	n, err := q.RetryFromReplying(ctx, db.RetryFromReplyingParams{ID: ids["replying"], NextAttemptAt: next, LastError: ptr("commentTooSoon")})
	if err != nil || n != 1 {
		t.Fatalf("RetryFromReplying(replying) = %d, %v; want 1", n, err)
	}
	var st string
	var attempts int
	var nextAt time.Time
	var lastErr string
	if err := pool.QueryRow(ctx, `select status, attempts, next_attempt_at, last_error from requests where id = $1`, ids["replying"]).Scan(&st, &attempts, &nextAt, &lastErr); err != nil {
		t.Fatal(err)
	}
	if st != "pending" || attempts != 1 || !nextAt.Equal(next.Truncate(time.Microsecond)) || lastErr != "commentTooSoon" {
		t.Fatalf("row = %s/%d/%v/%q", st, attempts, nextAt, lastErr)
	}
}

func TestSearchSongs_UsesIndexes(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	// Seed outside the explain tx and vacuum: unflushed GIN pending lists inflate
	// the planner's index cost and would hide the trigram indexes.
	if _, err := pool.Exec(ctx, `insert into songs (item_id, title, artist, provider, recognized_at)
select i, md5(i::text), md5((i*7)::text), 'shazam', now() - (i || ' minutes')::interval from generate_series(1, 20000) i`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `vacuum analyze songs`); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `set local enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `explain select * from songs
where title is not null and (title ilike '%sandstorm%' escape '\' or artist ilike '%sandstorm%' escape '\')
order by similarity(coalesce(title,'') || ' ' || coalesce(artist,''), 'sandstorm') desc, recognized_at desc limit 10`)
	if err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(l + "\n")
	}
	rows.Close()
	if !strings.Contains(plan.String(), "Bitmap Index Scan on songs_title_trgm") {
		t.Fatalf("plan does not use songs_title_trgm:\n%s", plan.String())
	}
}

func TestSongsPerWeek_BerlinBuckets(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	at := time.Date(2026, 10, 18, 22, 30, 0, 0, time.UTC) // Sunday UTC, Monday 00:30 CEST
	if err := q.UpsertSong(ctx, hit(1, "t", "a", at)); err != nil {
		t.Fatal(err)
	}
	rows, err := q.SongsPerWeek(ctx, at.Add(-30*24*time.Hour))
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if got := rows[0].Week.Time.Format("2006-01-02"); got != "2026-10-19" {
		t.Fatalf("week = %s; want 2026-10-19", got)
	}
}

func TestListRecentSongs_KeysetTies(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	q := db.New(pool)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := int64(1); i <= 3; i++ {
		if err := q.UpsertSong(ctx, hit(i, "t", "a", at)); err != nil {
			t.Fatal(err)
		}
	}
	p1, err := q.ListRecentSongs(ctx, db.ListRecentSongsParams{BeforeRecognizedAt: at.Add(time.Hour), BeforeItemID: 1 << 62, Lim: 2})
	if err != nil || len(p1) != 2 {
		t.Fatalf("p1 = %+v, %v", p1, err)
	}
	last := p1[1]
	p2, err := q.ListRecentSongs(ctx, db.ListRecentSongsParams{BeforeRecognizedAt: last.RecognizedAt, BeforeItemID: last.ItemID, Lim: 2})
	if err != nil || len(p2) != 1 {
		t.Fatalf("p2 = %+v, %v", p2, err)
	}
	if fmt.Sprint(p1[0].ItemID, p1[1].ItemID, p2[0].ItemID) != "3 2 1" {
		t.Fatalf("order = %d %d %d; want 3 2 1", p1[0].ItemID, p1[1].ItemID, p2[0].ItemID)
	}
}

func TestLatestBotCommentTime_Empty(t *testing.T) {
	q := db.New(testdb.New(t))
	got, err := q.LatestBotCommentTime(context.Background())
	if err != nil || got != nil {
		t.Fatalf("got %v, %v; want nil", got, err)
	}
}
