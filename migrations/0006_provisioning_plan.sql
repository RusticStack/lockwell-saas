BEGIN;

ALTER TABLE tenant_provisions
    ADD COLUMN plan_code text NOT NULL CHECK (plan_code IN ('starter', 'team', 'compliance')),
    ADD COLUMN quota_bytes bigint NOT NULL CHECK (quota_bytes > 0);

COMMIT;
