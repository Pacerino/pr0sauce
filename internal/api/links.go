package api

import (
	"net/url"
	"regexp"
	"strings"
)

var appleTrackIDRe = regexp.MustCompile(`^[0-9]{1,20}$`)

// songLinkURL builds a song.link URL from an Apple Music track URL's `i`
// query parameter. It returns nil unless the URL is a genuine Apple Music
// link with a numeric track id.
func songLinkURL(apple *string) *string {
	if apple == nil || *apple == "" {
		return nil
	}
	u, err := url.Parse(*apple)
	if err != nil {
		return nil
	}
	switch u.Hostname() {
	case "music.apple.com", "geo.music.apple.com":
	default:
		return nil
	}
	id := u.Query().Get("i")
	if !appleTrackIDRe.MatchString(id) {
		return nil
	}
	out := "https://song.link/i/" + id
	return &out
}

func searchTerm(artist, title string) string {
	return strings.Join(strings.Fields(artist+" "+title), " ")
}

// spotifySearchURL returns "" when there is nothing to search for.
func spotifySearchURL(artist, title string) string {
	t := searchTerm(artist, title)
	if t == "" {
		return ""
	}
	// PathEscape leaves "&" (and ";", "=", "+", "$", ",", ":", "@") as-is,
	// which are legal in a path segment; encode "&" too so no raw
	// query-style delimiter appears in the URL.
	return "https://open.spotify.com/search/" +
		strings.ReplaceAll(url.PathEscape(t), "&", "%26")
}

// youtubeSearchURL returns "" when there is nothing to search for.
func youtubeSearchURL(artist, title string) string {
	t := searchTerm(artist, title)
	if t == "" {
		return ""
	}
	return "https://www.youtube.com/results?search_query=" + url.QueryEscape(t)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
