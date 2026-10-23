package pr0gramm

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// cookieURL is the URL cookies are scoped to, derived from the base URL.
func (c *Client) cookieURL() *url.URL {
	u := *c.base
	u.Path, u.RawPath, u.RawQuery, u.Fragment = "/", "", "", ""
	return &u
}

// ExportCookies serializes the session cookies as JSON of []http.Cookie.
// The jar only exposes name and value, so expiry is not preserved; callers
// must call SessionValid after ImportCookies.
func (c *Client) ExportCookies() ([]byte, error) {
	cs := c.http.Jar.Cookies(c.cookieURL())
	out := make([]http.Cookie, 0, len(cs))
	for _, ck := range cs {
		out = append(out, *ck)
	}
	return json.Marshal(out)
}

// ImportCookies restores cookies exported by ExportCookies. Expiry is not
// preserved, so callers must call SessionValid afterwards.
func (c *Client) ImportCookies(data []byte) error {
	var in []http.Cookie
	if err := json.Unmarshal(data, &in); err != nil {
		return err
	}
	cs := make([]*http.Cookie, len(in))
	for i := range in {
		ck := in[i]
		cs[i] = &ck
	}
	c.http.Jar.SetCookies(c.cookieURL(), cs)
	return nil
}

// HasSession reports whether the cookie jar holds a usable "me" cookie. It
// does not contact pr0gramm, so it says nothing about whether the session is
// still valid.
func (c *Client) HasSession() bool {
	_, err := c.nonce()
	return err == nil
}

// nonce returns the first 16 chars of the id in the "me" cookie.
func (c *Client) nonce() (string, error) {
	for _, ck := range c.http.Jar.Cookies(c.cookieURL()) {
		if ck.Name != "me" {
			continue
		}
		raw, err := url.QueryUnescape(ck.Value)
		if err != nil {
			return "", ErrNoSession
		}
		var me struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(raw), &me); err != nil || me.ID == "" {
			return "", ErrNoSession
		}
		if len(me.ID) > 16 {
			return me.ID[:16], nil
		}
		return me.ID, nil
	}
	return "", ErrNoSession
}
