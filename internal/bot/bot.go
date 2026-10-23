// Package bot is the pr0sauce request state machine: it records @sauce
// mentions durably, answers each at most once, and marks it read on
// pr0gramm only after it was handled.
//
// Request lifecycle (requests.status):
//
//	pending  ── claim (MarkReplying) ──► replying ── reply ok ──► done
//	   │  ▲                                 │
//	   │  └── explicit rejection (429, commentTooSoon): RetryFromReplying
//	   │                                    └── any other reply error ──► failed
//	   ├── retryable error before claim: BumpAttempt (backoff), failed at MaxAttempts
//	   ├── non-retryable error before claim ──► failed
//	   └── banned / pre-live / not a video / item gone ──► skipped
//
// A request left in replying by a crash is failed at startup by
// RecoverReplying and never posted again.
//
// Remaining crash window: a new item's song is stored (UpsertSong) before the
// request is claimed. A crash between the two leaves the request pending with
// the song already known, so the retry takes the known-item path and the
// requester gets the "already asked" PM instead of the comment. That is a
// different reply, never a double answer and never a lost mention.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // Europe/Berlin must resolve in distroless images

	"github.com/Pacerino/pr0sauce/internal/config"
	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/deezer"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/Pacerino/pr0sauce/internal/recognize"
)

// ErrAuth wraps pr0gramm 401/403 answers. The caller should re-check the
// session; the batch that hit it stopped immediately.
var ErrAuth = errors.New("bot: pr0gramm auth error")

const (
	liveSinceKey = "live_since"
	batchSize    = 5

	markReadBatch       = 10 // MarkRead calls per tick
	markReadBaseBackoff = 30 * time.Second
	markReadMaxBackoff  = time.Hour
	markReadMaxAttempts = 8
	markReadGiveUp      = 100 * 365 * 24 * time.Hour
	baseBackoff         = time.Minute
	maxBackoff          = time.Hour

	statusPending = "pending"
	statusDone    = "done"
	statusFailed  = "failed"
	statusSkipped = "skipped"

	kindComment = "comment"
	kindPM      = "pm"

	// rejectedCommentPrefix / rejectedPMPrefix mark last_error of a request
	// released from replying because pr0gramm explicitly refused the reply.
	// The retry sends the same kind again: a refused comment is re-posted as
	// a comment (not the "someone already asked" PM), a refused PM stays a PM.
	rejectedCommentPrefix = "rejected comment: "
	rejectedPMPrefix      = "rejected pm: "
)

// Deps are the bot's collaborators. A nil Clock means the system clock.
type Deps struct {
	Store      Store
	Pr0        Pr0
	Recognizer Recognizer
	Linker     Linker
	Clock      Clock
}

// Bot handles mentions. Its methods are meant to be called from one goroutine.
type Bot struct {
	store  Store
	pr0    Pr0
	rec    Recognizer
	links  Linker
	clock  Clock
	cfg    config.Config
	log    *slog.Logger
	berlin *time.Location
}

// New builds a Bot. It fails when Europe/Berlin cannot be loaded.
func New(deps Deps, cfg config.Config, log *slog.Logger) (*Bot, error) {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, fmt.Errorf("bot: load Europe/Berlin: %w", err)
	}
	if deps.Store == nil || deps.Pr0 == nil || deps.Recognizer == nil || deps.Linker == nil {
		return nil, errors.New("bot: Store, Pr0, Recognizer and Linker are required")
	}
	if deps.Clock == nil {
		deps.Clock = systemClock{}
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.MaxAttempts <= 0 {
		return nil, fmt.Errorf("bot: MaxAttempts must be > 0, got %d", cfg.MaxAttempts)
	}
	return &Bot{
		store: deps.Store, pr0: deps.Pr0, rec: deps.Recognizer, links: deps.Linker, clock: deps.Clock,
		cfg: cfg, log: log, berlin: loc,
	}, nil
}

// Ingest polls the inbox once and records every new unread @sauce mention.
// On the very first call it sets live_since; mentions older than that, and
// mentions by banned users, are recorded as skipped.
func (b *Bot) Ingest(ctx context.Context) error {
	now := b.clock.Now()
	liveSince, err := b.liveSince(ctx, now)
	if err != nil {
		return err
	}
	msgs, err := b.pr0.InboxComments(ctx)
	if err != nil {
		return b.pr0Err("inbox", err)
	}
	for _, m := range msgs {
		if m.Kind != "mention" || m.Read || !strings.Contains(strings.ToLower(m.Message), "@sauce") {
			continue
		}
		status, reason := statusPending, ""
		switch {
		case b.cfg.IsBanned(m.Name):
			status, reason = statusSkipped, "banned requester"
		case m.Created.Before(liveSince):
			status, reason = statusSkipped, "mention predates live_since"
		}
		inserted, err := b.store.InsertRequestIgnore(ctx, db.InsertRequestIgnoreParams{
			MessageID: m.ID, ItemID: m.ItemID, Requester: m.Name, Thumb: m.Thumb,
			MentionedAt: m.Created, Status: status, NextAttemptAt: now,
		})
		if err != nil {
			return fmt.Errorf("bot: record mention %d: %w", m.ID, err)
		}
		if inserted {
			b.log.Info("mention recorded", "message", m.ID, "item", m.ItemID, "requester", m.Name,
				"status", status, "reason", reason)
		}
	}
	return nil
}

// liveSince returns bot_state.live_since, setting it to now (whole seconds,
// like pr0gramm's timestamps) when missing.
func (b *Bot) liveSince(ctx context.Context, now time.Time) (time.Time, error) {
	v, err := b.store.GetState(ctx, liveSinceKey)
	if errors.Is(err, ErrNotFound) {
		ls := now.UTC().Truncate(time.Second)
		if err := b.store.SetState(ctx, db.SetStateParams{Key: liveSinceKey, Value: ls.Format(time.RFC3339Nano)}); err != nil {
			return time.Time{}, fmt.Errorf("bot: set live_since: %w", err)
		}
		b.log.Info("first run: mentions before live_since are skipped", "live_since", ls)
		return ls, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("bot: get live_since: %w", err)
	}
	ls, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return time.Time{}, fmt.Errorf("bot: parse live_since %q: %w", v, err)
	}
	return ls, nil
}

// ProcessDue handles up to 5 due requests, one after another. It returns an
// ErrAuth-wrapped error as soon as pr0gramm rejects the session, and a plain
// error when the store fails; per-request failures are recorded, not returned.
func (b *Bot) ProcessDue(ctx context.Context) error {
	due, err := b.store.DueRequests(ctx, db.DueRequestsParams{Now: b.clock.Now(), Lim: batchSize})
	if err != nil {
		return fmt.Errorf("bot: due requests: %w", err)
	}
	// Without a session nonce every reply would fail after the claim and be
	// left failed for good. Report it up front; the requests stay pending.
	if len(due) > 0 && !b.pr0.HasSession() {
		return fmt.Errorf("%w: process: %w", ErrAuth, pr0gramm.ErrNoSession)
	}
	for _, r := range due {
		if err := ctx.Err(); err != nil {
			return err
		}
		stop, err := b.process(ctx, r)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	return nil
}

// process handles one request. stop ends the batch without an error (pr0gramm
// is rate limiting us); err ends it with one.
func (b *Bot) process(ctx context.Context, r db.Request) (stop bool, err error) {
	log := b.log.With("request", r.ID, "message", r.MessageID, "item", r.ItemID, "requester", r.Requester, "attempt", r.Attempts+1)

	if b.cfg.IsBanned(r.Requester) {
		return false, b.finish(ctx, log, r, statusSkipped, nil, "banned requester")
	}

	song, err := b.store.GetSong(ctx, r.ItemID)
	switch {
	case err == nil:
		// A request released after an explicit rejection still owes its comment.
		asComment := r.LastError != nil && strings.HasPrefix(*r.LastError, rejectedCommentPrefix)
		return b.reply(ctx, log, r, song, asComment)
	case !errors.Is(err, ErrNotFound):
		return false, fmt.Errorf("bot: get song %d: %w", r.ItemID, err)
	}

	item, err := b.pr0.Item(ctx, r.ItemID)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return false, ctx.Err()
		case pr0gramm.IsAuthError(err):
			log.Error("pr0gramm auth error before reply, request stays pending", "err", err)
			return false, b.pr0Err("item", err)
		case errors.Is(err, pr0gramm.ErrItemNotFound):
			return false, b.finish(ctx, log, r, statusSkipped, nil, "item not found")
		case pr0gramm.IsRetryable(err):
			return false, b.retry(ctx, log, r, err)
		default:
			return false, b.finish(ctx, log, r, statusFailed, nil, "item lookup: "+err.Error())
		}
	}
	video := recognize.VideoURL(item.Image)
	if video == "" {
		return false, b.finish(ctx, log, r, statusSkipped, nil, "not a video")
	}

	log.Info("recognizing", "url", video)
	res, err := b.rec.Recognize(ctx, video)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return false, ctx.Err()
		case errors.Is(err, recognize.ErrNotAVideo):
			return false, b.finish(ctx, log, r, statusSkipped, nil, "not a video")
		case errors.Is(err, recognize.ErrBadURL):
			return false, b.finish(ctx, log, r, statusFailed, nil, err.Error())
		default:
			return false, b.retry(ctx, log, r, err)
		}
	}

	song = db.Song{ItemID: r.ItemID, Provider: "shazam", RecognizedAt: b.clock.Now()}
	if res.Found {
		song.Title, song.Artist, song.Album = nilIfEmpty(res.Title), nilIfEmpty(res.Artist), nilIfEmpty(res.Album)
		song.CoverUrl = nilIfEmpty(res.CoverURL)
		song.AppleMusicUrl, song.Isrc = nilIfEmpty(res.AppleMusicURL), nilIfEmpty(res.ISRC)
		if res.ISRC != "" {
			u, err := b.links.DeezerURL(ctx, res.ISRC)
			switch {
			case err == nil:
				song.DeezerUrl = nilIfEmpty(u)
			case errors.Is(err, deezer.ErrNotFound):
			default:
				log.Warn("deezer lookup failed, storing without link", "err", err)
			}
		}
		log.Info("recognized", "title", res.Title, "artist", res.Artist)
	} else {
		log.Info("no match")
	}
	if err := b.store.UpsertSong(ctx, db.UpsertSongParams{
		ItemID: song.ItemID, Title: song.Title, Artist: song.Artist, Album: song.Album, Provider: song.Provider,
		CoverUrl: song.CoverUrl, RecognizedAt: song.RecognizedAt,
		AppleMusicUrl: song.AppleMusicUrl, Isrc: song.Isrc, DeezerUrl: song.DeezerUrl,
	}); err != nil {
		return false, fmt.Errorf("bot: upsert song %d: %w", r.ItemID, err)
	}
	return b.reply(ctx, log, r, song, true)
}

// reply claims the request and answers it: a comment under the mention (with
// tags on a hit) or a PM. Nothing here is ever repeated unless pr0gramm
// explicitly refused the post.
func (b *Bot) reply(ctx context.Context, log *slog.Logger, r db.Request, song db.Song, asComment bool) (bool, error) {
	// The claim and everything after it must not be cut short by shutdown:
	// a cancelled claim right after UpsertSong would widen the crash window
	// described in the package doc, and a cancelled post or status write
	// would leave the outcome unrecorded. Every call below is bounded by its
	// client's own timeout.
	ctx = context.WithoutCancel(ctx)
	n, err := b.store.MarkReplying(ctx, r.ID)
	if err != nil {
		return false, fmt.Errorf("bot: claim request %d: %w", r.ID, err)
	}
	if n == 0 {
		log.Warn("request already claimed elsewhere, not replying")
		return false, nil
	}
	// If a Store call below fails after a successful post, the request stays
	// in replying. That is safe: RecoverReplying marks it failed at the next
	// start, and nothing ever re-posts a replying request.

	link := b.cfg.PublicBaseURL + "/" + strconv.FormatInt(r.ItemID, 10)
	kind := kindPM
	var postErr error
	if asComment {
		kind = kindComment
		var text string
		if song.Title != nil {
			text = fmt.Sprintf(commentHit, deref(song.Title), deref(song.Artist), deref(song.Album), link, b.checkedAt())
		} else {
			text = fmt.Sprintf(commentMiss, b.checkedAt(), link)
		}
		postErr = b.pr0.PostComment(ctx, r.ItemID, r.MessageID, text)
	} else {
		var text string
		if song.Title != nil {
			text = fmt.Sprintf(pmKnownHit, r.Requester, r.ItemID, deref(song.Title), deref(song.Album), deref(song.Artist), link)
		} else {
			text = fmt.Sprintf(pmKnownMiss, r.Requester, r.ItemID, link)
		}
		postErr = b.pr0.SendMessage(ctx, r.Requester, text)
	}

	if postErr != nil {
		if pr0gramm.IsRejected(postErr) {
			return true, b.retryFromReplying(ctx, log, r, kind, postErr)
		}
		log.Error("reply failed, state ambiguous, not retrying", "kind", kind, "err", postErr)
		if err := b.finish(ctx, log, r, statusFailed, nil, kind+": "+postErr.Error()); err != nil {
			return false, err
		}
		if pr0gramm.IsAuthError(postErr) || errors.Is(postErr, pr0gramm.ErrNoSession) {
			return false, b.pr0Err(kind, postErr)
		}
		return false, nil
	}

	var authErr error
	if asComment && song.Title != nil {
		title, artist := deref(song.Title), deref(song.Artist)
		if err := b.pr0.AddTags(ctx, r.ItemID, []string{title, artist, artist + " - " + title}); err != nil {
			logAPIErr(log, "adding tags failed, reply counts as done", err)
			if pr0gramm.IsAuthError(err) {
				authErr = b.pr0Err("tags", err)
			}
		}
	}
	if err := b.finish(ctx, log, r, statusDone, &kind, ""); err != nil {
		return false, err
	}
	return false, authErr
}

// retry handles a retryable error before the claim: back off, or give up at
// MaxAttempts.
func (b *Bot) retry(ctx context.Context, log *slog.Logger, r db.Request, cause error) error {
	attempts := int(r.Attempts) + 1
	next := b.clock.Now().Add(backoff(attempts))
	msg := cause.Error()
	n, err := b.store.BumpAttempt(ctx, db.BumpAttemptParams{ID: r.ID, NextAttemptAt: next, LastError: &msg})
	if err != nil {
		return fmt.Errorf("bot: bump attempt %d: %w", r.ID, err)
	}
	if n == 0 {
		log.Warn("request no longer pending, attempt not recorded")
		return nil
	}
	if attempts >= b.cfg.MaxAttempts {
		log.Error("giving up after max attempts", "attempts", attempts, "err", cause)
		return b.finish(ctx, log, r, statusFailed, nil, msg)
	}
	logAPIErr(log, "transient error, will retry", cause, "attempts", attempts, "next_attempt_at", next)
	return nil
}

// retryFromReplying releases a claimed request whose reply pr0gramm refused
// outright, so it was definitely not posted.
func (b *Bot) retryFromReplying(ctx context.Context, log *slog.Logger, r db.Request, kind string, cause error) error {
	attempts := int(r.Attempts) + 1
	next := b.clock.Now().Add(backoff(attempts))
	prefix := rejectedPMPrefix
	if kind == kindComment {
		prefix = rejectedCommentPrefix
	}
	msg := prefix + cause.Error()
	n, err := b.store.RetryFromReplying(ctx, db.RetryFromReplyingParams{ID: r.ID, NextAttemptAt: next, LastError: &msg})
	if err != nil {
		return fmt.Errorf("bot: release request %d: %w", r.ID, err)
	}
	if n == 0 {
		log.Warn("request no longer replying, retry not recorded")
		return nil
	}
	if attempts >= b.cfg.MaxAttempts {
		log.Error("reply rejected, giving up after max attempts", "attempts", attempts, "err", cause)
		return b.finish(ctx, log, r, statusFailed, nil, msg)
	}
	logAPIErr(log, "reply rejected by pr0gramm, will retry", cause, "attempts", attempts, "next_attempt_at", next)
	return nil
}

// finish moves a live request to a final status.
func (b *Bot) finish(ctx context.Context, log *slog.Logger, r db.Request, status string, kind *string, reason string) error {
	var lastErr *string
	if status == statusFailed || status == statusSkipped {
		lastErr = nilIfEmpty(reason)
	}
	n, err := b.store.SetRequestStatus(ctx, db.SetRequestStatusParams{ID: r.ID, Status: status, ReplyKind: kind, LastError: lastErr})
	if err != nil {
		return fmt.Errorf("bot: set request %d %s: %w", r.ID, status, err)
	}
	if n == 0 {
		log.Warn("request no longer live, status not changed", "status", status)
		return nil
	}
	switch status {
	case statusFailed:
		log.Error("request failed", "reason", reason)
	default:
		log.Info("request "+status, "reply_kind", deref(kind), "reason", reason)
	}
	return nil
}

// MarkHandledRead marks finished requests read on pr0gramm, at most
// markReadBatch per call. A failed MarkRead backs off (30s x 2^(n-1), capped at
// 1h) and is given up on after markReadMaxAttempts; the database stays the
// source of truth, so delivery is unaffected. An auth error stops the run.
func (b *Bot) MarkHandledRead(ctx context.Context) error {
	now := b.clock.Now()
	reqs, err := b.store.UnreadHandledRequests(ctx, db.UnreadHandledRequestsParams{Now: now, Lim: markReadBatch})
	if err != nil {
		return fmt.Errorf("bot: unread handled requests: %w", err)
	}
	for _, r := range reqs {
		if err := b.pr0.MarkRead(ctx, r.MessageID); err != nil {
			if errors.Is(err, pr0gramm.ErrNoSession) || pr0gramm.IsAuthError(err) {
				return b.pr0Err("mark read", err)
			}
			if err := b.markReadFailed(ctx, r, err); err != nil {
				return err
			}
			continue
		}
		if err := b.store.SetMarkedRead(ctx, r.ID); err != nil {
			return fmt.Errorf("bot: set marked read %d: %w", r.ID, err)
		}
	}
	return nil
}

// markReadFailed records a failed MarkRead and schedules the next try.
func (b *Bot) markReadFailed(ctx context.Context, r db.Request, cause error) error {
	attempts := int(r.MarkReadAttempts) + 1
	after := b.clock.Now().Add(markReadBackoff(attempts))
	giveUp := attempts >= markReadMaxAttempts
	if giveUp {
		after = b.clock.Now().Add(markReadGiveUp)
	}
	msg := "mark read: " + cause.Error()
	if _, err := b.store.BumpMarkRead(ctx, db.BumpMarkReadParams{ID: r.ID, MarkReadAfter: after, LastError: &msg}); err != nil {
		return fmt.Errorf("bot: bump mark read %d: %w", r.ID, err)
	}
	if giveUp {
		b.log.Error("mark read failed, giving up (delivery is unaffected)",
			"request", r.ID, "message", r.MessageID, "attempts", attempts, "err", cause)
		return nil
	}
	logAPIErr(b.log, "mark read failed, will retry", cause,
		"request", r.ID, "message", r.MessageID, "attempts", attempts, "next_try", after)
	return nil
}

// markReadBackoff is 30s x 2^(attempts-1), capped at 1h.
func markReadBackoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 8 {
		return markReadMaxBackoff
	}
	return min(markReadBaseBackoff<<(attempts-1), markReadMaxBackoff)
}

// logAPIErr logs a pr0gramm *APIError at ERROR with path and status, anything
// else at WARN.
func logAPIErr(log *slog.Logger, msg string, err error, args ...any) {
	var ae *pr0gramm.APIError
	if errors.As(err, &ae) {
		log.Error(msg, append(args, "path", ae.Path, "status", ae.Status, "err", err)...)
		return
	}
	log.Warn(msg, append(args, "err", err)...)
}

// RecoverReplying fails every request a previous run left in replying. Call
// once at startup, before ProcessDue: those replies may or may not have been
// posted, and must not be posted twice.
func (b *Bot) RecoverReplying(ctx context.Context) error {
	stale, err := b.store.FailStaleReplying(ctx)
	if err != nil {
		return fmt.Errorf("bot: fail stale replying: %w", err)
	}
	for _, r := range stale {
		b.log.Error("reply state unknown after restart, not retrying",
			"request", r.ID, "message", r.MessageID, "item", r.ItemID, "requester", r.Requester)
	}
	return nil
}

func (b *Bot) pr0Err(op string, err error) error {
	if pr0gramm.IsAuthError(err) || errors.Is(err, pr0gramm.ErrNoSession) {
		return fmt.Errorf("%w: %s: %w", ErrAuth, op, err)
	}
	return fmt.Errorf("bot: %s: %w", op, err)
}

func (b *Bot) checkedAt() string {
	return b.clock.Now().In(b.berlin).Format(checkedAtLayout)
}

// backoff is 1m × 2^(attempts-1), capped at 1h.
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 7 { // 2^6 minutes already exceeds the cap
		return maxBackoff
	}
	return min(baseBackoff<<(attempts-1), maxBackoff)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
