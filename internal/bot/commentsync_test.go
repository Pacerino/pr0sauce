package bot_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Pacerino/pr0sauce/internal/bot"
	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
)

func botComment(id int64, up, down int, content string, created time.Time) pr0gramm.UserComment {
	return pr0gramm.UserComment{ID: id, ItemID: id + 1000, Up: up, Down: down, Content: content, Thumb: "t.jpg", Created: created}
}

func (e *env) seedComment(t *testing.T, id int64, up int32, created time.Time) {
	t.Helper()
	if err := e.q.UpsertBotComment(context.Background(), db.UpsertBotCommentParams{
		CommentID: id, ItemID: id + 1000, Content: "Es wurden alt", Up: up, CreatedAt: created,
	}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) commentVotes(t *testing.T, id int64) (up, down int32, found bool) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `select up, down from bot_comments where comment_id=$1`, id).Scan(&up, &down)
	if err != nil {
		return 0, 0, false
	}
	return up, down, true
}

func TestSyncComments_StartsFromLatest(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t) // live_since = testNow-1h
	latest := testNow.Add(-48 * time.Hour)
	e.seedComment(t, 1, 1, latest)
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.pr0.afters) == 0 || !e.pr0.afters[0].Equal(latest.Add(-time.Hour)) {
		t.Fatalf("afters = %v, want first %v", e.pr0.afters, latest.Add(-time.Hour))
	}
	floor := testNow.Add(-time.Hour - 7*24*time.Hour)
	for _, a := range e.pr0.afters {
		if a.Before(floor) {
			t.Errorf("after %v is before live_since-7d %v", a, floor)
		}
	}
	// The incremental pass started before now-7d? No: latest-1h is newer, so refresh runs.
	if len(e.pr0.afters) != 2 || !e.pr0.afters[1].Equal(testNow.Add(-7*24*time.Hour)) {
		t.Errorf("want refresh from now-7d, afters = %v", e.pr0.afters)
	}
}

func TestSyncComments_EmptyStartsFromLiveSinceMinus7d(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t)
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := testNow.Add(-time.Hour - 7*24*time.Hour)
	// start <= now-7d, so the refresh is skipped.
	if len(e.pr0.afters) != 1 || !e.pr0.afters[0].Equal(want) {
		t.Fatalf("afters = %v, want [%v]", e.pr0.afters, want)
	}
	if _, err := e.q.GetState(context.Background(), "live_since"); err != nil {
		t.Fatal(err)
	}
}

func TestSyncComments_UnsetLiveSinceNotSet(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := testNow.Add(-7 * 24 * time.Hour); len(e.pr0.afters) != 1 || !e.pr0.afters[0].Equal(want) {
		t.Fatalf("afters = %v, want [%v]", e.pr0.afters, want)
	}
	if _, err := e.q.GetState(context.Background(), "live_since"); err == nil {
		t.Fatal("SyncComments must not set live_since")
	}
}

func TestSyncComments_FiltersNonBotReplies(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t)
	c1 := testNow.Add(-time.Hour)
	e.pr0.userComments = func(time.Time) (pr0gramm.UserComments, error) {
		return pr0gramm.UserComments{Comments: []pr0gramm.UserComment{
			botComment(1, 5, 1, kumiteComment, c1),
			botComment(2, 9, 0, "danke sauce!", c1.Add(time.Minute)),
		}}, nil
	}
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := e.commentVotes(t, 1); !ok {
		t.Error("bot comment not stored")
	}
	if _, _, ok := e.commentVotes(t, 2); ok {
		t.Error("non-bot reply stored")
	}
}

func TestSyncComments_UpdatesVotes(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t)
	created := testNow.Add(-24 * time.Hour)
	e.seedComment(t, 1, 1, created)
	e.pr0.userComments = func(time.Time) (pr0gramm.UserComments, error) {
		return pr0gramm.UserComments{Comments: []pr0gramm.UserComment{botComment(1, 42, 3, kumiteComment, created)}}, nil
	}
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if up, down, _ := e.commentVotes(t, 1); up != 42 || down != 3 {
		t.Fatalf("votes = %d/%d, want 42/3", up, down)
	}
}

func TestSyncComments_PagesWhileHasNewer(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t)
	t1, t2 := testNow.Add(-3*time.Hour), testNow.Add(-2*time.Hour)
	e.pr0.userComments = func(after time.Time) (pr0gramm.UserComments, error) {
		switch {
		case after.Before(t1):
			return pr0gramm.UserComments{HasNewer: true, Comments: []pr0gramm.UserComment{botComment(1, 1, 0, kumiteComment, t1)}}, nil
		case after.Before(t2):
			return pr0gramm.UserComments{Comments: []pr0gramm.UserComment{botComment(2, 2, 0, kumiteComment, t2)}}, nil
		}
		return pr0gramm.UserComments{}, nil
	}
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		if _, _, ok := e.commentVotes(t, id); !ok {
			t.Errorf("comment %d not stored", id)
		}
	}
	if len(e.pr0.afters) != 2 || !e.pr0.afters[1].Equal(t1) {
		t.Errorf("afters = %v, want second == %v", e.pr0.afters, t1)
	}
}

func TestSyncComments_NoInfiniteLoop(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t)
	latest := testNow.Add(-24 * time.Hour) // start = latest-1h > now-7d, so the refresh runs too
	e.seedComment(t, 99, 1, latest)
	old := testNow.Add(-9 * 24 * time.Hour) // never after the cursor
	e.pr0.userComments = func(time.Time) (pr0gramm.UserComments, error) {
		return pr0gramm.UserComments{HasNewer: true, Comments: []pr0gramm.UserComment{botComment(1, 1, 0, kumiteComment, old)}}, nil
	}
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.pr0.afters) != 2 {
		t.Fatalf("fetches = %d (%v), want 2 (one per pass)", len(e.pr0.afters), e.pr0.afters)
	}
}

func TestSyncComments_PageCap(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t) // start before now-7d: no refresh
	n := 0
	e.pr0.userComments = func(time.Time) (pr0gramm.UserComments, error) {
		n++ // always advances, always HasNewer
		return pr0gramm.UserComments{HasNewer: true, Comments: []pr0gramm.UserComment{
			botComment(int64(n), 1, 0, kumiteComment, testNow.Add(-time.Hour-7*24*time.Hour).Add(time.Duration(n)*time.Second))}}, nil
	}
	if err := e.bot.SyncComments(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.pr0.afters) != 50 {
		t.Fatalf("fetches = %d, want cap 50", len(e.pr0.afters))
	}
}

func TestSyncComments_VoteOutOfRange(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t)
	e.pr0.userComments = func(time.Time) (pr0gramm.UserComments, error) {
		return pr0gramm.UserComments{Comments: []pr0gramm.UserComment{botComment(1, 1<<40, 0, kumiteComment, testNow)}}, nil
	}
	if err := e.bot.SyncComments(context.Background()); err == nil {
		t.Fatal("want range error")
	}
}

func TestSyncComments_AuthError(t *testing.T) {
	e := setup(t, func(c *config.Config) { c.Pr0User = "sauce" })
	e.goLive(t)
	e.pr0.userComments = func(time.Time) (pr0gramm.UserComments, error) {
		return pr0gramm.UserComments{}, &pr0gramm.APIError{Status: 401}
	}
	err := e.bot.SyncComments(context.Background())
	if !errors.Is(err, bot.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
}
