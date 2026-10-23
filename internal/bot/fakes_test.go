package bot_test

import (
	"context"
	"sync"
	"time"

	"github.com/Pacerino/pr0sauce/internal/odesli"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/Pacerino/pr0sauce/internal/recognize"
)

type post struct {
	ItemID, ParentID int64
	Text             string
}

type pm struct {
	Recipient, Text string
}

type tagCall struct {
	ItemID int64
	Tags   []string
}

// fakePr0 records every call. The *Err funcs, when set, decide the error per call.
type fakePr0 struct {
	mu        sync.Mutex
	inbox     []pr0gramm.InboxComment
	items     map[int64]pr0gramm.Item
	inboxErr  error
	itemErr   func(id int64) error
	postErr   func(n int) error // n = 1-based PostComment call number
	pmErr     func(n int) error
	tagErr    error
	markErr   func(messageID int64) error
	itemCalls []int64
	posts     []post // every PostComment attempt, failed or not
	pms       []pm
	tags      []tagCall
	markReads []int64 // successful MarkRead calls only

	// userComments answers UserComments; afters records every call's after.
	userComments func(after time.Time) (pr0gramm.UserComments, error)
	afters       []time.Time
}

func newFakePr0() *fakePr0 { return &fakePr0{items: map[int64]pr0gramm.Item{}} }

func (f *fakePr0) InboxComments(context.Context) ([]pr0gramm.InboxComment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inboxErr != nil {
		return nil, f.inboxErr
	}
	return append([]pr0gramm.InboxComment(nil), f.inbox...), nil
}

func (f *fakePr0) MarkRead(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErr != nil {
		if err := f.markErr(id); err != nil {
			return err
		}
	}
	f.markReads = append(f.markReads, id)
	return nil
}

func (f *fakePr0) PostComment(_ context.Context, itemID, parentID int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, post{itemID, parentID, text})
	if f.postErr != nil {
		return f.postErr(len(f.posts))
	}
	return nil
}

func (f *fakePr0) AddTags(_ context.Context, itemID int64, tags []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tags = append(f.tags, tagCall{itemID, tags})
	return f.tagErr
}

func (f *fakePr0) SendMessage(_ context.Context, recipient, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pms = append(f.pms, pm{recipient, text})
	if f.pmErr != nil {
		return f.pmErr(len(f.pms))
	}
	return nil
}

func (f *fakePr0) UserComments(_ context.Context, _ string, after time.Time) (pr0gramm.UserComments, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.afters = append(f.afters, after)
	if f.userComments != nil {
		return f.userComments(after)
	}
	return pr0gramm.UserComments{}, nil
}

func (f *fakePr0) Item(_ context.Context, id int64) (pr0gramm.Item, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.itemCalls = append(f.itemCalls, id)
	if f.itemErr != nil {
		if err := f.itemErr(id); err != nil {
			return pr0gramm.Item{}, err
		}
	}
	it, ok := f.items[id]
	if !ok {
		return pr0gramm.Item{}, pr0gramm.ErrItemNotFound
	}
	return it, nil
}

// fakeRecognizer returns result/err for every call and records the URLs.
type fakeRecognizer struct {
	mu     sync.Mutex
	result recognize.Result
	err    error
	calls  []string
}

func (f *fakeRecognizer) Recognize(_ context.Context, url string) (recognize.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, url)
	return f.result, f.err
}

type fakeLinker struct {
	mu    sync.Mutex
	links odesli.Links
	err   error
	calls []string
}

func (f *fakeLinker) Links(_ context.Context, url string) (odesli.Links, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, url)
	return f.links, f.err
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}
