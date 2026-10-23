// Package obs wires Sentry into the bot process: init, event scrubbing and a
// slog handler that forwards error records as Sentry events.
package obs

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

var sensitive = regexp.MustCompile(`(?i)cookie|token|password|secret|authorization|nonce`)

// Init configures the global Sentry hub. An empty dsn disables Sentry
// completely (no client, no network) and returns false.
func Init(dsn, environment, release string) (bool, error) {
	if dsn == "" {
		return false, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              dsn,
		Environment:      environment,
		Release:          release,
		SendDefaultPII:   false,
		AttachStacktrace: true,
		BeforeSend:       BeforeSend,
	})
	return err == nil, err
}

var valueScrubbers = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`\b(me|pp|session[_a-z]*)=[^;\s]+`), "${1}=<redacted>"},
	{regexp.MustCompile(`Bearer\s+\S+`), "Bearer <redacted>"},
	{regexp.MustCompile(`_nonce=[^&\s]+`), "_nonce=<redacted>"},
	{regexp.MustCompile(`[A-Za-z0-9_-]{32,}`), "<redacted>"},
}

// scrubString redacts cookie values, bearer tokens, nonces and long
// hex/base64url runs in s.
func scrubString(s string) string {
	for _, sc := range valueScrubbers {
		s = sc.re.ReplaceAllString(s, sc.repl)
	}
	return s
}

// BeforeSend strips request cookies/headers, drops sensitive-looking tag and
// context keys, and redacts secret-looking values in the message, tags,
// context strings and exception values.
func BeforeSend(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event.Request != nil {
		event.Request.Cookies = ""
		event.Request.Headers = nil
	}
	event.Message = scrubString(event.Message)
	// sentry-go v0.50 has no Event.Extra; contexts and tags are what remain.
	for k, v := range event.Tags {
		if sensitive.MatchString(k) {
			delete(event.Tags, k)
			continue
		}
		event.Tags[k] = scrubString(v)
	}
	for name, ctx := range event.Contexts {
		if sensitive.MatchString(name) {
			delete(event.Contexts, name)
			continue
		}
		for k, v := range ctx {
			if sensitive.MatchString(k) {
				delete(ctx, k)
			} else if str, ok := v.(string); ok {
				ctx[k] = scrubString(str)
			}
		}
	}
	for i := range event.Exception {
		event.Exception[i].Value = scrubString(event.Exception[i].Value)
	}
	return event
}

const (
	throttleWindow = 10 * time.Minute
	stepFailedMsg  = "step failed"
)

// tagKeys are the only attributes that become tags (searchable, low
// cardinality); every other attribute goes into the context only.
var tagKeys = map[string]bool{"step": true, "path": true, "status": true, "kind": true, "reply_kind": true, "attempt": true}

// NewEventHandler returns a slog handler that sends records at level >= ERROR
// to hub as events, at most one per fingerprint per 10 minutes.
func NewEventHandler(hub *sentry.Hub) slog.Handler {
	return NewEventHandlerClock(hub, time.Now)
}

// NewEventHandlerClock is NewEventHandler with an injectable clock.
func NewEventHandlerClock(hub *sentry.Hub, now func() time.Time) slog.Handler {
	return &eventHandler{hub: hub, th: &throttle{now: now, seen: map[string]*throttleEntry{}}}
}

type throttleEntry struct {
	last       time.Time
	suppressed int
}

// throttle is shared by all handlers derived via WithAttrs/WithGroup.
type throttle struct {
	mu   sync.Mutex
	now  func() time.Time
	seen map[string]*throttleEntry
}

// allow reports whether an event with this fingerprint may go out, and how
// many records were suppressed since the last one that did.
func (t *throttle) allow(key string) (ok bool, suppressed int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	e := t.seen[key]
	if e == nil {
		t.seen[key] = &throttleEntry{last: now}
		return true, 0
	}
	if now.Sub(e.last) < throttleWindow {
		e.suppressed++
		return false, 0
	}
	suppressed = e.suppressed
	e.last, e.suppressed = now, 0
	return true, suppressed
}

type eventHandler struct {
	hub    *sentry.Hub
	th     *throttle
	attrs  []slog.Attr // already prefixed with the groups open at WithAttrs time
	groups []string
}

func (h *eventHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelError }

func (h *eventHandler) Handle(_ context.Context, r slog.Record) error {
	extra := sentry.Context{}
	for _, a := range h.attrs {
		addAttr(extra, "", a)
	}
	prefix := strings.Join(h.groups, ".")
	r.Attrs(func(a slog.Attr) bool {
		addAttr(extra, prefix, a)
		return true
	})
	fp := []string{r.Message}
	if r.Message == stepFailedMsg {
		if step, ok := extra["step"]; ok {
			fp = append(fp, fmt.Sprint(step))
		}
	}
	ok, suppressed := h.th.allow(strings.Join(fp, "\x00"))
	if !ok {
		return nil
	}
	if suppressed > 0 {
		extra["suppressed_since_last"] = suppressed
	}
	ev := sentry.NewEvent()
	ev.Level = sentry.LevelError
	ev.Message = r.Message
	ev.Timestamp = r.Time
	ev.Logger = "slog"
	ev.Fingerprint = fp
	ev.Contexts = map[string]sentry.Context{"log_attributes": extra}
	ev.Tags = map[string]string{}
	for k, v := range extra {
		if tagKeys[k] {
			ev.Tags[k] = fmt.Sprint(v)
		}
	}
	h.hub.CaptureEvent(ev)
	return nil
}

func (h *eventHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	n := *h
	prefix := strings.Join(h.groups, ".")
	n.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, a := range attrs {
		if prefix != "" {
			a.Key = prefix + "." + a.Key
		}
		n.attrs = append(n.attrs, a)
	}
	return &n
}

func (h *eventHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	n := *h
	n.groups = append(append([]string(nil), h.groups...), name)
	return &n
}

func addAttr(dst sentry.Context, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	key := a.Key
	if prefix != "" && key != "" {
		key = prefix + "." + key
	} else if prefix != "" {
		key = prefix
	}
	if a.Value.Kind() == slog.KindGroup {
		for _, g := range a.Value.Group() {
			addAttr(dst, key, g)
		}
		return
	}
	if key == "" {
		return
	}
	if e, ok := a.Value.Any().(error); ok {
		dst[key] = e.Error()
		return
	}
	switch v := a.Value.Any().(type) {
	case string, bool, int, int64, uint64, float64:
		dst[key] = v
	default:
		dst[key] = fmt.Sprint(v)
	}
}
