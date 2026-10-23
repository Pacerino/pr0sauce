package deezer_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Pacerino/pr0sauce/internal/deezer"
)

const kumite = `{"id":1568632112,"title":"Kumite (From \"Bloodsport\")","link":"https://www.deezer.com/track/1568632112"}`

func serve(t *testing.T, status int, body string, hits *atomic.Int32, path *atomic.Value) *deezer.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if path != nil {
			path.Store(r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return deezer.New(nil, srv.URL)
}

func TestDeezer_FoundReturnsLink(t *testing.T) {
	var path atomic.Value
	c := serve(t, 200, kumite, nil, &path)
	got, err := c.TrackURL(context.Background(), "TCAFX2160415")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://www.deezer.com/track/1568632112" {
		t.Errorf("link = %q", got)
	}
	if path.Load() != "/track/isrc:TCAFX2160415" {
		t.Errorf("path = %v", path.Load())
	}
}

func TestDeezer_UnknownISRCIsNotFound(t *testing.T) {
	c := serve(t, 200, `{"error":{"type":"DataException","message":"no data","code":800}}`, nil, nil)
	got, err := c.TrackURL(context.Background(), "ZZZ")
	if !errors.Is(err, deezer.ErrNotFound) || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDeezer_EmptyISRCNoRequest(t *testing.T) {
	var hits atomic.Int32
	c := serve(t, 200, kumite, &hits, nil)
	got, err := c.TrackURL(context.Background(), "")
	if !errors.Is(err, deezer.ErrNotFound) || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
	if hits.Load() != 0 {
		t.Errorf("made %d requests", hits.Load())
	}
}

func TestDeezer_Non2xxIsError(t *testing.T) {
	c := serve(t, 500, `oops`, nil, nil)
	got, err := c.TrackURL(context.Background(), "X")
	if err == nil || errors.Is(err, deezer.ErrNotFound) || got != "" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDeezer_ForeignLinkIsNotFound(t *testing.T) {
	c := serve(t, 200, `{"link":"https://evil.example/x"}`, nil, nil)
	if _, err := c.TrackURL(context.Background(), "X"); !errors.Is(err, deezer.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
