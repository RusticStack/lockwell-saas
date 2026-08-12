BEGIN;

CREATE TABLE hosting_cells (
    id text PRIMARY KEY,
    region text NOT NULL,
    public_endpoint text NOT NULL,
    admin_endpoint text NOT NULL,
    admin_secret_ref text NOT NULL,
    status text NOT NULL CHECK (status IN ('ready', 'draining', 'offline')),
    tenant_capacity integer NOT NULL CHECK (tenant_capacity > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tenant_provisions (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL UNIQUE REFERENCES customer_accounts(id),
    cell_id text NOT NULL REFERENCES hosting_cells(id),
    tenant_id text NOT NULL,
    bucket_name text NOT NULL,
    access_key_id text,
    credential_secret_ref text,
    status text NOT NULL CHECK (status IN ('reserved', 'ready', 'failed', 'suspended')),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (cell_id, tenant_id)
);

CREATE TABLE credential_redemptions (
    id uuid PRIMARY KEY,
    provision_id uuid NOT NULL REFERENCES tenant_provisions(id),
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz,
    claim_expires_at timestamptz,
    redeemed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX credential_redemptions_active_idx
    ON credential_redemptions (token_hash, expires_at)
    WHERE redeemed_at IS NULL;

COMMIT;
