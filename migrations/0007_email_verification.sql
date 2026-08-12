BEGIN;
CREATE TABLE email_verifications (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash)=32),
    account_id uuid NOT NULL REFERENCES customer_accounts(id),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX email_verifications_account_idx ON email_verifications(account_id,expires_at) WHERE consumed_at IS NULL;
COMMIT;
