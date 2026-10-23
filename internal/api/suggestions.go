package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	maxSuggestBody   = 8 << 10
	maxFieldRunes    = 200
	maxProofRunes    = 500
	maxReasonRunes   = 500
	suggestPerDay    = 20
	suggestWindow    = 24 * time.Hour
	queuePageSize    = 50
	autoApproveAfter = 10 * time.Second // budget for the post-vote check, detached from the client

	uniqueOneOpen = "suggestions_one_open"
	pgUniqueCode  = "23505"
	pgFKCode      = "23503"
)

func (s *server) routeSuggestions() {
	s.mux.Handle("GET /api/internal/songs/{itemId}/suggestions", s.internal(http.HandlerFunc(s.listSuggestions)))
	s.mux.Handle("POST /api/internal/songs/{itemId}/suggestions", s.internal(s.requireUser(s.createSuggestion)))
	s.mux.Handle("PUT /api/internal/suggestions/{id}/vote", s.internal(s.requireUser(s.voteSuggestion)))
	s.mux.Handle("POST /api/internal/suggestions/{id}/approve", s.internal(s.requireMod(s.approveSuggestion)))
	s.mux.Handle("POST /api/internal/suggestions/{id}/reject", s.internal(s.requireMod(s.rejectSuggestion)))
	s.mux.Handle("GET /api/internal/mod/queue", s.internal(s.requireMod(s.modQueue)))
	s.mux.Handle("GET /api/internal/songs/{itemId}/history", s.internal(s.requireMod(s.songHistory)))
	s.mux.Handle("POST /api/internal/songs/{itemId}/revert", s.internal(s.requireMod(s.revertSong)))
}

type suggestionJSON struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Artist    string  `json:"artist"`
	Album     *string `json:"album"`
	ProofURL  *string `json:"proofUrl"`
	Author    string  `json:"author"`
	Up        int64   `json:"up"`
	Down      int64   `json:"down"`
	Net       int64   `json:"net"`
	MyVote    *int16  `json:"myVote"`
	Mine      bool    `json:"mine"`
	CreatedAt string  `json:"createdAt"`
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// decodeBody reads a JSON body of at most limit bytes; an empty body leaves v untouched.
func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v)
	return err == nil || errors.Is(err, io.EOF)
}

// cleanText trims s and checks it is valid UTF-8 without NUL (which Postgres
// text rejects) and at most max runes.
func cleanText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) || utf8.RuneCountInString(s) > max {
		return "", false
	}
	return s, true
}

// optionalText is cleanText where empty means NULL.
func optionalText(s *string, max int) (*string, bool) {
	if s == nil {
		return nil, true
	}
	v, ok := cleanText(*s, max)
	if !ok || v == "" {
		return nil, ok
	}
	return &v, true
}

func validProofURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func pathID(r *http.Request, name string) (int64, bool) { return parseItemID(r.PathValue(name)) }

// songExists maps a missing song to 404; true means the handler may continue.
func (s *server) songExists(w http.ResponseWriter, r *http.Request, item int64) bool {
	_, err := s.q.GetSong(r.Context(), item)
	switch {
	case err == nil:
		return true
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "song not found")
	default:
		s.internalError(w, r, err)
	}
	return false
}

// loadSuggestion maps a missing suggestion to 404.
func (s *server) loadSuggestion(w http.ResponseWriter, r *http.Request) (db.Suggestion, bool) {
	id, ok := pathID(r, "id")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid suggestion id")
		return db.Suggestion{}, false
	}
	sug, err := s.q.GetSuggestion(r.Context(), id)
	switch {
	case err == nil:
		return sug, true
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "suggestion not found")
	default:
		s.internalError(w, r, err)
	}
	return db.Suggestion{}, false
}

// listSuggestions is session-optional: myVote and mine come from the session when present.
func (s *server) listSuggestions(w http.ResponseWriter, r *http.Request) {
	item, ok := pathID(r, "itemId")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	if !s.songExists(w, r, item) {
		return
	}
	ctx := r.Context()
	rows, err := s.q.ListOpenSuggestionsForItem(ctx, item)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var me int64
	mine := map[int64]int16{}
	if u := sessionFrom(ctx).user; u != nil {
		me = u.ID
		votes, err := s.q.ListUserVotesForItem(ctx, db.ListUserVotesForItemParams{ItemID: item, UserID: me})
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		for _, v := range votes {
			mine[v.SuggestionID] = v.Value
		}
	}
	out := make([]suggestionJSON, 0, len(rows))
	for _, row := range rows {
		sj := suggestionJSON{
			ID: row.ID, Title: row.Title, Artist: row.Artist, Album: row.Album, ProofURL: row.ProofUrl,
			Author: row.AuthorName, Up: row.Up, Down: row.Down, Net: row.Net,
			Mine: me != 0 && row.UserID == me, CreatedAt: fmtTime(row.CreatedAt),
		}
		if v, ok := mine[row.ID]; ok {
			sj.MyVote = &v
		}
		out = append(out, sj)
	}
	writeJSON(w, http.StatusOK, out)
}

type suggestReq struct {
	Title    string  `json:"title"`
	Artist   string  `json:"artist"`
	Album    *string `json:"album"`
	ProofURL *string `json:"proofUrl"`
}

func (s *server) createSuggestion(w http.ResponseWriter, r *http.Request, u db.User) {
	item, ok := pathID(r, "itemId")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	var req suggestReq
	if !decodeBody(w, r, maxSuggestBody, &req) {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	title, okT := cleanText(req.Title, maxFieldRunes)
	artist, okA := cleanText(req.Artist, maxFieldRunes)
	album, okAl := optionalText(req.Album, maxFieldRunes)
	proof, okP := optionalText(req.ProofURL, maxProofRunes)
	switch {
	case !okT || title == "":
		writeError(w, http.StatusBadRequest, "title must be 1-200 characters")
		return
	case !okA || artist == "":
		writeError(w, http.StatusBadRequest, "artist must be 1-200 characters")
		return
	case !okAl:
		writeError(w, http.StatusBadRequest, "album must be at most 200 characters")
		return
	case !okP || (proof != nil && !validProofURL(*proof)):
		writeError(w, http.StatusBadRequest, "proofUrl must be an http(s) URL of at most 500 characters")
		return
	}
	if !s.songExists(w, r, item) {
		return
	}
	ctx := r.Context()
	n, err := s.q.CountUserSuggestionsSince(ctx, db.CountUserSuggestionsSinceParams{UserID: u.ID, CreatedAt: s.now().Add(-suggestWindow)})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if n >= suggestPerDay {
		writeError(w, http.StatusTooManyRequests, "too many suggestions, try again tomorrow")
		return
	}
	sug, err := s.q.CreateSuggestion(ctx, db.CreateSuggestionParams{
		ItemID: item, UserID: u.ID, Title: title, Artist: artist, Album: album, ProofUrl: proof,
	})
	var pgErr *pgconn.PgError
	switch {
	case err == nil:
	case errors.As(err, &pgErr) && pgErr.Code == pgUniqueCode && pgErr.ConstraintName == uniqueOneOpen:
		writeError(w, http.StatusConflict, "you already have an open suggestion for this item")
		return
	case errors.As(err, &pgErr) && pgErr.Code == pgFKCode:
		writeError(w, http.StatusNotFound, "song not found")
		return
	default:
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, suggestionJSON{
		ID: sug.ID, Title: sug.Title, Artist: sug.Artist, Album: sug.Album, ProofURL: sug.ProofUrl,
		Author: u.Name, Mine: true, CreatedAt: fmtTime(sug.CreatedAt),
	})
}

type voteReq struct {
	Value *int `json:"value"`
}

func (s *server) voteSuggestion(w http.ResponseWriter, r *http.Request, u db.User) {
	var req voteReq
	if !decodeBody(w, r, maxAuthBody, &req) || req.Value == nil ||
		(*req.Value != 1 && *req.Value != -1 && *req.Value != 0) {
		writeError(w, http.StatusBadRequest, "value must be 1, -1 or 0")
		return
	}
	sug, ok := s.loadSuggestion(w, r)
	if !ok {
		return
	}
	if sug.UserID == u.ID {
		writeError(w, http.StatusForbidden, "cannot vote on your own suggestion")
		return
	}
	if sug.Status != statusOpen {
		writeError(w, http.StatusConflict, "suggestion is not open")
		return
	}
	ctx := r.Context()
	var err error
	if *req.Value == 0 {
		err = s.q.DeleteVote(ctx, db.DeleteVoteParams{SuggestionID: sug.ID, UserID: u.ID})
	} else {
		err = s.q.UpsertVote(ctx, db.UpsertVoteParams{SuggestionID: sug.ID, UserID: u.ID, Value: int16(*req.Value)})
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// The vote is committed; the approval check must not die with the client.
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), autoApproveAfter)
	defer cancel()
	if err := s.maybeAutoApprove(actx, sug.ID); err != nil {
		s.log.Error("auto-approve failed", "suggestion", sug.ID, "err", err)
	}
	cur, err := s.q.GetSuggestion(ctx, sug.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	t, err := s.q.TallySuggestion(ctx, sug.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var my *int16
	if *req.Value != 0 {
		v := int16(*req.Value)
		my = &v
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": sug.ID, "status": cur.Status, "up": t.Up, "down": t.Down, "net": t.Net, "myVote": my,
	})
}

func (s *server) approveSuggestion(w http.ResponseWriter, r *http.Request, mod db.User) {
	sug, ok := s.loadSuggestion(w, r)
	if !ok {
		return
	}
	if sug.Status != statusOpen {
		writeError(w, http.StatusConflict, "suggestion is not open")
		return
	}
	if mod.Role == "moderator" && sug.UserID == mod.ID {
		writeError(w, http.StatusForbidden, "Eigener Vorschlag")
		return
	}
	hit := s.searchDeezer(r.Context(), sug.Artist, sug.Title) // before the tx: no lock across the network
	switch err := s.accept(r.Context(), sug.ID, approvedByMod(mod.ID), hit); {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"id": sug.ID, "status": statusApproved})
	case errors.Is(err, errNotOpen):
		writeError(w, http.StatusConflict, "suggestion is not open")
	default:
		s.internalError(w, r, err)
	}
}

type rejectReq struct {
	Reason *string `json:"reason"`
}

func (s *server) rejectSuggestion(w http.ResponseWriter, r *http.Request, mod db.User) {
	var req rejectReq
	if !decodeBody(w, r, maxSuggestBody, &req) {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	reason, ok := optionalText(req.Reason, maxReasonRunes)
	if !ok {
		writeError(w, http.StatusBadRequest, "reason must be at most 500 characters")
		return
	}
	sug, ok := s.loadSuggestion(w, r)
	if !ok {
		return
	}
	reviewer := mod.ID
	n, err := s.q.SetSuggestionStatus(r.Context(), db.SetSuggestionStatusParams{
		ID: sug.ID, Status: statusRejected, ReviewerID: &reviewer, Reason: reason,
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if n != 1 {
		writeError(w, http.StatusConflict, "suggestion is not open")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": sug.ID, "status": statusRejected})
}

type currentJSON struct {
	Title  *string `json:"title"`
	Artist *string `json:"artist"`
	Album  *string `json:"album"`
}

type queueItemJSON struct {
	ID        int64       `json:"id"`
	ItemID    int64       `json:"itemId"`
	Title     string      `json:"title"`
	Artist    string      `json:"artist"`
	Album     *string     `json:"album"`
	ProofURL  *string     `json:"proofUrl"`
	Author    string      `json:"author"`
	Up        int64       `json:"up"`
	Down      int64       `json:"down"`
	Net       int64       `json:"net"`
	CreatedAt string      `json:"createdAt"`
	Current   currentJSON `json:"current"`
}

func (s *server) modQueue(w http.ResponseWriter, r *http.Request, _ db.User) {
	off := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 1<<30 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		off = n
	}
	rows, err := s.q.ListOpenQueue(r.Context(), db.ListOpenQueueParams{Off: int32(off), Lim: queuePageSize})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	items := make([]queueItemJSON, 0, len(rows))
	for _, row := range rows {
		items = append(items, queueItemJSON{
			ID: row.ID, ItemID: row.ItemID, Title: row.Title, Artist: row.Artist, Album: row.Album,
			ProofURL: row.ProofUrl, Author: row.AuthorName, Up: row.Up, Down: row.Down, Net: row.Net,
			CreatedAt: fmtTime(row.CreatedAt),
			Current:   currentJSON{Title: row.CurTitle, Artist: row.CurArtist, Album: row.CurAlbum},
		})
	}
	var next *int
	if len(rows) == queuePageSize {
		n := off + queuePageSize
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "nextOffset": next})
}

type historyJSON struct {
	ID           int64           `json:"id"`
	Source       string          `json:"source"`
	ActorID      *int64          `json:"actorId"`
	Actor        *string         `json:"actor"`
	SuggestionID *int64          `json:"suggestionId"`
	CreatedAt    string          `json:"createdAt"`
	Snapshot     json.RawMessage `json:"snapshot"`
}

// songHistory lists the pre-change snapshots, newest first; the last one is the bot's original.
func (s *server) songHistory(w http.ResponseWriter, r *http.Request, _ db.User) {
	item, ok := pathID(r, "itemId")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	if !s.songExists(w, r, item) {
		return
	}
	rows, err := s.q.ListHistory(r.Context(), item)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]historyJSON, 0, len(rows))
	for _, h := range rows {
		out = append(out, historyJSON{
			ID: h.ID, Source: h.Source, ActorID: h.ActorID, Actor: h.ActorName,
			SuggestionID: h.SuggestionID, CreatedAt: fmtTime(h.CreatedAt), Snapshot: h.Snapshot,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

type revertReq struct {
	HistoryID int64 `json:"historyId"`
}

func (s *server) revertSong(w http.ResponseWriter, r *http.Request, mod db.User) {
	item, ok := pathID(r, "itemId")
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	var req revertReq
	if !decodeBody(w, r, maxAuthBody, &req) || req.HistoryID <= 0 {
		writeError(w, http.StatusBadRequest, "historyId required")
		return
	}
	h, err := s.q.GetHistory(r.Context(), req.HistoryID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && h.ItemID != item) {
		writeError(w, http.StatusNotFound, "history entry not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	id, err := s.revertTo(r.Context(), h, mod.ID)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"historyId": id})
	case errors.Is(err, errNoSong):
		writeError(w, http.StatusNotFound, "song not found")
	default:
		s.internalError(w, r, err)
	}
}
