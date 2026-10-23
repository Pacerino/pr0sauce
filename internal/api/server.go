// Package api implements the read-only JSON API consumed by the pr0sauce.info website.
package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"

	_ "time/tzdata" // Europe/Berlin must resolve on minimal container images
)

type server struct {
	q    *db.Queries
	pool *pgxpool.Pool
	log  *slog.Logger
	now  func() time.Time
	mux  *http.ServeMux

	internalToken string
	adminName     string
	botHealthURL  string
	pr0           Pr0Lookup
	deezer        DeezerSearch

	statsMu      sync.Mutex
	statsCache   *statsResp
	statsExpires time.Time
	compute      func(ctx context.Context, now time.Time) (*statsResp, error)
}

// Options configures the account/moderation layer. An empty InternalToken
// makes every /api/internal/* request fail with 401.
type Options struct {
	InternalToken string
	AdminName     string
	BotHealthURL  string
	Pr0           Pr0Lookup
	Deezer        DeezerSearch
	Now           func() time.Time
}

// New returns the API handler.
func New(q *db.Queries, pool *pgxpool.Pool, log *slog.Logger, opts Options) http.Handler {
	return newServer(q, pool, log, opts)
}

func newServer(q *db.Queries, pool *pgxpool.Pool, log *slog.Logger, opts Options) *server {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := &server{q: q, pool: pool, log: log, now: now, mux: http.NewServeMux(),
		internalToken: opts.InternalToken, adminName: opts.AdminName, botHealthURL: opts.BotHealthURL,
		pr0: opts.Pr0, deezer: opts.Deezer}
	s.compute = s.computeStats
	s.mux.HandleFunc("GET /api/songs", s.listSongs)
	s.mux.HandleFunc("GET /api/songs/{itemId}", s.getSong)
	s.mux.HandleFunc("GET /api/stats", s.stats)
	s.mux.HandleFunc("GET /healthz", s.healthz)
	s.mux.Handle("POST /api/internal/sessions", s.internal(http.HandlerFunc(s.createSession)))
	s.mux.Handle("DELETE /api/internal/sessions", s.internal(s.requireSession(s.deleteSession)))
	s.mux.Handle("GET /api/internal/me", s.internal(s.requireUser(s.me)))
	s.routeSuggestions()
	s.routeAdmin()
	return s
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
	s.mux.ServeHTTP(sw, r)
	if r.URL.Path != "/healthz" {
		s.log.Info("request", "method", r.Method, "path", r.URL.Path,
			"status", sw.status, "duration", time.Since(start))
	}
}

// statusWriter records the status and rewrites the mux's plain-text 404/405 as JSON.
type statusWriter struct {
	http.ResponseWriter
	status   int
	rewrite  string
	wroteHdr bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHdr {
		return
	}
	w.wroteHdr = true
	w.status = code
	if (code == http.StatusMethodNotAllowed || code == http.StatusNotFound) &&
		w.Header().Get("Content-Type") != "application/json" {
		w.rewrite = `{"error":"not found"}`
		if code == http.StatusMethodNotAllowed {
			w.rewrite = `{"error":"method not allowed"}`
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wroteHdr {
		w.WriteHeader(http.StatusOK)
	}
	if w.rewrite != "" {
		body := w.rewrite + "\n"
		w.rewrite = ""
		if _, err := w.ResponseWriter.Write([]byte(body)); err != nil {
			return 0, err
		}
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"internal"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// internalError logs err and returns a generic 500; nothing internal leaks.
func (s *server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal")
}

func (s *server) healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		s.log.Error("healthz ping failed", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
