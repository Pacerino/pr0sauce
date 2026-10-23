package bot_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/bot"
	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/odesli"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/Pacerino/pr0sauce/internal/recognize"
	"github.com/Pacerino/pr0sauce/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testNow is 15:15 in Europe/Berlin (CEST).
var testNow = time.Date(2026, 10, 23, 13, 15, 0, 0, time.UTC)

const (
	itemID    = int64(6796145)
	videoPath = "2026/10/23/abc.mp4"
	checked   = "23.10.2026 um 15:15"
)

var kumite = recognize.Result{
	Found: true, Title: "Kumite", Artist: "Paul Hertzog", Album: "Bloodsport",
	CoverURL: "https://is1.example/cover.jpg", AppleMusicURL: "https://music.apple.com/x",
}

const kumiteComment = "Es wurden folgende Informationen dazu gefunden:\nKumite - Paul Hertzog\nAus dem Album: Bloodsport\n\nHier ist ein Link: https://pr0sauce.info/6796145\nZeitpunkt der Überprüfung 23.10.2026 um 15:15"
const missComment = "Es wurden keine Informationen zu dem Lied gefunden\n\nZeitpunkt der Überprüfung 23.10.2026 um 15:15"

type env struct {
	bot   *bot.Bot
	pr0   *fakePr0
	rec   *fakeRecognizer
	link  *fakeLinker
	clock *fakeClock
	pool  *pgxpool.Pool
	q     *db.Queries
}

// setup must be called once per top-level test (testdb.New is not reentrant).
func setup(t *testing.T, mod func(*config.Config)) *env {
	t.Helper()
	pool := testdb.New(t)
	cfg := config.Config{PublicBaseURL: "https://pr0sauce.info", MaxAttempts: 5}
	if mod != nil {
		mod(&cfg)
	}
	e := &env{
		pr0:   newFakePr0(),
		rec:   &fakeRecognizer{result: kumite},
		link:  &fakeLinker{links: odesli.Links{SpotifyURL: "https://open.spotify.com/track/s1", SpotifyID: "s1", YouTubeURL: "https://youtube.com/watch?v=y1", YouTubeID: "y1"}},
		clock: &fakeClock{t: testNow},
		pool:  pool,
		q:     db.New(pool),
	}
	e.pr0.items[itemID] = pr0gramm.Item{ID: itemID, Image: videoPath, Thumb: "2026/10/23/abc.jpg"}
	b, err := bot.New(bot.Deps{
		Store: bot.NewStore(e.q), Pr0: e.pr0, Recognizer: e.rec, Linker: e.link, Clock: e.clock,
	}, cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	e.bot = b
	return e
}

// goLive pretends the bot went live an hour before testNow.
func (e *env) goLive(t *testing.T) {
	t.Helper()
	if err := e.q.SetState(context.Background(), db.SetStateParams{Key: "live_since", Value: testNow.Add(-time.Hour).Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
}

func mention(id, item int64, name string) pr0gramm.InboxComment {
	return pr0gramm.InboxComment{ID: id, ItemID: item, Thumb: "2026/10/23/abc.jpg", Name: name,
		Message: "@sauce was ist das?", Kind: "mention", Created: testNow.Add(-time.Minute)}
}

// ingest puts the mentions in the inbox and runs Ingest.
func (e *env) ingest(t *testing.T, ms ...pr0gramm.InboxComment) {
	t.Helper()
	e.pr0.inbox = ms
	if err := e.bot.Ingest(context.Background()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
}

func (e *env) process(t *testing.T) {
	t.Helper()
	if err := e.bot.ProcessDue(context.Background()); err != nil {
		t.Fatalf("ProcessDue: %v", err)
	}
}

type reqRow struct {
	Status        string
	ReplyKind     *string
	Attempts      int
	NextAttemptAt time.Time
	LastError     *string
	MarkedRead    bool
}

func (e *env) req(t *testing.T, messageID int64) reqRow {
	t.Helper()
	var r reqRow
	err := e.pool.QueryRow(context.Background(),
		`select status, reply_kind, attempts, next_attempt_at, last_error, marked_read from requests where message_id = $1`, messageID).
		Scan(&r.Status, &r.ReplyKind, &r.Attempts, &r.NextAttemptAt, &r.LastError, &r.MarkedRead)
	if err != nil {
		t.Fatalf("request %d: %v", messageID, err)
	}
	return r
}

func (e *env) count(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), `select count(*) from requests`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func str(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func (e *env) assertNoReplies(t *testing.T) {
	t.Helper()
	if len(e.pr0.posts) != 0 || len(e.pr0.pms) != 0 || len(e.pr0.tags) != 0 {
		t.Fatalf("unexpected replies: posts=%+v pms=%+v tags=%+v", e.pr0.posts, e.pr0.pms, e.pr0.tags)
	}
}

func TestIngest_FirstRunSkipsOldMentions(t *testing.T) {
	e := setup(t, nil)
	e.ingest(t, mention(1, itemID, "alice")) // created 1 minute before now, live_since unset

	ls, err := e.q.GetState(context.Background(), "live_since")
	if err != nil {
		t.Fatalf("live_since not set: %v", err)
	}
	if got, err := time.Parse(time.RFC3339Nano, ls); err != nil || !got.Equal(testNow) {
		t.Fatalf("live_since = %q, %v; want %v", ls, err, testNow)
	}
	if r := e.req(t, 1); r.Status != "skipped" {
		t.Fatalf("status = %s; want skipped", r.Status)
	}
	e.process(t)
	e.clock.Advance(time.Hour)
	e.process(t)
	e.assertNoReplies(t)
	if len(e.rec.calls) != 0 || len(e.pr0.itemCalls) != 0 {
		t.Fatalf("old mention was processed: rec=%v item=%v", e.rec.calls, e.pr0.itemCalls)
	}
	// a later Ingest must keep the original live_since
	e.ingest(t)
	if ls2, _ := e.q.GetState(context.Background(), "live_since"); ls2 != ls {
		t.Fatalf("live_since changed: %q -> %q", ls, ls2)
	}
}

func TestIngest_IgnoresReadAndNonMentions(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	read := mention(1, itemID, "a")
	read.Read = true
	notMention := mention(2, itemID, "b")
	notMention.Kind = "comment"
	noSauce := mention(3, itemID, "c")
	noSauce.Message = "schöner Beitrag"
	upper := mention(4, itemID, "d")
	upper.Message = "Hey @SAUCE!"
	e.ingest(t, read, notMention, noSauce, upper)

	if n := e.count(t); n != 1 {
		t.Fatalf("rows = %d; want 1", n)
	}
	if r := e.req(t, 4); r.Status != "pending" || !r.NextAttemptAt.Equal(testNow) {
		t.Fatalf("row = %+v; want pending due at %v", r, testNow)
	}
	var thumb string
	if err := e.pool.QueryRow(context.Background(), `select thumb from requests where message_id = 4`).Scan(&thumb); err != nil || thumb != "2026/10/23/abc.jpg" {
		t.Fatalf("thumb = %q, %v", thumb, err)
	}
}

func TestIngest_BannedUserSkipped(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.BannedUsers = []string{"Troll"} })
	e.goLive(t)
	e.ingest(t, mention(1, itemID, "troll"), mention(2, itemID, "alice"))
	if r := e.req(t, 1); r.Status != "skipped" {
		t.Fatalf("banned status = %s; want skipped", r.Status)
	}
	if r := e.req(t, 2); r.Status != "pending" {
		t.Fatalf("alice status = %s; want pending", r.Status)
	}
}

func TestProcess_BannedAfterIngestSkipped(t *testing.T) {
	// The ban list can change between ingest and processing (restart with new config).
	e := setup(t, func(c *config.Config) { c.BannedUsers = []string{"TROLL"} })
	if _, err := e.q.InsertRequestIgnore(context.Background(), db.InsertRequestIgnoreParams{
		MessageID: 1, ItemID: itemID, Requester: "troll", MentionedAt: testNow, Status: "pending", NextAttemptAt: testNow,
	}); err != nil {
		t.Fatal(err)
	}
	e.process(t)
	if r := e.req(t, 1); r.Status != "skipped" {
		t.Fatalf("status = %s; want skipped", r.Status)
	}
	e.assertNoReplies(t)
	if len(e.pr0.itemCalls) != 0 || len(e.rec.calls) != 0 {
		t.Fatalf("pr0gramm/recognizer called for banned user")
	}
}

func TestIngest_Idempotent(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.ingest(t, mention(1, itemID, "alice"))
	e.ingest(t, mention(1, itemID, "alice"))
	if n := e.count(t); n != 1 {
		t.Fatalf("rows = %d; want 1", n)
	}
}

func TestProcess_HitPostsCommentAndTags(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)

	if want := []string{"https://vid.pr0gramm.com/" + videoPath}; !reflect.DeepEqual(e.rec.calls, want) {
		t.Fatalf("recognize calls = %v; want %v", e.rec.calls, want)
	}
	if want := []string{kumite.AppleMusicURL}; !reflect.DeepEqual(e.link.calls, want) {
		t.Fatalf("links calls = %v; want %v", e.link.calls, want)
	}
	if want := []post{{itemID, 11, kumiteComment}}; !reflect.DeepEqual(e.pr0.posts, want) {
		t.Fatalf("posts = %+v\nwant  %+v", e.pr0.posts, want)
	}
	if want := []tagCall{{itemID, []string{"Kumite", "Paul Hertzog", "Paul Hertzog - Kumite"}}}; !reflect.DeepEqual(e.pr0.tags, want) {
		t.Fatalf("tags = %+v; want %+v", e.pr0.tags, want)
	}
	if len(e.pr0.pms) != 0 {
		t.Fatalf("pms = %+v", e.pr0.pms)
	}
	r := e.req(t, 11)
	if r.Status != "done" || str(r.ReplyKind) != "comment" {
		t.Fatalf("row = %s/%s; want done/comment", r.Status, str(r.ReplyKind))
	}
	s, err := e.q.GetSong(context.Background(), itemID)
	if err != nil {
		t.Fatal(err)
	}
	if str(s.Title) != "Kumite" || str(s.Artist) != "Paul Hertzog" || str(s.Album) != "Bloodsport" ||
		s.Provider != "shazam" || str(s.CoverUrl) != kumite.CoverURL ||
		str(s.SpotifyUrl) != "https://open.spotify.com/track/s1" || str(s.SpotifyID) != "s1" ||
		str(s.YoutubeUrl) != "https://youtube.com/watch?v=y1" || str(s.YoutubeID) != "y1" ||
		!s.RecognizedAt.Equal(testNow) {
		t.Fatalf("song = %+v", s)
	}
}

func TestProcess_HitLinksErrorAndEmptyAlbum(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.rec.result = recognize.Result{Found: true, Title: "Kumite", Artist: "Paul Hertzog", AppleMusicURL: "https://music.apple.com/x"}
	e.link.err = errors.New("odesli down")
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)
	want := "Es wurden folgende Informationen dazu gefunden:\nKumite - Paul Hertzog\nAus dem Album: \n\nHier ist ein Link: https://pr0sauce.info/6796145\nZeitpunkt der Überprüfung " + checked
	if len(e.pr0.posts) != 1 || e.pr0.posts[0].Text != want {
		t.Fatalf("posts = %+v", e.pr0.posts)
	}
	s, err := e.q.GetSong(context.Background(), itemID)
	if err != nil || s.Album != nil || s.SpotifyUrl != nil || s.YoutubeID != nil || s.CoverUrl != nil {
		t.Fatalf("song = %+v, %v; want NULL album/links/cover", s, err)
	}
	if r := e.req(t, 11); r.Status != "done" {
		t.Fatalf("status = %s", r.Status)
	}
}

func TestProcess_AddTagsErrorStillDone(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.pr0.tagErr = &pr0gramm.APIError{Status: 500}
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)
	if r := e.req(t, 11); r.Status != "done" || str(r.ReplyKind) != "comment" {
		t.Fatalf("row = %+v; want done/comment", r)
	}
	if len(e.pr0.posts) != 1 {
		t.Fatalf("posts = %d", len(e.pr0.posts))
	}
}

func TestProcess_MissPostsMissComment(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.rec.result = recognize.Result{}
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)
	if want := []post{{itemID, 11, missComment}}; !reflect.DeepEqual(e.pr0.posts, want) {
		t.Fatalf("posts = %+v\nwant  %+v", e.pr0.posts, want)
	}
	if len(e.pr0.tags) != 0 || len(e.link.calls) != 0 {
		t.Fatalf("tags=%v links=%v; want none", e.pr0.tags, e.link.calls)
	}
	s, err := e.q.GetSong(context.Background(), itemID)
	if err != nil || s.Title != nil || s.Provider != "shazam" {
		t.Fatalf("song = %+v, %v; want miss row", s, err)
	}
	if r := e.req(t, 11); r.Status != "done" || str(r.ReplyKind) != "comment" {
		t.Fatalf("row = %+v", r)
	}
}

func TestProcess_SecondMentionGetsPM(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.ingest(t, mention(11, itemID, "alice"), mention(12, itemID, "bob"))
	e.process(t)

	if len(e.pr0.posts) != 1 || e.pr0.posts[0].ParentID != 11 {
		t.Fatalf("posts = %+v; want one comment under 11", e.pr0.posts)
	}
	want := []pm{{"bob", "Hallo bob,\n\nDu hast bei https://pr0gramm.com/new/6796145 nach der Musik gefragt.\nJemand hat bereits danach gefragt, daher erhälst du hier nur eine Kopie.\n\nTitel: Kumite\nAlbum: Bloodsport\nArtist: Paul Hertzog\n\nHier ist ein Link: https://pr0sauce.info/6796145"}}
	if !reflect.DeepEqual(e.pr0.pms, want) {
		t.Fatalf("pms = %q\nwant  %q", e.pr0.pms, want)
	}
	if len(e.rec.calls) != 1 || len(e.pr0.itemCalls) != 1 {
		t.Fatalf("recognize=%d item=%d; want 1 each", len(e.rec.calls), len(e.pr0.itemCalls))
	}
	if r := e.req(t, 12); r.Status != "done" || str(r.ReplyKind) != "pm" {
		t.Fatalf("row = %+v; want done/pm", r)
	}
}

func TestProcess_KnownMissGetsMissPM(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	if err := e.q.UpsertSong(context.Background(), db.UpsertSongParams{ItemID: itemID, Provider: "acrcloud", RecognizedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	e.ingest(t, mention(12, itemID, "bob"))
	e.process(t)
	want := []pm{{"bob", "Hallo bob,\n\nDu hast bei https://pr0gramm.com/new/6796145 nach der Musik gefragt.\nLeider wurden keine Informationen gefunden."}}
	if !reflect.DeepEqual(e.pr0.pms, want) || len(e.pr0.posts) != 0 || len(e.pr0.itemCalls) != 0 {
		t.Fatalf("pms = %q posts=%v items=%v", e.pr0.pms, e.pr0.posts, e.pr0.itemCalls)
	}
}

func TestProcess_NotAVideoSkipped(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.pr0.items[2] = pr0gramm.Item{ID: 2, Image: "2026/10/23/pic.jpg"}
	// item 3 is missing → ErrItemNotFound; item 4 is an mp4 the sidecar rejects as not a video
	e.pr0.items[4] = pr0gramm.Item{ID: 4, Image: "2026/10/23/x.mp4"}
	e.ingest(t, mention(21, 2, "a"), mention(22, 3, "b"))
	e.process(t)
	e.rec.err = recognize.ErrNotAVideo
	e.ingest(t, mention(23, 4, "c"))
	e.process(t)

	for _, id := range []int64{21, 22, 23} {
		if r := e.req(t, id); r.Status != "skipped" || r.ReplyKind != nil {
			t.Fatalf("message %d = %+v; want skipped", id, r)
		}
	}
	if len(e.rec.calls) != 1 {
		t.Fatalf("recognize calls = %v; want only the mp4", e.rec.calls)
	}
	e.assertNoReplies(t)
}

func TestProcess_BadURLFailed(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.rec.err = errors.Join(recognize.ErrBadURL, errors.New("422"))
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)
	if r := e.req(t, 11); r.Status != "failed" || r.Attempts != 0 || r.LastError == nil {
		t.Fatalf("row = %+v; want failed without retry", r)
	}
	e.assertNoReplies(t)
}

func TestProcess_PostFailsStaysPending(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.rec.err = errors.New("recognize: sidecar status 503: busy")
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)

	r := e.req(t, 11)
	if r.Status != "pending" || r.Attempts != 1 || !r.NextAttemptAt.Equal(testNow.Add(time.Minute)) || r.LastError == nil {
		t.Fatalf("row = %+v; want pending, 1 attempt, due now+1m", r)
	}
	if err := e.bot.MarkHandledRead(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.pr0.markReads) != 0 {
		t.Fatalf("MarkRead called for pending request: %v", e.pr0.markReads)
	}
	e.assertNoReplies(t)
	// not due yet: nothing happens
	e.process(t)
	if len(e.rec.calls) != 1 {
		t.Fatalf("recognize retried before backoff: %d", len(e.rec.calls))
	}
	// due: succeeds now
	e.rec.err = nil
	e.clock.Advance(time.Minute)
	e.process(t)
	if r := e.req(t, 11); r.Status != "done" || len(e.pr0.posts) != 1 {
		t.Fatalf("after retry row = %+v posts=%d", r, len(e.pr0.posts))
	}
}

func TestProcess_PostCommentFailsAfterReplyingIsFailedNotRetried(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.pr0.postErr = func(int) error { return &pr0gramm.APIError{Path: "/comments/post", Status: 502, Body: "bad gateway"} }
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)

	r := e.req(t, 11)
	if r.Status != "failed" || r.Attempts != 0 || r.LastError == nil {
		t.Fatalf("row = %+v; want failed, no retry", r)
	}
	e.pr0.postErr = nil
	e.clock.Advance(2 * time.Hour)
	e.process(t)
	if len(e.pr0.posts) != 1 || len(e.pr0.pms) != 0 {
		t.Fatalf("reply repeated: posts=%d pms=%d", len(e.pr0.posts), len(e.pr0.pms))
	}
	if err := e.bot.MarkHandledRead(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.pr0.markReads, []int64{11}) {
		t.Fatalf("markReads = %v; want [11]", e.pr0.markReads)
	}
}

func TestProcess_PMFailsAfterReplyingIsFailedNotRetried(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	if err := e.q.UpsertSong(context.Background(), db.UpsertSongParams{ItemID: itemID, Provider: "shazam", RecognizedAt: testNow}); err != nil {
		t.Fatal(err)
	}
	e.pr0.pmErr = func(int) error { return errors.New("connection reset") }
	e.ingest(t, mention(12, itemID, "bob"))
	e.process(t)
	if r := e.req(t, 12); r.Status != "failed" || r.Attempts != 0 {
		t.Fatalf("row = %+v; want failed", r)
	}
	e.pr0.pmErr = nil
	e.clock.Advance(2 * time.Hour)
	e.process(t)
	if len(e.pr0.pms) != 1 {
		t.Fatalf("pm repeated: %d", len(e.pr0.pms))
	}
}

func TestProcess_CommentTooSoonAfterReplyingRetries(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.pr0.postErr = func(n int) error {
		if n == 1 {
			return &pr0gramm.APIError{Path: "/comments/post", Status: 200, Body: `{"success":false,"error":"commentTooSoon"}`}
		}
		return nil
	}
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)

	r := e.req(t, 11)
	if r.Status != "pending" || r.Attempts != 1 || !r.NextAttemptAt.Equal(testNow.Add(time.Minute)) {
		t.Fatalf("row = %+v; want pending, 1 attempt, due now+1m", r)
	}
	if err := e.bot.MarkHandledRead(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.pr0.markReads) != 0 {
		t.Fatalf("MarkRead called: %v", e.pr0.markReads)
	}
	if len(e.pr0.tags) != 0 {
		t.Fatalf("tags added for rejected comment")
	}

	// Retry: the song is stored now, but this requester still gets the comment, not the PM.
	e.clock.Advance(time.Minute)
	e.process(t)
	if len(e.pr0.posts) != 2 || len(e.pr0.pms) != 0 {
		t.Fatalf("posts=%d pms=%d; want 2 comment attempts, no pm", len(e.pr0.posts), len(e.pr0.pms))
	}
	wantText := "Es wurden folgende Informationen dazu gefunden:\nKumite - Paul Hertzog\nAus dem Album: Bloodsport\n\nHier ist ein Link: https://pr0sauce.info/6796145\nZeitpunkt der Überprüfung 23.10.2026 um 15:16"
	if p := e.pr0.posts[1]; p.ParentID != 11 || p.Text != wantText {
		t.Fatalf("retry post = %+v", p)
	}
	if len(e.rec.calls) != 1 {
		t.Fatalf("recognize calls = %d; want 1 (song reused)", len(e.rec.calls))
	}
	if len(e.pr0.tags) != 1 {
		t.Fatalf("tags = %d; want 1", len(e.pr0.tags))
	}
	if r := e.req(t, 11); r.Status != "done" || str(r.ReplyKind) != "comment" {
		t.Fatalf("row = %+v; want done/comment", r)
	}
}

func TestProcess_RejectedRepeatedlyGivesUp(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.MaxAttempts = 2 })
	e.goLive(t)
	e.pr0.postErr = func(int) error { return &pr0gramm.APIError{Status: 429} }
	e.ingest(t, mention(11, itemID, "alice"))
	e.process(t)
	e.clock.Advance(time.Hour)
	e.process(t)
	if r := e.req(t, 11); r.Status != "failed" {
		t.Fatalf("row = %+v; want failed after MaxAttempts", r)
	}
	e.clock.Advance(time.Hour)
	e.process(t)
	if len(e.pr0.posts) != 2 {
		t.Fatalf("posts = %d; want 2", len(e.pr0.posts))
	}
}

func TestProcess_GivesUpAfterMaxAttempts(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.MaxAttempts = 3 })
	e.goLive(t)
	e.rec.err = errors.New("recognize: sidecar status 503")
	e.ingest(t, mention(11, itemID, "alice"))

	wantDelay := []time.Duration{time.Minute, 2 * time.Minute}
	for i, d := range wantDelay {
		e.process(t)
		r := e.req(t, 11)
		if r.Status != "pending" || r.Attempts != i+1 || !r.NextAttemptAt.Equal(e.clock.Now().Add(d)) {
			t.Fatalf("attempt %d: row = %+v; want pending due +%v", i+1, r, d)
		}
		e.clock.Advance(d)
	}
	e.process(t)
	if r := e.req(t, 11); r.Status != "failed" || r.Attempts != 3 || r.LastError == nil {
		t.Fatalf("row = %+v; want failed after 3 attempts", r)
	}
	e.clock.Advance(24 * time.Hour)
	e.process(t)
	if len(e.rec.calls) != 3 {
		t.Fatalf("recognize calls = %d; want 3", len(e.rec.calls))
	}
	e.assertNoReplies(t)
}

func TestProcess_BackoffCappedAtOneHour(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.MaxAttempts = 20 })
	e.goLive(t)
	e.rec.err = errors.New("recognize: sidecar status 503")
	e.ingest(t, mention(11, itemID, "alice"))
	if _, err := e.pool.Exec(context.Background(), `update requests set attempts = 9 where message_id = 11`); err != nil {
		t.Fatal(err)
	}
	e.process(t)
	if r := e.req(t, 11); r.Attempts != 10 || !r.NextAttemptAt.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("row = %+v; want due now+1h", r)
	}
}

func TestProcess_ClaimLostSkipsSilently(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.ingest(t, mention(11, itemID, "alice"))
	// Another worker claims the request between DueRequests and our MarkReplying.
	e.rec.result = kumite
	claimed := &claimingRecognizer{inner: e.rec, claim: func() {
		if _, err := e.pool.Exec(context.Background(), `update requests set status = 'replying' where message_id = 11`); err != nil {
			t.Error(err)
		}
	}}
	b, err := bot.New(bot.Deps{Store: bot.NewStore(e.q), Pr0: e.pr0, Recognizer: claimed, Linker: e.link, Clock: e.clock},
		config.Config{PublicBaseURL: "https://pr0sauce.info", MaxAttempts: 5}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := b.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.assertNoReplies(t)
	if r := e.req(t, 11); r.Status != "replying" {
		t.Fatalf("row = %+v; want untouched replying", r)
	}
}

type claimingRecognizer struct {
	inner bot.Recognizer
	claim func()
}

func (c *claimingRecognizer) Recognize(ctx context.Context, url string) (recognize.Result, error) {
	c.claim()
	return c.inner.Recognize(ctx, url)
}

func TestProcess_AuthErrorStopsBatchNoBump(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.pr0.items[2] = pr0gramm.Item{ID: 2, Image: "b.mp4"}
	e.ingest(t, mention(11, itemID, "alice"), mention(12, 2, "bob"))

	// Before MarkReplying: stays pending, no bump, batch stops.
	e.pr0.itemErr = func(int64) error {
		return &pr0gramm.APIError{Path: "/items/get", Status: 403, Body: `{"error":"forbidden"}`}
	}
	err := e.bot.ProcessDue(context.Background())
	if !errors.Is(err, bot.ErrAuth) {
		t.Fatalf("err = %v; want ErrAuth", err)
	}
	if len(e.pr0.itemCalls) != 1 {
		t.Fatalf("item calls = %v; batch should stop after the first", e.pr0.itemCalls)
	}
	for _, id := range []int64{11, 12} {
		if r := e.req(t, id); r.Status != "pending" || r.Attempts != 0 || !r.NextAttemptAt.Equal(testNow) {
			t.Fatalf("message %d = %+v; want untouched pending", id, r)
		}
	}

	// After MarkReplying: the normal rule, failed with no retry; batch still stops.
	e.pr0.itemErr = nil
	e.pr0.postErr = func(int) error { return &pr0gramm.APIError{Path: "/comments/post", Status: 401} }
	err = e.bot.ProcessDue(context.Background())
	if !errors.Is(err, bot.ErrAuth) {
		t.Fatalf("err = %v; want ErrAuth", err)
	}
	if r := e.req(t, 11); r.Status != "failed" || r.Attempts != 0 {
		t.Fatalf("message 11 = %+v; want failed", r)
	}
	if r := e.req(t, 12); r.Status != "pending" || r.Attempts != 0 {
		t.Fatalf("message 12 = %+v; want untouched pending", r)
	}
	if len(e.pr0.posts) != 1 {
		t.Fatalf("posts = %d; want 1", len(e.pr0.posts))
	}

	// Ingest and MarkHandledRead report ErrAuth too.
	e.pr0.inboxErr = &pr0gramm.APIError{Path: "/inbox/comments", Status: 401}
	if err := e.bot.Ingest(context.Background()); !errors.Is(err, bot.ErrAuth) {
		t.Fatalf("Ingest err = %v; want ErrAuth", err)
	}
	e.pr0.markErr = func(int64) error { return &pr0gramm.APIError{Path: "/inbox/read", Status: 403} }
	if err := e.bot.MarkHandledRead(context.Background()); !errors.Is(err, bot.ErrAuth) {
		t.Fatalf("MarkHandledRead err = %v; want ErrAuth", err)
	}
	if r := e.req(t, 11); r.MarkedRead {
		t.Fatal("marked read despite MarkRead failing")
	}
}

func TestRecoverReplying(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	e.ingest(t, mention(11, itemID, "alice"), mention(12, itemID, "bob"))
	var id int64
	if err := e.pool.QueryRow(context.Background(), `select id from requests where message_id = 11`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if n, err := e.q.MarkReplying(context.Background(), id); err != nil || n != 1 {
		t.Fatalf("MarkReplying = %d, %v", n, err)
	}
	if err := e.bot.RecoverReplying(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := e.req(t, 11); r.Status != "failed" || str(r.LastError) != "reply state unknown after restart" {
		t.Fatalf("row = %+v; want failed", r)
	}
	if r := e.req(t, 12); r.Status != "pending" {
		t.Fatalf("pending row touched: %+v", r)
	}
	e.assertNoReplies(t)
	// the failed request is never processed again; the pending one is (as a fresh lookup)
	e.process(t)
	if len(e.pr0.posts) != 1 || e.pr0.posts[0].ParentID != 12 {
		t.Fatalf("posts = %+v; want only the reply to 12", e.pr0.posts)
	}
}

func TestMarkHandledRead_OnlyAfterHandled(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	var ms []pr0gramm.InboxComment
	for i := int64(1); i <= 5; i++ {
		ms = append(ms, mention(i, itemID, "u"))
	}
	e.ingest(t, ms...)
	for id, st := range map[int64]string{2: "replying", 3: "done", 4: "skipped", 5: "failed"} {
		if _, err := e.pool.Exec(context.Background(), `update requests set status = $1 where message_id = $2`, st, id); err != nil {
			t.Fatal(err)
		}
	}
	// MarkRead fails transiently for message 4: logged, retried next tick.
	e.pr0.markErr = func(id int64) error {
		if id == 4 {
			return &pr0gramm.APIError{Status: 502}
		}
		return nil
	}
	if err := e.bot.MarkHandledRead(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.pr0.markReads, []int64{3, 5}) {
		t.Fatalf("markReads = %v; want [3 5]", e.pr0.markReads)
	}
	for id, want := range map[int64]bool{1: false, 2: false, 3: true, 4: false, 5: true} {
		if r := e.req(t, id); r.MarkedRead != want {
			t.Fatalf("message %d marked_read = %v; want %v", id, r.MarkedRead, want)
		}
	}
	e.pr0.markErr = nil
	if err := e.bot.MarkHandledRead(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.pr0.markReads, []int64{3, 5, 4}) {
		t.Fatalf("markReads = %v; want [3 5 4]", e.pr0.markReads)
	}
}

func TestProcess_PMRejectedRetriesAsPM(t *testing.T) {
	e := setup(t, nil)
	e.goLive(t)
	if err := e.q.UpsertSong(context.Background(), db.UpsertSongParams{
		ItemID: itemID, Title: ptr("Kumite"), Artist: ptr("Paul Hertzog"), Album: ptr("Bloodsport"), Provider: "shazam", RecognizedAt: testNow,
	}); err != nil {
		t.Fatal(err)
	}
	e.pr0.pmErr = func(n int) error {
		if n == 1 {
			return &pr0gramm.APIError{Path: "/inbox/post", Status: 429}
		}
		return nil
	}
	e.ingest(t, mention(12, itemID, "bob"))
	e.process(t)
	if r := e.req(t, 12); r.Status != "pending" || r.Attempts != 1 {
		t.Fatalf("row = %+v; want pending, 1 attempt", r)
	}
	e.clock.Advance(time.Minute)
	e.process(t)
	if len(e.pr0.posts) != 0 || len(e.pr0.tags) != 0 || len(e.pr0.pms) != 2 {
		t.Fatalf("posts=%d tags=%d pms=%d; want 0/0/2", len(e.pr0.posts), len(e.pr0.tags), len(e.pr0.pms))
	}
	if e.pr0.pms[1] != e.pr0.pms[0] || e.pr0.pms[1].Recipient != "bob" {
		t.Fatalf("retry pm = %+v; want same PM to bob", e.pr0.pms[1])
	}
	if r := e.req(t, 12); r.Status != "done" || str(r.ReplyKind) != "pm" {
		t.Fatalf("row = %+v; want done/pm", r)
	}
}

func TestNew_RejectsNonPositiveMaxAttempts(t *testing.T) {
	deps := bot.Deps{Store: nopStore{}, Pr0: newFakePr0(), Recognizer: &fakeRecognizer{}, Linker: &fakeLinker{}}
	for _, n := range []int{0, -1} {
		if _, err := bot.New(deps, config.Config{MaxAttempts: n}, nil); err == nil {
			t.Fatalf("New with MaxAttempts=%d succeeded; want error", n)
		}
	}
	if _, err := bot.New(deps, config.Config{MaxAttempts: 1}, nil); err != nil {
		t.Fatalf("New with MaxAttempts=1: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

// nopStore satisfies bot.Store for constructor tests; it is never called.
type nopStore struct{ bot.Store }
