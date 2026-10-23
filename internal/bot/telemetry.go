package bot

import (
	"context"
	"sync"
	"time"

	"github.com/getsentry/sentry-go"
)

const (
	// SessionInvalidMessage is the Sentry message sent once per transition to
	// an invalid pr0gramm session.
	SessionInvalidMessage = "pr0gramm session invalid – bot-login nötig"

	// MonitorSlug is the Sentry cron monitor that reports bot liveness.
	MonitorSlug = "pr0sauce-bot-poll"

	checkInEvery = 60 * time.Second
	flushTimeout = 5 * time.Second
)

// Telemetry is everything the bot reports to Sentry. All Sentry calls go
// through it, so tests use a recording fake and nothing touches the network.
type Telemetry interface {
	// CheckIn reports one successful poll to the cron monitor (throttled).
	CheckIn(ctx context.Context)
	// SessionInvalid reports that the pr0gramm session just became invalid.
	SessionInvalid()
	// Panic reports a recovered panic and flushes; the caller re-panics.
	Panic(recovered any)
}

type noopTelemetry struct{}

func (noopTelemetry) CheckIn(context.Context) {}
func (noopTelemetry) SessionInvalid()         {}
func (noopTelemetry) Panic(any)               {}

// NewTelemetry returns the Sentry-backed Telemetry, or a no-op one when hub is
// nil or has no client (Sentry disabled: empty DSN or failed init).
func NewTelemetry(hub *sentry.Hub, clock Clock) Telemetry {
	if hub == nil || hub.Client() == nil || !hub.Client().IsEnabled() {
		return noopTelemetry{}
	}
	if clock == nil {
		clock = systemClock{}
	}
	return &sentryTelemetry{hub: hub, clock: clock}
}

type sentryTelemetry struct {
	hub   *sentry.Hub
	clock Clock

	mu      sync.Mutex
	last    time.Time
	started bool // the first check-in (with the monitor upsert) was sent
}

func (s *sentryTelemetry) CheckIn(context.Context) {
	s.mu.Lock()
	now := s.clock.Now()
	if s.started && now.Sub(s.last) < checkInEvery {
		s.mu.Unlock()
		return
	}
	first := !s.started
	s.started, s.last = true, now
	s.mu.Unlock()

	var cfg *sentry.MonitorConfig
	if first {
		cfg = &sentry.MonitorConfig{
			Schedule:      sentry.IntervalSchedule(1, sentry.MonitorScheduleUnitMinute),
			CheckInMargin: 10,
			MaxRuntime:    1,
			Timezone:      "Europe/Berlin",
		}
	}
	s.hub.CaptureCheckIn(&sentry.CheckIn{MonitorSlug: MonitorSlug, Status: sentry.CheckInStatusOK}, cfg)
}

func (s *sentryTelemetry) SessionInvalid() {
	s.hub.WithScope(func(scope *sentry.Scope) {
		scope.SetLevel(sentry.LevelError)
		s.hub.CaptureMessage(SessionInvalidMessage)
	})
}

func (s *sentryTelemetry) Panic(recovered any) {
	s.hub.Recover(recovered)
	s.hub.Flush(flushTimeout)
}
