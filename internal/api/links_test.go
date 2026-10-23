package api

import (
	"net/url"
	"strings"
	"testing"
)

const kumiteApple = "https://music.apple.com/gb/album/kumite-from-bloodsport/1597617995?i=1597618301&mttnagencyid=s2n&app=music"

func TestSongLinkURL(t *testing.T) {
	got := songLinkURL(str(kumiteApple))
	if got == nil || *got != "https://song.link/i/1597618301" {
		t.Fatalf("got %v", got)
	}
	got = songLinkURL(str("https://geo.music.apple.com/gb/album/x/1?i=42"))
	if got == nil || *got != "https://song.link/i/42" {
		t.Fatalf("geo: got %v", got)
	}
}

func TestSongLinkURL_NoTrackID(t *testing.T) {
	for _, in := range []*string{
		str("https://music.apple.com/gb/album/x/1597617995"),
		str("https://music.apple.com/gb/album/x/1597617995?i=abc"),
		str("https://music.apple.com/gb/album/x/1?i="),
		str("https://music.apple.com/gb/album/x/1?i=123456789012345678901"),
		str("https://evil.example/gb/album/x/1?i=42"),
		str("https://music.apple.com.evil.example/x?i=42"),
		str(""), nil, str("::"),
	} {
		if got := songLinkURL(in); got != nil {
			t.Errorf("%v: want nil, got %q", in, *got)
		}
	}
}

const (
	escArtist = "AC/DC"
	escTitle  = "Rock & Roll Ain't Noise Pollution?"
)

func TestSpotifySearchURL_Escaping(t *testing.T) {
	const prefix = "https://open.spotify.com/search/"
	got := spotifySearchURL(escArtist, escTitle)
	if !strings.HasPrefix(got, prefix) {
		t.Fatal(got)
	}
	rest := strings.TrimPrefix(got, prefix)
	if strings.ContainsAny(rest, "&/?#") {
		t.Fatalf("raw reserved char: %s", got)
	}
	dec, err := url.PathUnescape(rest)
	if err != nil || dec != "AC/DC Rock & Roll Ain't Noise Pollution?" {
		t.Fatalf("%q %v", dec, err)
	}
	cy := spotifySearchURL("Новая", "патриотическая")
	dec, _ = url.PathUnescape(strings.TrimPrefix(cy, prefix))
	if dec != "Новая патриотическая" {
		t.Fatalf("%q", dec)
	}
	if spotifySearchURL("  ", "") != "" {
		t.Fatal("empty should be empty")
	}
	dec, _ = url.PathUnescape(strings.TrimPrefix(spotifySearchURL(" A ", "  B\t C "), prefix))
	if dec != "A B C" {
		t.Fatalf("%q", dec)
	}
}

func TestYouTubeSearchURL_Escaping(t *testing.T) {
	const prefix = "https://www.youtube.com/results?search_query="
	got := youtubeSearchURL(escArtist, escTitle)
	if !strings.HasPrefix(got, prefix) || strings.Count(got, "?") != 1 {
		t.Fatal(got)
	}
	if strings.Contains(got, "&") {
		t.Fatalf("raw &: %s", got)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || q.Get("search_query") != "AC/DC Rock & Roll Ain't Noise Pollution?" {
		t.Fatalf("%v %v", q, err)
	}
	cy := youtubeSearchURL("Новая", "патриотическая")
	u, _ = url.Parse(cy)
	if u.Query().Get("search_query") != "Новая патриотическая" {
		t.Fatal(cy)
	}
	if youtubeSearchURL("", " ") != "" {
		t.Fatal("empty should be empty")
	}
}
