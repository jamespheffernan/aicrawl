package archive

import (
	"context"
	"database/sql"
	"errors"
	"os"

	"github.com/openclaw/aicrawl/internal/schema"
	"github.com/openclaw/crawlkit/store"
)

type Archive struct {
	store *store.Store
}

func Open(ctx context.Context, path string) (*Archive, error) {
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, err
	}
	if err := schema.Migrate(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Archive{store: st}, nil
}

func OpenReadOnly(ctx context.Context, path string) (*Archive, error) {
	st, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	version, err := schema.UserVersion(ctx, st.DB())
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if version > schema.Version {
		_ = st.Close()
		return nil, errors.New("database schema is newer than this aicrawl binary supports")
	}
	return &Archive{store: st}, nil
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (a *Archive) SchemaVersion(ctx context.Context) (int, error) {
	if a == nil || a.store == nil {
		return 0, errors.New("archive is not open")
	}
	return schema.UserVersion(ctx, a.store.DB())
}

func (a *Archive) Close() error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.Close()
}

func (a *Archive) DB() *sql.DB {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.DB()
}
