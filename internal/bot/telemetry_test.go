package bot

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

// recTransport records events instead of sending them; no network.
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
func (t *recTransport) all() []*sentry.Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*sentry.Event(nil), t.events...)
}

func testHub(t *testing.T) (*sentry.Hub, *recTransport) {
	t.Helper()
	tr := &recTransport{}
	c, err := sentry.NewClient(sentry.ClientOptions{Dsn: "https://public@example.invalid/1", Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	return sentry.NewHub(c, sentry.NewScope()), tr
}

type manualClock struct{ t time.Time }

func (c *manualClock) Now() time.Time { return c.t }

func TestTelemetry_CheckInThrottledTo60s(t *testing.T) {
	hub, tr := testHub(t)
	clk := &manualClock{t: time.Unix(1_000_000, 0)}
	tel := NewTelemetry(hub, clk)
	ctx := context.Background()

	for i := 0; i < 5; i++ { // 5 ticks within 20 s
		tel.CheckIn(ctx)
		clk.t = clk.t.Add(5 * time.Second)
	}
	if n := len(tr.all()); n != 1 {
		t.Fatalf("check-ins in first 25s = %d, want 1", n)
	}
	clk.t = clk.t.Add(40 * time.Second) // 65 s after the first
	tel.CheckIn(ctx)
	tel.CheckIn(ctx)
	evs := tr.all()
	if len(evs) != 2 {
		t.Fatalf("check-ins after 65s = %d, want 2", len(evs))
	}
	first, second := evs[0], evs[1]
	if first.CheckIn == nil || first.CheckIn.MonitorSlug != "pr0sauce-bot-poll" || first.CheckIn.Status != sentry.CheckInStatusOK {
		t.Fatalf("first check-in = %+v", first.CheckIn)
	}
	mc := first.MonitorConfig
	if mc == nil || mc.CheckInMargin != 10 || mc.MaxRuntime != 1 || mc.Timezone != "Europe/Berlin" || mc.Schedule == nil {
		t.Fatalf("monitor config upsert missing on first check-in: %+v", mc)
	}
	if second.MonitorConfig != nil {
		t.Errorf("monitor config sent again on second check-in")
	}
}

func TestTelemetry_DisabledSendsNothing(t *testing.T) {
	// nil hub and a hub without client both mean disabled.
	for name, hub := range map[string]*sentry.Hub{"nil": nil, "noclient": sentry.NewHub(nil, sentry.NewScope())} {
		tel := NewTelemetry(hub, &manualClock{})
		if _, ok := tel.(noopTelemetry); !ok {
			t.Fatalf("%s: want noopTelemetry, got %T", name, tel)
		}
		tel.CheckIn(context.Background())
		tel.SessionInvalid()
		tel.Panic("x")
	}
}

func TestTelemetry_SessionInvalidIsErrorMessage(t *testing.T) {
	hub, tr := testHub(t)
	NewTelemetry(hub, nil).SessionInvalid()
	evs := tr.all()
	if len(evs) != 1 || evs[0].Message != "pr0gramm session invalid – bot-login nötig" || evs[0].Level != sentry.LevelError {
		t.Fatalf("events = %+v", evs)
	}
}

// recTel is a recording Telemetry fake.
type recTel struct {
	checkIns, invalids int
	panics             []any
}

func (r *recTel) CheckIn(context.Context) { r.checkIns++ }
func (r *recTel) SessionInvalid()         { r.invalids++ }
func (r *recTel) Panic(v any)             { r.panics = append(r.panics, v) }

func TestRunner_SessionInvalidOncePerTransition(t *testing.T) {
	tel := &recTel{}
	r := &runner{log: discard, health: &health{}, clock: &manualClock{}, tel: tel, recheck: time.Hour}
	r.health.setSession(sessionOK)

	r.setInvalid()
	r.setInvalid() // repeated re-checks while still invalid
	r.setInvalid()
	if tel.invalids != 1 {
		t.Fatalf("invalids = %d, want 1", tel.invalids)
	}
	r.setOK()
	r.setInvalid() // a new transition
	if tel.invalids != 2 {
		t.Fatalf("invalids = %d, want 2", tel.invalids)
	}
	r.setUnknown(nil)
	r.setInvalid() // unknown -> invalid is a transition too
	if tel.invalids != 3 {
		t.Fatalf("invalids = %d, want 3", tel.invalids)
	}
}

func TestRunner_GuardReportsAndRepanics(t *testing.T) {
	tel := &recTel{}
	r := &runner{tel: tel}
	var got any
	func() {
		defer func() { got = recover() }()
		defer r.guard()
		panic("boom")
	}()
	if got != "boom" {
		t.Fatalf("re-panic value = %v, want boom", got)
	}
	if len(tel.panics) != 1 || tel.panics[0] != "boom" {
		t.Fatalf("reported panics = %v", tel.panics)
	}
}
