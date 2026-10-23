// Package legacy performs the one-time import of the old pr0sauce database.
package legacy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Report summarises an import run.
type Report struct {
	Songs             int // distinct item_ids written
	Hits              int // songs with a non-NULL title
	DuplicatesDropped int // legacy item rows discarded by dedupe
	Comments          int

	DatesFromCreated, DatesFromComment, DatesFromUpdated, DatesEstimated int // recognized_at provenance
}

func nullIfEmpty(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// Import copies pr0music.items and pr0music.comments from src into dst in a
// single transaction. Unless force is set it refuses to run when dst already
// has songs. Reruns with force are safe because every write is an upsert.
func Import(ctx context.Context, src, dst *pgxpool.Pool, log *slog.Logger, force bool) (Report, error) {
	var rep Report
	if !force {
		var exists bool
		if err := dst.QueryRow(ctx, `select exists(select 1 from songs)`).Scan(&exists); err != nil {
			return rep, fmt.Errorf("legacy: check songs: %w", err)
		}
		if exists {
			return rep, errors.New("legacy: songs is not empty; use --force to import anyway")
		}
	}

	tx, err := dst.Begin(ctx)
	if err != nil {
		return rep, fmt.Errorf("legacy: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := db.New(tx)

	// The legacy comments table has no index on item_id, so the per-item
	// earliest comment time is computed once with a group by.
	rows, err := src.Query(ctx, `
with cm as (
  select item_id, min(created) as first_comment
  from pr0music.comments
  where deleted_at is null
  group by item_id
)
select i.id, i.item_id, i.title, i.album, i.artist, i.spotify_url, i.spotify_id, i.youtube_url, i.youtube_id,
       coalesce(i.provider, ''), i.created_at, cm.first_comment, i.updated_at
from pr0music.items i
left join cm on cm.item_id = i.item_id
where i.deleted_at is null
order by i.item_id, (coalesce(i.title, '') <> '') desc, i.created_at desc nulls last, i.id desc`)
	if err != nil {
		return rep, fmt.Errorf("legacy: query items: %w", err)
	}
	defer rows.Close()

	emit := func(p db.UpsertSongParams) error {
		if err := q.UpsertSong(ctx, p); err != nil {
			return fmt.Errorf("legacy: upsert song %d: %w", p.ItemID, err)
		}
		rep.Songs++
		if p.Title != nil {
			rep.Hits++
		}
		return nil
	}
	var (
		last        int64
		first       = true
		haveLast    bool
		lastDate    time.Time
		pending     []db.UpsertSongParams // leading rows with no date yet
		anyResolved bool
	)
	for rows.Next() {
		var (
			id, itemID                                     int64
			title, album, artist, spURL, spID, ytURL, ytID *string
			provider                                       string
			createdAt, firstComment, updatedAt             *time.Time
		)
		if err := rows.Scan(&id, &itemID, &title, &album, &artist, &spURL, &spID, &ytURL, &ytID, &provider,
			&createdAt, &firstComment, &updatedAt); err != nil {
			return rep, fmt.Errorf("legacy: scan item: %w", err)
		}
		if !first && itemID == last {
			rep.DuplicatesDropped++
			log.Info("dropped duplicate legacy item row", "item_id", itemID, "id", id)
			continue
		}
		first, last = false, itemID
		p := db.UpsertSongParams{
			ItemID: itemID, Title: nullIfEmpty(title), Artist: nullIfEmpty(artist), Album: nullIfEmpty(album),
			Provider: provider, SpotifyUrl: nullIfEmpty(spURL), SpotifyID: nullIfEmpty(spID),
			YoutubeUrl: nullIfEmpty(ytURL), YoutubeID: nullIfEmpty(ytID),
		}
		var resolved *time.Time
		switch {
		case createdAt != nil:
			resolved = createdAt
			rep.DatesFromCreated++
		case firstComment != nil:
			resolved = firstComment
			rep.DatesFromComment++
		case updatedAt != nil:
			resolved = updatedAt
			rep.DatesFromUpdated++
		}
		if resolved == nil {
			rep.DatesEstimated++
			if haveLast {
				p.RecognizedAt = lastDate
				if err := emit(p); err != nil {
					return rep, err
				}
			} else {
				pending = append(pending, p)
			}
			continue
		}
		haveLast, lastDate, anyResolved = true, *resolved, true
		for _, pp := range pending { // nothing lower exists: take the nearest higher
			pp.RecognizedAt = lastDate
			if err := emit(pp); err != nil {
				return rep, err
			}
		}
		pending = nil
		p.RecognizedAt = lastDate
		if err := emit(p); err != nil {
			return rep, err
		}
	}
	if err := rows.Err(); err != nil {
		return rep, fmt.Errorf("legacy: items: %w", err)
	}
	rows.Close()
	if len(pending) > 0 && !anyResolved {
		return rep, errors.New("legacy: no item has any usable date (created_at, comment or updated_at)")
	}
	log.Info("recognized_at sources", "created_at", rep.DatesFromCreated, "first_comment", rep.DatesFromComment,
		"updated_at", rep.DatesFromUpdated, "estimated", rep.DatesEstimated)

	crows, err := src.Query(ctx, `
select comment_id, item_id, coalesce(content, ''), coalesce(up, 0), coalesce(down, 0), thumb, created
from pr0music.comments
where deleted_at is null
order by comment_id`)
	if err != nil {
		return rep, fmt.Errorf("legacy: query comments: %w", err)
	}
	defer crows.Close()
	for crows.Next() {
		var p db.UpsertBotCommentParams
		var up, down int64
		var createdAt *time.Time
		if err := crows.Scan(&p.CommentID, &p.ItemID, &p.Content, &up, &down, &p.Thumb, &createdAt); err != nil {
			return rep, fmt.Errorf("legacy: scan comment: %w", err)
		}
		if up < math.MinInt32 || up > math.MaxInt32 {
			return rep, fmt.Errorf("legacy: comment_id %d: up=%d out of int32 range", p.CommentID, up)
		}
		if down < math.MinInt32 || down > math.MaxInt32 {
			return rep, fmt.Errorf("legacy: comment_id %d: down=%d out of int32 range", p.CommentID, down)
		}
		if createdAt == nil {
			return rep, fmt.Errorf("legacy: comment_id %d has NULL created", p.CommentID)
		}
		p.Up, p.Down, p.CreatedAt = int32(up), int32(down), *createdAt
		if err := q.UpsertBotComment(ctx, p); err != nil {
			return rep, fmt.Errorf("legacy: upsert comment %d: %w", p.CommentID, err)
		}
		rep.Comments++
	}
	if err := crows.Err(); err != nil {
		return rep, fmt.Errorf("legacy: comments: %w", err)
	}
	crows.Close()

	if err := tx.Commit(ctx); err != nil {
		return rep, fmt.Errorf("legacy: commit: %w", err)
	}
	return rep, nil
}
