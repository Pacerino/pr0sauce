package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// maxPollAge is how old the last successful poll may be for /healthz to be 200.
const maxPollAge = 2 * time.Minute

// sessionState is the bot's belief about its pr0gramm session.
type sessionState int

const (
	// sessionUnknown: the check itself failed (network, Cloudflare). Not healthy.
	sessionUnknown sessionState = iota
	sessionOK
	sessionInvalid
)

func (s sessionState) String() string {
	switch s {
	case sessionOK:
		return "ok"
	case sessionInvalid:
		return "invalid"
	default:
		return "unknown"
	}
}

// pinger is the database liveness check behind /healthz.
type pinger interface {
	Ping(ctx context.Context) error
}

// health is the state behind /healthz. Safe for concurrent use.
type health struct {
	mu       sync.Mutex
	session  sessionState
	lastPoll time.Time
	pinger   pinger
	clock    Clock
}

func (h *health) setSession(s sessionState) {
	h.mu.Lock()
	h.session = s
	h.mu.Unlock()
}

func (h *health) getSession() sessionState {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.session
}

func (h *health) markPoll(t time.Time) {
	h.mu.Lock()
	h.lastPoll = t
	h.mu.Unlock()
}

type healthBody struct {
	DB       string  `json:"db"`
	Session  string  `json:"session"`
	LastPoll *string `json:"last_poll"`
}

func (h *health) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer cancel()
		dbOK := h.pinger.Ping(ctx) == nil

		h.mu.Lock()
		session, last := h.session, h.lastPoll
		h.mu.Unlock()

		body := healthBody{DB: "ok", Session: session.String()}
		if !dbOK {
			body.DB = "error"
		}
		fresh := false
		if !last.IsZero() {
			s := last.UTC().Format(time.RFC3339)
			body.LastPoll = &s
			fresh = h.clock.Now().Sub(last) <= maxPollAge
		}
		status := http.StatusServiceUnavailable
		if dbOK && session == sessionOK && fresh {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
	return mux
}
