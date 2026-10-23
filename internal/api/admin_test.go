package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/Pacerino/pr0sauce/internal/testdb"
)

type fakePr0 map[string]pr0gramm.Profile

func (f fakePr0) ProfileInfo(_ context.Context, name string) (pr0gramm.Profile, error) {
	if p, ok := f[name]; ok {
		return p, nil
	}
	return pr0gramm.Profile{}, pr0gramm.ErrUserNotFound
}

// adminSetup builds a server whose first "Helix" login is the admin.
func adminSetup(t *testing.T, lookup fakePr0) (*server, *db.Queries, loginResp) {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	s := newServer(q, pool, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{InternalToken: testToken, AdminName: "Helix", Pr0: lookup, Deezer: &fakeDeezer{}})
	return s, q, login(t, s, 900, "Helix")
}

func adminDo(t *testing.T, s *server, tok, method, path string, body any) (int, []byte) {
	t.Helper()
	return do(t, s, method, "/api/internal/admin"+path, testToken, tok, body)
}

func TestModerators_AddByName_Unknown404(t *testing.T) {
	s, q, admin := adminSetup(t, fakePr0{"alice": {ID: 55, Name: "Alice"}, "Helix": {ID: 900, Name: "Helix"}})
	u := login(t, s, 1, "u1")

	if code, _ := adminDo(t, s, u.Token, "POST", "/moderators", map[string]string{"name": "alice"}); code != 403 {
		t.Errorf("non-admin add = %d", code)
	}
	code, body := adminDo(t, s, admin.Token, "POST", "/moderators", map[string]string{"name": "nobody"})
	if code != 404 || !strings.Contains(string(body), "pr0gramm-Nutzer nicht gefunden") {
		t.Fatalf("unknown = %d %s", code, body)
	}
	code, body = adminDo(t, s, admin.Token, "POST", "/moderators", map[string]string{"name": "alice"})
	if code != 200 {
		t.Fatalf("add = %d %s", code, body)
	}
	alice, err := q.GetUserByPr0ID(context.Background(), 55)
	if err != nil || alice.Role != "moderator" || alice.Name != "Alice" {
		t.Fatalf("alice %+v %v", alice, err)
	}
	code, body = adminDo(t, s, admin.Token, "POST", "/moderators", map[string]string{"name": "Helix"})
	if code != 409 || !strings.Contains(string(body), "Admin bleibt Admin") {
		t.Errorf("add admin = %d %s", code, body)
	}
	if h, _ := q.GetUser(context.Background(), admin.User.ID); h.Role != "admin" {
		t.Errorf("admin demoted to %s", h.Role)
	}
	code, body = adminDo(t, s, admin.Token, "GET", "/moderators", nil)
	var list []map[string]any
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list) != 2 {
		t.Fatalf("list = %d %s", code, body)
	}
	if code, _ := adminDo(t, s, admin.Token, "DELETE", fmt.Sprintf("/moderators/%d", admin.User.ID), nil); code != 409 {
		t.Errorf("remove admin = %d", code)
	}
	if code, _ := adminDo(t, s, admin.Token, "DELETE", fmt.Sprintf("/moderators/%d", alice.ID), nil); code != 204 {
		t.Errorf("remove mod = %d", code)
	}
	if a, _ := q.GetUser(context.Background(), alice.ID); a.Role != "user" {
		t.Errorf("alice role %s", a.Role)
	}
}

func TestBan_RemovesVotesFromTally(t *testing.T) {
	s, q, admin := adminSetup(t, fakePr0{"u1": {ID: 1, Name: "u1"}})
	seedHit(t, q, 1)
	author := login(t, s, 100, "author")
	voters := logins(t, s, 1, 5) // voters[0] is X (u1)
	sid := suggest(t, s, author.Token, 1, "Right", "Artist")
	ctx := context.Background()
	for _, v := range voters { // direct inserts: no auto-approval check runs
		if err := q.UpsertVote(ctx, db.UpsertVoteParams{SuggestionID: sid, UserID: v.User.ID, Value: 1}); err != nil {
			t.Fatal(err)
		}
	}
	up := func() int64 {
		t.Helper()
		tl, err := q.TallySuggestion(ctx, sid)
		if err != nil {
			t.Fatal(err)
		}
		return tl.Up
	}
	if up() != 5 {
		t.Fatalf("seed tally %d", up())
	}
	if code, body := adminDo(t, s, admin.Token, "POST", "/bans", map[string]string{"name": "u1", "reason": "spam"}); code != 200 {
		t.Fatalf("ban = %d %s", code, body)
	}
	if up() != 4 || status(t, q, sid) != "open" {
		t.Fatalf("after ban: up %d status %s", up(), status(t, q, sid))
	}
	if code, body := adminDo(t, s, admin.Token, "DELETE", fmt.Sprintf("/bans/%d", voters[0].User.ID), nil); code != 204 {
		t.Fatalf("unban = %d %s", code, body)
	}
	if up() != 5 {
		t.Errorf("after unban up %d", up())
	}
	if st := status(t, q, sid); st != "open" {
		t.Errorf("unban must not auto-approve, status %s", st)
	}
	if u, _ := q.GetUser(ctx, voters[0].User.ID); u.BannedAt != nil || u.BanReason != nil {
		t.Errorf("ban not cleared %+v", u)
	}
}

func TestBan_ClosesOpenSuggestionsAndSessions(t *testing.T) {
	s, q, admin := adminSetup(t, fakePr0{"u1": {ID: 1, Name: "u1"}})
	seedHit(t, q, 1)
	seedHit(t, q, 2)
	x := login(t, s, 1, "u1")
	other := login(t, s, 2, "u2")
	a := suggest(t, s, x.Token, 1, "A", "Art")
	b := suggest(t, s, x.Token, 2, "B", "Art")
	c := suggest(t, s, other.Token, 1, "C", "Art")
	// a second session for x
	x2 := login(t, s, 1, "u1")

	if code, body := adminDo(t, s, admin.Token, "POST", "/bans", map[string]string{"name": "u1", "reason": "spam"}); code != 200 {
		t.Fatalf("ban = %d %s", code, body)
	}
	ctx := context.Background()
	for _, id := range []int64{a, b} {
		sg, _ := q.GetSuggestion(ctx, id)
		if sg.Status != "rejected" || sg.Reason == nil || *sg.Reason != "gesperrt" {
			t.Errorf("suggestion %d: %+v", id, sg)
		}
	}
	if status(t, q, c) != "open" {
		t.Errorf("other user's suggestion touched")
	}
	for _, tok := range []string{x.Token, x2.Token} {
		if code, _ := do(t, s, "GET", "/api/internal/me", testToken, tok, nil); code != 401 {
			t.Errorf("session still valid: %d", code)
		}
	}
	if u, _ := q.GetUser(ctx, x.User.ID); u.BannedAt == nil || u.BanReason == nil || *u.BanReason != "spam" {
		t.Errorf("user %+v", u)
	}
	code, body := adminDo(t, s, admin.Token, "GET", "/bans", nil)
	var list []map[string]any
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list) != 1 || list[0]["reason"] != "spam" {
		t.Errorf("bans = %d %s", code, body)
	}
}

func TestBan_CannotTargetAdmin(t *testing.T) {
	s, q, admin := adminSetup(t, fakePr0{"Helix": {ID: 900, Name: "Helix"}})
	code, body := adminDo(t, s, admin.Token, "POST", "/bans", map[string]string{"name": "Helix", "reason": "x"})
	if code != 409 {
		t.Fatalf("ban admin = %d %s", code, body)
	}
	if u, _ := q.GetUser(context.Background(), admin.User.ID); u.BannedAt != nil {
		t.Errorf("admin banned")
	}
	if code, _ := do(t, s, "GET", "/api/internal/me", testToken, admin.Token, nil); code != 200 {
		t.Errorf("admin session gone: %d", code)
	}
	if code, _ := adminDo(t, s, admin.Token, "POST", "/bans", map[string]string{"name": "ghost"}); code != 404 {
		t.Errorf("unknown ban = %d", code)
	}
}

func TestBotHealth_ParsesDegraded(t *testing.T) {
	s, q, admin := adminSetup(t, nil)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"degraded","lastPoll":"stale"}`))
	}))
	s.botHealthURL = ts.URL
	if _, err := q.InsertRequestIgnore(context.Background(), db.InsertRequestIgnoreParams{
		MessageID: 1, ItemID: 1, Requester: "r", Thumb: "t", MentionedAt: time.Now(),
		Status: "pending", NextAttemptAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	type botResp struct {
		Health     map[string]any   `json:"health"`
		HTTPStatus int              `json:"httpStatus"`
		Requests   map[string]int64 `json:"requests"`
		Retry      map[string]any   `json:"retry"`
		Backlog    *int64           `json:"markReadBacklog"`
		LastDone   *string          `json:"lastDoneAt"`
	}
	var r botResp
	code, body := adminDo(t, s, admin.Token, "GET", "/bot", nil)
	if code != 200 || json.Unmarshal(body, &r) != nil {
		t.Fatalf("bot = %d %s", code, body)
	}
	if r.HTTPStatus != 503 || r.Health["status"] != "degraded" {
		t.Errorf("health %+v status %d", r.Health, r.HTTPStatus)
	}
	if r.Requests["pending"] != 1 || r.Requests["done"] != 0 || r.Requests["failed"] != 0 ||
		r.Requests["replying"] != 0 || r.Requests["skipped"] != 0 {
		t.Errorf("requests %+v", r.Requests)
	}
	if r.Backlog == nil || r.LastDone != nil || r.Retry["nextAttemptAt"] != nil {
		t.Errorf("body %s", body)
	}

	ts.Close() // unreachable now
	r = botResp{}
	code, body = adminDo(t, s, admin.Token, "GET", "/bot", nil)
	if code != 200 || json.Unmarshal(body, &r) != nil || r.HTTPStatus != 0 || r.Health["error"] != "unreachable" {
		t.Errorf("unreachable = %d %s", code, body)
	}
}

func TestFailedList(t *testing.T) {
	s, q, admin := adminSetup(t, nil)
	ctx := context.Background()
	for i := int64(1); i <= 3; i++ {
		if _, err := q.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{
			MessageID: i, ItemID: 10 + i, Requester: fmt.Sprintf("r%d", i), Thumb: "t",
			MentionedAt: time.Now(), Status: "pending", NextAttemptAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	errMsg := "boom"
	rows, _ := q.DueRequests(ctx, db.DueRequestsParams{Now: time.Now().Add(time.Hour), Lim: 10})
	for _, r := range rows[:2] {
		if _, err := q.SetRequestStatus(ctx, db.SetRequestStatusParams{ID: r.ID, Status: "failed", LastError: &errMsg}); err != nil {
			t.Fatal(err)
		}
	}
	code, body := adminDo(t, s, admin.Token, "GET", "/failed", nil)
	var list []struct {
		ID        int64   `json:"id"`
		ItemID    int64   `json:"itemId"`
		Requester string  `json:"requester"`
		Attempts  int     `json:"attempts"`
		LastError *string `json:"lastError"`
		UpdatedAt string  `json:"updatedAt"`
	}
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list) != 2 {
		t.Fatalf("failed = %d %s", code, body)
	}
	if list[0].LastError == nil || *list[0].LastError != "boom" || list[0].Requester == "" || list[0].UpdatedAt == "" {
		t.Errorf("row %+v", list[0])
	}
	if code, body := adminDo(t, s, admin.Token, "GET", "/failed?offset=1", nil); code != 200 || !strings.HasPrefix(string(body), "[{") {
		t.Errorf("offset = %d %s", code, body)
	}
	for _, bad := range []string{"-1", "10001", "x"} {
		if code, _ := adminDo(t, s, admin.Token, "GET", "/failed?offset="+bad, nil); code != 400 {
			t.Errorf("offset %s = %d", bad, code)
		}
	}
	if code, _ := adminDo(t, s, "", "GET", "/failed", nil); code != 401 {
		t.Errorf("anon = %d", code)
	}
}

func TestVotes_ModOnly(t *testing.T) {
	s, q, _ := adminSetup(t, nil)
	seedHit(t, q, 1)
	author := login(t, s, 100, "author")
	voters := logins(t, s, 1, 2)
	mod := login(t, s, 50, "modo")
	makeMod(t, q, mod)
	sid := suggest(t, s, author.Token, 1, "T", "A")
	mustVote(t, s, voters[0].Token, sid, 1)
	time.Sleep(5 * time.Millisecond)
	mustVote(t, s, voters[1].Token, sid, -1)
	path := fmt.Sprintf("/api/internal/suggestions/%d/votes", sid)

	if code, _ := do(t, s, "GET", path, testToken, voters[0].Token, nil); code != 403 {
		t.Errorf("user = %d", code)
	}
	code, body := do(t, s, "GET", path, testToken, mod.Token, nil)
	var list []struct {
		UserID int64  `json:"userId"`
		Name   string `json:"name"`
		Value  int    `json:"value"`
		Banned bool   `json:"banned"`
	}
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list) != 2 {
		t.Fatalf("mod = %d %s", code, body)
	}
	if list[0].Name != "u2" || list[0].Value != -1 || list[1].Name != "u1" || list[1].Value != 1 || list[0].Banned {
		t.Errorf("list %+v", list)
	}
	if code, _ := do(t, s, "GET", "/api/internal/suggestions/999999/votes", testToken, mod.Token, nil); code != 404 {
		t.Errorf("missing = %d", code)
	}
}

func TestApprove_ModCannotApproveOwn(t *testing.T) {
	s, q, admin := adminSetup(t, nil)
	seedHit(t, q, 1)
	seedHit(t, q, 2)
	mod := login(t, s, 50, "modo")
	makeMod(t, q, mod)
	own := suggest(t, s, mod.Token, 1, "Mine", "A")
	code, body := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/approve", own), testToken, mod.Token, nil)
	if code != 403 || !strings.Contains(string(body), "Eigener Vorschlag") {
		t.Fatalf("mod own = %d %s", code, body)
	}
	if st := status(t, q, own); st != "open" {
		t.Errorf("status %s", st)
	}
	// admin may approve their own
	adm := suggest(t, s, admin.Token, 2, "Adm", "A")
	if code, body := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/approve", adm), testToken, admin.Token, nil); code != 200 {
		t.Errorf("admin own = %d %s", code, body)
	}
	// a mod may approve someone else's
	other := login(t, s, 7, "other")
	o := suggest(t, s, other.Token, 1, "Theirs", "A")
	if code, body := do(t, s, "POST", fmt.Sprintf("/api/internal/suggestions/%d/approve", o), testToken, mod.Token, nil); code != 200 {
		t.Errorf("mod other = %d %s", code, body)
	}
}
