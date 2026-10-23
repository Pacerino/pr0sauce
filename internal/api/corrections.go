package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/deezer"
	"github.com/jackc/pgx/v5"
)

const (
	statusOpen         = "open"
	statusApproved     = "approved"
	statusAutoApproved = "auto_approved"
	statusRejected     = "rejected"

	sourceVote      = "vote"
	sourceModerator = "moderator"
	sourceRevert    = "revert"

	// Auto-approval needs both: at least autoApproveUp upvotes and a net score of autoApproveNet.
	autoApproveUp  = 5
	autoApproveNet = 5

	deezerTimeout = 5 * time.Second
)

var (
	errNotOpen        = errors.New("suggestion is not open")
	errBelowThreshold = errors.New("suggestion is below the auto-approval threshold")
	errNoSong         = errors.New("song not found")
)

// deezerHit is the best-effort Deezer refill of a correction; nil fields mean no match.
type deezerHit struct{ link, cover *string }

// searchDeezer looks up the corrected track. It must run before any
// transaction: no network call may hold the song row lock. Any failure means
// no link and no cover.
func (s *server) searchDeezer(ctx context.Context, artist, title string) deezerHit {
	if s.deezer == nil {
		return deezerHit{}
	}
	ctx, cancel := context.WithTimeout(ctx, deezerTimeout)
	defer cancel()
	link, cover, err := s.deezer.SearchTrack(ctx, artist, title)
	if err != nil {
		if !errors.Is(err, deezer.ErrNotFound) {
			s.log.Warn("deezer search failed", "err", err)
		}
		return deezerHit{}
	}
	return deezerHit{link: nilIfEmpty(link), cover: nilIfEmpty(cover)}
}

func meetsAutoApproval(t db.TallySuggestionRow) bool {
	return t.Up >= autoApproveUp && t.Net >= autoApproveNet
}

// decision describes how an open suggestion gets accepted.
type decision struct {
	status   string // approved | auto_approved
	source   string // history source: moderator | vote
	provider string // moderation | community
	reviewer *int64 // the moderator; nil for a community vote
	// byVote re-checks the tally under the lock and refuses below the threshold.
	byVote bool
}

func approvedByMod(mod int64) decision {
	return decision{status: statusApproved, source: sourceModerator, provider: providerModeration, reviewer: &mod}
}

var approvedByVotes = decision{status: statusAutoApproved, source: sourceVote, provider: providerCommunity, byVote: true}

// accept applies suggestion id to its song in ONE transaction that starts by
// locking the song row, so concurrent decisions on the same item serialise and
// exactly one wins. Under the lock it re-reads the suggestion (and, for votes,
// the tally) and returns errNotOpen or errBelowThreshold without changing
// anything if it no longer qualifies; the Deezer hit is then simply discarded.
func (s *server) accept(ctx context.Context, id int64, d decision, hit deezerHit) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	qt := s.q.WithTx(tx)

	sug, err := qt.GetSuggestion(ctx, id) // item_id never changes; needed to know which row to lock
	if err != nil {
		return err
	}
	if _, err := qt.LockSong(ctx, sug.ItemID); err != nil {
		return fmt.Errorf("lock song: %w", err)
	}
	if sug, err = qt.GetSuggestion(ctx, id); err != nil { // fresh read under the lock
		return err
	}
	if sug.Status != statusOpen {
		return errNotOpen
	}
	if d.byVote {
		t, err := qt.TallySuggestion(ctx, id)
		if err != nil {
			return err
		}
		if !meetsAutoApproval(t) {
			return errBelowThreshold
		}
	}
	if err := applyCorrection(ctx, qt, sug, d.source, d.reviewer, d.provider, hit); err != nil {
		return err
	}
	// Guarded by status='open': a concurrent reject or ban (which do not take
	// the song lock) makes this 0 rows, and the whole correction rolls back.
	n, err := qt.SetSuggestionStatus(ctx, db.SetSuggestionStatusParams{ID: id, Status: d.status, ReviewerID: d.reviewer})
	if err != nil {
		return err
	}
	if n != 1 {
		return errNotOpen
	}
	if err := qt.SupersedeOthers(ctx, db.SupersedeOthersParams{ItemID: sug.ItemID, ID: id}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// applyCorrection records the pre-change snapshot and writes the suggestion
// onto the song. The caller holds the song row lock inside its transaction.
func applyCorrection(ctx context.Context, qt *db.Queries, sug db.Suggestion, source string, actor *int64, provider string, hit deezerHit) error {
	snap, err := qt.SongSnapshot(ctx, sug.ItemID)
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	sid := sug.ID
	if _, err := qt.InsertHistory(ctx, db.InsertHistoryParams{
		ItemID: sug.ItemID, Snapshot: snap, Source: source, ActorID: actor, SuggestionID: &sid,
	}); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	title, artist := sug.Title, sug.Artist
	return qt.ApplyCorrection(ctx, db.ApplyCorrectionParams{
		ItemID: sug.ItemID, Title: &title, Artist: &artist, Album: sug.Album,
		Provider: provider, DeezerUrl: hit.link, CoverUrl: hit.cover,
	})
}

// maybeAutoApprove runs after every vote. A cheap unlocked pre-check skips the
// Deezer search for the common below-threshold case; accept re-checks
// everything under the song lock.
func (s *server) maybeAutoApprove(ctx context.Context, id int64) error {
	sug, err := s.q.GetSuggestion(ctx, id)
	if err != nil {
		return err
	}
	if sug.Status != statusOpen {
		return nil
	}
	t, err := s.q.TallySuggestion(ctx, id)
	if err != nil {
		return err
	}
	if !meetsAutoApproval(t) {
		return nil
	}
	hit := s.searchDeezer(ctx, sug.Artist, sug.Title)
	err = s.accept(ctx, id, approvedByVotes, hit)
	if errors.Is(err, errNotOpen) || errors.Is(err, errBelowThreshold) {
		return nil // someone else decided first, or a vote was withdrawn meanwhile
	}
	return err
}

// revertTo restores history row h onto its song in one transaction, first
// recording the current state as a 'revert' history row. Suggestion statuses
// are left alone. Returns the new history row's id.
func (s *server) revertTo(ctx context.Context, h db.SongHistory, actor int64) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit
	qt := s.q.WithTx(tx)

	if _, err := qt.LockSong(ctx, h.ItemID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, errNoSong
		}
		return 0, fmt.Errorf("lock song: %w", err)
	}
	snap, err := qt.SongSnapshot(ctx, h.ItemID)
	if err != nil {
		return 0, fmt.Errorf("snapshot: %w", err)
	}
	row, err := qt.InsertHistory(ctx, db.InsertHistoryParams{
		ItemID: h.ItemID, Snapshot: snap, Source: sourceRevert, ActorID: &actor,
	})
	if err != nil {
		return 0, fmt.Errorf("history: %w", err)
	}
	n, err := qt.RestoreSnapshot(ctx, db.RestoreSnapshotParams{ItemID: h.ItemID, Snapshot: h.Snapshot})
	if err != nil {
		return 0, fmt.Errorf("restore: %w", err)
	}
	if n != 1 {
		return 0, fmt.Errorf("restore: snapshot %d does not match item %d", h.ID, h.ItemID)
	}
	return row.ID, tx.Commit(ctx)
}
