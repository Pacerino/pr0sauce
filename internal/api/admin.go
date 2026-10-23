package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/jackc/pgx/v5"
)

const (
	adminPageSize    = 50
	maxAdminOffset   = 10000
	botHealthTimeout = 3 * time.Second
	maxHealthBody    = 64 << 10
	reasonBanned     = "gesperrt"

	msgAdminStays = "Admin bleibt Admin"
)

func (s *server) routeAdmin() {
	const p = "/api/internal/admin"
	s.mux.Handle("GET "+p+"/moderators", s.internal(s.requireAdmin(s.listModerators)))
	s.mux.Handle("POST "+p+"/moderators", s.internal(s.requireAdmin(s.addModerator)))
	s.mux.Handle("DELETE "+p+"/moderators/{userId}", s.internal(s.requireAdmin(s.removeModerator)))
	s.mux.Handle("GET "+p+"/bans", s.internal(s.requireAdmin(s.listBans)))
	s.mux.Handle("POST "+p+"/bans", s.internal(s.requireAdmin(s.banUser)))
	s.mux.Handle("DELETE "+p+"/bans/{userId}", s.internal(s.requireAdmin(s.unbanUser)))
	s.mux.Handle("GET "+p+"/bot", s.internal(s.requireAdmin(s.botStatus)))
	s.mux.Handle("GET "+p+"/failed", s.internal(s.requireAdmin(s.failedRequests)))
	s.mux.Handle("GET /api/internal/suggestions/{id}/votes", s.internal(s.requireMod(s.suggestionVotes)))
}

type adminUserJSON struct {
	ID        int64   `json:"id"`
	Pr0UserID int64   `json:"pr0UserId"`
	Name      string  `json:"name"`
	Role      string  `json:"role"`
	BannedAt  *string `json:"bannedAt"`
	Reason    *string `json:"reason"`
}

func toAdminUser(u db.User) adminUserJSON {
	var at *string
	if u.BannedAt != nil {
		v := fmtTime(*u.BannedAt)
		at = &v
	}
	return adminUserJSON{ID: u.ID, Pr0UserID: u.Pr0UserID, Name: u.Name, Role: u.Role, BannedAt: at, Reason: u.BanReason}
}

func toAdminUsers(us []db.User) []adminUserJSON {
	out := make([]adminUserJSON, 0, len(us))
	for _, u := range us {
		out = append(out, toAdminUser(u))
	}
	return out
}

// parseOffset reads the optional offset query parameter (0..maxAdminOffset).
func parseOffset(w http.ResponseWriter, r *http.Request) (int32, bool) {
	v := r.URL.Query().Get("offset")
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > maxAdminOffset {
		writeError(w, http.StatusBadRequest, "invalid offset")
		return 0, false
	}
	return int32(n), true
}

// resolveProfile maps a submitted pr0gramm name to its profile, answering the
// error itself (and returning false) when it cannot.
func (s *server) resolveProfile(w http.ResponseWriter, r *http.Request, name string) (pr0gramm.Profile, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		writeError(w, http.StatusBadRequest, "name required")
		return pr0gramm.Profile{}, false
	}
	if s.pr0 == nil {
		writeError(w, http.StatusServiceUnavailable, "pr0gramm-Abfrage nicht konfiguriert")
		return pr0gramm.Profile{}, false
	}
	p, err := s.pr0.ProfileInfo(r.Context(), name)
	switch {
	case err == nil && p.ID > 0 && strings.TrimSpace(p.Name) != "":
		return p, true
	case err == nil || errors.Is(err, pr0gramm.ErrUserNotFound):
		writeError(w, http.StatusNotFound, "pr0gramm-Nutzer nicht gefunden")
	default:
		s.log.Error("pr0gramm lookup failed", "err", err)
		writeError(w, http.StatusBadGateway, "pr0gramm nicht erreichbar")
	}
	return pr0gramm.Profile{}, false
}

// targetUser parses {userId} and loads the row; 404 when missing.
func (s *server) targetUser(w http.ResponseWriter, r *http.Request) (db.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("userId"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return db.User{}, false
	}
	u, err := s.q.GetUser(r.Context(), id)
	switch {
	case err == nil:
		return u, true
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "user not found")
	default:
		s.internalError(w, r, err)
	}
	return db.User{}, false
}

func (s *server) listModerators(w http.ResponseWriter, r *http.Request, _ db.User) {
	us, err := s.q.ListModerators(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAdminUsers(us))
}

type nameReq struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func (s *server) addModerator(w http.ResponseWriter, r *http.Request, _ db.User) {
	var req nameReq
	if !decodeBody(w, r, maxAuthBody, &req) {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	p, ok := s.resolveProfile(w, r, req.Name)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	qt := s.q.WithTx(tx)
	u, err := qt.UpsertUserByPr0ID(ctx, db.UpsertUserByPr0IDParams{Pr0UserID: p.ID, Name: p.Name})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if u.Role == "admin" {
		writeError(w, http.StatusConflict, msgAdminStays)
		return
	}
	if err := qt.SetRole(ctx, db.SetRoleParams{ID: u.ID, Role: "moderator"}); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, r, err)
		return
	}
	u.Role = "moderator"
	writeJSON(w, http.StatusOK, toAdminUser(u))
}

func (s *server) removeModerator(w http.ResponseWriter, r *http.Request, _ db.User) {
	u, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	if u.Role == "admin" {
		writeError(w, http.StatusConflict, msgAdminStays)
		return
	}
	if u.Role == "moderator" {
		if err := s.q.SetRole(r.Context(), db.SetRoleParams{ID: u.ID, Role: "user"}); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listBans(w http.ResponseWriter, r *http.Request, _ db.User) {
	us, err := s.q.ListBanned(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAdminUsers(us))
}

// banUser bans in ONE transaction: the ban, closing the user's open
// suggestions and killing every session either all happen or none do.
func (s *server) banUser(w http.ResponseWriter, r *http.Request, admin db.User) {
	var req nameReq
	if !decodeBody(w, r, maxAuthBody, &req) {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	reasonText, ok := cleanText(req.Reason, maxReasonRunes)
	if !ok {
		writeError(w, http.StatusBadRequest, "reason must be at most 500 characters")
		return
	}
	var reason *string
	if reasonText != "" {
		reason = &reasonText
	}
	p, ok := s.resolveProfile(w, r, req.Name)
	if !ok {
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	qt := s.q.WithTx(tx)
	u, err := qt.UpsertUserByPr0ID(ctx, db.UpsertUserByPr0IDParams{Pr0UserID: p.ID, Name: p.Name})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if u.Role == "admin" || u.ID == admin.ID {
		writeError(w, http.StatusConflict, "Admin kann nicht gesperrt werden")
		return
	}
	if err := qt.SetBan(ctx, db.SetBanParams{ID: u.ID, BanReason: reason}); err != nil {
		s.internalError(w, r, err)
		return
	}
	closeReason := reasonBanned
	if err := qt.CloseOpenSuggestionsOfUser(ctx, db.CloseOpenSuggestionsOfUserParams{UserID: u.ID, Reason: &closeReason}); err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := qt.DeleteSessionsOfUser(ctx, u.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	u, err = qt.GetUser(ctx, u.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toAdminUser(u))
}

// unbanUser only clears the ban: closed suggestions stay closed and nothing is
// re-evaluated for auto-approval (that only ever runs on a vote).
func (s *server) unbanUser(w http.ResponseWriter, r *http.Request, _ db.User) {
	u, ok := s.targetUser(w, r)
	if !ok {
		return
	}
	if err := s.q.ClearBan(r.Context(), u.ID); err != nil {
		s.internalError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type retryJSON struct {
	Pending       int64   `json:"pending"`
	MaxAttempts   int32   `json:"maxAttempts"`
	NextAttemptAt *string `json:"nextAttemptAt"`
}

func optTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := fmtTime(*t)
	return &v
}

// fetchHealth GETs the bot's /healthz; the JSON body is parsed on any status
// (the bot answers 503 with details when degraded). Unreachable is status 0.
func (s *server) fetchHealth(ctx context.Context) (json.RawMessage, int) {
	unreachable := json.RawMessage(`{"error":"unreachable"}`)
	if s.botHealthURL == "" {
		return unreachable, 0
	}
	ctx, cancel := context.WithTimeout(ctx, botHealthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.botHealthURL, nil)
	if err != nil {
		return unreachable, 0
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return unreachable, 0
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxHealthBody))
	if err != nil {
		return unreachable, 0
	}
	var probe map[string]any
	if json.Unmarshal(b, &probe) != nil {
		return json.RawMessage(`{"error":"invalid response"}`), resp.StatusCode
	}
	return json.RawMessage(b), resp.StatusCode
}

func (s *server) botStatus(w http.ResponseWriter, r *http.Request, _ db.User) {
	ctx := r.Context()
	health, code := s.fetchHealth(ctx)
	counts, err := s.q.CountRequestsByStatus(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	reqs := map[string]int64{"pending": 0, "replying": 0, "done": 0, "failed": 0, "skipped": 0}
	for _, c := range counts {
		reqs[c.Status] = c.N
	}
	retry, err := s.q.PendingRetryStats(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	backlog, err := s.q.CountMarkReadBacklog(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	lastDone, err := s.q.LastDoneAt(ctx)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"health": health, "httpStatus": code, "requests": reqs,
		"retry":           retryJSON{Pending: retry.Pending, MaxAttempts: retry.MaxAttempts, NextAttemptAt: optTime(retry.NextAttemptAt)},
		"markReadBacklog": backlog, "lastDoneAt": optTime(lastDone),
	})
}

func (s *server) failedRequests(w http.ResponseWriter, r *http.Request, _ db.User) {
	off, ok := parseOffset(w, r)
	if !ok {
		return
	}
	rows, err := s.q.ListFailedRequests(r.Context(), db.ListFailedRequestsParams{Off: off, Lim: adminPageSize})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	type failedJSON struct {
		ID        int64   `json:"id"`
		ItemID    int64   `json:"itemId"`
		Requester string  `json:"requester"`
		Attempts  int32   `json:"attempts"`
		LastError *string `json:"lastError"`
		UpdatedAt string  `json:"updatedAt"`
	}
	out := make([]failedJSON, 0, len(rows))
	for _, q := range rows {
		out = append(out, failedJSON{ID: q.ID, ItemID: q.ItemID, Requester: q.Requester,
			Attempts: q.Attempts, LastError: q.LastError, UpdatedAt: fmtTime(q.UpdatedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) suggestionVotes(w http.ResponseWriter, r *http.Request, _ db.User) {
	sug, ok := s.loadSuggestion(w, r)
	if !ok {
		return
	}
	rows, err := s.q.ListVotesForSuggestion(r.Context(), sug.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	type voteJSON struct {
		UserID    int64  `json:"userId"`
		Name      string `json:"name"`
		Value     int16  `json:"value"`
		Banned    bool   `json:"banned"`
		CreatedAt string `json:"createdAt"`
	}
	out := make([]voteJSON, 0, len(rows))
	for _, v := range rows {
		out = append(out, voteJSON{UserID: v.UserID, Name: v.Name, Value: v.Value, Banned: v.Banned, CreatedAt: fmtTime(v.CreatedAt)})
	}
	writeJSON(w, http.StatusOK, out)
}
