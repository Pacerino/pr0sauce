package pr0gramm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultBaseURL is the production API root.
	DefaultBaseURL = "https://pr0gramm.com/api"
	userAgent      = "pr0sauce-bot (+https://pr0sauce.info)"
	maxBody        = 8 << 20
	maxErrBody     = 4 << 10
)

// Client talks to the pr0gramm API. It holds the session in a cookie jar.
type Client struct {
	http    *http.Client
	baseURL string
	base    *url.URL
}

// New builds a client. A nil httpClient gets a 15s timeout; a missing cookie
// jar is added. An empty baseURL means DefaultBaseURL.
func New(httpClient *http.Client, baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	if httpClient.Jar == nil {
		cp := *httpClient
		jar, _ := cookiejar.New(nil)
		cp.Jar = jar
		httpClient = &cp
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		u = &url.URL{Scheme: "https", Host: "pr0gramm.com"}
	}
	return &Client{http: httpClient, baseURL: baseURL, base: u}
}

// do performs a request and returns the body for 2xx responses.
// Non-2xx becomes *APIError.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, form url.Values) ([]byte, error) {
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newAPIError(path, resp.StatusCode, data)
	}
	if len(data) > maxBody {
		return nil, fmt.Errorf("pr0gramm: %s: response too large", path)
	}
	return data, nil
}

// call is do plus the {"success":false} check and JSON decoding into out.
func (c *Client) call(ctx context.Context, method, path string, query, form url.Values, out any) error {
	data, err := c.do(ctx, method, path, query, form)
	if err != nil {
		return err
	}
	var probe struct {
		Success *bool `json:"success"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.Success != nil && !*probe.Success {
		return newAPIError(path, http.StatusOK, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("pr0gramm: %s: decode: %w", path, err)
		}
	}
	return nil
}

func (c *Client) post(ctx context.Context, path string, form url.Values) error {
	n, err := c.nonce()
	if err != nil {
		return err
	}
	form.Set("_nonce", n)
	return c.call(ctx, http.MethodPost, path, nil, form, nil)
}

func newAPIError(path string, status int, body []byte) *APIError {
	if len(body) > maxErrBody {
		body = body[:maxErrBody]
	}
	return &APIError{Path: path, Status: status, Body: string(body)}
}

// IsRetryable reports whether err is transient: network errors, 5xx, 429 and
// commentTooSoon.
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status >= 500 || ae.Status == http.StatusTooManyRequests ||
			strings.Contains(ae.Body, "commentTooSoon")
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	var ne net.Error
	return errors.As(err, &ne)
}

// IsRejected reports whether pr0gramm explicitly refused the request, so it
// definitely did not take effect: status 429, or a non-5xx answer whose body
// says commentTooSoon. Network errors and 5xx are ambiguous and return false.
func IsRejected(err error) bool {
	var ae *APIError
	if !errors.As(err, &ae) {
		return false
	}
	return ae.Status == http.StatusTooManyRequests ||
		(ae.Status < 500 && strings.Contains(ae.Body, "commentTooSoon"))
}

// IsAuthError reports whether err is an APIError with status 401 or 403.
// A caller should confirm with SessionValid, since Cloudflare challenges also
// return 403.
func IsAuthError(err error) bool {
	var ae *APIError
	return errors.As(err, &ae) && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden)
}

// Captcha fetches a login captcha.
func (c *Client) Captcha(ctx context.Context) (Captcha, error) {
	var r struct {
		Token   string `json:"token"`
		Captcha string `json:"captcha"`
	}
	if err := c.call(ctx, http.MethodGet, "/user/captcha", nil, nil, &r); err != nil {
		return Captcha{}, err
	}
	return Captcha{Token: r.Token, ImageURL: r.Captcha}, nil
}

// Login authenticates; the session lands in the cookie jar.
func (c *Client) Login(ctx context.Context, name, password, token, captcha string) error {
	form := url.Values{"name": {name}, "password": {password}, "token": {token}, "captcha": {captcha}}
	data, err := c.do(ctx, http.MethodPost, "/user/login", nil, form)
	if err != nil {
		return err
	}
	var r struct {
		Success bool            `json:"success"`
		Ban     json.RawMessage `json:"ban"`
		Error   string          `json:"error"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("pr0gramm: /user/login: decode: %w", err)
	}
	if r.Success {
		return nil
	}
	if b := strings.TrimSpace(string(r.Ban)); b != "" && b != "null" {
		return ErrBanned
	}
	return fmt.Errorf("%w: %s", ErrLoginFailed, r.Error)
}

// SessionValid checks the session via /user/identifier. A 401/403 whose body
// is a JSON object with "error" or "code" (the API's logged-out answer) yields
// (false, nil). Any other failure, including a Cloudflare HTML 403 challenge,
// is returned as an error.
func (c *Client) SessionValid(ctx context.Context) (bool, error) {
	_, err := c.do(ctx, http.MethodGet, "/user/identifier", nil, nil)
	if err == nil {
		return true, nil
	}
	var ae *APIError
	if errors.As(err, &ae) && IsAuthError(err) && isAPIJSONError(ae.Body) {
		return false, nil
	}
	return false, err
}

func isAPIJSONError(body string) bool {
	if !json.Valid([]byte(body)) {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &m) != nil {
		return false
	}
	_, hasErr := m["error"]
	_, hasCode := m["code"]
	return hasErr || hasCode
}

// InboxComments lists inbox comments without marking them read.
func (c *Client) InboxComments(ctx context.Context) ([]InboxComment, error) {
	var r struct {
		Messages []struct {
			ID      int64  `json:"id"`
			ItemID  int64  `json:"itemId"`
			Thumb   string `json:"thumb"`
			Name    string `json:"name"`
			Message string `json:"message"`
			Kind    string `json:"kind"`
			Created int64  `json:"created"`
			Read    int    `json:"read"`
		} `json:"messages"`
	}
	q := url.Values{"markAsRead": {"false"}}
	if err := c.call(ctx, http.MethodGet, "/inbox/comments", q, nil, &r); err != nil {
		return nil, err
	}
	out := make([]InboxComment, 0, len(r.Messages))
	for _, m := range r.Messages {
		out = append(out, InboxComment{
			ID: m.ID, ItemID: m.ItemID, Thumb: m.Thumb, Name: m.Name, Message: m.Message,
			Kind: m.Kind, Created: time.Unix(m.Created, 0), Read: m.Read == 1,
		})
	}
	return out, nil
}

// MarkRead marks one comment message as read.
func (c *Client) MarkRead(ctx context.Context, messageID int64) error {
	q := url.Values{"messageType": {"comment"}, "messageId": {strconv.FormatInt(messageID, 10)}}
	return c.call(ctx, http.MethodGet, "/inbox/read", q, nil, nil)
}

// PostComment posts a comment; parentID 0 means top level.
func (c *Client) PostComment(ctx context.Context, itemID, parentID int64, text string) error {
	return c.post(ctx, "/comments/post", url.Values{
		"itemId":   {strconv.FormatInt(itemID, 10)},
		"parentId": {strconv.FormatInt(parentID, 10)},
		"comment":  {text},
	})
}

// AddTags adds tags to an item.
func (c *Client) AddTags(ctx context.Context, itemID int64, tags []string) error {
	return c.post(ctx, "/tags/add", url.Values{
		"itemId": {strconv.FormatInt(itemID, 10)},
		"tags":   {strings.Join(tags, ",")},
	})
}

// SendMessage sends a private message.
func (c *Client) SendMessage(ctx context.Context, recipient, text string) error {
	return c.post(ctx, "/inbox/post", url.Values{"recipientName": {recipient}, "comment": {text}})
}

// UserComments lists a user's comments newer than after (zero means no bound).
func (c *Client) UserComments(ctx context.Context, name string, after time.Time) (UserComments, error) {
	q := url.Values{"name": {name}, "flags": {"31"}}
	if !after.IsZero() {
		q.Set("after", strconv.FormatInt(after.Unix(), 10))
	}
	var r struct {
		Comments []struct {
			ID      int64  `json:"id"`
			Up      int    `json:"up"`
			Down    int    `json:"down"`
			Content string `json:"content"`
			Created int64  `json:"created"`
			ItemID  int64  `json:"itemId"`
			Thumb   string `json:"thumb"`
		} `json:"comments"`
		HasNewer bool `json:"hasNewer"`
	}
	if err := c.call(ctx, http.MethodGet, "/profile/comments", q, nil, &r); err != nil {
		return UserComments{}, err
	}
	out := UserComments{HasNewer: r.HasNewer, Comments: make([]UserComment, 0, len(r.Comments))}
	for _, m := range r.Comments {
		out.Comments = append(out.Comments, UserComment{
			ID: m.ID, ItemID: m.ItemID, Up: m.Up, Down: m.Down, Content: m.Content,
			Thumb: m.Thumb, Created: time.Unix(m.Created, 0),
		})
	}
	return out, nil
}

// Item fetches a single post. The API returns a window of items around the
// id, so the matching entry is picked; ErrItemNotFound if absent.
func (c *Client) Item(ctx context.Context, id int64) (Item, error) {
	var r struct {
		Items []struct {
			ID    int64  `json:"id"`
			Image string `json:"image"`
			Thumb string `json:"thumb"`
		} `json:"items"`
	}
	q := url.Values{"id": {strconv.FormatInt(id, 10)}, "flags": {"31"}}
	if err := c.call(ctx, http.MethodGet, "/items/get", q, nil, &r); err != nil {
		return Item{}, err
	}
	for _, it := range r.Items {
		if it.ID == id {
			return Item{ID: it.ID, Image: it.Image, Thumb: it.Thumb}, nil
		}
	}
	return Item{}, ErrItemNotFound
}
