package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/pathresolver"
	"github.com/agentsmith-project/agentsmith-fs-control-plane/internal/workloadmount"
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
	exec                           executor
	clock                          func() time.Time
	workloadMountRuntimeSecretRefs map[string]workloadmount.SecretRef
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

func WithWorkloadMountRuntimeSecretRefs(refs map[string]workloadmount.SecretRef) Option {
	return func(store *Store) {
		store.workloadMountRuntimeSecretRefs = cloneWorkloadMountRuntimeSecretRefs(refs)
	}
}

func (store *Store) now() time.Time {
	if store.clock == nil {
		return time.Now().UTC()
	}
	return store.clock().UTC()
}

func cloneWorkloadMountRuntimeSecretRefs(refs map[string]workloadmount.SecretRef) map[string]workloadmount.SecretRef {
	if len(refs) == 0 {
		return nil
	}
	cloned := make(map[string]workloadmount.SecretRef, len(refs))
	for volumeID, ref := range refs {
		cloned[volumeID] = ref
	}
	return cloned
}

func validateWorkloadMountRuntimeSecretRefs(refs map[string]workloadmount.SecretRef) error {
	for volumeID, ref := range refs {
		if err := pathresolver.ValidateID(pathresolver.VolumeID, volumeID); err != nil {
			return fmt.Errorf("workload mount runtime secret refs must contain valid volume ids")
		}
		if err := workloadmount.ValidateSecretRef(ref); err != nil {
			return fmt.Errorf("workload mount runtime secret refs must contain valid secret refs")
		}
	}
	return nil
}
