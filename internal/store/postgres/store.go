package postgres

import (
	"context"
	"database/sql"
	"time"
)

type rowScanner interface {
	Scan(dest ...any) error
}

type rowsScanner interface {
	Close() error
	Err() error
	Next() bool
	Scan(dest ...any) error
}

type executor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error)
	QueryRowContext(ctx context.Context, query string, args ...any) rowScanner
}

type Store struct {
	exec  executor
	clock func() time.Time
}

type Option func(*Store)

func New(db *sql.DB, opts ...Option) *Store {
	store := &Store{
		exec:  sqlExecutor{db: db},
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(store)
	}
	return store
}

type sqlExecutor struct {
	db *sql.DB
}

func (exec sqlExecutor) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return exec.db.ExecContext(ctx, query, args...)
}

func (exec sqlExecutor) QueryContext(ctx context.Context, query string, args ...any) (rowsScanner, error) {
	return exec.db.QueryContext(ctx, query, args...)
}

func (exec sqlExecutor) QueryRowContext(ctx context.Context, query string, args ...any) rowScanner {
	return exec.db.QueryRowContext(ctx, query, args...)
}

func WithClock(clock func() time.Time) Option {
	return func(store *Store) {
		if clock != nil {
			store.clock = clock
		}
	}
}

func (store *Store) now() time.Time {
	if store.clock == nil {
		return time.Now().UTC()
	}
	return store.clock().UTC()
}
