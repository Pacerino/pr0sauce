package bot

import (
	"context"
	"errors"

	"github.com/Pacerino/pr0sauce/internal/db"
	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned by Store lookups that find no row.
var ErrNotFound = errors.New("bot: not found")

// dbStore adapts *db.Queries to Store, mapping pgx.ErrNoRows to ErrNotFound.
type dbStore struct{ *db.Queries }

var _ Store = dbStore{}

// NewStore wraps generated queries as a Store.
func NewStore(q *db.Queries) Store { return dbStore{q} }

func (s dbStore) GetSong(ctx context.Context, itemID int64) (db.Song, error) {
	song, err := s.Queries.GetSong(ctx, itemID)
	return song, notFound(err)
}

func (s dbStore) GetState(ctx context.Context, key string) (string, error) {
	v, err := s.Queries.GetState(ctx, key)
	return v, notFound(err)
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
