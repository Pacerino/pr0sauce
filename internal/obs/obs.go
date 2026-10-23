// Package obs wires Sentry into the bot process: init, event scrubbing and a
// slog handler that forwards error records as Sentry events.
package obs

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

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

// BeforeSend strips request cookies/headers and sensitive-looking tag and
// context keys from an event.
func BeforeSend(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event.Request != nil {
		event.Request.Cookies = ""
		event.Request.Headers = nil
	}
	// sentry-go v0.50 has no Event.Extra; contexts and tags are what remain.
	for k := range event.Tags {
		if sensitive.MatchString(k) {
			delete(event.Tags, k)
		}
	}
	for name, ctx := range event.Contexts {
		for k := range ctx {
			if sensitive.MatchString(k) {
				delete(ctx, k)
			}
		}
		if sensitive.MatchString(name) {
			delete(event.Contexts, name)
		}
	}
	return event
}

// NewEventHandler returns a slog handler that sends records at level >= ERROR
// to hub as events. The message is the log message and the fingerprint is the
// message alone, so attributes (request ids, errors) never split a group.
func NewEventHandler(hub *sentry.Hub) slog.Handler {
	return &eventHandler{hub: hub}
}

type eventHandler struct {
	hub    *sentry.Hub
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
	ev := sentry.NewEvent()
	ev.Level = sentry.LevelError
	ev.Message = r.Message
	ev.Timestamp = r.Time
	ev.Logger = "slog"
	ev.Contexts = map[string]sentry.Context{"log_attributes": extra}
	ev.Tags = map[string]string{}
	for k, v := range extra {
		if str, ok := v.(string); ok && len(str) <= 200 {
			ev.Tags[k] = str
		}
	}
	ev.Fingerprint = []string{r.Message}
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
