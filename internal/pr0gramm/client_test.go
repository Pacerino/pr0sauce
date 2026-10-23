package pr0gramm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const meCookie = "%7B%22id%22%3A%220123456789abcdefXYZ%22%7D"

func newTestClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(nil, srv.URL), srv
}

func setMe(t *testing.T, c *Client, srv *httptest.Server) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	c.http.Jar.SetCookies(u, []*http.Cookie{{Name: "me", Value: meCookie, Path: "/"}})
}

func TestInboxComments_SendsMarkAsReadFalse(t *testing.T) {
	var gotPath, gotMark, gotUA string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMark, gotUA = r.URL.Path, r.URL.Query().Get("markAsRead"), r.UserAgent()
		w.Write([]byte(`{"messages":[{"id":5,"itemId":9,"thumb":"t.jpg","image":null,"name":"bob","message":"hi","kind":"mention","type":"comment","created":1700000000,"read":0}],"atEnd":true}`))
	})
	msgs, err := c.InboxComments(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/inbox/comments" || gotMark != "false" {
		t.Fatalf("path=%q markAsRead=%q", gotPath, gotMark)
	}
	if gotUA != "pr0sauce-bot (+https://pr0sauce.info)" {
		t.Fatalf("ua=%q", gotUA)
	}
	if len(msgs) != 1 {
		t.Fatalf("len=%d", len(msgs))
	}
	m := msgs[0]
	if m.ID != 5 || m.ItemID != 9 || m.Name != "bob" || m.Message != "hi" || m.Kind != "mention" || m.Read || m.Created.Unix() != 1700000000 {
		t.Fatalf("bad mapping: %+v", m)
	}
}

func TestPostComment_SendsNonceFromMeCookie(t *testing.T) {
	var form url.Values
	var method string
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"success":true}`))
	})
	setMe(t, c, srv)
	if err := c.PostComment(context.Background(), 10, 20, "hello"); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || form.Get("_nonce") != "0123456789abcdef" || form.Get("itemId") != "10" || form.Get("parentId") != "20" || form.Get("comment") != "hello" {
		t.Fatalf("method=%s form=%v", method, form)
	}
}

func TestHasSession(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if c.HasSession() {
		t.Fatal("HasSession true without a me cookie")
	}
	setMe(t, c, srv)
	if !c.HasSession() {
		t.Fatal("HasSession false with a me cookie")
	}
}

func TestPostComment_NoSession(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Fatal("must not hit server") })
	if err := c.PostComment(context.Background(), 1, 2, "x"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err=%v", err)
	}
}

func TestAPIError_On403HTML(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`<html>Just a moment...</html>`))
	})
	_, err := c.InboxComments(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 403 {
		t.Fatalf("err=%v", err)
	}
	if IsRetryable(err) {
		t.Fatal("403 must not be retryable")
	}
}

func TestIsRetryable_CommentTooSoon(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"error":"commentTooSoon"}`))
	})
	setMe(t, c, srv)
	err := c.PostComment(context.Background(), 1, 2, "x")
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("err=%v", err)
	}
	if !IsRetryable(err) {
		t.Fatal("commentTooSoon must be retryable")
	}
}

func TestIsRetryable_StatusAndNetwork(t *testing.T) {
	if !IsRetryable(&APIError{Status: 503}) || !IsRetryable(&APIError{Status: 429}) || IsRetryable(&APIError{Status: 404}) {
		t.Fatal("status rules")
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	c := New(nil, srv.URL)
	srv.Close()
	_, err := c.InboxComments(context.Background())
	if err == nil || !IsRetryable(err) {
		t.Fatalf("network error should be retryable: %v", err)
	}
}

func TestIsAuthError(t *testing.T) {
	if !IsAuthError(&APIError{Status: 401}) || !IsAuthError(&APIError{Status: 403}) {
		t.Fatal("401/403 must be auth errors")
	}
	if IsAuthError(&APIError{Status: 500}) || IsAuthError(errors.New("x")) || IsAuthError(nil) {
		t.Fatal("non-auth must be false")
	}
	wrapped := errors.Join(errors.New("ctx"), &APIError{Status: 403})
	if !IsAuthError(wrapped) {
		t.Fatal("wrapped must match")
	}
}

func TestCookies_RoundTrip(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	setMe(t, c, srv)
	data, err := c.ExportCookies()
	if err != nil {
		t.Fatal(err)
	}
	c2 := New(nil, srv.URL)
	if err := c2.ImportCookies(data); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(srv.URL)
	cs := c2.http.Jar.Cookies(u)
	if len(cs) != 1 || cs[0].Name != "me" || cs[0].Value != meCookie {
		t.Fatalf("cookies=%v", cs)
	}
}

func TestUserComments_ParsesUnixTimes(t *testing.T) {
	var q url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Write([]byte(`{"comments":[{"id":1,"up":5,"down":2,"content":"c","created":1700000000,"itemId":77,"thumb":"a.jpg"}],"hasOlder":false,"hasNewer":true}`))
	})
	after := time.Unix(1690000000, 0)
	uc, err := c.UserComments(context.Background(), "sauce", after)
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("name") != "sauce" || q.Get("flags") != "31" || q.Get("after") != "1690000000" {
		t.Fatalf("query=%v", q)
	}
	if !uc.HasNewer || len(uc.Comments) != 1 {
		t.Fatalf("%+v", uc)
	}
	x := uc.Comments[0]
	if x.ID != 1 || x.ItemID != 77 || x.Up != 5 || x.Down != 2 || x.Content != "c" || x.Thumb != "a.jpg" || x.Created.Unix() != 1700000000 {
		t.Fatalf("%+v", x)
	}
}

func TestSessionValid_403IsFalseNil(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"error":"forbidden","code":403,"msg":"Nicht eingeloggt"}`))
	})
	ok, err := c.SessionValid(context.Background())
	if ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestSessionValid_OK(t *testing.T) {
	var path string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"identifier":"abc"}`))
	})
	ok, err := c.SessionValid(context.Background())
	if !ok || err != nil || path != "/user/identifier" {
		t.Fatalf("ok=%v err=%v path=%s", ok, err, path)
	}
}

func TestSessionValid_OtherErrorPropagates(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	ok, err := c.SessionValid(context.Background())
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestItem_PicksMatchingID(t *testing.T) {
	var q url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Write([]byte(`{"items":[{"id":7181445,"image":"x.mp4","thumb":"x.jpg"},{"id":7181444,"image":"2026/10/23/4891a7e031bc5868-h264-full_hd.mp4","thumb":"2026/10/23/4891a7e031bc5868.jpg"},{"id":7181443,"image":"y.mp4","thumb":"y.jpg"}]}`))
	})
	it, err := c.Item(context.Background(), 7181444)
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("id") != "7181444" || q.Get("flags") != "31" {
		t.Fatalf("query=%v", q)
	}
	if it.ID != 7181444 || it.Image != "2026/10/23/4891a7e031bc5868-h264-full_hd.mp4" || it.Thumb != "2026/10/23/4891a7e031bc5868.jpg" {
		t.Fatalf("%+v", it)
	}
}

func TestItem_NotFound(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[{"id":1,"image":"a","thumb":"b"}]}`))
	})
	if _, err := c.Item(context.Background(), 2); !errors.Is(err, ErrItemNotFound) {
		t.Fatalf("err=%v", err)
	}
}

func TestCaptcha(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"token":"tok","captcha":"data:image/png;base64,AAAA"}`))
	})
	cp, err := c.Captcha(context.Background())
	if err != nil || cp.Token != "tok" || cp.ImageURL != "data:image/png;base64,AAAA" {
		t.Fatalf("%+v %v", cp, err)
	}
}

func TestLogin_Success(t *testing.T) {
	var form url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"success":true,"ban":null,"identifier":"id"}`))
	})
	if err := c.Login(context.Background(), "n", "p", "", ""); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "password", "token", "captcha"} {
		if _, ok := form[k]; !ok {
			t.Fatalf("missing field %s: %v", k, form)
		}
	}
}

func TestLogin_Banned(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"ban":{"banned":true,"reason":"x","till":null}}`))
	})
	if err := c.Login(context.Background(), "n", "p", "", ""); !errors.Is(err, ErrBanned) {
		t.Fatalf("err=%v", err)
	}
}

func TestLogin_Failed(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"ban":null,"error":"invalidLogin"}`))
	})
	err := c.Login(context.Background(), "n", "p", "", "")
	if !errors.Is(err, ErrLoginFailed) {
		t.Fatalf("err=%v", err)
	}
	if got := err.Error(); !strings.Contains(got, "invalidLogin") {
		t.Fatalf("msg=%q", got)
	}
}

func TestAddTagsMarkReadSendMessage(t *testing.T) {
	var got []string
	var forms []url.Values
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = append(got, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		forms = append(forms, r.PostForm)
		w.Write([]byte(`{"success":true}`))
	})
	setMe(t, c, srv)
	ctx := context.Background()
	if err := c.AddTags(ctx, 3, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SendMessage(ctx, "bob", "yo"); err != nil {
		t.Fatal(err)
	}
	if forms[0].Get("tags") != "a,b" || forms[0].Get("itemId") != "3" || forms[0].Get("_nonce") != "0123456789abcdef" {
		t.Fatalf("tags form %v", forms[0])
	}
	if forms[1].Get("recipientName") != "bob" || forms[1].Get("comment") != "yo" || forms[1].Get("_nonce") != "0123456789abcdef" {
		t.Fatalf("msg form %v", forms[1])
	}
	_ = got
}

func TestMarkRead_PostsQueryWithFormNonce(t *testing.T) {
	var method, path string
	var query url.Values
	var form url.Values
	calls := 0
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		r.ParseForm()
		method, path, query, form = r.Method, r.URL.Path, r.URL.Query(), r.PostForm
		w.Write([]byte(`{"ts":1}`))
	})
	setMe(t, c, srv)
	if err := c.MarkRead(context.Background(), 123); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || method != http.MethodPost || path != "/inbox/read" {
		t.Fatalf("calls=%d method=%q path=%q", calls, method, path)
	}
	if query.Get("messageType") != "comment" || query.Get("messageId") != "123" {
		t.Fatalf("query %v", query)
	}
	if form.Get("_nonce") != "0123456789abcdef" {
		t.Fatalf("form %v", form)
	}
	if _, ok := form["messageType"]; ok {
		t.Fatalf("messageType leaked into form: %v", form)
	}
}

func TestMarkRead_NoSessionSendsNothing(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { calls++ })
	if err := c.MarkRead(context.Background(), 123); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err=%v", err)
	}
	if calls != 0 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestSessionValid_403HTMLIsErr(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`<html><body>Just a moment...</body></html>`))
	})
	ok, err := c.SessionValid(context.Background())
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestAPIError_BodyTruncatedStillDetectsCommentTooSoon(t *testing.T) {
	c, srv := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":false,"error":"commentTooSoon","pad":"` + strings.Repeat("x", 100000) + `"}`))
	})
	setMe(t, c, srv)
	err := c.PostComment(context.Background(), 1, 2, "x")
	var ae *APIError
	if !errors.As(err, &ae) || len(ae.Body) != 4096 {
		t.Fatalf("err=%v", err)
	}
	if !IsRetryable(err) {
		t.Fatal("must still be retryable")
	}
}

func TestTooLargeResponse(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"messages":[],"pad":"`))
		w.Write([]byte(strings.Repeat("x", 9<<20)))
		w.Write([]byte(`"}`))
	})
	_, err := c.InboxComments(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response too large") {
		t.Fatalf("err=%v", err)
	}
}

func TestIsRejected(t *testing.T) {
	yes := []error{
		&APIError{Status: 429},
		&APIError{Status: 200, Body: `{"success":false,"error":"commentTooSoon"}`},
		&APIError{Status: 400, Body: `{"error":"commentTooSoon"}`},
		fmt.Errorf("wrapped: %w", &APIError{Status: 429}),
	}
	for _, err := range yes {
		if !IsRejected(err) {
			t.Errorf("IsRejected(%v) = false; want true", err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	c := New(nil, srv.URL)
	srv.Close()
	_, netErr := c.InboxComments(context.Background())
	no := []error{
		nil,
		errors.New("x"),
		netErr,
		&APIError{Status: 500},
		&APIError{Status: 503, Body: "commentTooSoon"},
		&APIError{Status: 400, Body: `{"error":"other"}`},
		&APIError{Status: 403},
	}
	for _, err := range no {
		if IsRejected(err) {
			t.Errorf("IsRejected(%v) = true; want false", err)
		}
	}
}

func TestProfileInfo_Parses(t *testing.T) {
	var gotPath, gotName, gotFlags string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotName, gotFlags = r.URL.Path, r.URL.Query().Get("name"), r.URL.Query().Get("flags")
		w.Write([]byte(`{"user":{"id":42,"name":"Helix","registered":1}}`))
	})
	p, err := c.ProfileInfo(context.Background(), "helix")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != 42 || p.Name != "Helix" {
		t.Errorf("profile = %+v", p)
	}
	if gotPath != "/profile/info" || gotName != "helix" || gotFlags != "1" {
		t.Errorf("request = %q name=%q flags=%q", gotPath, gotName, gotFlags)
	}
}

func TestProfileInfo_Unknown(t *testing.T) {
	for _, status := range []int{404, 400} {
		c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte(`{"error":"notFound"}`))
		})
		if _, err := c.ProfileInfo(context.Background(), "nobody"); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("status %d: err = %v, want ErrUserNotFound", status, err)
		}
	}
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	if _, err := c.ProfileInfo(context.Background(), "x"); err == nil || errors.Is(err, ErrUserNotFound) {
		t.Errorf("500 err = %v", err)
	}
	c, _ = newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"user":null}`)) })
	if _, err := c.ProfileInfo(context.Background(), "x"); !errors.Is(err, ErrUserNotFound) {
		t.Errorf("null user err = %v", err)
	}
}
