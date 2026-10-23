package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
)

// Pr0Lookup resolves a pr0gramm user name to its profile.
type Pr0Lookup interface {
	ProfileInfo(ctx context.Context, name string) (pr0gramm.Profile, error)
}

// DeezerSearch finds a track link and cover by artist and title.
type DeezerSearch interface {
	SearchTrack(ctx context.Context, artist, title string) (link, cover string, err error)
}

const sessionCookiesKey = "session_cookies"

// DBPr0Lookup does read-only profile lookups with the bot's stored session
// cookies, loaded afresh on each call so a bot re-login is picked up.
type DBPr0Lookup struct {
	Q       *db.Queries
	BaseURL string
	Client  *http.Client
}

func (l DBPr0Lookup) ProfileInfo(ctx context.Context, name string) (pr0gramm.Profile, error) {
	c := pr0gramm.New(l.Client, l.BaseURL)
	v, err := l.Q.GetState(ctx, sessionCookiesKey)
	if err == nil {
		if err := c.ImportCookies([]byte(v)); err != nil {
			return pr0gramm.Profile{}, errors.New("api: stored pr0gramm cookies unreadable")
		}
	}
	return c.ProfileInfo(ctx, name)
}
