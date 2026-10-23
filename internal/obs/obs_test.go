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
	if len(e.Fingerprint) != 1 || e.Fingerprint[0] != "step failed" {
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
