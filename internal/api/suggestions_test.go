package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/testdb"
)

type fakeDeezer struct{ calls atomic.Int32 }

func (f *fakeDeezer) SearchTrack(_ context.Context, _, _ string) (string, string, error) {
	f.calls.Add(1)
	return "https://www.deezer.com/track/42", "https://cdn-images.dzcdn.net/c.jpg", nil
}

func sugSetup(t *testing.T) (*server, *db.Queries, *fakeDeezer) {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	fd := &fakeDeezer{}
	s := newServer(q, pool, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{InternalToken: testToken, Deezer: fd})
	return s, q, fd
}

// seedHit stores a bot hit with every streaming field filled.
func seedHit(t *testing.T, q *db.Queries, id int64) {
	t.Helper()
	if err := q.UpsertSong(context.Background(), db.UpsertSongParams{
		ItemID: id, Title: str("Bot Title"), Artist: str("Bot Artist"), Album: str("Bot Album"),
		Provider: "shazam", RecognizedAt: time.Now().UTC().Truncate(time.Microsecond),
		SpotifyUrl: str("https://open.spotify.com/track/x"), SpotifyID: str("x"),
		YoutubeUrl: str("https://youtube.com/watch?v=y"), YoutubeID: str("y"),
		CoverUrl: str("https://img/bot.jpg"), AppleMusicUrl: str("https://music.apple.com/a?i=1"),
		Isrc: str("ISRC1"), DeezerUrl: str("https://www.deezer.com/track/1"),
	}); err != nil {
		t.Fatal(err)
	}
}

func makeMod(t *testing.T, q *db.Queries, l loginResp) {
	t.Helper()
	if err := q.SetRole(context.Background(), db.SetRoleParams{ID: l.User.ID, Role: "moderator"}); err != nil {
		t.Fatal(err)
	}
}

func suggest(t *testing.T, s *server, tok string, item int64, title, artist string) int64 {
	t.Helper()
	code, body := do(t, s, "POST", fmt.Sprintf("/api/internal/songs/%d/suggestions", item), testToken, tok,
		map[string]any{"title": title, "artist": artist})
	if code != 201 {
		t.Fatalf("suggest = %d: %s", code, body)
	}
	var r struct{ ID int64 }
	if err := json.Unmarshal(body, &r); err != nil || r.ID == 0 {
		t.Fatalf("suggest body %s", body)
	}
	return r.ID
}

func vote(t *testing.T, s *server, tok string, id int64, v int) (int, []byte) {
	t.Helper()
	return do(t, s, "PUT", fmt.Sprintf("/api/internal/suggestions/%d/vote", id), testToken, tok, map[string]any{"value": v})
}

func mustVote(t *testing.T, s *server, tok string, id int64, v int) {
	t.Helper()
	if code, body := vote(t, s, tok, id, v); code != 200 {
		t.Fatalf("vote %d on %d = %d: %s", v, id, code, body)
	}
}

func status(t *testing.T, q *db.Queries, id int64) string {
	t.Helper()
	sg, err := q.GetSuggestion(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return sg.Status
}

func songMap(t *testing.T, s *server, item int64) map[string]any {
	t.Helper()
	code, _, body := get(t, s, fmt.Sprintf("/api/songs/%d", item))
	if code != 200 {
		t.Fatalf("get song %d = %d: %s", item, code, body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func logins(t *testing.T, s *server, base int64, n int) []loginResp {
	t.Helper()
	out := make([]loginResp, n)
	for i := range out {
		out[i] = login(t, s, base+int64(i), fmt.Sprintf("u%d", base+int64(i)))
	}
	return out
}

func TestSuggest_Validation(t *testing.T) {
	s, q, _ := sugSetup(t)
	seedHit(t, q, 1)
	u := login(t, s, 1, "alice")
	long := strings.Repeat("ä", 201)
	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"empty title", map[string]any{"title": "  ", "artist": "A"}, 400},
		{"missing artist", map[string]any{"title": "T"}, 400},
		{"title too long", map[string]any{"title": long, "artist": "A"}, 400},
		{"artist too long", map[string]any{"title": "T", "artist": long}, 400},
		{"album too long", map[string]any{"title": "T", "artist": "A", "album": long}, 400},
		{"proof not http", map[string]any{"title": "T", "artist": "A", "proofUrl": "javascript:alert(1)"}, 400},
		{"proof no host", map[string]any{"title": "T", "artist": "A", "proofUrl": "https://"}, 400},
		{"proof too long", map[string]any{"title": "T", "artist": "A", "proofUrl": "https://x.de/" + strings.Repeat("a", 490)}, 400},
		{"nul byte", map[string]any{"title": "T\x00", "artist": "A"}, 400},
		{"200 runes ok", map[string]any{"title": strings.Repeat("ä", 200), "artist": " A ", "album": "", "proofUrl": "https://youtu.be/x"}, 201},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := do(t, s, "POST", "/api/internal/songs/1/suggestions", testToken, u.Token, c.body)
			if code != c.want {
				t.Fatalf("= %d, want %d: %s", code, c.want, body)
			}
		})
	}
	if code, _ := do(t, s, "POST", "/api/internal/songs/999/suggestions", testToken, u.Token,
		map[string]any{"title": "T", "artist": "A"}); code != 404 {
		t.Errorf("unknown item = %d, want 404", code)
	}
	if code, _ := do(t, s, "POST", "/api/internal/songs/1/suggestions", testToken, "",
		map[string]any{"title": "T", "artist": "A"}); code != 401 {
		t.Errorf("no session = %d, want 401", code)
	}
	// Trimmed values are stored; empty album/proof become NULL.
	rows, err := q.ListOpenSuggestionsForItem(context.Background(), 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %v %v", rows, err)
	}
	if rows[0].Artist != "A" || rows[0].Album != nil || rows[0].ProofUrl == nil {
		t.Errorf("stored %+v", rows[0])
	}
}

func TestSuggest_OneOpenPerItem(t *testing.T) {
	s, q, _ := sugSetup(t)
	seedHit(t, q, 1)
	seedHit(t, q, 2)
	u := login(t, s, 1, "alice")
	mod := login(t, s, 2, "mod")
	makeMod(t, q, mod)
	id := suggest(t, s, u.Token, 1, "T", "A")
	if code, body := do(t, s, "POST", "/api/internal/songs/1/suggestions", testToken, u.Token,
		map[string]any{"title": "T2", "artist": "A"}); code != 409 {
		t.Fatalf("second open = %d: %s", code, body)
	}
	suggest(t, s, u.Token, 2, "T", "A") // other item is fine
	if code, body := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/reject", id), testToken, mod.Token,
		map[string]any{"reason": "falsch"}); code != 200 {
		t.Fatalf("reject = %d: %s", code, body)
	}
	sg, _ := q.GetSuggestion(context.Background(), id)
	if sg.Status != "rejected" || sg.Reason == nil || *sg.Reason != "falsch" || sg.ReviewerID == nil || *sg.ReviewerID != mod.User.ID {
		t.Errorf("rejected = %+v", sg)
	}
	if h, _ := q.ListHistory(context.Background(), 1); len(h) != 0 {
		t.Errorf("reject wrote history: %v", h)
	}
	suggest(t, s, u.Token, 1, "T3", "A") // allowed again after the reject
	if code, _ := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/reject", id), testToken, mod.Token, nil); code != 409 {
		t.Errorf("reject decided = %d, want 409", code)
	}
}

func TestSuggest_RateLimit(t *testing.T) {
	s, q, _ := sugSetup(t)
	u := login(t, s, 1, "alice")
	for i := int64(1); i <= 21; i++ {
		seedHit(t, q, i)
	}
	for i := int64(1); i <= 20; i++ {
		suggest(t, s, u.Token, i, "T", "A")
	}
	if code, body := do(t, s, "POST", "/api/internal/songs/21/suggestions", testToken, u.Token,
		map[string]any{"title": "T", "artist": "A"}); code != 429 {
		t.Fatalf("21st = %d: %s", code, body)
	}
}

func TestVote_NotOwn(t *testing.T) {
	s, q, _ := sugSetup(t)
	seedHit(t, q, 1)
	author := login(t, s, 1, "alice")
	voter := login(t, s, 2, "bob")
	banned := login(t, s, 3, "badguy")
	mod := login(t, s, 4, "mod")
	makeMod(t, q, mod)
	id := suggest(t, s, author.Token, 1, "T", "A")
	if code, _ := vote(t, s, author.Token, id, 1); code != 403 {
		t.Errorf("own vote = %d, want 403", code)
	}
	for _, v := range []int{2, -2} {
		if code, _ := vote(t, s, voter.Token, id, v); code != 400 {
			t.Errorf("value %d = %d, want 400", v, code)
		}
	}
	if code, _ := do(t, s, "PUT", fmt.Sprintf("/api/internal/suggestions/%d/vote", id), testToken, voter.Token, map[string]any{}); code != 400 {
		t.Errorf("missing value = %d, want 400", code)
	}
	if code, _ := vote(t, s, voter.Token, 99999, 1); code != 404 {
		t.Errorf("unknown suggestion = %d, want 404", code)
	}
	if err := q.SetBan(context.Background(), db.SetBanParams{ID: banned.User.ID, BanReason: str("x")}); err != nil {
		t.Fatal(err)
	}
	if code, _ := vote(t, s, banned.Token, id, 1); code != 403 {
		t.Errorf("banned vote = %d, want 403", code)
	}
	mustVote(t, s, voter.Token, id, 1)

	// Listing: session optional, myVote/mine from the session.
	list := func(tok string) []map[string]any {
		code, body := do(t, s, "GET", "/api/internal/songs/1/suggestions", testToken, tok, nil)
		if code != 200 {
			t.Fatalf("list = %d: %s", code, body)
		}
		var out []map[string]any
		if err := json.Unmarshal(body, &out); err != nil || len(out) != 1 {
			t.Fatalf("list body %s", body)
		}
		return out
	}
	if m := list(voter.Token)[0]; m["myVote"] != float64(1) || m["mine"] != false || m["up"] != float64(1) ||
		m["net"] != float64(1) || m["author"] != "alice" || m["title"] != "T" {
		t.Errorf("voter view %v", m)
	}
	if m := list(author.Token)[0]; m["myVote"] != nil || m["mine"] != true {
		t.Errorf("author view %v", m)
	}
	if m := list("")[0]; m["myVote"] != nil || m["mine"] != false {
		t.Errorf("anon view %v", m)
	}
	mustVote(t, s, voter.Token, id, 0)
	if m := list(voter.Token)[0]; m["myVote"] != nil || m["up"] != float64(0) {
		t.Errorf("after unvote %v", m)
	}
	if code, _ := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/reject", id), testToken, mod.Token, nil); code != 200 {
		t.Fatalf("reject = %d", code)
	}
	if code, _ := vote(t, s, voter.Token, id, 1); code != 409 {
		t.Errorf("vote on rejected = %d, want 409", code)
	}
}

func TestAutoApprove_AtFiveNetFive(t *testing.T) {
	s, q, fd := sugSetup(t)
	seedHit(t, q, 1)
	seedHit(t, q, 2)
	author := login(t, s, 100, "author")
	voters := logins(t, s, 1, 9)

	// 5 up, 0 down approves.
	a := suggest(t, s, author.Token, 1, "Right", "Artist")
	for i := 0; i < 4; i++ {
		mustVote(t, s, voters[i].Token, a, 1)
	}
	if st := status(t, q, a); st != "open" {
		t.Fatalf("after 4 up: %s", st)
	}
	mustVote(t, s, voters[4].Token, a, 1)
	if st := status(t, q, a); st != "auto_approved" {
		t.Fatalf("after 5 up: %s", st)
	}
	sg, _ := q.GetSuggestion(context.Background(), a)
	if sg.ReviewerID != nil {
		t.Errorf("reviewer should be NULL, got %v", *sg.ReviewerID)
	}
	m := songMap(t, s, 1)
	if m["title"] != "Right" || m["provider"] != "community" || m["corrected"] != "community" ||
		m["deezerUrl"] != "https://www.deezer.com/track/42" || m["spotifyUrl"] != nil {
		t.Errorf("song after auto-approve %v", m)
	}
	h, _ := q.ListHistory(context.Background(), 1)
	if len(h) != 1 || h[0].Source != "vote" || h[0].ActorID != nil || h[0].SuggestionID == nil || *h[0].SuggestionID != a {
		t.Errorf("history %+v", h)
	}
	calls := fd.calls.Load()

	// 6 up, 2 down (net 4) does not; the 7th up (net 5) does.
	b := suggest(t, s, author.Token, 2, "Other", "Artist")
	mustVote(t, s, voters[0].Token, b, -1)
	mustVote(t, s, voters[1].Token, b, -1)
	for i := 2; i < 8; i++ {
		mustVote(t, s, voters[i].Token, b, 1)
	}
	if st := status(t, q, b); st != "open" {
		t.Fatalf("6 up 2 down: %s", st)
	}
	if fd.calls.Load() != calls {
		t.Errorf("deezer searched below the threshold")
	}
	mustVote(t, s, voters[8].Token, b, 1)
	if st := status(t, q, b); st != "auto_approved" {
		t.Fatalf("7 up 2 down: %s", st)
	}
}

func TestAutoApprove_ConcurrentOnlyOneWins(t *testing.T) {
	s, q, _ := sugSetup(t)
	const items = 5
	authorA := login(t, s, 100, "authorA")
	authorB := login(t, s, 101, "authorB")
	voters := logins(t, s, 1, 5)
	type pair struct{ a, b int64 }
	pairs := make([]pair, items)
	for i := range pairs {
		item := int64(i + 1)
		seedHit(t, q, item)
		pairs[i] = pair{suggest(t, s, authorA.Token, item, "A", "A"), suggest(t, s, authorB.Token, item, "B", "B")}
		for _, v := range voters[:4] {
			mustVote(t, s, v.Token, pairs[i].a, 1)
			mustVote(t, s, v.Token, pairs[i].b, 1)
		}
	}
	// The fifth upvotes on both suggestions of every item race each other.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan string, 4*items)
	for _, p := range pairs {
		for _, id := range []int64{p.a, p.b} {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				<-start
				code, body := vote(t, s, voters[4].Token, id, 1)
				if code != 200 && code != 409 {
					errs <- fmt.Sprintf("vote %d = %d: %s", id, code, body)
				}
			}(id)
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	for i, p := range pairs {
		sa, sb := status(t, q, p.a), status(t, q, p.b)
		if !(sa == "auto_approved" && sb == "superseded") && !(sa == "superseded" && sb == "auto_approved") {
			t.Errorf("item %d: a=%s b=%s, want exactly one auto_approved and one superseded", i+1, sa, sb)
		}
		h, _ := q.ListHistory(context.Background(), int64(i+1))
		if len(h) != 1 {
			t.Errorf("item %d: %d history rows, want 1", i+1, len(h))
		}
	}
}

func TestApprove_ClearsLinksAndSetsProvider(t *testing.T) {
	s, q, fd := sugSetup(t)
	seedHit(t, q, 1)
	u := login(t, s, 1, "alice")
	other := login(t, s, 2, "bob")
	mod := login(t, s, 3, "mod")
	makeMod(t, q, mod)
	id := suggest(t, s, u.Token, 1, "Real Title", "Real Artist")
	loser := suggest(t, s, other.Token, 1, "Else", "Else")

	if code, _ := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/approve", id), testToken, u.Token, nil); code != 403 {
		t.Errorf("user approve = %d, want 403", code)
	}
	code, body := do(t, s, "GET", "/api/internal/mod/queue", testToken, mod.Token, nil)
	var queue struct{ Items []map[string]any }
	if code != 200 || json.Unmarshal(body, &queue) != nil || len(queue.Items) != 2 || queue.Items[0]["id"] != float64(id) {
		t.Fatalf("queue = %d %s", code, body)
	}
	if cur, _ := queue.Items[0]["current"].(map[string]any); cur == nil || cur["title"] != "Bot Title" {
		t.Errorf("queue current = %v", queue.Items[0]["current"])
	}
	if code, body := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/approve", id), testToken, mod.Token, nil); code != 200 {
		t.Fatalf("approve = %d: %s", code, body)
	}
	if fd.calls.Load() != 1 {
		t.Errorf("deezer calls = %d", fd.calls.Load())
	}
	m := songMap(t, s, 1)
	for _, k := range []string{"spotifyUrl", "youtubeUrl", "appleMusicUrl", "songLinkUrl", "album"} {
		if m[k] != nil {
			t.Errorf("%s = %v, want null", k, m[k])
		}
	}
	if m["title"] != "Real Title" || m["artist"] != "Real Artist" || m["provider"] != "moderation" ||
		m["corrected"] != "moderation" || m["found"] != true ||
		m["deezerUrl"] != "https://www.deezer.com/track/42" || m["coverUrl"] != "https://cdn-images.dzcdn.net/c.jpg" {
		t.Errorf("song = %v", m)
	}
	sg, _ := q.GetSuggestion(context.Background(), id)
	if sg.Status != "approved" || sg.ReviewerID == nil || *sg.ReviewerID != mod.User.ID {
		t.Errorf("suggestion = %+v", sg)
	}
	if st := status(t, q, loser); st != "superseded" {
		t.Errorf("loser = %s", st)
	}
	h, _ := q.ListHistory(context.Background(), 1)
	if len(h) != 1 || h[0].Source != "moderator" || h[0].ActorID == nil || *h[0].ActorID != mod.User.ID {
		t.Errorf("history = %+v", h)
	}
	if code, _ := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/approve", loser), testToken, mod.Token, nil); code != 409 {
		t.Errorf("approve superseded = %d, want 409", code)
	}
	if code, _ := do(t, s, "POST", "/api/internal/suggestions/99999/approve", testToken, mod.Token, nil); code != 404 {
		t.Errorf("approve unknown = %d, want 404", code)
	}
}

func TestRevert_ToBotOriginal(t *testing.T) {
	s, q, _ := sugSetup(t)
	seedHit(t, q, 1)
	seedHit(t, q, 2)
	u := login(t, s, 1, "alice")
	mod := login(t, s, 3, "mod")
	makeMod(t, q, mod)
	approve := func(id int64) {
		t.Helper()
		if code, body := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/approve", id), testToken, mod.Token, nil); code != 200 {
			t.Fatalf("approve = %d: %s", code, body)
		}
	}
	original := songMap(t, s, 1)
	approve(suggest(t, s, u.Token, 1, "First", "Fix"))
	approve(suggest(t, s, u.Token, 1, "Second", "Fix"))
	if m := songMap(t, s, 1); m["title"] != "Second" {
		t.Fatalf("after two corrections %v", m)
	}

	code, body := do(t, s, "GET", "/api/internal/songs/1/history", testToken, mod.Token, nil)
	var hist []struct {
		ID       int64
		Source   string
		Snapshot map[string]any
	}
	if code != 200 || json.Unmarshal(body, &hist) != nil || len(hist) != 2 {
		t.Fatalf("history = %d %s", code, body)
	}
	botOriginal := hist[len(hist)-1] // newest first: the last one is the bot's original
	if botOriginal.Snapshot["title"] != "Bot Title" {
		t.Fatalf("first snapshot %v", botOriginal.Snapshot)
	}
	if code, _ := do(t, s, "GET", "/api/internal/songs/1/history", testToken, u.Token, nil); code != 403 {
		t.Errorf("user history = %d, want 403", code)
	}
	if code, _ := do(t, s, "POST", "/api/internal/songs/2/revert", testToken, mod.Token,
		map[string]any{"historyId": botOriginal.ID}); code != 404 {
		t.Errorf("foreign history id = %d, want 404", code)
	}
	if code, body := do(t, s, "POST", "/api/internal/songs/1/revert", testToken, mod.Token,
		map[string]any{"historyId": botOriginal.ID}); code != 200 {
		t.Fatalf("revert = %d: %s", code, body)
	}
	m := songMap(t, s, 1)
	for _, k := range []string{"title", "artist", "album", "provider", "spotifyUrl", "youtubeUrl", "coverUrl", "appleMusicUrl", "deezerUrl", "songLinkUrl", "recognizedAt"} {
		if fmt.Sprint(m[k]) != fmt.Sprint(original[k]) {
			t.Errorf("%s = %v, want %v", k, m[k], original[k])
		}
	}
	if m["corrected"] != nil || m["provider"] != "shazam" {
		t.Errorf("corrected = %v provider = %v", m["corrected"], m["provider"])
	}
	h, _ := q.ListHistory(context.Background(), 1)
	if len(h) != 3 || h[0].Source != "revert" || h[0].ActorID == nil || *h[0].ActorID != mod.User.ID || h[0].SuggestionID != nil {
		t.Fatalf("history after revert = %+v", h)
	}
	var snap map[string]any
	_ = json.Unmarshal(h[0].Snapshot, &snap)
	if snap["title"] != "Second" {
		t.Errorf("revert row should snapshot the pre-revert state, got %v", snap["title"])
	}
	// The song is protected again only by corrected_at of the restored row: the bot original has none.
	if sg, _ := q.GetSong(context.Background(), 1); sg.CorrectedAt != nil {
		t.Errorf("corrected_at = %v, want NULL after revert to bot original", sg.CorrectedAt)
	}
}

func TestGetSong_MissFound(t *testing.T) {
	s, q, _ := sugSetup(t)
	seed(t, q, 5, nil, nil, time.Now())
	seedHit(t, q, 6)
	m := songMap(t, s, 5)
	if m["found"] != false || m["corrected"] != nil {
		t.Errorf("miss = %v", m)
	}
	for _, k := range []string{"title", "artist", "album", "spotifyUrl", "youtubeUrl", "deezerUrl", "appleMusicUrl", "songLinkUrl", "spotifySearchUrl", "youtubeSearchUrl"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("miss %s = %v (present %v), want null", k, v, ok)
		}
	}
	if m := songMap(t, s, 6); m["found"] != true || m["corrected"] != nil || m["title"] != "Bot Title" {
		t.Errorf("hit = %v", m)
	}
	if code, _, _ := get(t, s, "/api/songs/424242"); code != 404 {
		t.Errorf("unknown = %d, want 404", code)
	}
	// Lists still return hits only.
	if r := getList(t, s, "/api/songs", 200); fmt.Sprint(ids(r)) != "[6]" {
		t.Errorf("list = %v", ids(r))
	}
}
