package bot

import (
	"context"
	"time"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/Pacerino/pr0sauce/internal/odesli"
	"github.com/Pacerino/pr0sauce/internal/pr0gramm"
	"github.com/Pacerino/pr0sauce/internal/recognize"
)

// Pr0 is the subset of the pr0gramm client the bot uses.
type Pr0 interface {
	InboxComments(ctx context.Context) ([]pr0gramm.InboxComment, error)
	MarkRead(ctx context.Context, messageID int64) error
	PostComment(ctx context.Context, itemID, parentID int64, text string) error
	AddTags(ctx context.Context, itemID int64, tags []string) error
	SendMessage(ctx context.Context, recipient, text string) error
	UserComments(ctx context.Context, name string, after time.Time) (pr0gramm.UserComments, error)
	Item(ctx context.Context, id int64) (pr0gramm.Item, error)
}

// Recognizer identifies the music in a video.
type Recognizer interface {
	Recognize(ctx context.Context, videoURL string) (recognize.Result, error)
}

// Linker resolves streaming links for a song URL.
type Linker interface {
	Links(ctx context.Context, url string) (odesli.Links, error)
}

// Store is the persistence the bot needs. GetSong and GetState return
// ErrNotFound when there is no row.
type Store interface {
	GetSong(ctx context.Context, itemID int64) (db.Song, error)
	UpsertSong(ctx context.Context, arg db.UpsertSongParams) error
	InsertRequestIgnore(ctx context.Context, arg db.InsertRequestIgnoreParams) (bool, error)
	DueRequests(ctx context.Context, arg db.DueRequestsParams) ([]db.Request, error)
	SetRequestStatus(ctx context.Context, arg db.SetRequestStatusParams) (int64, error)
	BumpAttempt(ctx context.Context, arg db.BumpAttemptParams) (int64, error)
	MarkReplying(ctx context.Context, id int64) (int64, error)
	RetryFromReplying(ctx context.Context, arg db.RetryFromReplyingParams) (int64, error)
	FailStaleReplying(ctx context.Context) ([]db.Request, error)
	UnreadHandledRequests(ctx context.Context) ([]db.Request, error)
	SetMarkedRead(ctx context.Context, id int64) error
	GetState(ctx context.Context, key string) (string, error)
	SetState(ctx context.Context, arg db.SetStateParams) error
	LatestBotCommentTime(ctx context.Context) (*time.Time, error)
	UpsertBotComment(ctx context.Context, arg db.UpsertBotCommentParams) error
}

// Clock supplies the current time.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }
