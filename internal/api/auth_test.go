package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/testdb"
)

const testToken = "internal-secret-for-tests"

func authSetup(t *testing.T) (*server, *db.Queries) {
	t.Helper()
	pool := testdb.New(t)
	q := db.New(pool)
	s := newServer(q, pool, slog.New(slog.NewTextHandler(io.Discard, nil)),
		Options{InternalToken: testToken, AdminName: "Helix"})
	// Test-only routes to exercise the role guards.
	ok := func(w http.ResponseWriter, r *http.Request, u db.User) {
		writeJSON(w, 200, map[string]string{"ok": u.Name})
	}
	s.mux.Handle("GET /api/internal/_t/user", s.internal(s.requireUser(ok)))
	s.mux.Handle("POST /api/internal/_t/user", s.internal(s.requireUser(ok)))
	s.mux.Handle("GET /api/internal/_t/mod", s.internal(s.requireMod(ok)))
	s.mux.Handle("GET /api/internal/_t/admin", s.internal(s.requireAdmin(ok)))
	return s, q
}

func do(t *testing.T, h http.Handler, method, path, internal, session string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if internal != "" {
		req.Header.Set("X-Internal-Token", internal)
	}
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

type loginResp struct {
	Token string `json:"token"`
	User  struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"user"`
}

func login(t *testing.T, s *server, id int64, name string) loginResp {
	t.Helper()
	code, body := do(t, s, "POST", "/api/internal/sessions", testToken, "", map[string]any{"pr0UserId": id, "name": name})
	if code != 200 {
		t.Fatalf("login %s = %d: %s", name, code, body)
	}
	var r loginResp
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAuthz_Matrix(t *testing.T) {
	s, q := authSetup(t)
	admin := login(t, s, 1, "Helix")
	mod := login(t, s, 2, "modguy")
	user := login(t, s, 3, "plain")
	banned := login(t, s, 4, "badguy")
	if err := q.SetRole(context.Background(), db.SetRoleParams{ID: mod.User.ID, Role: "moderator"}); err != nil {
		t.Fatal(err)
	}
	if err := q.SetBan(context.Background(), db.SetBanParams{ID: banned.User.ID, BanReason: str("x")}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name           string
		method, path   string
		internal, sess string
		want           int
	}{
		{"no internal token", "GET", "/api/internal/me", "", user.Token, 401},
		{"wrong internal token", "GET", "/api/internal/me", "nope", user.Token, 401},
		{"prefix of token", "GET", "/api/internal/me", testToken[:5], user.Token, 401},
		{"token no session on me", "GET", "/api/internal/me", testToken, "", 401},
		{"token bogus session on me", "GET", "/api/internal/me", testToken, "bogus", 401},
		{"user on me", "GET", "/api/internal/me", testToken, user.Token, 200},
		{"user on mod route", "GET", "/api/internal/_t/mod", testToken, user.Token, 403},
		{"mod on mod route", "GET", "/api/internal/_t/mod", testToken, mod.Token, 200},
		{"mod on admin route", "GET", "/api/internal/_t/admin", testToken, mod.Token, 403},
		{"admin on admin route", "GET", "/api/internal/_t/admin", testToken, admin.Token, 200},
		{"admin on mod route", "GET", "/api/internal/_t/mod", testToken, admin.Token, 200},
		{"no session on mod route", "GET", "/api/internal/_t/mod", testToken, "", 401},
		{"banned reads", "GET", "/api/internal/_t/user", testToken, banned.Token, 200},
		{"banned writes", "POST", "/api/internal/_t/user", testToken, banned.Token, 403},
		{"unbanned writes", "POST", "/api/internal/_t/user", testToken, user.Token, 200},
		{"public songs untouched", "GET", "/api/songs", "", "", 200},
		{"login needs internal token", "POST", "/api/internal/sessions", "", "", 401},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			code, body := do(t, s, c.method, c.path, c.internal, c.sess, nil)
			if code != c.want {
				t.Fatalf("%s %s = %d, want %d: %s", c.method, c.path, code, c.want, body)
			}
		})
	}
	_, body := do(t, s, "POST", "/api/internal/_t/user", testToken, banned.Token, nil)
	if !strings.Contains(string(body), `"Gesperrt"`) {
		t.Errorf("banned body = %s", body)
	}
	// 401 bodies are identical whichever credential is wrong.
	_, b1 := do(t, s, "GET", "/api/internal/me", "nope", user.Token, nil)
	_, b2 := do(t, s, "GET", "/api/internal/me", testToken, "bogus", nil)
	if !bytes.Equal(b1, b2) {
		t.Errorf("401 bodies differ: %s vs %s", b1, b2)
	}
}

func TestAuthz_EmptyConfiguredTokenRejectsAll(t *testing.T) {
	pool := testdb.New(t)
	s := newServer(db.New(pool), pool, slog.New(slog.NewTextHandler(io.Discard, nil)), Options{})
	if code, _ := do(t, s, "POST", "/api/internal/sessions", "", "", map[string]any{"pr0UserId": 1, "name": "a"}); code != 401 {
		t.Errorf("code = %d", code)
	}
}

func TestSessions_CreateMeDelete(t *testing.T) {
	s, _ := authSetup(t)
	l := login(t, s, 7, "alice")
	raw, err := base64.RawURLEncoding.DecodeString(l.Token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token not 32 bytes base64url: %v len=%d", err, len(raw))
	}
	if l.User.Role != "user" || l.User.Name != "alice" {
		t.Errorf("user = %+v", l.User)
	}
	code, body := do(t, s, "GET", "/api/internal/me", testToken, l.Token, nil)
	var me struct {
		ID     int64
		Name   string
		Role   string
		Banned bool
	}
	if code != 200 || json.Unmarshal(body, &me) != nil || me.Name != "alice" || me.Role != "user" || me.Banned || me.ID != l.User.ID {
		t.Fatalf("me = %d %s", code, body)
	}
	if code, _ := do(t, s, "DELETE", "/api/internal/sessions", testToken, l.Token, nil); code != 204 {
		t.Errorf("delete = %d", code)
	}
	if code, _ := do(t, s, "GET", "/api/internal/me", testToken, l.Token, nil); code != 401 {
		t.Errorf("me after delete = %d", code)
	}
	for _, bad := range []map[string]any{{"pr0UserId": 0, "name": "x"}, {"pr0UserId": 1, "name": ""}, {"pr0UserId": 1, "name": strings.Repeat("a", 65)}} {
		if code, _ := do(t, s, "POST", "/api/internal/sessions", testToken, "", bad); code != 400 {
			t.Errorf("bad %v = %d", bad, code)
		}
	}
}

func TestSessions_SlidingExpiry(t *testing.T) {
	s, q := authSetup(t)
	l := login(t, s, 8, "bob")
	hash := hashToken(l.Token)
	expiry := func() time.Time {
		var e time.Time
		if err := s.pool.QueryRow(context.Background(), `select expires_at from sessions where token_hash=$1`, hash).Scan(&e); err != nil {
			t.Fatal(err)
		}
		return e
	}
	_ = q
	set := func(d time.Duration) {
		if _, err := s.pool.Exec(context.Background(), `update sessions set expires_at=$2 where token_hash=$1`, hash, time.Now().Add(d)); err != nil {
			t.Fatal(err)
		}
	}
	set(20 * 24 * time.Hour)
	before := expiry()
	do(t, s, "GET", "/api/internal/me", testToken, l.Token, nil)
	if !expiry().Equal(before) {
		t.Error("session with 20d left was touched")
	}
	set(10 * 24 * time.Hour)
	do(t, s, "GET", "/api/internal/me", testToken, l.Token, nil)
	if d := time.Until(expiry()); d < 29*24*time.Hour {
		t.Errorf("session with 10d left not extended: %v", d)
	}
}

func TestSessions_ExpiredRejected(t *testing.T) {
	s, _ := authSetup(t)
	l := login(t, s, 9, "carl")
	if _, err := s.pool.Exec(context.Background(), `update sessions set expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if code, _ := do(t, s, "GET", "/api/internal/me", testToken, l.Token, nil); code != 401 {
		t.Errorf("expired session = %d", code)
	}
}

func TestAdminBootstrap_OnlyFirstHelix(t *testing.T) {
	s, q := authSetup(t)
	if r := login(t, s, 10, "someone"); r.User.Role != "user" {
		t.Errorf("other name role = %s", r.User.Role)
	}
	first := login(t, s, 11, "helix") // case-insensitive
	if first.User.Role != "admin" {
		t.Fatalf("first Helix role = %s", first.User.Role)
	}
	second := login(t, s, 12, "Helix") // different pr0 account, same name
	if second.User.Role != "user" {
		t.Errorf("second Helix role = %s", second.User.Role)
	}
	if again := login(t, s, 11, "Helix"); again.User.Role != "admin" {
		t.Errorf("returning admin role = %s", again.User.Role)
	}
	if n, _ := q.CountAdmins(context.Background()); n != 1 {
		t.Errorf("admins = %d", n)
	}
}

func TestAdminBootstrap_ConcurrentFirstLogins(t *testing.T) {
	s, q := authSetup(t)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			do(t, s, "POST", "/api/internal/sessions", testToken, "", map[string]any{"pr0UserId": 100 + i, "name": "Helix"})
		}(i)
	}
	wg.Wait()
	if n, _ := q.CountAdmins(context.Background()); n != 1 {
		t.Errorf("admins = %d, want 1", n)
	}
}

func TestSession_TokenNotStoredPlain(t *testing.T) {
	s, _ := authSetup(t)
	l := login(t, s, 20, "dora")
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`select count(*) from sessions where token_hash = convert_to($1,'UTF8') or encode(token_hash,'base64') like '%'||$1||'%'`, l.Token).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("plain token found in sessions")
	}
	var stored []byte
	if err := s.pool.QueryRow(context.Background(), `select token_hash from sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, hashToken(l.Token)) || len(stored) != 32 {
		t.Error("stored value is not sha256(token)")
	}
}
