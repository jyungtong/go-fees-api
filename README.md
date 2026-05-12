# go-fees-api

Billing service that tracks progressive fee accrual over a billing period. Temporal workflows stay alive for the duration of the billing period, receiving line items as signals — no polling, no cron.

## Prerequisites

- Go 1.22+
- [Encore CLI](https://encore.dev/docs/install)
- [Temporal CLI](https://docs.temporal.io/cli)
- Docker (Encore auto-provisions PostgreSQL)

## Quick Start

```bash
# Start Temporal dev server (keep running in another terminal)
temporal server start-dev

# Run the service
encore run
```

Service available at `http://localhost:4000`.

## API

| Method | Path | Purpose |
|--------|------|---------|
| `GET` | `/health` | Service + Temporal health check |
| `POST` | `/bills` | Create bill, start Temporal workflow |
| `POST` | `/bills/:id/line-items` | Add line item (signal workflow) |
| `POST` | `/bills/:id/close` | Close bill, compute total |
| `GET` | `/bills/:id` | View bill + line items |
| `GET` | `/bills` | List tenant bills |

All endpoints (except `/health`) require `customer-id` header for multi-tenant isolation.

### Example

```bash
# Create bill
curl -s -X POST http://localhost:4000/bills \
  -H "Content-Type: application/json" \
  -H "customer-id: acme-1" \
  -d '{"currency":"USD"}'

# Add line item
curl -s -X POST http://localhost:4000/bills/<BILL_ID>/line-items \
  -H "Content-Type: application/json" \
  -H "customer-id: acme-1" \
  -d '{"description":"widget","quantity":2,"unit_price":"3.50"}'

# Close bill
curl -s -X POST http://localhost:4000/bills/<BILL_ID>/close \
  -H "customer-id: acme-1"
```

## Key Design

- **Temporal workflows** are the bill lifecycle — the workflow stays alive for the whole billing period, receiving line items as signals
- **Repository layer** centralizes PostgreSQL access — API handlers and Temporal activities do not embed SQL directly
- **Multi-tenant** via `customer-id` header — all queries scoped by tenant, cross-tenant access returns `404`
- **Idempotency** via optional `Idempotency-Key` header — safe retries for bill creation and line item additions

Full details: [docs/system-design.md](docs/system-design.md)

## Code Review

Financial-systems audit findings are documented in [CODE_REVIEW.md](CODE_REVIEW.md).

## Testing

```bash
# Integration tests (requires Temporal dev server)
encore test ./...

# Smoke tests via shell script (requires service running)
./verify.sh
```

### Customizing verify.sh

```bash
BASE_URL=http://localhost:4000 CUSTOMER_ID=my-tenant ./verify.sh

# Use a unique tenant when rerunning against a persistent local DB, because
# verify.sh uses fixed idempotency keys for replay assertions.
CUSTOMER_ID="smoke-$(date +%s)" ./verify.sh
```

## Environment Variables

| Variable | Default | Used by |
|----------|---------|---------|
| `TEMPORAL_HOST_PORT` | `localhost:7233` | API server |
| `BASE_URL` | `http://localhost:4000` | `verify.sh` |
| `CUSTOMER_ID` | `acme-1` | `verify.sh` |

## Tech Stack

- [Encore](https://encore.dev) — service framework, API layer, DI, provisioning
- [Temporal](https://temporal.io) — long-running workflow engine
- PostgreSQL — persistent state (auto-provisioned by Encore)
