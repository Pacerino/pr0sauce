package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/testdb"
)

func str(s string) *string { return &s }

func setup(t *testing.T) (*server, *db.Queries) {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	s := newServer(q, pool, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	return s, q
}

func seed(t *testing.T, q *db.Queries, id int64, title, artist *string, at time.Time) {
	t.Helper()
	err := q.UpsertSong(context.Background(), db.UpsertSongParams{
		ItemID: id, Title: title, Artist: artist, Provider: "shazam", RecognizedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func get(t *testing.T, h http.Handler, path string) (int, http.Header, []byte) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
	return rec.Code, rec.Header(), rec.Body.Bytes()
}

type testList struct {
	Items      []map[string]any `json:"items"`
	NextCursor *string          `json:"nextCursor"`
}

func getList(t *testing.T, h http.Handler, path string, want int) testList {
	t.Helper()
	code, hdr, body := get(t, h, path)
	if code != want {
		t.Fatalf("GET %s = %d, want %d: %s", path, code, want, body)
	}
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type %q", ct)
	}
	var r testList
	if want == 200 {
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func ids(r testList) []int64 {
	var out []int64
	for _, it := range r.Items {
		out = append(out, int64(it["itemId"].(float64)))
	}
	return out
}

func TestListRecent_PaginatesHitsOnly(t *testing.T) {
	s, q := setup(t)
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := int64(1); i <= 5; i++ {
		seed(t, q, i, str(fmt.Sprintf("T%d", i)), str("A"), base.Add(time.Duration(i)*time.Minute))
	}
	seed(t, q, 99, nil, nil, base.Add(time.Hour)) // miss, newest
	// tie on recognized_at: ids 6 and 7 share a timestamp with 5? use separate tie pair
	seed(t, q, 6, str("T6"), str("A"), base.Add(5*time.Minute))

	p1 := getList(t, s, "/api/songs?limit=3", 200)
	got := ids(p1)
	if fmt.Sprint(got) != "[6 5 4]" || p1.NextCursor == nil {
		t.Fatalf("page1 = %v cursor=%v", got, p1.NextCursor)
	}
	p2 := getList(t, s, "/api/songs?limit=3&cursor="+url.QueryEscape(*p1.NextCursor), 200)
	if fmt.Sprint(ids(p2)) != "[3 2 1]" {
		t.Fatalf("page2 = %v", ids(p2))
	}
	if p2.NextCursor == nil { // exactly limit items returned: cursor still given
		t.Fatal("expected cursor when len == limit")
	}
	p3 := getList(t, s, "/api/songs?limit=3&cursor="+url.QueryEscape(*p2.NextCursor), 200)
	if len(p3.Items) != 0 || p3.NextCursor != nil {
		t.Fatalf("page3 = %v %v", p3.Items, p3.NextCursor)
	}
	if p3.Items == nil {
		t.Fatal("items must be [] not null")
	}
}

func TestSearch_MatchesTitleAndArtist(t *testing.T) {
	s, q := setup(t)
	now := time.Now()
	seed(t, q, 1, str("Kumite"), str("Hertzog"), now)
	seed(t, q, 2, str("Other"), str("Someone"), now)
	r := getList(t, s, "/api/songs?q=hertz", 200)
	if fmt.Sprint(ids(r)) != "[1]" {
		t.Fatalf("got %v", ids(r))
	}
	r = getList(t, s, "/api/songs?q=KUMITE", 200)
	if fmt.Sprint(ids(r)) != "[1]" {
		t.Fatalf("title: got %v", ids(r))
	}
}

func TestSearch_Pagination(t *testing.T) {
	s, q := setup(t)
	now := time.Now()
	for i := int64(1); i <= 3; i++ {
		seed(t, q, i, str(fmt.Sprintf("Song %d", i)), str("X"), now.Add(time.Duration(i)*time.Second))
	}
	p1 := getList(t, s, "/api/songs?q=song&limit=2", 200)
	if len(p1.Items) != 2 || p1.NextCursor == nil {
		t.Fatalf("p1 %v", p1)
	}
	p2 := getList(t, s, "/api/songs?q=song&limit=2&cursor="+url.QueryEscape(*p1.NextCursor), 200)
	if len(p2.Items) != 1 || p2.NextCursor != nil {
		t.Fatalf("p2 %v", p2)
	}
}

func TestSearch_LiteralSpecialChars(t *testing.T) {
	s, q := setup(t)
	now := time.Now()
	seed(t, q, 1, str("100% Pure"), str("A"), now)
	seed(t, q, 2, str("1000 Pure"), str("A"), now)
	seed(t, q, 3, str("a_b"), str("A"), now)
	seed(t, q, 4, str("axb"), str("A"), now)
	seed(t, q, 5, str("O'Brien"), str("A"), now)
	seed(t, q, 6, str("Новая песня"), str("A"), now)
	seed(t, q, 7, str(`back\slash`), str("A"), now)
	cases := []struct {
		q    string
		want string
	}{
		{"100%", "[1]"},
		{"a_b", "[3]"},
		{"O'Brien", "[5]"},
		{"Новая", "[6]"},
		{`k\s`, "[7]"},
		{"%", "[1]"},
	}
	for _, c := range cases {
		r := getList(t, s, "/api/songs?q="+url.QueryEscape(c.q), 200)
		if fmt.Sprint(ids(r)) != c.want {
			t.Errorf("q=%q got %v want %s", c.q, ids(r), c.want)
		}
	}
}

func TestSearch_NumericQueryIsItemLookup(t *testing.T) {
	s, q := setup(t)
	now := time.Now()
	seed(t, q, 6796145, str("Hit"), str("A"), now)
	seed(t, q, 55, nil, nil, now)                  // miss
	seed(t, q, 7, str("0123 text"), str("A"), now) // text search target
	r := getList(t, s, "/api/songs?q=6796145", 200)
	if fmt.Sprint(ids(r)) != "[6796145]" || r.NextCursor != nil {
		t.Fatalf("hit: %v", r)
	}
	r = getList(t, s, "/api/songs?q=55", 200)
	if len(r.Items) != 0 || r.Items == nil {
		t.Fatalf("miss: %v", r)
	}
	r = getList(t, s, "/api/songs?q=0123", 200)
	if fmt.Sprint(ids(r)) != "[7]" {
		t.Fatalf("0123 should be text search, got %v", ids(r))
	}
	// 19 digits overflowing int64 falls through to text search, not an error
	getList(t, s, "/api/songs?q=9999999999999999999", 200)
}

func TestGetSong_BadIDs(t *testing.T) {
	s, _ := setup(t)
	for _, id := range []string{"0123", "99999999999999999999", "9999999999999999999", "-5", "abc", "0"} {
		code, hdr, body := get(t, s, "/api/songs/"+id)
		if code != 400 {
			t.Errorf("%s = %d %s", id, code, body)
		}
		if !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") {
			t.Errorf("%s content-type %q", id, hdr.Get("Content-Type"))
		}
	}
	code, _, body := get(t, s, "/api/songs/424242")
	if code != 404 || !strings.Contains(string(body), `"error"`) {
		t.Errorf("unknown = %d %s", code, body)
	}
}

func TestGetSong_MissIs404(t *testing.T) {
	s, q := setup(t)
	seed(t, q, 5, nil, nil, time.Now())
	if code, _, _ := get(t, s, "/api/songs/5"); code != 404 {
		t.Fatalf("code %d", code)
	}
}

func TestGetSong_IncludesComment(t *testing.T) {
	s, q := setup(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	seed(t, q, 10, str("T"), str("A"), now)
	seed(t, q, 11, str("T2"), str("A"), now)
	if err := q.UpsertBotComment(context.Background(), db.UpsertBotCommentParams{
		CommentID: 777, ItemID: 10, Content: "x", Up: 4, Down: 1, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	code, _, body := get(t, s, "/api/songs/10")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	c := m["comment"].(map[string]any)
	if c["up"].(float64) != 4 || c["down"].(float64) != 1 || c["commentId"].(float64) != 777 {
		t.Fatalf("comment %v", c)
	}
	if m["title"] != "T" || m["artist"] != "A" || m["provider"] != "shazam" || m["album"] != nil {
		t.Fatalf("song %v", m)
	}
	if _, err := time.Parse(time.RFC3339, m["recognizedAt"].(string)); err != nil {
		t.Fatal(err)
	}
	_, _, body = get(t, s, "/api/songs/11")
	_ = json.Unmarshal(body, &m)
	if v, ok := m["comment"]; !ok || v != nil {
		t.Fatalf("comment should be null: %s", body)
	}
}

func TestLimitBounds(t *testing.T) {
	s, _ := setup(t)
	for _, l := range []string{"0", "-1", "51", "abc", ""} {
		code, _, _ := get(t, s, "/api/songs?limit="+l)
		if l == "" {
			if code != 400 { // explicit empty value is invalid
				t.Errorf("limit= -> %d", code)
			}
			continue
		}
		if code != 400 {
			t.Errorf("limit=%s -> %d", l, code)
		}
	}
	for _, l := range []string{"1", "50"} {
		if code, _, _ := get(t, s, "/api/songs?limit="+l); code != 200 {
			t.Errorf("limit=%s -> %d", l, code)
		}
	}
}

func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

func TestCursor_WrongKindRejected(t *testing.T) {
	s, q := setup(t)
	seed(t, q, 1, str("Song"), str("A"), time.Now())
	recent := b64("r|2026-01-01T00:00:00Z|5")
	search := b64("s|20")
	for _, tc := range []struct{ path string }{
		{"/api/songs?cursor=" + search},         // search cursor, no q
		{"/api/songs?q=song&cursor=" + recent},  // recent cursor with q
		{"/api/songs?cursor=!!!"},               // not base64
		{"/api/songs?cursor=" + b64("r|bad|5")}, // bad timestamp
		{"/api/songs?cursor=" + b64("r|2026-01-01T00:00:00Z|x")},
		{"/api/songs?q=song&cursor=" + b64("s|-1")}, // negative offset
		{"/api/songs?q=song&cursor=" + b64("s|abc")},
		{"/api/songs?cursor=" + b64("zzz")},
	} {
		if code, _, body := get(t, s, tc.path); code != 400 {
			t.Errorf("%s = %d %s", tc.path, code, body)
		}
	}
	if code, _, _ := get(t, s, "/api/songs?cursor="+recent); code != 200 {
		t.Errorf("valid recent cursor = %d", code)
	}
	if code, _, _ := get(t, s, "/api/songs?q=song&cursor="+search); code != 200 {
		t.Errorf("valid search cursor = %d", code)
	}
}

func TestStats_Shape(t *testing.T) {
	s, q := setup(t)
	ctx := context.Background()
	now := time.Now()
	seed(t, q, 1, str("A song"), str("Art"), now)
	seed(t, q, 2, str("A song"), str("art"), now.Add(-time.Hour*24*40))
	seed(t, q, 3, nil, nil, now)
	if err := q.SetState(ctx, db.SetStateParams{Key: "live_since", Value: "2026-03-01T10:00:00.123456789Z"}); err != nil {
		t.Fatal(err)
	}
	code, hdr, body := get(t, s, "/api/stats")
	if code != 200 || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") {
		t.Fatalf("%d %s", code, body)
	}
	var m struct {
		TotalLookups int64   `json:"totalLookups"`
		Hits         int64   `json:"hits"`
		HitRate      float64 `json:"hitRate"`
		Today        int64   `json:"today"`
		Providers    []struct {
			Provider string `json:"provider"`
			Count    int64  `json:"count"`
		} `json:"providers"`
		PerWeek []struct {
			Week  string `json:"week"`
			Count int64  `json:"count"`
		} `json:"perWeek"`
		TopSongs []struct {
			Title, Artist string
			Count         int64
		} `json:"topSongs"`
		TopRequesters []struct {
			Name  string
			Count int64
		} `json:"topRequesters"`
		RequestersSince *string `json:"requestersSince"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	if m.TotalLookups != 3 || m.Hits != 2 || m.HitRate < 0.66 || m.HitRate > 0.67 {
		t.Fatalf("counts %+v", m)
	}
	if m.Today != 2 {
		t.Fatalf("today %d", m.Today)
	}
	if len(m.Providers) != 1 || m.Providers[0].Provider != "shazam" || m.Providers[0].Count != 2 {
		t.Fatalf("providers %+v", m.Providers)
	}
	if len(m.PerWeek) < 2 {
		t.Fatalf("perWeek %+v", m.PerWeek)
	}
	for _, w := range m.PerWeek {
		if _, err := time.Parse("2006-01-02", w.Week); err != nil {
			t.Fatalf("week %q", w.Week)
		}
	}
	if len(m.TopSongs) != 1 || m.TopSongs[0].Count != 2 {
		t.Fatalf("topSongs %+v", m.TopSongs)
	}
	if m.TopRequesters == nil {
		t.Fatalf("topRequesters must be [] : %s", body)
	}
	if m.RequestersSince == nil || *m.RequestersSince != "2026-03-01T10:00:00.123456789Z" {
		t.Fatalf("requestersSince %v", m.RequestersSince)
	}
}

func TestStats_EmptyDB(t *testing.T) {
	s, _ := setup(t)
	code, _, body := get(t, s, "/api/stats")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m["hitRate"].(float64) != 0 || m["requestersSince"] != nil {
		t.Fatalf("%s", body)
	}
	for _, k := range []string{"providers", "perWeek", "topSongs", "topRequesters"} {
		if _, ok := m[k].([]any); !ok {
			t.Errorf("%s not an array: %s", k, body)
		}
	}
}

func TestStats_CachedFor60s(t *testing.T) {
	pool := testdb.New(t)
	q := db.New(pool)
	var mu sync.Mutex
	clock := time.Now()
	s := newServer(q, pool, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	})
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	hits := func() int64 {
		_, _, body := get(t, s, "/api/stats")
		var m struct{ TotalLookups int64 }
		_ = json.Unmarshal(body, &m)
		return m.TotalLookups
	}
	seed(t, q, 1, str("T"), str("A"), clock)
	if n := hits(); n != 1 {
		t.Fatalf("first %d", n)
	}
	seed(t, q, 2, str("T"), str("A"), clock)
	advance(59 * time.Second)
	if n := hits(); n != 1 {
		t.Fatalf("cached expected 1, got %d", n)
	}
	advance(2 * time.Second)
	if n := hits(); n != 2 {
		t.Fatalf("expired expected 2, got %d", n)
	}
}

func TestStats_ComputedOncePerExpiry(t *testing.T) {
	s, _ := setup(t)
	var calls int
	var cmu sync.Mutex
	orig := s.compute
	s.compute = func(ctx context.Context, now time.Time) (*statsResp, error) {
		cmu.Lock()
		calls++
		cmu.Unlock()
		time.Sleep(50 * time.Millisecond)
		return orig(ctx, now)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get(t, s, "/api/stats") }()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("compute called %d times", calls)
	}
}

func TestErrorsAreJSON(t *testing.T) {
	s, _ := setup(t)
	code, hdr, body := get(t, s, "/nope")
	if code != 404 || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") || !strings.Contains(string(body), `"error"`) {
		t.Fatalf("404: %d %v %s", code, hdr, body)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/api/songs", nil))
	if rec.Code != 405 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("405: %d %v", rec.Code, rec.Header())
	}
}

func TestHealthz(t *testing.T) {
	s, _ := setup(t)
	if code, _, _ := get(t, s, "/healthz"); code != 200 {
		t.Fatalf("code %d", code)
	}
}

func TestSearch_PaginationStableOnTies(t *testing.T) {
	s, q := setup(t)
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := int64(1); i <= 5; i++ {
		seed(t, q, i, str("Same"), str("Same"), at)
	}
	var got []int64
	path := "/api/songs?q=same&limit=2"
	for page := 0; page < 10; page++ {
		r := getList(t, s, path, 200)
		got = append(got, ids(r)...)
		if r.NextCursor == nil {
			break
		}
		path = "/api/songs?q=same&limit=2&cursor=" + url.QueryEscape(*r.NextCursor)
	}
	if fmt.Sprint(got) != "[5 4 3 2 1]" {
		t.Fatalf("got %v", got)
	}
}

func TestSearch_QueryLengthCap(t *testing.T) {
	s, _ := setup(t)
	ok := strings.Repeat("я", 200)
	if code, _, _ := get(t, s, "/api/songs?q="+url.QueryEscape(ok)); code != 200 {
		t.Fatalf("200 runes = %d", code)
	}
	code, _, body := get(t, s, "/api/songs?q="+url.QueryEscape(ok+"x"))
	if code != 400 || !strings.Contains(string(body), "q too long") {
		t.Fatalf("201 runes = %d %s", code, body)
	}
	// whitespace is trimmed before counting
	if code, _, _ := get(t, s, "/api/songs?q="+url.QueryEscape("  "+ok+"  ")); code != 200 {
		t.Fatalf("padded = %d", code)
	}
}
