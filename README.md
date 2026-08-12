# Lockwell hosted control plane

This repository contains the customer, billing, and infrastructure orchestration boundary for hosted Lockwell Object
Storage. It is intentionally separate from the self-hosted data plane: customer payloads and Lockwell access-key
secrets must never pass through or persist in this service.

Status: Phase 3 implementation. The service is not ready for paid customers. Live collection remains blocked on the
seller-of-record, VAT/OSS, invoicing, product-catalog, and operating-policy decisions tracked in the core repository.

## First production slice

- PostgreSQL is the control-plane authority.
- Stripe webhooks are verified from the exact raw body, deduplicated transactionally, and converted into an outbox job
  in the same database transaction.
- `/healthz` is liveness only; `/readyz` proves database reachability.
- No Checkout redirect grants entitlement. Workers will reconcile authoritative Stripe objects before provisioning.
- Secrets are environment references for development only; production deployment must inject them from a secret
  manager.

## Development

```powershell
go test ./...
go vet ./...
go run ./cmd/lockwell-saas
```

Required environment:

| Variable | Purpose |
| --- | --- |
| `LOCKWELL_SAAS_DATABASE_URL` | PostgreSQL connection string |
| `LOCKWELL_SAAS_STRIPE_API_KEY` | Stripe restricted/test API key |
| `LOCKWELL_SAAS_STRIPE_API_VERSION` | Account-pinned Stripe API version required on outbound calls and inbound events |
| `LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET` | Stripe endpoint signing secret |
| `LOCKWELL_SAAS_STRIPE_STARTER_PRICE` | Server-side allowlisted recurring Starter Price ID |
| `LOCKWELL_SAAS_STRIPE_TEAM_PRICE` | Server-side allowlisted recurring Team Price ID |
| `LOCKWELL_SAAS_STRIPE_COMPLIANCE_PRICE` | Server-side allowlisted recurring Compliance Price ID |
| `LOCKWELL_SAAS_STRIPE_STORAGE_EVENT_NAME` | Approved storage MiB-hour meter event name |
| `LOCKWELL_SAAS_STRIPE_OPERATIONS_EVENT_NAME` | Approved successful-operation meter event name |
| `LOCKWELL_SAAS_STRIPE_EGRESS_EVENT_NAME` | Approved delivered-egress MiB meter event name |
| `LOCKWELL_SAAS_CHECKOUT_SUCCESS_URL` | Post-Checkout UI URL; never treated as entitlement proof |
| `LOCKWELL_SAAS_CHECKOUT_CANCEL_URL` | Checkout cancellation UI URL |
| `LOCKWELL_SAAS_PORTAL_RETURN_URL` | Customer Portal return URL |
| `LOCKWELL_SAAS_TERMS_VERSION` | Exact terms version new accounts must accept |
| `LOCKWELL_SAAS_LISTEN_ADDR` | Optional listener; defaults to `127.0.0.1:8080` |

Provisioning is disabled unless `LOCKWELL_SAAS_PROVISIONING_ENABLED=true`. When enabled, startup fails closed unless
all of the following are present: `LOCKWELL_SAAS_SCALEWAY_PROJECT_ID`, `LOCKWELL_SAAS_SCALEWAY_REGION`,
`LOCKWELL_SAAS_SCALEWAY_AUTH_TOKEN`, `LOCKWELL_SAAS_CELL_ID`, `LOCKWELL_SAAS_CELL_PUBLIC_ENDPOINT`,
`LOCKWELL_SAAS_CELL_ADMIN_ENDPOINT`, `LOCKWELL_SAAS_CELL_ADMIN_SECRET_REF`, `LOCKWELL_SAAS_CELL_CAPACITY`, and positive
`LOCKWELL_SAAS_STARTER_QUOTA_BYTES`, `LOCKWELL_SAAS_TEAM_QUOTA_BYTES`, and
`LOCKWELL_SAAS_COMPLIANCE_QUOTA_BYTES`. The admin secret reference must identify an existing secret in the configured
Scaleway region; the token needs least-privilege access to that secret and the hosted credential path.

Email delivery is independently disabled unless `LOCKWELL_SAAS_EMAIL_ENABLED=true`. Enabling it requires the same
Scaleway project and IAM token plus `LOCKWELL_SAAS_EMAIL_FROM` from a provider-verified domain and an absolute HTTPS
`LOCKWELL_SAAS_EMAIL_VERIFICATION_URL`; `LOCKWELL_SAAS_EMAIL_FROM_NAME` is optional. Verification tokens are random,
expire after one hour, are stored only as SHA-256 digests, and are consumed once. An authenticated user requests a new
message through `/v1/accounts/verification/request`; the confirmation link submits its token to
`/v1/accounts/verification/confirm`. The token is placed in the link fragment, not its query, so the browser does not
send the bearer value to the landing host or a request log; the verification page reads the fragment locally and POSTs
it in JSON.

Apply migrations in numeric order before starting the service. Accounts are created unverified; Checkout and Portal
fail closed until a future verified-email delivery/redemption slice marks the address verified. Never put real
credentials in source, `.env` examples, test fixtures, logs, or pull-request text.

Usage metering accepts only trusted, immutable rollups from future cell collectors. Each rollup creates a deterministic
Stripe Meter Event identifier and a transactional export row. Workers use bounded retries, reclaim abandoned claims,
dead-letter repeated delivery failures, and compare Stripe's asynchronous meter summary with the internal aggregate
before marking a window reconciled. Customer-facing requests cannot submit or alter billable usage.

Verified subscription events are processed asynchronously from the transactional inbox. The worker retrieves the
authoritative Stripe Subscription, rechecks account/customer metadata and the server-side plan/Price allowlist, and
projects entitlement state transactionally. Only `invoice.paid` on an active/trialing subscription grants access;
payment failure starts a bounded grace period, cancellation suspends access, stale events cannot roll state back, and
equal-timestamp conflicts use a documented fail-closed priority. Every entitlement change creates a durable downstream
outbox job for the cell-provisioning/suspension worker.

The cell-provisioning boundary reserves capacity transactionally, persists the selected plan/quota, and derives a
stable tenant identity. Its Lockwell adapter creates and reads back the tenant, quota, private default bucket, and
bucket-scoped data key; the temporary bucket-administration key is revoked after use. Credential delivery is completed
inside the adapter so a vault failure triggers compensating key revocation. The Scaleway Secret Manager adapter uses
the regional v1beta1 API, creates protected opaque secrets, writes version data, and reads only `latest_enabled`.
PostgreSQL stores only cell metadata, access-key IDs, opaque secret references, and SHA-256 hashes of short-lived
redemption tokens; it never stores admin tokens or access-key secrets. Redemption is account-bound, expiring,
transactionally claimed, and exactly once. These adapters are not yet mounted as public routes: configured live cell
inventory, production IAM credentials, entitlement-outbox enforcement, and provider readback must land first so the
service cannot expose a nonfunctional provisioning UI.
