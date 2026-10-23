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

	rows, err := src.Query(ctx, `
select id, item_id, title, album, artist, spotify_url, spotify_id, youtube_url, youtube_id,
       coalesce(provider, ''), created_at
from pr0music.items
where deleted_at is null
order by item_id, (coalesce(title, '') <> '') desc, created_at desc nulls last, id desc`)
	if err != nil {
		return rep, fmt.Errorf("legacy: query items: %w", err)
	}
	defer rows.Close()
	var last int64
	first := true
	for rows.Next() {
		var (
			id, itemID                                     int64
			title, album, artist, spURL, spID, ytURL, ytID *string
			provider                                       string
			createdAt                                      *time.Time
		)
		if err := rows.Scan(&id, &itemID, &title, &album, &artist, &spURL, &spID, &ytURL, &ytID, &provider, &createdAt); err != nil {
			return rep, fmt.Errorf("legacy: scan item: %w", err)
		}
		if !first && itemID == last {
			rep.DuplicatesDropped++
			log.Info("dropped duplicate legacy item row", "item_id", itemID, "id", id)
			continue
		}
		first, last = false, itemID
		if createdAt == nil {
			return rep, fmt.Errorf("legacy: item id %d (item_id %d) has NULL created_at", id, itemID)
		}
		recognized := *createdAt
		p := db.UpsertSongParams{
			ItemID: itemID, Title: nullIfEmpty(title), Artist: nullIfEmpty(artist), Album: nullIfEmpty(album),
			Provider: provider, SpotifyUrl: nullIfEmpty(spURL), SpotifyID: nullIfEmpty(spID),
			YoutubeUrl: nullIfEmpty(ytURL), YoutubeID: nullIfEmpty(ytID), RecognizedAt: recognized,
		}
		if err := q.UpsertSong(ctx, p); err != nil {
			return rep, fmt.Errorf("legacy: upsert song %d: %w", itemID, err)
		}
		rep.Songs++
		if p.Title != nil {
			rep.Hits++
		}
	}
	if err := rows.Err(); err != nil {
		return rep, fmt.Errorf("legacy: items: %w", err)
	}
	rows.Close()

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
