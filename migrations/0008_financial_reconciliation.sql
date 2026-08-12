BEGIN;

CREATE TABLE hosted_invoices (
    stripe_invoice_id text PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customer_accounts(id),
    stripe_customer_id text NOT NULL,
    stripe_subscription_id text,
    currency text NOT NULL CHECK (currency ~ '^[a-z]{3}$'),
    status text NOT NULL,
    subtotal bigint NOT NULL CHECK (subtotal >= 0),
    tax bigint NOT NULL CHECK (tax >= 0),
    total bigint NOT NULL CHECK (total >= 0),
    amount_paid bigint NOT NULL CHECK (amount_paid >= 0),
    amount_remaining bigint NOT NULL CHECK (amount_remaining >= 0),
    hosted_invoice_url text,
    invoice_pdf text,
    stripe_created_at timestamptz NOT NULL,
    reconciled_at timestamptz NOT NULL,
    CHECK (tax <= total)
);

CREATE TABLE hosted_invoice_lines (
    stripe_line_id text PRIMARY KEY,
    stripe_invoice_id text NOT NULL REFERENCES hosted_invoices(stripe_invoice_id) ON DELETE CASCADE,
    stripe_price_id text,
    description text NOT NULL,
    currency text NOT NULL CHECK (currency ~ '^[a-z]{3}$'),
    amount bigint NOT NULL,
    quantity bigint NOT NULL CHECK (quantity >= 0),
    period_start timestamptz,
    period_end timestamptz
);

CREATE INDEX hosted_invoice_lines_invoice_idx ON hosted_invoice_lines (stripe_invoice_id);

CREATE TABLE hosted_refunds (
    stripe_refund_id text PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customer_accounts(id),
    stripe_customer_id text NOT NULL,
    stripe_invoice_id text,
    stripe_charge_id text NOT NULL,
    stripe_payment_intent_id text,
    currency text NOT NULL CHECK (currency ~ '^[a-z]{3}$'),
    amount bigint NOT NULL CHECK (amount > 0),
    status text NOT NULL,
    reason text,
    stripe_created_at timestamptz NOT NULL,
    reconciled_at timestamptz NOT NULL
);

CREATE INDEX hosted_refunds_invoice_idx ON hosted_refunds (stripe_invoice_id);

COMMIT;
