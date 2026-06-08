package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const savePointCreateRecoveryCapability = "save_point_create_recovery"

func (store *Store) RecordSavePointCreateRecoveryCapability(ctx context.Context, owner string, observedAt, expiresAt time.Time) error {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return fmt.Errorf("worker capability owner is required")
	}
	observedAt = observedAt.UTC()
	expiresAt = expiresAt.UTC()
	if !expiresAt.After(observedAt) {
		return fmt.Errorf("worker capability expiry must be after observation")
	}
	_, err := store.exec.ExecContext(ctx, recordSavePointCreateRecoveryCapabilitySQL(), savePointCreateRecoveryCapability, owner, observedAt, expiresAt)
	return err
}

func (store *Store) SavePointCreateRecoveryCapabilityReady(ctx context.Context, now time.Time) (bool, error) {
	var ready bool
	err := store.exec.QueryRowContext(ctx, savePointCreateRecoveryCapabilityReadySQL(), savePointCreateRecoveryCapability, now.UTC()).Scan(&ready)
	return ready, err
}

func recordSavePointCreateRecoveryCapabilitySQL() string {
	return `
INSERT INTO worker_capability_heartbeats (capability, owner, observed_at, expires_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (capability) DO UPDATE
SET owner = EXCLUDED.owner,
    observed_at = EXCLUDED.observed_at,
    expires_at = EXCLUDED.expires_at
`
}

func savePointCreateRecoveryCapabilityReadySQL() string {
	return `
SELECT EXISTS (
    SELECT 1
    FROM worker_capability_heartbeats
    WHERE capability = $1
      AND observed_at <= $2
      AND expires_at > $2
)
`
}
