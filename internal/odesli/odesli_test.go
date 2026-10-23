package odesli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

const full = `{"linksByPlatform":{
"spotify":{"url":"https://open.spotify.com/track/abc","entityUniqueId":"SPOTIFY_SONG::abc"},
"youtube":{"url":"https://www.youtube.com/watch?v=xyz","entityUniqueId":"YOUTUBE_VIDEO::xyz"}}}`

const src = "https://music.apple.com/x?i=1&a=b"

func newClient(t *testing.T, status int, body string) (*Client, *int) {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if r.URL.Path != "/v1-alpha.1/links" || r.URL.Query().Get("songIfSingle") != "true" ||
			r.URL.Query().Get("url") != src {
			t.Errorf("bad request %s", r.URL.String())
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	c := New(srv.Client(), srv.URL)
	c.limiter = rate.NewLimiter(rate.Inf, 1)
	return c, &n
}

func TestLinks_ParsesIDs(t *testing.T) {
	c, _ := newClient(t, 200, full)
	l, err := c.Links(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	want := Links{"https://open.spotify.com/track/abc", "abc", "https://www.youtube.com/watch?v=xyz", "xyz"}
	if l != want {
		t.Fatalf("got %+v", l)
	}
}

func TestLinks_MissingYouTube(t *testing.T) {
	c, _ := newClient(t, 200, `{"linksByPlatform":{"spotify":{"url":"u","entityUniqueId":"SPOTIFY_SONG::abc"}}}`)
	l, err := c.Links(context.Background(), src)
	if err != nil || l.YouTubeURL != "" || l.YouTubeID != "" || l.SpotifyID != "abc" {
		t.Fatalf("got %+v, %v", l, err)
	}
}

func TestLinks_Non2xx(t *testing.T) {
	c, _ := newClient(t, 429, `{}`)
	if _, err := c.Links(context.Background(), src); err == nil {
		t.Fatal("expected error")
	}
}

func TestLinks_RateLimiterShared(t *testing.T) {
	c, n := newClient(t, 200, full)
	c.limiter = rate.NewLimiter(rate.Every(time.Hour), 1)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := c.Links(ctx, src); err != nil {
		t.Fatalf("first call: %v", err)
	}
	_, err := c.Links(ctx, src)
	if err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatalf("second call should fail with deadline error, got %v", err)
	}
	if *n != 1 {
		t.Fatalf("server hit %d times", *n)
	}
}
