BEGIN;

CREATE TABLE usage_rollups (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customer_accounts(id),
    metric text NOT NULL CHECK (metric IN ('storage_mib_hours', 'operations', 'egress_mib')),
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    value bigint NOT NULL CHECK (value >= 0),
    source_revision text NOT NULL,
    source_digest bytea NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (account_id, metric, window_start, window_end, source_revision),
    CHECK (window_end > window_start),
    CHECK (octet_length(source_digest) = 32)
);

CREATE TABLE stripe_meter_exports (
    id uuid PRIMARY KEY,
    usage_rollup_id uuid NOT NULL UNIQUE REFERENCES usage_rollups(id),
    stripe_customer_id text NOT NULL,
    meter_event_name text NOT NULL,
    stripe_meter_id text NOT NULL,
    stripe_identifier text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sending', 'sent', 'reconciling', 'reconciled', 'dead_letter')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz,
    sent_at timestamptz,
    reconciled_at timestamptz,
    stripe_aggregated_value bigint,
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX stripe_meter_exports_ready_idx
    ON stripe_meter_exports (available_at, created_at)
    WHERE status = 'pending';

COMMIT;
