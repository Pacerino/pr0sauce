package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/Pacerino/pr0sauce/internal/recognize"
	"github.com/Pacerino/pr0sauce/internal/testdb"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeServer is a fake pr0gramm API. The session is valid when the "me"
// cookie equals goodCookie (or acceptAll is set).
type fakeServer struct {
	*httptest.Server
	mu          sync.Mutex
	goodCookie  string
	acceptAll   bool
	cloudflare  bool // /user/identifier answers an HTML 403
	loginOK     bool
	lastForm    map[string]string
	loginCookie string // cookie value login sets; empty means goodCookie
	rotateTo    string // when set, the next inbox response rotates the session cookie to this
	captcha     string // captcha image; empty means a data URI

	logins, inbox, idents, items atomic.Int32
}

func newFakeServer(t *testing.T) *fakeServer {
	t.Helper()
	f := &fakeServer{goodCookie: "good"}
	mux := http.NewServeMux()
	authed := func(r *http.Request) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.acceptAll {
			return true
		}
		c, err := r.Cookie("me")
		return err == nil && c.Value == f.goodCookie
	}
	denied := func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"error":"forbidden","code":403}`)
	}
	mux.HandleFunc("/user/identifier", func(w http.ResponseWriter, r *http.Request) {
		f.idents.Add(1)
		f.mu.Lock()
		cf := f.cloudflare
		f.mu.Unlock()
		switch {
		case cf:
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "<html>Just a moment...</html>")
		case authed(r):
			io.WriteString(w, `{"identifier":"x"}`)
		default:
			denied(w)
		}
	})
	mux.HandleFunc("/user/login", func(w http.ResponseWriter, r *http.Request) {
		f.logins.Add(1)
		r.ParseForm()
		f.mu.Lock()
		f.lastForm = map[string]string{}
		for k := range r.PostForm {
			f.lastForm[k] = r.PostForm.Get(k)
		}
		ok, good := f.loginOK, f.goodCookie
		if f.loginCookie != "" {
			good = f.loginCookie
		}
		f.mu.Unlock()
		if !ok {
			io.WriteString(w, `{"success":false,"error":"invalid"}`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "me", Value: good, Path: "/"})
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/user/captcha", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		img := f.captcha
		f.mu.Unlock()
		if img == "" {
			img = "data:image/png;base64,AAAA"
		}
		json.NewEncoder(w).Encode(map[string]string{"token": "tok1", "captcha": img})
	})
	mux.HandleFunc("/inbox/comments", func(w http.ResponseWriter, r *http.Request) {
		f.inbox.Add(1)
		if !authed(r) {
			denied(w)
			return
		}
		f.mu.Lock()
		if f.rotateTo != "" {
			f.goodCookie = f.rotateTo
			http.SetCookie(w, &http.Cookie{Name: "me", Value: f.rotateTo, Path: "/"})
			f.rotateTo = ""
		}
		f.mu.Unlock()
		io.WriteString(w, `{"messages":[]}`)
	})
	mux.HandleFunc("/items/get", func(w http.ResponseWriter, r *http.Request) {
		f.items.Add(1)
		if !authed(r) {
			denied(w)
			return
		}
		io.WriteString(w, `{"items":[]}`)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

type stubRec struct{}

func (stubRec) Recognize(context.Context, string) (recognize.Result, error) {
	return recognize.Result{}, errors.New("unused")
}

type stubLinker struct{}

func (stubLinker) DeezerURL(context.Context, string) (string, error) {
	return "", errors.New("unused")
}

type harness struct {
	r      *runner
	srv    *fakeServer
	store  Store
	health string // base URL of the health server
	done   chan error
	cancel context.CancelFunc
}

// newHarness must be called once per top-level test (testdb.New is not reentrant).
func newHarness(t *testing.T, mod func(*runner)) *harness {
	t.Helper()
	pool := testdb.New(t)
	srv := newFakeServer(t)
	store := NewStore(db.New(pool))
	client := pr0gramm.New(nil, srv.URL)
	cfg := config.Config{MaxAttempts: 5, PublicBaseURL: "https://example.invalid", Pr0User: "bot", Pr0Password: "s3cret"}
	b, err := New(Deps{Store: store, Pr0: client, Recognizer: stubRec{}, Linker: stubLinker{}}, cfg, discard)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	clock := systemClock{}
	r := &runner{
		cfg: cfg, log: discard, bot: b, store: store, sess: client, clock: clock, ln: ln,
		health: &health{pinger: pool, clock: clock},
		poll:   10 * time.Millisecond, sync: time.Hour, recheck: time.Hour,
		boMin: time.Hour, boMax: time.Hour,
	}
	if mod != nil {
		mod(r)
	}
	return &harness{r: r, srv: srv, store: store, health: "http://" + ln.Addr().String()}
}

func (h *harness) start(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan error, 1)
	go func() { h.done <- h.r.run(ctx); close(h.done) }()
	t.Cleanup(h.stop)
}

func (h *harness) stop() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
	}
}

func (h *harness) healthz(t *testing.T) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(h.health + "/healthz")
	if err != nil {
		return 0, nil
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	return resp.StatusCode, m
}

// waitHealth polls /healthz until it returns code with the given session.
func (h *harness) waitHealth(t *testing.T, code int, session string) map[string]any {
	t.Helper()
	var last map[string]any
	var lastCode int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		lastCode, last = h.healthz(t)
		if lastCode == code && last["session"] == session {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("health never reached %d/%s; last %d %v", code, session, lastCode, last)
	return nil
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func cookiesJSON(value string) string {
	b, _ := json.Marshal([]http.Cookie{{Name: "me", Value: value}})
	return string(b)
}

func TestRun_DBDownReturnsError(t *testing.T) {
	cfg := config.Config{DatabaseURL: "postgres://u:p@127.0.0.1:1/x?sslmode=disable&connect_timeout=2", MaxAttempts: 5}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := Run(ctx, cfg, discard); err == nil {
		t.Fatal("Run with unreachable database returned nil")
	}
}

func TestRun_InvalidSessionStaysUpUnhealthy(t *testing.T) {
	h := newHarness(t, nil)
	h.start(t)

	body := h.waitHealth(t, http.StatusServiceUnavailable, "invalid")
	if body["db"] != "ok" || body["last_poll"] != nil {
		t.Errorf("body = %v", body)
	}
	time.Sleep(60 * time.Millisecond) // about 6 poll ticks
	if n := h.srv.logins.Load(); n != 1 {
		t.Errorf("login attempts = %d, want 1", n)
	}
	if n := h.srv.inbox.Load(); n != 0 {
		t.Errorf("inbox calls = %d, want 0 while session invalid", n)
	}
}

func TestRun_HealthyAfterPollAndGracefulShutdown(t *testing.T) {
	h := newHarness(t, nil)
	h.srv.set(func() { h.srv.loginOK = true })
	h.start(t)

	body := h.waitHealth(t, http.StatusOK, "ok")
	if body["db"] != "ok" {
		t.Errorf("db = %v", body["db"])
	}
	lp, _ := body["last_poll"].(string)
	if _, err := time.Parse(time.RFC3339, lp); err != nil {
		t.Errorf("last_poll %q not RFC3339: %v", lp, err)
	}
	if n := h.srv.logins.Load(); n != 1 {
		t.Errorf("logins = %d, want 1", n)
	}
	v, err := h.store.GetState(context.Background(), sessionCookiesKey)
	if err != nil || !strings.Contains(v, "good") {
		t.Errorf("persisted cookies = %q, %v", v, err)
	}

	h.cancel()
	select {
	case err := <-h.done:
		if err != nil {
			t.Errorf("run returned %v on shutdown, want nil", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("run did not return after cancel")
	}
	if c, _ := h.healthz(t); c != 0 {
		t.Errorf("health still answering after shutdown: %d", c)
	}
}

func TestRun_CloudflareOnStartupNoLogin(t *testing.T) {
	h := newHarness(t, func(r *runner) { r.boMin, r.boMax = 20*time.Millisecond, 20*time.Millisecond })
	h.srv.set(func() { h.srv.cloudflare, h.srv.loginOK = true, true })
	h.start(t)

	h.waitHealth(t, http.StatusServiceUnavailable, "unknown")
	waitFor(t, "a retried check", func() bool { return h.srv.idents.Load() >= 3 })
	if n := h.srv.logins.Load(); n != 0 {
		t.Fatalf("logins = %d, want 0 while Cloudflare blocks the check", n)
	}

	// Cloudflare goes away and the session turns out fine: recovers, still no login.
	h.srv.set(func() { h.srv.cloudflare, h.srv.acceptAll = false, true })
	h.waitHealth(t, http.StatusOK, "ok")
	if n := h.srv.logins.Load(); n != 0 {
		t.Errorf("logins = %d, want 0", n)
	}
}

func TestRun_AuthErrorMidLoopRechecksNoLogin(t *testing.T) {
	h := newHarness(t, nil)
	h.srv.set(func() { h.srv.loginOK = true })
	if err := h.store.SetState(context.Background(), db.SetStateParams{Key: sessionCookiesKey, Value: cookiesJSON("good")}); err != nil {
		t.Fatal(err)
	}
	h.start(t)
	h.waitHealth(t, http.StatusOK, "ok")

	// The session dies: inbox answers 403 JSON, identifier says logged out.
	h.srv.set(func() { h.srv.goodCookie = "rotated" })
	h.waitHealth(t, http.StatusServiceUnavailable, "invalid")

	before := h.srv.inbox.Load()
	time.Sleep(60 * time.Millisecond)
	if after := h.srv.inbox.Load(); after != before {
		t.Errorf("inbox polled while invalid: %d -> %d", before, after)
	}
	if n := h.srv.logins.Load(); n != 0 {
		t.Errorf("logins = %d, want 0 (never log in after startup)", n)
	}
}

func TestRun_AuthErrorWithCloudflareCheckKeepsPolling(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.store.SetState(context.Background(), db.SetStateParams{Key: sessionCookiesKey, Value: cookiesJSON("good")}); err != nil {
		t.Fatal(err)
	}
	h.start(t)
	h.waitHealth(t, http.StatusOK, "ok")

	// Inbox starts failing with a 403, but the check itself is blocked: not conclusive.
	h.srv.set(func() { h.srv.goodCookie, h.srv.cloudflare = "rotated", true })
	before := h.srv.inbox.Load()
	waitFor(t, "more inbox polls", func() bool { return h.srv.inbox.Load() >= before+3 })
	if s := h.r.health.getSession(); s != sessionOK {
		t.Errorf("session = %v, want ok (inconclusive check must not flip it)", s)
	}
	if n := h.srv.logins.Load(); n != 0 {
		t.Errorf("logins = %d, want 0", n)
	}
}

func TestRun_PicksUpNewCookiesAfterBotLogin(t *testing.T) {
	h := newHarness(t, func(r *runner) { r.recheck = 20 * time.Millisecond })
	h.start(t) // no cookies, login fails
	h.waitHealth(t, http.StatusServiceUnavailable, "invalid")
	if n := h.srv.logins.Load(); n != 1 {
		t.Fatalf("logins = %d, want 1", n)
	}

	// bot-login in another process writes fresh cookies to the DB.
	if err := h.store.SetState(context.Background(), db.SetStateParams{Key: sessionCookiesKey, Value: cookiesJSON("good")}); err != nil {
		t.Fatal(err)
	}
	h.waitHealth(t, http.StatusOK, "ok")
	if n := h.srv.logins.Load(); n != 1 {
		t.Errorf("logins = %d, want still 1", n)
	}
}

func TestLoginInteractive_SavesCookies(t *testing.T) {
	pool := testdb.New(t)
	srv := newFakeServer(t)
	srv.set(func() { srv.loginOK = true })
	store := NewStore(db.New(pool))
	cfg := config.Config{Pr0User: "bot", Pr0Password: "s3cret"}

	t.Setenv("TMPDIR", t.TempDir())
	var out bytes.Buffer
	err := loginInteractive(context.Background(), cfg, pr0gramm.New(nil, srv.URL), store, strings.NewReader("xK7p\n"), &out, "")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^data:image/png;base64,\S+$`).MatchString(out.String()) {
		t.Errorf("output lacks the data URI on its own line: %q", out.String())
	}
	if !strings.Contains(out.String(), "Captcha (in die Browser-Adresszeile kopieren / paste into browser address bar):") {
		t.Errorf("output lacks the paste hint: %q", out.String())
	}
	path := captchaPath(t, out.String())
	if !strings.HasSuffix(path, ".png") || !strings.Contains(path, "pr0sauce-captcha-") {
		t.Errorf("captcha file path = %q", path)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, []byte{0, 0, 0}) {
		t.Errorf("captcha file = %v, %v", b, err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out.String()), "ok") {
		t.Errorf("output does not end with ok: %q", out.String())
	}
	if strings.Contains(out.String(), "s3cret") {
		t.Error("output contains the password")
	}
	srv.mu.Lock()
	form := srv.lastForm
	srv.mu.Unlock()
	if form["token"] != "tok1" || form["captcha"] != "xK7p" || form["name"] != "bot" || form["password"] != "s3cret" {
		t.Errorf("login form = %v", form)
	}
	v, err := store.GetState(context.Background(), sessionCookiesKey)
	if err != nil || !strings.Contains(v, `"good"`) {
		t.Errorf("saved cookies = %q, %v", v, err)
	}
}

func TestLoginHint_TargetsV2Stack(t *testing.T) {
	want := "pr0gramm session invalid — run: docker compose -p pr0sauce2 -f /opt/pr0sauce/v2/compose.yml run --rm -it bot bot-login --captcha-out /tmp"
	if loginHint != want {
		t.Errorf("loginHint = %q", loginHint)
	}
}

func TestLoginInteractive_CaptchaOutDir(t *testing.T) {
	pool := testdb.New(t)
	srv := newFakeServer(t)
	srv.set(func() { srv.loginOK = true })
	dir := t.TempDir()
	var out bytes.Buffer
	err := loginInteractive(context.Background(), config.Config{Pr0User: "bot", Pr0Password: "s3cret"},
		pr0gramm.New(nil, srv.URL), NewStore(db.New(pool)), strings.NewReader("xK7p\n"), &out, dir)
	if err != nil {
		t.Fatal(err)
	}
	path := captchaPath(t, out.String())
	if filepath.Dir(path) != dir {
		t.Errorf("captcha file %q not in %q", path, dir)
	}
	if b, err := os.ReadFile(path); err != nil || !bytes.Equal(b, []byte{0, 0, 0}) {
		t.Errorf("captcha file = %v, %v", b, err)
	}
	if !strings.Contains(out.String(), "data:image/png;base64,") {
		t.Errorf("data URI missing with --captcha-out: %q", out.String())
	}
}

type stubPinger struct{ err error }

func (p stubPinger) Ping(context.Context) error { return p.err }

type stepClock struct{ t time.Time }

func (c *stepClock) Now() time.Time { return c.t }

func TestHealth_Rules(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		session  sessionState
		lastPoll time.Time
		pingErr  error
		code     int
		db       string
	}{
		{"fresh", sessionOK, now.Add(-time.Minute), nil, 200, "ok"},
		{"stale", sessionOK, now.Add(-3 * time.Minute), nil, 503, "ok"},
		{"never polled", sessionOK, time.Time{}, nil, 503, "ok"},
		{"db down", sessionOK, now, errors.New("down"), 503, "error"},
		{"invalid", sessionInvalid, now, nil, 503, "ok"},
		{"unknown", sessionUnknown, now, nil, 503, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &health{pinger: stubPinger{c.pingErr}, clock: &stepClock{now}, session: c.session, lastPoll: c.lastPoll}
			rec := httptest.NewRecorder()
			h.handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
			if rec.Code != c.code {
				t.Errorf("code = %d, want %d", rec.Code, c.code)
			}
			var b map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
				t.Fatal(err)
			}
			if b["db"] != c.db || b["session"] != c.session.String() {
				t.Errorf("body = %v", b)
			}
			if c.lastPoll.IsZero() != (b["last_poll"] == nil) {
				t.Errorf("last_poll = %v", b["last_poll"])
			}
		})
	}
}

func captchaPath(t *testing.T, out string) string {
	t.Helper()
	m := regexp.MustCompile(`(?m)^Captcha image.*?: (\S+)$`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no captcha path in output: %q", out)
	}
	return m[1]
}

func TestLoginInteractive_PrintsPlainURLAsIs(t *testing.T) {
	pool := testdb.New(t)
	srv := newFakeServer(t)
	srv.set(func() { srv.loginOK, srv.captcha = true, "https://example.invalid/captcha/1.png" })
	var out bytes.Buffer
	err := loginInteractive(context.Background(), config.Config{Pr0User: "bot", Pr0Password: "s3cret"},
		pr0gramm.New(nil, srv.URL), NewStore(db.New(pool)), strings.NewReader("abc\n"), &out, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "https://example.invalid/captcha/1.png") {
		t.Errorf("output lacks the URL: %q", out.String())
	}
}

func TestRun_CloudflareThenLoggedOutUsesOneLogin(t *testing.T) {
	h := newHarness(t, func(r *runner) {
		r.boMin, r.boMax, r.recheck = 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond
	})
	// login "succeeds" but yields a cookie the API rejects, so every check says logged out
	h.srv.set(func() { h.srv.cloudflare, h.srv.loginOK, h.srv.loginCookie = true, true, "bad" })
	h.start(t)
	h.waitHealth(t, http.StatusServiceUnavailable, "unknown")
	if n := h.srv.logins.Load(); n != 0 {
		t.Fatalf("logins = %d during Cloudflare block", n)
	}

	h.srv.set(func() { h.srv.cloudflare = false }) // identifier now answers JSON 403
	h.waitHealth(t, http.StatusServiceUnavailable, "invalid")
	if n := h.srv.logins.Load(); n != 1 {
		t.Fatalf("logins = %d, want exactly 1", n)
	}
	before := h.srv.idents.Load()
	waitFor(t, "further re-checks", func() bool { return h.srv.idents.Load() >= before+3 })
	if n := h.srv.logins.Load(); n != 1 {
		t.Errorf("logins = %d after further re-checks, want 1", n)
	}
}

type countingStore struct {
	Store
	cookieSets atomic.Int32
}

func (s *countingStore) SetState(ctx context.Context, a db.SetStateParams) error {
	if a.Key == sessionCookiesKey {
		s.cookieSets.Add(1)
	}
	return s.Store.SetState(ctx, a)
}

func TestRun_PersistsRotatedCookies(t *testing.T) {
	var cs *countingStore
	h := newHarness(t, func(r *runner) {
		cs = &countingStore{Store: r.store}
		r.store = cs
		r.persistEvery = 40 * time.Millisecond
	})
	if err := h.store.SetState(context.Background(), db.SetStateParams{Key: sessionCookiesKey, Value: cookiesJSON("good")}); err != nil {
		t.Fatal(err)
	}
	h.start(t)
	h.waitHealth(t, http.StatusOK, "ok")
	time.Sleep(150 * time.Millisecond) // several intervals with unchanged cookies
	if n := cs.cookieSets.Load(); n != 0 {
		t.Fatalf("cookies rewritten %d times although unchanged", n)
	}

	h.srv.set(func() { h.srv.rotateTo = "rotated" })
	waitFor(t, "rotated cookie persisted", func() bool {
		v, err := h.store.GetState(context.Background(), sessionCookiesKey)
		return err == nil && strings.Contains(v, "rotated")
	})
	n := cs.cookieSets.Load()
	time.Sleep(150 * time.Millisecond)
	if m := cs.cookieSets.Load(); m != n || n != 1 {
		t.Errorf("cookie writes = %d then %d, want exactly 1", n, m)
	}
}

func TestRun_OneSessionCheckPerTick(t *testing.T) {
	h := newHarness(t, nil)
	// A cookie that carries a nonce, so ProcessDue gets past its HasSession check.
	const me = "%7B%22id%22%3A%220123456789abcdef0%22%7D"
	h.srv.set(func() { h.srv.goodCookie = me })
	if err := h.store.SetState(context.Background(), db.SetStateParams{Key: sessionCookiesKey, Value: cookiesJSON(me)}); err != nil {
		t.Fatal(err)
	}
	h.start(t)
	h.waitHealth(t, http.StatusOK, "ok")

	// Session dies behind a Cloudflare block, and a due request whose item
	// lookup also fails with 403: two ErrAuth steps per tick.
	h.srv.set(func() { h.srv.goodCookie, h.srv.cloudflare = "rotated", true })
	if err := insertDue(h.store); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "items lookups", func() bool { return h.srv.items.Load() >= 5 })
	idents, inbox := h.srv.idents.Load(), h.srv.inbox.Load()
	if idents > inbox+1 { // +1: the startup check
		t.Errorf("identifier calls = %d for %d ticks, want at most one per tick", idents, inbox)
	}
}

func insertDue(s Store) error {
	_, err := s.InsertRequestIgnore(context.Background(), db.InsertRequestIgnoreParams{
		MessageID: 1, ItemID: 42, Requester: "someone", Thumb: "t", MentionedAt: time.Now(),
		Status: statusPending, NextAttemptAt: time.Now().Add(-time.Hour),
	})
	return err
}

func TestRun_CloudflareThenLoggedOutDeferredLoginSucceeds(t *testing.T) {
	h := newHarness(t, func(r *runner) { r.boMin, r.boMax = 20*time.Millisecond, 20*time.Millisecond })
	h.srv.set(func() { h.srv.cloudflare, h.srv.loginOK = true, true }) // login sets a good cookie
	h.start(t)
	h.waitHealth(t, http.StatusServiceUnavailable, "unknown")

	h.srv.set(func() { h.srv.cloudflare = false }) // identifier: JSON 403 without cookie, 200 with it
	h.waitHealth(t, http.StatusOK, "ok")           // recheck interval is 1h, so no waiting for it
	if n := h.srv.logins.Load(); n != 1 {
		t.Errorf("logins = %d, want exactly 1", n)
	}
	waitFor(t, "polling to resume", func() bool { return h.srv.inbox.Load() > 0 })
	v, err := h.store.GetState(context.Background(), sessionCookiesKey)
	if err != nil || !strings.Contains(v, "good") {
		t.Errorf("persisted cookies = %q, %v", v, err)
	}
}
