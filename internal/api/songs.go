package api

import (
	"encoding/base64"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/jackc/pgx/v5"
)

const (
	defaultLimit = 20
	maxLimit     = 50
)

var itemIDRe = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)

type comment struct {
	Up        int32 `json:"up"`
	Down      int32 `json:"down"`
	CommentID int64 `json:"commentId"`
}

type songJSON struct {
	ItemID       int64    `json:"itemId"`
	Title        string   `json:"title"`
	Artist       string   `json:"artist"`
	Album        *string  `json:"album"`
	Provider     string   `json:"provider"`
	SpotifyURL   *string  `json:"spotifyUrl"`
	YoutubeURL   *string  `json:"youtubeUrl"`
	CoverURL     *string  `json:"coverUrl"`
	RecognizedAt string   `json:"recognizedAt"`
	Comment      *comment `json:"comment"`
}

func toSong(s db.Song) songJSON {
	out := songJSON{
		ItemID: s.ItemID, Album: s.Album, Provider: s.Provider,
		SpotifyURL: s.SpotifyUrl, YoutubeURL: s.YoutubeUrl, CoverURL: s.CoverUrl,
		RecognizedAt: s.RecognizedAt.UTC().Format(time.RFC3339Nano),
	}
	if s.Title != nil {
		out.Title = *s.Title
	}
	if s.Artist != nil {
		out.Artist = *s.Artist
	}
	return out
}

type listResp struct {
	Items      []songJSON `json:"items"`
	NextCursor *string    `json:"nextCursor"`
}

// parseItemID accepts only canonical positive int64 ids.
func parseItemID(s string) (int64, bool) {
	if !itemIDRe.MatchString(s) {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil
}

func encodeCursor(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

var errBadCursor = errors.New("invalid cursor")

// decodeCursor returns the kind ("r" or "s") and the remaining fields.
func decodeCursor(c string) (kind string, parts []string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", nil, errBadCursor
	}
	f := strings.Split(string(raw), "|")
	switch {
	case f[0] == "r" && len(f) == 3, f[0] == "s" && len(f) == 2:
		return f[0], f[1:], nil
	}
	return "", nil, errBadCursor
}

func likeEscape(q string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

func (s *server) listSongs(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	limit := int32(defaultLimit)
	if qs.Has("limit") {
		n, err := strconv.Atoi(qs.Get("limit"))
		if err != nil || n < 1 || n > maxLimit {
			writeError(w, http.StatusBadRequest, "limit must be between 1 and 50")
			return
		}
		limit = int32(n)
	}
	q := qs.Get("q")
	cursor := qs.Get("cursor")

	var kind string
	var parts []string
	if cursor != "" {
		var err error
		kind, parts, err = decodeCursor(cursor)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
	}
	searching := q != ""
	if cursor != "" && (kind == "s") != searching {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	var (
		songs []db.Song
		next  func(last db.Song) string
		err   error
	)
	ctx := r.Context()
	switch {
	case searching:
		if id, ok := parseItemID(q); ok {
			song, gerr := s.q.GetSong(ctx, id)
			switch {
			case errors.Is(gerr, pgx.ErrNoRows):
			case gerr != nil:
				s.internalError(w, r, gerr)
				return
			case song.Title != nil:
				songs = []db.Song{song}
			}
			writeJSON(w, http.StatusOK, s.buildList(songs, math.MaxInt32, nil))
			return
		}
		var off int64
		if kind == "s" {
			off, err = strconv.ParseInt(parts[0], 10, 32)
			if err != nil || off < 0 {
				writeError(w, http.StatusBadRequest, "invalid cursor")
				return
			}
		}
		songs, err = s.q.SearchSongs(ctx, db.SearchSongsParams{
			Pattern: likeEscape(q), Q: q, Off: int32(off), Lim: limit,
		})
		next = func(db.Song) string {
			return encodeCursor("s|" + strconv.FormatInt(off+int64(limit), 10))
		}
	default:
		// Far-future sentinel: every row is strictly older.
		before, beforeID := time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC), int64(math.MaxInt64)
		if kind == "r" {
			t, terr := time.Parse(time.RFC3339Nano, parts[0])
			id, ok := parseItemID(parts[1])
			if terr != nil || !ok {
				writeError(w, http.StatusBadRequest, "invalid cursor")
				return
			}
			before, beforeID = t, id
		}
		songs, err = s.q.ListRecentSongs(ctx, db.ListRecentSongsParams{
			BeforeRecognizedAt: before, BeforeItemID: beforeID, Lim: limit,
		})
		next = func(last db.Song) string {
			return encodeCursor("r|" + last.RecognizedAt.UTC().Format(time.RFC3339Nano) + "|" +
				strconv.FormatInt(last.ItemID, 10))
		}
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.buildList(songs, int(limit), next))
}

func (s *server) buildList(songs []db.Song, limit int, next func(db.Song) string) listResp {
	resp := listResp{Items: make([]songJSON, 0, len(songs))}
	for _, sg := range songs {
		resp.Items = append(resp.Items, toSong(sg))
	}
	if next != nil && len(songs) >= limit && len(songs) > 0 {
		c := next(songs[len(songs)-1])
		resp.NextCursor = &c
	}
	return resp
}

func (s *server) getSong(w http.ResponseWriter, r *http.Request) {
	id, ok := parseItemID(r.PathValue("itemId"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid item id")
		return
	}
	song, err := s.q.GetSong(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && song.Title == nil) {
		writeError(w, http.StatusNotFound, "song not found")
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := toSong(song)
	c, err := s.q.GetBotCommentForItem(r.Context(), id)
	switch {
	case err == nil:
		out.Comment = &comment{Up: c.Up, Down: c.Down, CommentID: c.CommentID}
	case errors.Is(err, pgx.ErrNoRows):
	default:
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
