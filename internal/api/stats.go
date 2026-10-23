package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	statsTTL   = 60 * time.Second
	topN       = 10
	weeksShown = 26
)

type countedProvider struct {
	Provider string `json:"provider"`
	Count    int64  `json:"count"`
}
type weekCount struct {
	Week  string `json:"week"`
	Count int64  `json:"count"`
}
type topSong struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
	Count  int64  `json:"count"`
}
type topRequester struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

type statsResp struct {
	TotalLookups    int64             `json:"totalLookups"`
	Hits            int64             `json:"hits"`
	HitRate         float64           `json:"hitRate"`
	Today           int64             `json:"today"`
	Providers       []countedProvider `json:"providers"`
	PerWeek         []weekCount       `json:"perWeek"`
	TopSongs        []topSong         `json:"topSongs"`
	TopRequesters   []topRequester    `json:"topRequesters"`
	RequestersSince *string           `json:"requestersSince"`
}

// stats serves the cached response; the mutex is held while computing so
// concurrent requests trigger at most one computation per expiry.
func (s *server) stats(w http.ResponseWriter, r *http.Request) {
	s.statsMu.Lock()
	now := s.now()
	if s.statsCache == nil || !now.Before(s.statsExpires) {
		resp, err := s.compute(r.Context(), now)
		if err != nil {
			s.statsMu.Unlock()
			s.internalError(w, r, err)
			return
		}
		s.statsCache, s.statsExpires = resp, now.Add(statsTTL)
	}
	resp := s.statsCache
	s.statsMu.Unlock()
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) computeStats(ctx context.Context, now time.Time) (*statsResp, error) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, err
	}
	y, m, d := now.In(berlin).Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, berlin)

	out := &statsResp{
		Providers: []countedProvider{}, PerWeek: []weekCount{},
		TopSongs: []topSong{}, TopRequesters: []topRequester{},
	}
	if out.TotalLookups, err = s.q.CountSongs(ctx); err != nil {
		return nil, err
	}
	if out.Hits, err = s.q.CountHits(ctx); err != nil {
		return nil, err
	}
	if out.TotalLookups > 0 {
		out.HitRate = float64(out.Hits) / float64(out.TotalLookups)
	}
	if out.Today, err = s.q.CountSongsSince(ctx, midnight); err != nil {
		return nil, err
	}
	prov, err := s.q.ProviderShare(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range prov {
		out.Providers = append(out.Providers, countedProvider{p.Provider, p.Hits})
	}
	weeks, err := s.q.SongsPerWeek(ctx, now.AddDate(0, 0, -7*weeksShown))
	if err != nil {
		return nil, err
	}
	for _, wk := range weeks {
		if !wk.Week.Valid {
			continue
		}
		out.PerWeek = append(out.PerWeek, weekCount{wk.Week.Time.Format("2006-01-02"), wk.Songs})
	}
	ts, err := s.q.TopSongs(ctx, topN)
	if err != nil {
		return nil, err
	}
	for _, t := range ts {
		out.TopSongs = append(out.TopSongs, topSong{t.Title, t.Artist, t.Plays})
	}
	tr, err := s.q.TopRequesters(ctx, topN)
	if err != nil {
		return nil, err
	}
	for _, t := range tr {
		out.TopRequesters = append(out.TopRequesters, topRequester{t.Requester, t.Requests})
	}
	switch v, err := s.q.GetState(ctx, "live_since"); {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		if t, perr := time.Parse(time.RFC3339Nano, v); perr == nil {
			f := t.UTC().Format(time.RFC3339Nano)
			out.RequestersSince = &f
		} else {
			s.log.Warn("unparseable live_since state", "value", v)
		}
	}
	return out, nil
}
