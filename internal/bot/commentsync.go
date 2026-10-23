package bot

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
)

const (
	syncOverlap   = time.Hour
	syncWindow    = 7 * 24 * time.Hour
	syncMaxPages  = 50
	botCommentTag = "Es wurden" // legacy filter: only the bot's answers carry it
)

type syncStats struct{ fetched, upserted, pages int }

// SyncComments mirrors the bot's own pr0gramm comments (and their votes) into
// bot_comments incrementally: from just before the newest stored comment, then
// once more from 7 days ago to refresh recent votes. It never sets live_since.
func (b *Bot) SyncComments(ctx context.Context) error {
	now := b.clock.Now()
	latest, err := b.store.LatestBotCommentTime(ctx)
	if err != nil {
		return fmt.Errorf("bot: latest bot comment: %w", err)
	}
	refreshFrom := now.Add(-syncWindow)
	var start time.Time
	if latest != nil {
		start = latest.Add(-syncOverlap)
	} else {
		base := now
		if v, err := b.store.GetState(ctx, liveSinceKey); err == nil {
			ls, perr := time.Parse(time.RFC3339Nano, v)
			if perr != nil {
				return fmt.Errorf("bot: parse live_since %q: %w", v, perr)
			}
			base = ls
		} else if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("bot: get live_since: %w", err)
		}
		start = base.Add(-syncWindow)
	}

	var st syncStats
	if err := b.syncFrom(ctx, start, &st); err != nil {
		return err
	}
	refreshed := false
	if start.After(refreshFrom) {
		refreshed = true
		if err := b.syncFrom(ctx, refreshFrom, &st); err != nil {
			return err
		}
	}
	b.log.Info("comment sync", "fetched", st.fetched, "upserted", st.upserted, "pages", st.pages,
		"from", start, "refreshed", refreshed)
	return nil
}

// syncFrom pages UserComments from after while HasNewer, upserting bot answers.
func (b *Bot) syncFrom(ctx context.Context, after time.Time, st *syncStats) error {
	for page := 0; ; page++ {
		if page >= syncMaxPages {
			b.log.Warn("comment sync page cap hit, rest is picked up next sync", "cap", syncMaxPages, "after", after)
			return nil
		}
		res, err := b.pr0.UserComments(ctx, b.cfg.Pr0User, after)
		if err != nil {
			return b.pr0Err("user comments", err)
		}
		st.pages++
		st.fetched += len(res.Comments)
		var newest time.Time
		for _, c := range res.Comments {
			if c.Created.After(newest) {
				newest = c.Created
			}
			if !strings.Contains(c.Content, botCommentTag) {
				continue
			}
			if err := b.upsertComment(ctx, c); err != nil {
				return err
			}
			st.upserted++
		}
		if !res.HasNewer {
			return nil
		}
		if !newest.After(after) {
			b.log.Warn("comment sync page did not advance, stopping", "after", after, "newest", newest)
			return nil
		}
		after = newest
	}
}

func (b *Bot) upsertComment(ctx context.Context, c pr0gramm.UserComment) error {
	if c.Up < math.MinInt32 || c.Up > math.MaxInt32 {
		return fmt.Errorf("bot: comment %d: up=%d out of int32 range", c.ID, c.Up)
	}
	if c.Down < math.MinInt32 || c.Down > math.MaxInt32 {
		return fmt.Errorf("bot: comment %d: down=%d out of int32 range", c.ID, c.Down)
	}
	if err := b.store.UpsertBotComment(ctx, db.UpsertBotCommentParams{
		CommentID: c.ID, ItemID: c.ItemID, Content: c.Content, Up: int32(c.Up), Down: int32(c.Down),
		Thumb: nilIfEmpty(c.Thumb), CreatedAt: c.Created,
	}); err != nil {
		return fmt.Errorf("bot: upsert comment %d: %w", c.ID, err)
	}
	return nil
}
