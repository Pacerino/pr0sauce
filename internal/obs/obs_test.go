package obs

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

type recTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *recTransport) Flush(time.Duration) bool              { return true }
func (t *recTransport) FlushWithContext(context.Context) bool { return true }
func (t *recTransport) Configure(sentry.ClientOptions)        {}
func (t *recTransport) Close()                                {}
func (t *recTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	t.events = append(t.events, e)
	t.mu.Unlock()
}

func TestInit_EmptyDSNDisabled(t *testing.T) {
	on, err := Init("", "production", "")
	if on || err != nil {
		t.Fatalf("Init(\"\") = %v, %v", on, err)
	}
}

func TestBeforeSend_Scrubs(t *testing.T) {
	ev := &sentry.Event{
		Request: &sentry.Request{Cookies: "me=abc", Headers: map[string]string{"Authorization": "x"}},
		Tags:    map[string]string{"Session_Token": "t", "step": "ingest"},
		Contexts: map[string]sentry.Context{
			"log_attributes": {"Cookie": "c", "nonce": "n", "PASSWORD": "p", "client_secret": "s", "authorization": "a", "step": "ingest"},
		},
	}
	out := BeforeSend(ev, nil)
	if out.Request.Cookies != "" || out.Request.Headers != nil {
		t.Errorf("request not scrubbed: %+v", out.Request)
	}
	if _, ok := out.Tags["Session_Token"]; ok || out.Tags["step"] != "ingest" {
		t.Errorf("tags = %v", out.Tags)
	}
	attrs := out.Contexts["log_attributes"]
	if len(attrs) != 1 || attrs["step"] != "ingest" {
		t.Errorf("context = %v", attrs)
	}
}

func TestEventHandler_ErrorOnlyMessageFingerprint(t *testing.T) {
	tr := &recTransport{}
	c, err := sentry.NewClient(sentry.ClientOptions{Dsn: "https://public@example.invalid/1", Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(NewEventHandler(sentry.NewHub(c, sentry.NewScope())))

	log.Info("ignored")
	log.Warn("ignored too")
	log.With("request_id", "r1").Error("step failed", "step", "ingest", "err", errors.New("boom"))
	log.WithGroup("g").Error("step failed", "request_id", "r2")

	if len(tr.events) != 2 {
		t.Fatalf("events = %d, want 2", len(tr.events))
	}
	e := tr.events[0]
	if e.Message != "step failed" || e.Level != sentry.LevelError {
		t.Errorf("event = %+v", e)
	}
	if len(e.Fingerprint) != 2 || e.Fingerprint[0] != "step failed" || e.Fingerprint[1] != "ingest" {
		t.Errorf("fingerprint = %v", e.Fingerprint)
	}
	attrs := e.Contexts["log_attributes"]
	if attrs["step"] != "ingest" || attrs["err"] != "boom" || attrs["request_id"] != "r1" {
		t.Errorf("attrs = %v", attrs)
	}
	if e.Tags["step"] != "ingest" {
		t.Errorf("tags = %v", e.Tags)
	}
	if tr.events[1].Contexts["log_attributes"]["g.request_id"] != "r2" {
		t.Errorf("group attrs = %v", tr.events[1].Contexts["log_attributes"])
	}
}

func throttleHub(t *testing.T) (*sentry.Hub, *recTransport) {
	t.Helper()
	tr := &recTransport{}
	c, err := sentry.NewClient(sentry.ClientOptions{Dsn: "https://public@example.invalid/1", Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	return sentry.NewHub(c, sentry.NewScope()), tr
}

func TestEventHandler_Throttle(t *testing.T) {
	hub, tr := throttleHub(t)
	now := time.Unix(1_000_000, 0)
	log := slog.New(NewEventHandlerClock(hub, func() time.Time { return now }))

	for i := 0; i < 100; i++ { // 100 identical records within 1 minute
		log.Error("step failed", "step", "ingest")
		now = now.Add(600 * time.Millisecond)
	}
	if len(tr.events) != 1 {
		t.Fatalf("events after 100 records = %d, want 1", len(tr.events))
	}
	if _, ok := tr.events[0].Contexts["log_attributes"]["suppressed_since_last"]; ok {
		t.Errorf("first event must not carry suppressed_since_last")
	}
	// A different fingerprint is not throttled together with it.
	log.Error("step failed", "step", "process")
	log.Error("other failure")
	if len(tr.events) != 3 {
		t.Fatalf("events with distinct fingerprints = %d, want 3", len(tr.events))
	}
	now = now.Add(10 * time.Minute)
	log.Error("step failed", "step", "ingest")
	if len(tr.events) != 4 {
		t.Fatalf("events after window = %d, want 4", len(tr.events))
	}
	if got := tr.events[3].Contexts["log_attributes"]["suppressed_since_last"]; got != 99 {
		t.Errorf("suppressed_since_last = %v, want 99", got)
	}
}

func TestEventHandler_FingerprintAndTagAllowList(t *testing.T) {
	hub, tr := throttleHub(t)
	log := slog.New(NewEventHandler(hub))
	log.Error("step failed", "step", "ingest", "request_id", "r1", "attempt", 3)
	log.Error("something else", "step", "ingest")
	a, b := tr.events[0], tr.events[1]
	if len(a.Fingerprint) != 2 || a.Fingerprint[0] != "step failed" || a.Fingerprint[1] != "ingest" {
		t.Errorf("step failed fingerprint = %v", a.Fingerprint)
	}
	if len(b.Fingerprint) != 1 || b.Fingerprint[0] != "something else" {
		t.Errorf("other fingerprint = %v", b.Fingerprint)
	}
	if a.Tags["step"] != "ingest" || a.Tags["attempt"] != "3" {
		t.Errorf("tags = %v", a.Tags)
	}
	if _, ok := a.Tags["request_id"]; ok {
		t.Errorf("request_id must not be a tag: %v", a.Tags)
	}
	if a.Contexts["log_attributes"]["request_id"] != "r1" {
		t.Errorf("request_id missing from context")
	}
}

func TestScrubString_Patterns(t *testing.T) {
	hex := "0123456789abcdef0123456789abcdef"
	tests := []struct{ name, in, want string }{
		{"cookie me", "got me=abc123; path=/", "got me=<redacted>; path=/"},
		{"cookie pp", "pp=zzz", "pp=<redacted>"},
		{"cookie session", "session_id=abc def", "session_id=<redacted> def"},
		{"no false positive name=", "name=bob", "name=bob"},
		{"bearer", "Authorization: Bearer abc.def-ghi next", "Authorization: Bearer <redacted> next"},
		{"nonce", "url?x=1&_nonce=deadbeef&y=2", "url?x=1&_nonce=<redacted>&y=2"},
		{"hex", "id " + hex + " end", "id <redacted> end"},
		{"base64url", "tok AbCdEf_-AbCdEf_-AbCdEf_-AbCdEf_-AbCdEf end", "tok <redacted> end"},
		{"short kept", "abcdef0123", "abcdef0123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := scrubString(tc.in); got != tc.want {
				t.Errorf("scrubString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBeforeSend_ScrubsValues(t *testing.T) {
	ev := &sentry.Event{
		Message:  "failed me=abc",
		Tags:     map[string]string{"step": "Bearer xyz"},
		Contexts: map[string]sentry.Context{"log_attributes": {"err": "session_x=1 _nonce=9", "n": 5}},
	}
	out := BeforeSend(ev, nil)
	if out.Message != "failed me=<redacted>" || out.Tags["step"] != "Bearer <redacted>" {
		t.Errorf("message/tags = %q %v", out.Message, out.Tags)
	}
	c := out.Contexts["log_attributes"]
	if c["err"] != "session_x=<redacted> _nonce=<redacted>" || c["n"] != 5 {
		t.Errorf("context = %v", c)
	}
}
