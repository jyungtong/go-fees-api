# Progress

## Completed
- [x] Step 1: Project scaffold
  - Encore CLI installed (brew, v1.57.1)
  - `encore app create go-fees-api`
  - `bill/migrations/` created
  - Git history restored, pushed to `origin main`
  - Commit: `chore: scaffold Encore project`
- [x] Step 2: DB migration (`bill/migrations/1_create_tables.up.sql`)
  - Two tables: `bills` + `line_items`
  - BIGINT for monetary amounts (cents/tetri)
  - CHECK constraints on status, currency, quantity, unit_price
  - Index on `line_items.bill_id`
- [x] Step 3: Service struct + DB handle (`bill/service.go`)
  - `var db` — sqldb.NewDatabase("fees_db", ...)
  - `//encore:service` struct with `temporalClient client.Client`
  - `initService()` dials Temporal at `localhost:7233`
  - `Shutdown(force)` closes client
  - No worker/registration (deferred to step 4)
- [x] Step 3b: Health endpoint (`bill/health.go`)
  - `GET /health` — returns `{status, temporal_reachable}`
  - Uses `client.CheckHealthRequest` to verify Temporal connectivity
  - Verified: `encore run` compiles, Docker PG starts, health endpoint responds
- [x] Step 4: Temporal activities (`bill/activities.go`)
  - CreateBillActivity — INSERT bills row with workflow ID
  - AddLineItemActivity — FOR UPDATE check + INSERT line item
  - CloseBillActivity — FOR UPDATE + SUM amounts + SET closed
  - All monetary values int64 (BIGINT — cents/tetri)
  - Shared types: BillParams, LineItemSignal, LineItemRecord, results
- [x] Step 5: Temporal workflow (`bill/workflow.go`)
  - BillWorkflow: CreateBillActivity → selector loop (AddLineItem/CloseBill) → CloseBillActivity
  - Workflow + activities registered to worker in initService()
  - Worker started on taskQueue "bill-task-queue"
- [x] Step 6: API endpoints (`bill/bill.go`)
  - Added `CreatedAt` to `BillParams` and `CreateBillActivity` (DB as source of truth)
  - Implemented 5 endpoints: CreateBill, AddLineItem, CloseBill, GetBill, ListBills
  - Layer 1 status checks for 409 handling
  - CloseBill blocks until workflow completes

## In Progress
- [ ] Verify: full integration test

## Future Improvements

### Activity Idempotency (Production Hardening)
Temporal retries activities on timeout. Risk of duplicate inserts on retry:
- CreateBillActivity: `INSERT ... ON CONFLICT (id) DO NOTHING`
- AddLineItemActivity: Same `ON CONFLICT` on signal.ID
- CloseBillActivity: `UPDATE ... WHERE status = 'open'` + check `RowsAffected`; if 0, query existing total instead of erroring
