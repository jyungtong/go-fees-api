# Fees API — System Design

## 1. Overview

A billing service that tracks progressive fee accrual over a billing period. Line items are added throughout the period. At period close, total is computed and the bill is locked against further additions.

**Core constraint**: bills reject line item additions after close (state integrity).

**Unique approach**: Temporal workflows are used as the bill lifecycle itself — the workflow stays alive for the duration of the billing period, receiving line items as signals. No polling, no cron.

---

## 2. Architecture

```
┌─────────┐     ┌──────────────┐     ┌────────────┐
│ Client  │────▶│ Encore API   │────▶│ Temporal   │
│         │◀────│ (bill.go)    │◀────│ Workflow   │
└─────────┘     └──────┬───────┘     └─────┬──────┘
                       │                   │
                       ▼                   ▼
               ┌──────────────┐     ┌────────────┐
               │ PostgreSQL   │◀────│ Activities │
               │ (bills,      │     │ (DB ops)   │
               │  line_items) │     └────────────┘
               └──────────────┘
```

| Component | Role |
|-----------|------|
| **Encore** | Service framework, API layer, DI, auto-provisioning |
| **Temporal** | Long-running workflow engine. Workflow = bill lifecycle |
| **PostgreSQL** | Persistent state (bills, line items, idempotency records). Accessed via Encore `sqldb` |
| **Activities** | Side-effect-free DB operations called by workflow |

---

## 2a. Multi-Tenant Isolation

| Concept | Detail |
|---------|--------|
| **Tenant source** | `customer-id` HTTP header attached by auth middleware |
| **No body tenant field** | `customer_id` MUST NOT appear in request bodies |
| **Per-request** | Middleware resolves API key → `customer-id` → attached to each request |
| **Lookup scope** | All bill queries include `WHERE customer_id = :tenant` |
| **Cross-bill access** | Bill IDs are scoped — foreign/missing IDs return `404 Not Found` |
| **Idempotency scope** | Keys scoped by authenticated `customer_id` |
| **Anonymous unsupported** | No shared/empty namespace for idempotency |

---

## 3. API Design

| Method | Path | Purpose |
|--------|------|---------|
| `POST` | `/bills` | Create bill, start Temporal workflow |
| `POST` | `/bills/:id/line-items` | Add line item (signal workflow) |
| `POST` | `/bills/:id/close` | Close bill, compute total |
| `GET` | `/bills/:id` | View bill + items (direct DB query) |
| `GET` | `/bills` | List all bills |

### Request / Response

```
POST /bills
  Idempotency-Key?: client-generated retry key
  → { "currency": "USD"|"GEL" }
  ← { "id", "status": "open", "currency", "workflow_id", "created_at" }

POST /bills/:id/line-items
  Idempotency-Key?: client-generated retry key
  → { "description": "broccoli", "quantity": 1, "unit_price": "3.50" }
      // unit_price is a decimal string in major units
  ← { "id", "bill_id", "description", "quantity", "unit_price", "amount", "created_at" }

POST /bills/:id/close
  ← { "id", "status": "closed", "currency", "total", "line_items": [...], "closed_at" }

GET /bills/:id
  ← { "id", "status", "currency", "customer_id", "total", "line_items": [...],
       "created_at", "closed_at?" }

GET /bills
  ← { "bills": [{ "id", "status", ... }, ...] }
```

---

## 4. Data Model

### `bills`

| Column | Type | Notes |
|--------|------|-------|
| `id` | `UUID PK` | Bill identifier |
| `status` | `TEXT NOT NULL` | `open` or `closed` |
| `currency` | `TEXT` | `USD` or `GEL` |
| `customer_id` | `TEXT NOT NULL` | Tenant ID — set from `customer-id` header, never request body |
| `period_start` | `TIMESTAMPTZ` | Start of billing period |
| `period_end` | `TIMESTAMPTZ` | End of billing period |
| `total_amount` | `BIGINT` | Sum in minor units, set on close |
| `workflow_id` | `TEXT` | Temporal workflow execution ID |
| `created_at` | `TIMESTAMPTZ` | |
| `closed_at` | `TIMESTAMPTZ` | Set on close, nullable |
| `updated_at` | `TIMESTAMPTZ` | |

**Indexes**: `(customer_id, created_at DESC)` for tenant listing.

### `line_items`

| Column | Type | Notes |
|--------|------|-------|
| `id` | `UUID PK` | |
| `bill_id` | `UUID FK` | References `bills.id` |
| `description` | `TEXT` | Line item name |
| `quantity` | `INT` | Must be > 0 |
| `unit_price` | `BIGINT` | Price in minor units, > 0 |
| `created_at` | `TIMESTAMPTZ` | |

**Total** = `SUM(quantity * unit_price)` computed at close time. Not stored per line item.

### `idempotency_records`

| Column | Type | Notes |
|--------|------|-------|
| `customer_id` | `TEXT NOT NULL` | Customer/tenant namespace for the idempotency key |
| `scope` | `TEXT` | Mutation/resource scope, e.g. `create_bill`, `add_line_item:<bill_id>` |
| `key` | `TEXT` | Client-provided `Idempotency-Key` |
| `request_hash` | `TEXT` | Hash of canonical mutation payload |
| `state` | `TEXT` | `in_progress` or `completed` |
| `response_status` | `INT` | Stored HTTP response status for successful mutation |
| `response_body` | `JSONB` | Stored successful response body |
| `created_at` | `TIMESTAMPTZ` | |
| `updated_at` | `TIMESTAMPTZ` | |
| `completed_at` | `TIMESTAMPTZ` | Set when response is persisted |

Primary key: `(customer_id, scope, key)`.

---

## 5. Temporal Workflow Design

### Workflow: `BillWorkflow`

```
1. ExecuteActivity(CreateBillActivity) → INSERT bill (status=open)
2. Enter selector loop:
   ├─ On "AddLineItem" signal:
   │     ExecuteActivity(AddLineItemActivity) → INSERT line_item
   └─ On "CloseBill" signal:
         break loop
3. ExecuteActivity(CloseBillActivity) → SUM items, UPDATE status=closed
4. Return total
```

### Activities

| Activity | Operation |
|----------|-----------|
| `CreateBillActivity` | INSERT into `bills` |
| `AddLineItemActivity` | INSERT into `line_items` (inside TX with status check) |
| `CloseBillActivity` | `SELECT SUM` + `UPDATE bills SET status=closed` (inside TX) |

### Activity Idempotency

Temporal may retry activities after timeout or worker failure. Activities are safe to rerun:

- `CreateBillActivity`: `INSERT ... ON CONFLICT (id) DO NOTHING`; existing bill ID returns success.
- `AddLineItemActivity`: first checks `line_items.id`. If same bill/payload already exists, returns success even if bill is now closed. If not present, locks bill row, verifies `open`, then inserts with `ON CONFLICT DO NOTHING`.
- `CloseBillActivity`: locks bill row. If already `closed`, returns stored `total_amount`. If `open`, computes total and updates with `WHERE status = 'open'`; a lost update race falls back to stored total.

### User-Facing Idempotency

`POST /bills` and `POST /bills/:id/line-items` accept optional `Idempotency-Key` headers to protect client/API retries. Requests without a key preserve normal behavior: repeated calls create distinct bills or line items and no idempotency record.

- Keys are scoped by customer + mutation/resource (`customer_id`, `create_bill`, `add_line_item:<bill_id>`), so different customers can reuse the same key safely. The `customer_id` is derived from the authenticated `customer-id` header.
- Same customer + scoped key + same canonical payload returns the original successful response without creating a duplicate bill/item.
- Same customer + scoped key + different payload returns `409 Conflict`.
- Only successful mutations are cached. Validation errors, missing-resource errors, closed-bill errors for new keys, Temporal failures, and incomplete executions are not cached as successes.
- In-progress records are never returned as successful responses; concurrent same-key requests wait/poll for completion or return an in-progress error.
- Add-line-item replay checks idempotency storage before current bill status, so replaying a previously successful keyed add after bill close returns the original item response instead of `409`.

### State Integrity (two-layer guard)

**Layer 1 — API handler**: Before signaling workflow, queries `SELECT status FROM bills WHERE id = $1`. If `closed` → 409 immediately. No signal sent.

**Layer 2 — Activity**: Inside a transaction with `SELECT ... FOR UPDATE`. If status has changed to `closed` since layer 1 → activity returns error, workflow logs and skips.

---

## 6. Multi-Currency

- Currency set at bill creation: `CHECK (currency IN ('USD', 'GEL'))`
- All line items on a bill are denominated in that bill's currency
- API monetary amounts are decimal strings in major units:
  - `unit_price` = `"3.50"`
  - `amount` in response = `quantity * unit_price`, formatted as `"3.50"`
  - `total` in response = final bill total, formatted as `"3.50"`
- Database monetary amounts are stored as `BIGINT` in minor units:
  - `unit_price` = cents (USD) or tetri (GEL) — `350` = $3.50 or ₾3.50
  - `total_amount` = sum in minor units
- Integer math for totals — `SUM(quantity * unit_price)` — no floating-point, no precision drift

---

## 7. Error Responses

| Scenario | HTTP Code |
|----------|-----------|
| Missing/invalid API key or tenant context | 401 Unauthorized |
| Invalid currency (not USD/GEL) | 400 Bad Request |
| Quantity ≤ 0 or invalid/non-positive unit_price | 400 Bad Request |
| Bill not found | 404 Not Found |
| Add item to closed bill | 409 Conflict |
| Close already-closed bill | 409 Conflict |
| Same Idempotency-Key with different payload | 409 Conflict |
| Idempotency request still in progress | 409 Conflict |
| Temporal server unreachable | 500 Internal Server Error |

---

## 8. Edge Cases

### Add Items to Closed Bill

API handler queries bill status first. If closed → 409 immediately (~1ms). No Temporal signal sent.

Exception: if `Idempotency-Key` matches a completed earlier add-item request with the same payload, the API returns the original item response before checking current bill status. This makes client retries safe after a bill closes.

If a race exists (close signal sent moments before add signal, but workflow hasn't processed close yet), the activity's `FOR UPDATE` lock serializes: whichever acquires the row lock first wins. If close wins, add sees `status != 'open'` and fails.

### Close Bill with Zero Line Items

Valid. `CloseBillActivity` computes `COALESCE(SUM(quantity * unit_price), 0)` → total = 0. Bill transitions to `closed`.

### Duplicate Close

API handler queries `status` before signaling. If already `closed` → 409. No second close signal sent.

### Concurrent Add + Close

`FOR UPDATE` row lock in both activities serializes access to the bill row. Only one succeeds first; the other sees `status != 'open'` and errors.

---

## 8a. Cross-Tenant Access

- `GET /bills/:id`, `POST /bills/:id/line-items`, `POST /bills/:id/close` all query with `WHERE id = $1 AND customer_id = $2`.
- If bill not found OR bill belongs to different `customer_id` → `404 Not Found`.
- This prevents bill ID enumeration attacks.
- `GET /bills` returns only bills matching the authenticated `customer_id`.

---

## 9. Local Development

```bash
# Prerequisites: Go 1.22+, Encore CLI, Temporal CLI

# Start Temporal dev server
temporal server start-dev

# Run the service
encore run
```

Service available at `http://localhost:4000`.
