// Package recognize is a client for the Shazam sidecar service.
package recognize

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var (
	// ErrNotAVideo is returned when the sidecar answers 404 (nothing to recognize).
	ErrNotAVideo = errors.New("recognize: not a video")
	// ErrBadURL is returned when the sidecar rejects the URL (422). Not retryable.
	ErrBadURL = errors.New("recognize: bad url")
)

const defaultTimeout = 90 * time.Second

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

type Result struct {
	Found         bool
	Title         string
	Artist        string
	Album         string
	CoverURL      string
	AppleMusicURL string
	ISRC          string
}

type response struct {
	Track struct {
		Title    string `json:"title"`
		ISRC     string `json:"isrc"`
		Subtitle string `json:"subtitle"`
		Images   struct {
			CoverArtHQ string `json:"coverarthq"`
			CoverArt   string `json:"coverart"`
		} `json:"images"`
		Sections []struct {
			Type     string `json:"type"`
			Metadata []struct {
				Title string `json:"title"`
				Text  string `json:"text"`
			} `json:"metadata"`
		} `json:"sections"`
		Hub struct {
			Options []struct {
				Caption string `json:"caption"`
				Actions []struct {
					Type string `json:"type"`
					URI  string `json:"uri"`
				} `json:"actions"`
			} `json:"options"`
		} `json:"hub"`
	} `json:"track"`
}

func (c *Client) Recognize(ctx context.Context, videoURL string) (Result, error) {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	body, err := json.Marshal(map[string]string{"url": videoURL})
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/recognize", bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("recognize: request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Result{}, fmt.Errorf("recognize: read body: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Result{}, ErrNotAVideo
	case resp.StatusCode == http.StatusUnprocessableEntity:
		return Result{}, fmt.Errorf("%w: %s", ErrBadURL, truncate(data))
	case resp.StatusCode != http.StatusOK:
		return Result{}, fmt.Errorf("recognize: sidecar status %d: %s", resp.StatusCode, truncate(data))
	}
	var r response
	if err := json.Unmarshal(data, &r); err != nil {
		return Result{}, fmt.Errorf("recognize: decode: %w", err)
	}
	t := r.Track
	res := Result{Found: t.Title != "", Title: t.Title, Artist: t.Subtitle, ISRC: t.ISRC, CoverURL: t.Images.CoverArtHQ}
	if res.CoverURL == "" {
		res.CoverURL = t.Images.CoverArt
	}
	for _, s := range t.Sections {
		if s.Type != "SONG" {
			continue
		}
		for _, m := range s.Metadata {
			if m.Title == "Album" {
				res.Album = m.Text
			}
		}
	}
	for _, o := range t.Hub.Options {
		if o.Caption != "OPEN IN" {
			continue
		}
		for _, a := range o.Actions {
			if a.Type == "applemusicopen" {
				res.AppleMusicURL = a.URI
			}
		}
	}
	return res, nil
}

func truncate(b []byte) string {
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

// VideoURL returns the CDN URL for an item's image path when it is an mp4,
// or "" for images and GIFs.
func VideoURL(image string) string {
	if !strings.HasSuffix(strings.ToLower(image), ".mp4") {
		return ""
	}
	return "https://vid.pr0gramm.com/" + image
}
