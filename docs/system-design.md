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
| **PostgreSQL** | Persistent state (bills, line items). Accessed via Encore `sqldb` |
| **Activities** | Side-effect-free DB operations called by workflow |

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
  → { "currency": "USD"|"GEL", "customer_id"?: "client-1" }
  ← { "id", "status": "open", "currency", "workflow_id", "created_at" }

POST /bills/:id/line-items
  → { "description": "broccoli", "quantity": 1, "unit_price": 350 }
      // unit_price in minor units: 350 = $3.50
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
| `status` | `TEXT` | `open` or `closed` |
| `currency` | `TEXT` | `USD` or `GEL` |
| `customer_id` | `TEXT` | Optional customer reference |
| `period_start` | `TIMESTAMPTZ` | Start of billing period |
| `period_end` | `TIMESTAMPTZ` | End of billing period |
| `total_amount` | `BIGINT` | Sum in minor units, set on close |
| `workflow_id` | `TEXT` | Temporal workflow execution ID |
| `created_at` | `TIMESTAMPTZ` | |
| `closed_at` | `TIMESTAMPTZ` | Set on close, nullable |
| `updated_at` | `TIMESTAMPTZ` | |

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

### State Integrity (two-layer guard)

**Layer 1 — API handler**: Before signaling workflow, queries `SELECT status FROM bills WHERE id = $1`. If `closed` → 409 immediately. No signal sent.

**Layer 2 — Activity**: Inside a transaction with `SELECT ... FOR UPDATE`. If status has changed to `closed` since layer 1 → activity returns error, workflow logs and skips.

---

## 6. Multi-Currency

- Currency set at bill creation: `CHECK (currency IN ('USD', 'GEL'))`
- All line items on a bill are denominated in that bill's currency
- All monetary amounts stored as `BIGINT` in minor units:
  - `unit_price` = cents (USD) or tetri (GEL) — `350` = $3.50 or ₾3.50
  - `total_amount` = sum in minor units
  - `amount` in response = `quantity * unit_price`
- Integer math for totals — `SUM(quantity * unit_price)` — no floating-point, no precision drift

---

## 7. Error Responses

| Scenario | HTTP Code |
|----------|-----------|
| Invalid currency (not USD/GEL) | 400 Bad Request |
| Quantity or unit_price ≤ 0 | 400 Bad Request |
| Bill not found | 404 Not Found |
| Add item to closed bill | 409 Conflict |
| Close already-closed bill | 409 Conflict |
| Temporal server unreachable | 500 Internal Server Error |

---

## 8. Edge Cases

### Add Items to Closed Bill

API handler queries bill status first. If closed → 409 immediately (~1ms). No Temporal signal sent.

If a race exists (close signal sent moments before add signal, but workflow hasn't processed close yet), the activity's `FOR UPDATE` lock serializes: whichever acquires the row lock first wins. If close wins, add sees `status != 'open'` and fails.

### Close Bill with Zero Line Items

Valid. `CloseBillActivity` computes `COALESCE(SUM(quantity * unit_price), 0)` → total = 0. Bill transitions to `closed`.

### Duplicate Close

API handler queries `status` before signaling. If already `closed` → 409. No second close signal sent.

### Concurrent Add + Close

`FOR UPDATE` row lock in both activities serializes access to the bill row. Only one succeeds first; the other sees `status != 'open'` and errors.

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
