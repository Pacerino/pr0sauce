// Package pr0gramm is a small client for the pr0gramm.com JSON API.
package pr0gramm

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	// ErrBanned is returned by Login when the account is banned.
	ErrBanned = errors.New("pr0gramm: account banned")
	// ErrLoginFailed is returned by Login when the server rejects the credentials.
	ErrLoginFailed = errors.New("pr0gramm: login failed")
	// ErrNoSession is returned for POSTs when no usable "me" cookie is present.
	ErrNoSession = errors.New("pr0gramm: no session")
	// ErrItemNotFound is returned by Item when the id is not in the response.
	ErrItemNotFound = errors.New("pr0gramm: item not found")
)

// Captcha is a login captcha challenge.
type Captcha struct {
	Token    string
	ImageURL string // URL or data URI
}

// InboxComment is one message from the comment inbox.
type InboxComment struct {
	ID, ItemID int64
	Thumb      string
	Name       string
	Message    string
	Kind       string
	Created    time.Time
	Read       bool
}

// UserComment is one comment from a user profile.
type UserComment struct {
	ID, ItemID int64
	Up, Down   int
	Content    string
	Thumb      string
	Created    time.Time
}

// UserComments is a page of profile comments.
type UserComments struct {
	Comments []UserComment
	HasNewer bool
}

// Item is a post with its media paths (relative to the media hosts).
type Item struct {
	ID           int64
	Image, Thumb string
}

// APIError is returned for non-2xx responses and for 2xx {"success":false}.
type APIError struct {
	Path   string
	Status int
	Body   string
}

func (e *APIError) Error() string {
	body := e.Body
	if len(body) > 200 {
		body = body[:200] + "..."
	}
	return fmt.Sprintf("pr0gramm: %s: status %d: %s", e.Path, e.Status, strings.TrimSpace(body))
}
