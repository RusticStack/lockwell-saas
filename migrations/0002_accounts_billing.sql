BEGIN;

CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE customer_accounts (
    id uuid PRIMARY KEY,
    email citext NOT NULL UNIQUE,
    password_hash text NOT NULL,
    email_verified_at timestamptz,
    terms_version text NOT NULL,
    terms_accepted_at timestamptz NOT NULL,
    stripe_customer_id text UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE customer_sessions (
    token_hash bytea PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customer_accounts(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    CHECK (octet_length(token_hash) = 32)
);

CREATE INDEX customer_sessions_account_idx ON customer_sessions (account_id, expires_at);

CREATE TABLE checkout_sessions (
    id uuid PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customer_accounts(id),
    plan_code text NOT NULL,
    stripe_checkout_session_id text NOT NULL UNIQUE,
    stripe_customer_id text NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    status text NOT NULL DEFAULT 'open',
    created_at timestamptz NOT NULL DEFAULT now()
);

COMMIT;
