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

The cell-provisioning boundary now reserves capacity transactionally, derives a stable tenant identity, and delegates
idempotent tenant/bucket/key creation through a provider interface. PostgreSQL stores only cell metadata, access-key
IDs, opaque secret-manager references, and SHA-256 hashes of short-lived redemption tokens; it never stores admin
tokens or access-key secrets. Redemption is account-bound, expiring, transactionally claimed, and exactly once. This
contract is not yet mounted as a public route: a production secret-manager adapter, a read-back-verified Lockwell cell
adapter, and a configured cell inventory must land first so the service cannot expose a nonfunctional provisioning UI.
