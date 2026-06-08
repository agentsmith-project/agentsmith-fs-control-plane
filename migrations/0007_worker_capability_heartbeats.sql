CREATE TABLE IF NOT EXISTS worker_capability_heartbeats (
    capability TEXT PRIMARY KEY CHECK (btrim(capability) <> ''),
    owner TEXT NOT NULL CHECK (btrim(owner) <> ''),
    observed_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > observed_at)
);

CREATE INDEX IF NOT EXISTS worker_capability_heartbeats_ready_idx
    ON worker_capability_heartbeats (capability, expires_at);
