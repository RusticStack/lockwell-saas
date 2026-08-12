BEGIN;

ALTER TABLE hosted_invoices
    ADD COLUMN automatic_tax_enabled boolean NOT NULL DEFAULT false,
    ADD COLUMN automatic_tax_status text,
    ADD COLUMN customer_tax_exempt text NOT NULL DEFAULT 'none' CHECK (customer_tax_exempt IN ('none', 'exempt', 'reverse')),
    ADD COLUMN customer_country text CHECK (customer_country IS NULL OR customer_country ~ '^[A-Z]{2}$'),
    ADD COLUMN customer_state text,
    ADD COLUMN customer_postal_code text;

CREATE TABLE hosted_invoice_tax_amounts (
    stripe_invoice_id text NOT NULL REFERENCES hosted_invoices(stripe_invoice_id) ON DELETE CASCADE,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    amount bigint NOT NULL CHECK (amount >= 0),
    taxable_amount bigint NOT NULL CHECK (taxable_amount >= 0),
    inclusive boolean NOT NULL,
    stripe_tax_rate_id text,
    taxability_reason text,
    PRIMARY KEY (stripe_invoice_id, ordinal)
);

COMMIT;
