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

type syncStats struct{ fetched, upserted, skipped, pages int }

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
	var floor time.Time
	if v, err := b.store.GetState(ctx, liveSinceKey); err == nil {
		ls, perr := time.Parse(time.RFC3339Nano, v)
		if perr != nil {
			return fmt.Errorf("bot: parse live_since %q: %w", v, perr)
		}
		floor = ls.Add(-syncWindow)
	} else if errors.Is(err, ErrNotFound) {
		floor = now.Add(-syncWindow)
	} else {
		return fmt.Errorf("bot: get live_since: %w", err)
	}
	// Clamp: at cutover the newest legacy comment can be months old, but the
	// bot posted nothing between then and live_since.
	start := floor
	if latest != nil && latest.Add(-syncOverlap).After(floor) {
		start = latest.Add(-syncOverlap)
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
	b.log.Info("comment sync", "fetched", st.fetched, "upserted", st.upserted, "skipped", st.skipped, "pages", st.pages,
		"from", start, "refreshed", refreshed)
	return nil
}

// syncFrom pages UserComments from after while HasNewer, upserting bot answers.
func (b *Bot) syncFrom(ctx context.Context, after time.Time, st *syncStats) error {
	// Paging assumes `after` is exclusive. Comments sharing a second across a
	// page boundary could be missed here; the 1h overlap of the next sync's
	// start and the 7-day refresh pass cover them.
	for {
		if st.pages >= syncMaxPages { // cap is per sync, shared by both passes
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
			if c.Up < math.MinInt32 || c.Up > math.MaxInt32 || c.Down < math.MinInt32 || c.Down > math.MaxInt32 {
				b.log.Warn("comment votes out of int32 range, skipping", "comment", c.ID, "up", c.Up, "down", c.Down)
				st.skipped++
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
	if err := b.store.UpsertBotComment(ctx, db.UpsertBotCommentParams{
		CommentID: c.ID, ItemID: c.ItemID, Content: c.Content, Up: int32(c.Up), Down: int32(c.Down),
		Thumb: nilIfEmpty(c.Thumb), CreatedAt: c.Created,
	}); err != nil {
		return fmt.Errorf("bot: upsert comment %d: %w", c.ID, err)
	}
	return nil
}
