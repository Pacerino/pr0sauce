package recognize

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func serve(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/recognize" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL}
}

func TestRecognize_ParsesKumiteFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/kumite.json")
	if err != nil {
		t.Fatal(err)
	}
	c := serve(t, 200, string(data))
	r, err := c.Recognize(context.Background(), "https://vid.pr0gramm.com/x.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Found || r.Title != `Kumite (From "Bloodsport")` || r.Artist != "Paul Hertzog" {
		t.Fatalf("got %+v", r)
	}
	if r.Album != `Kumite (From "Bloodsport") - Single` {
		t.Errorf("album %q", r.Album)
	}
	if r.CoverURL == "" || !strings.HasPrefix(r.AppleMusicURL, "https://music.apple.com/") {
		t.Errorf("cover/apple: %+v", r)
	}
}

func TestRecognize_NoMatch(t *testing.T) {
	r, err := serve(t, 200, `{}`).Recognize(context.Background(), "u")
	if err != nil || r.Found {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestRecognize_404IsErrNotAVideo(t *testing.T) {
	_, err := serve(t, 404, `{"error":"not_found"}`).Recognize(context.Background(), "u")
	if !errors.Is(err, ErrNotAVideo) {
		t.Fatalf("got %v", err)
	}
}

func TestRecognize_422And502(t *testing.T) {
	_, err := serve(t, 422, `{}`).Recognize(context.Background(), "u")
	if err == nil || errors.Is(err, ErrNotAVideo) {
		t.Fatalf("422: %v", err)
	}
	_, err = serve(t, 502, `{"error":"boom"}`).Recognize(context.Background(), "u")
	if err == nil || errors.Is(err, ErrBadURL) || !strings.Contains(err.Error(), "502") {
		t.Fatalf("502: %v", err)
	}
}

func TestVideoURL(t *testing.T) {
	cases := map[string]string{
		"2026/10/23/4891a7e031bc5868-h264-full_hd.mp4": "https://vid.pr0gramm.com/2026/10/23/4891a7e031bc5868-h264-full_hd.mp4",
		"2026/10/23/ABC.MP4":                           "https://vid.pr0gramm.com/2026/10/23/ABC.MP4",
		"2026/10/23/abc.jpg":                           "",
		"":                                             "",
	}
	for in, want := range cases {
		if got := VideoURL(in); got != want {
			t.Errorf("VideoURL(%q)=%q want %q", in, got, want)
		}
	}
}
