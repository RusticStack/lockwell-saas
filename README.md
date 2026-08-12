# Lockwell hosted control plane

This first-party Lockwell source is **source-available under PolyForm Noncommercial 1.0.0**, not OSI Open Source.
Commercial use—including TangibleShift ERP, paid use, resale, managed hosting, and OEM use—requires an explicit written
commercial grant. See [`LICENSE`](LICENSE), [`NOTICE`](NOTICE), and
[`COMMERCIAL-LICENSE.md`](COMMERCIAL-LICENSE.md). This repository remains prelaunch engineering evidence; these terms do
not make hosted Lockwell available, authorize payment collection, or clear its provider, legal, recovery, or support
gates.

This repository contains the customer, billing, and infrastructure orchestration boundary for hosted Lockwell Object
Storage. It is intentionally separate from the self-hosted data plane: customer payloads and Lockwell access-key
secrets must never pass through or persist in this service.

Status: Phase 3 implementation. The service is not ready for paid customers. Live collection remains blocked on the
seller-of-record, VAT/OSS, invoicing, product-catalog, and operating-policy decisions tracked in the core repository.

## First production slice

- PostgreSQL is the control-plane authority.
- Stripe webhooks are verified from the exact raw body, deduplicated transactionally, and converted into an outbox job
  in the same database transaction.
- `/healthz` is liveness only; `/readyz` proves database reachability. `/metrics` requires a strong bearer token and
  exposes only bounded aggregate gauges for worker backlog, dead letters, entitlements, provisions, and reconciled
  financial records; it never labels by account, tenant, bucket, or provider identity.
- No Checkout redirect grants entitlement. Workers will reconcile authoritative Stripe objects before provisioning.
- Invoice lifecycle and refund webhooks enqueue a separate bounded accounting job. It retrieves the authoritative
  Invoice, all paginated line items, and Refund plus Charge before transactionally projecting financial state.
- Financial projections validate Stripe customer/subscription binding, lease jobs, retry with a bounded policy, and
  dead-letter persistent failures. `invoice.paid` is complete only after entitlement and accounting both reconcile.
- Checkout requires a billing address, updates the existing Stripe Customer address/name, enables automatic tax, and
  collects tax IDs. Reconciliation preserves automatic-tax completion, customer tax treatment/location, and each
  taxable/tax amount; inconsistent or incomplete automatic-tax evidence is retried rather than accepted as billable.
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
| `LOCKWELL_SAAS_METRICS_TOKEN` | Required random bearer token of at least 32 characters for `/metrics` |
| `LOCKWELL_SAAS_USAGE_INGEST_TOKEN` | Required random bearer token of at least 32 characters for the private cell usage-ingest route |
| `LOCKWELL_SAAS_STRIPE_API_KEY` | Stripe restricted/test API key |
| `LOCKWELL_SAAS_STRIPE_API_VERSION` | Account-pinned Stripe API version required on outbound calls and inbound events |
| `LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET` | Stripe endpoint signing secret |
| `LOCKWELL_SAAS_STRIPE_STARTER_PRICE` | Server-side allowlisted recurring Starter Price ID |
| `LOCKWELL_SAAS_STRIPE_TEAM_PRICE` | Server-side allowlisted recurring Team Price ID |
| `LOCKWELL_SAAS_STRIPE_COMPLIANCE_PRICE` | Server-side allowlisted recurring Compliance Price ID |
| `LOCKWELL_SAAS_STRIPE_STORAGE_EVENT_NAME` | Approved storage MiB-hour meter event name |
| `LOCKWELL_SAAS_STRIPE_OPERATIONS_EVENT_NAME` | Approved successful-operation meter event name |
| `LOCKWELL_SAAS_STRIPE_EGRESS_EVENT_NAME` | Approved delivered-egress MiB meter event name |
| `LOCKWELL_SAAS_STRIPE_STORAGE_METER_ID` | Stripe Meter ID paired with the storage event name |
| `LOCKWELL_SAAS_STRIPE_OPERATIONS_METER_ID` | Stripe Meter ID paired with the operations event name |
| `LOCKWELL_SAAS_STRIPE_EGRESS_METER_ID` | Stripe Meter ID paired with the egress event name |
| `LOCKWELL_SAAS_CHECKOUT_SUCCESS_URL` | Post-Checkout UI URL; never treated as entitlement proof |
| `LOCKWELL_SAAS_CHECKOUT_CANCEL_URL` | Checkout cancellation UI URL |
| `LOCKWELL_SAAS_PORTAL_RETURN_URL` | Customer Portal return URL |
| `LOCKWELL_SAAS_TERMS_VERSION` | Exact terms version new accounts must accept |
| `LOCKWELL_SAAS_LISTEN_ADDR` | Optional listener; defaults to `127.0.0.1:8080` |
| `LOCKWELL_SAAS_CUSTOMER_ORIGIN` | Optional exact HTTP(S) origin allowed to call `/v1/` from a browser |

## Stripe test-mode readiness

Before starting a live customer-flow drill, set the service's Stripe variables plus
`LOCKWELL_SAAS_STRIPE_PORTAL_CONFIG_ID`, `LOCKWELL_SAAS_STRIPE_WEBHOOK_ENDPOINT_ID`, and the exact public
`LOCKWELL_SAAS_STRIPE_WEBHOOK_URL`, then run:

```powershell
go run ./cmd/stripe-readiness
```

The read-only verifier accepts only an `sk_test_` key and fails closed unless all three allowlisted Prices are distinct,
active monthly EUR recurring Prices; all three Meters are distinct, active, test-mode `sum` meters with the configured
event identity and payload mapping; the Portal configuration is active; and the exact enabled webhook endpoint is
test-mode, pinned to the configured API version and URL, and subscribes to every event the entitlement and financial
workers consume. Successful output is a redacted JSON inventory containing object IDs and status only. It is a
precondition check, not evidence that Checkout, Portal, Meter delivery, invoice/refund reconciliation, Tax, or
provisioning completed end to end.

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
it to the API. Browser access is disabled unless `LOCKWELL_SAAS_CUSTOMER_ORIGIN` is set to one exact origin. The CORS
policy permits only `GET`/`POST`/`OPTIONS` with `Authorization`, `Content-Type`, and `Idempotency-Key`, never enables credentialed cookies, and
does not expose Stripe webhooks or operational endpoints.

Apply migrations in numeric order before starting the service. Accounts are created unverified; Checkout, Portal,
and credential provisioning fail closed until the single-use email-verification flow marks the address verified. Never put real
credentials in source, `.env` examples, test fixtures, logs, or pull-request text.

Usage metering accepts only trusted, immutable windows at private `POST /internal/v1/usage-windows`. The route requires
its own strong bearer token, accepts no account or Stripe customer identity, and resolves the `(cell_id, tenant_id)`
pair against the serving tenant provision. The caller supplies minute-aligned storage MiB-hours, successful operations,
and delivered egress MiB under one source revision plus the SHA-256 digest of the canonical JSON evidence. The service
recomputes that digest and transactionally creates one immutable window, all three rollups, and all three Stripe export
rows. Exact replay is a no-op; the same revision with different evidence and an unbound tenant both fail closed. A real
cell/edge collector that produces these reconciled windows and live evidence remains required before paid launch.
Each rollup creates a deterministic Stripe Meter Event identifier. Workers use bounded retries, reclaim abandoned claims,
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
transactionally claimed, and exactly once. The provisioning and redemption routes are mounted only when provisioning
is explicitly enabled with complete cell, quota, IAM, and secret-reference configuration. Live provider apply, IAM
review, and cell readback are still required before enabling them for customers.

## Operational scrape contract

Scrape `GET /metrics` with `Authorization: Bearer <LOCKWELL_SAAS_METRICS_TOKEN>`. Alert at minimum when any dead-letter
gauge is non-zero, when unprocessed Stripe events or pending jobs grow continuously, when provisions fail, or when
grace/suspended entitlements change unexpectedly. The endpoint returns `503` if its aggregate database read fails and
`401` for a missing or incorrect token. Keep it private at the reverse proxy even though application authentication is
mandatory. Rotate the token through the deployment secret manager and never place it in URLs.
