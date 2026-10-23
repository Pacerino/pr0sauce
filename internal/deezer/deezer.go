// Package deezer looks up a track's Deezer link by ISRC via the public API.
package deezer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound means Deezer has no track for the ISRC.
var ErrNotFound = errors.New("deezer: track not found")

const (
	defaultBase    = "https://api.deezer.com"
	defaultTimeout = 10 * time.Second
	maxBody        = 1 << 20
	linkPrefix     = "https://www.deezer.com/"
)

type Client struct {
	http *http.Client
	base string
}

// New returns a client. An empty baseURL means the public API; a nil client
// gets a 10 s timeout.
func New(httpClient *http.Client, baseURL string) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if baseURL == "" {
		baseURL = defaultBase
	}
	return &Client{http: httpClient, base: strings.TrimRight(baseURL, "/")}
}

type trackResponse struct {
	Link  string    `json:"link"`
	Error *struct{} `json:"error"`
}

// TrackURL returns the Deezer track link for isrc, or ErrNotFound.
func (c *Client) TrackURL(ctx context.Context, isrc string) (string, error) {
	if isrc == "" {
		return "", ErrNotFound
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/track/isrc:"+url.PathEscape(isrc), nil)
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("deezer: request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("deezer: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("deezer: status %d", resp.StatusCode)
	}
	var r trackResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("deezer: decode: %w", err)
	}
	if r.Error != nil || !strings.HasPrefix(r.Link, linkPrefix) {
		return "", ErrNotFound
	}
	return r.Link, nil
}

// DeezerURL is TrackURL under the name the bot's Linker port uses.
func (c *Client) DeezerURL(ctx context.Context, isrc string) (string, error) {
	return c.TrackURL(ctx, isrc)
}
