BEGIN;

CREATE TABLE stripe_event_inbox (
    event_id text PRIMARY KEY,
    event_type text NOT NULL,
    api_version text NOT NULL,
    payload_sha256 bytea NOT NULL,
    payload_json jsonb NOT NULL,
    stripe_created_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    processing_error text,
    CHECK (octet_length(payload_sha256) = 32)
);

CREATE TABLE control_plane_outbox (
    id uuid PRIMARY KEY,
    topic text NOT NULL,
    aggregate_id text NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    payload_json jsonb NOT NULL,
    available_at timestamptz NOT NULL DEFAULT now(),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    claimed_at timestamptz,
    completed_at timestamptz,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX control_plane_outbox_ready_idx
    ON control_plane_outbox (available_at, created_at)
    WHERE completed_at IS NULL;

COMMIT;
