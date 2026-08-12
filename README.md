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
| `LOCKWELL_SAAS_STRIPE_WEBHOOK_SECRET` | Stripe endpoint signing secret |
| `LOCKWELL_SAAS_LISTEN_ADDR` | Optional listener; defaults to `127.0.0.1:8080` |

Apply [`migrations/0001_control_plane.sql`](migrations/0001_control_plane.sql) before starting the service. Never put
real credentials in source, `.env` examples, test fixtures, logs, or pull-request text.
