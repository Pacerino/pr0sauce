// Package odesli looks up streaming links via the song.link API.
package odesli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

const DefaultBaseURL = "https://api.song.link"

type Links struct {
	SpotifyURL, SpotifyID string
	YouTubeURL, YouTubeID string
}

type Client struct {
	http    *http.Client
	baseURL string
	limiter *rate.Limiter // one per client: 10 requests/minute, burst 1
}

func New(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		http:    httpClient,
		baseURL: strings.TrimRight(baseURL, "/"),
		limiter: rate.NewLimiter(rate.Every(6*time.Second), 1),
	}
}

type platform struct {
	URL            string `json:"url"`
	EntityUniqueID string `json:"entityUniqueId"`
}

func (p platform) id() string {
	if _, id, ok := strings.Cut(p.EntityUniqueID, "::"); ok {
		return id
	}
	return ""
}

func (c *Client) Links(ctx context.Context, sourceURL string) (Links, error) {
	if err := c.limiter.Wait(ctx); err != nil {
		return Links{}, fmt.Errorf("odesli: rate limit: %w", err)
	}
	u := c.baseURL + "/v1-alpha.1/links?songIfSingle=true&url=" + url.QueryEscape(sourceURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return Links{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Links{}, fmt.Errorf("odesli: request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return Links{}, fmt.Errorf("odesli: status %d", resp.StatusCode)
	}
	var body struct {
		LinksByPlatform struct {
			Spotify platform `json:"spotify"`
			YouTube platform `json:"youtube"`
		} `json:"linksByPlatform"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Links{}, fmt.Errorf("odesli: decode: %w", err)
	}
	s, y := body.LinksByPlatform.Spotify, body.LinksByPlatform.YouTube
	return Links{SpotifyURL: s.URL, SpotifyID: s.id(), YouTubeURL: y.URL, YouTubeID: y.id()}, nil
}
