BEGIN;

CREATE TABLE usage_windows (
    id uuid PRIMARY KEY,
    cell_id text NOT NULL REFERENCES hosting_cells(id),
    tenant_id text NOT NULL,
    account_id uuid NOT NULL REFERENCES customer_accounts(id),
    window_start timestamptz NOT NULL,
    window_end timestamptz NOT NULL,
    source_revision text NOT NULL,
    source_digest bytea NOT NULL CHECK (octet_length(source_digest) = 32),
    storage_mib_hours bigint NOT NULL CHECK (storage_mib_hours >= 0),
    operations bigint NOT NULL CHECK (operations >= 0),
    egress_mib bigint NOT NULL CHECK (egress_mib >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cell_id, tenant_id, window_start, window_end, source_revision),
    CHECK (window_end > window_start)
);

COMMIT;
