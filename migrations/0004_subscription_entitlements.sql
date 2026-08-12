BEGIN;

ALTER TABLE control_plane_outbox ADD COLUMN dead_lettered_at timestamptz;

CREATE TABLE hosted_subscriptions (
    stripe_subscription_id text PRIMARY KEY,
    account_id uuid NOT NULL REFERENCES customer_accounts(id),
    stripe_customer_id text NOT NULL,
    plan_code text NOT NULL CHECK (plan_code IN ('starter', 'team', 'compliance')),
    stripe_price_id text NOT NULL,
    stripe_status text NOT NULL,
    entitlement_status text NOT NULL CHECK (entitlement_status IN ('pending', 'active', 'grace', 'suspended', 'canceled')),
    entitlement_until timestamptz,
    grace_until timestamptz,
    cancel_at_period_end boolean NOT NULL DEFAULT false,
    last_stripe_event_created timestamptz NOT NULL,
    last_stripe_event_priority integer NOT NULL,
    last_stripe_event_id text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX hosted_subscriptions_active_account_idx
    ON hosted_subscriptions (account_id)
    WHERE entitlement_status IN ('pending', 'active', 'grace');

COMMIT;
