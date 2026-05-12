# Financial Systems Code Review

Role: Senior Principal Engineer and Financial Systems Auditor.

Scope: Fees API review for precision, idempotency, transactionality, tiering, auditability, compliance, performance, and race conditions.

## Critical Findings

| Location | Finding | Financial Risk Impact |
|---|---|---|
| `bill/idempotency.go:25-28`, `README.md:62`, `bill/idempotency_test.go:58-67,171-182` | Idempotency is optional. Requests without `Idempotency-Key` create duplicate bills/line items on retry by design. Make idempotency mandatory for all mutating endpoints. | Duplicate fees, customer disputes, regulatory complaints |
| `bill/bill.go:189-219`, `bill/idempotency.go:65-80`, `bill/workflow.go:35-38` | `AddLineItem` returns success and stores idempotency response after `SignalWorkflow`, before DB persistence. Workflow activity failure is only logged, so client can receive a permanent successful replay for a line item never posted. | Revenue leakage, ledger mismatch, audit failure |
| `bill/workflow.go:29-49` | Close signal can terminate workflow before pending add signals are durably processed. Accepted add responses can be excluded from final total. Need deterministic drain/order protocol or synchronous command persistence before acknowledgement. | Underbilling, customer dispute, reconciliation break |
| `bill/bill.go:115,160,223,271,306`, `bill/bill.go:92-97` | Public APIs trust caller-supplied `customer-id` header as tenant identity. No authentication, signed tenant context, or auth middleware exists in code. | Unauthorized bill access/mutation, PCI/PII breach, regulatory fine |
| `bill/migrations/1_create_tables.up.sql:1-24`, `bill/repository.go:255-269` | No fee schedule, versioned rate table, tier boundary model, or execution-time schedule snapshot exists. System cannot prove which fee schedule produced a fee. | Audit failure, regulatory fine, fee dispute exposure |

## High Findings

| Location | Finding | Financial Risk Impact |
|---|---|---|
| `bill/bill.go:223-248`, `bill/bill.go:230-235` | `CloseBill` has no client idempotency. Network retry after successful close returns conflict instead of original close response. | Operational retry failure, customer dispute |
| `bill/bill.go:166-178,217`, `bill/repository.go:257` | Quantity and amount arithmetic lacks overflow bounds. `int64(req.Quantity) * unitPriceMinor` can overflow in Go; Postgres `SUM(quantity * unit_price)` can overflow or fail. | Incorrect totals, failed closes, revenue leakage |
| `bill/money.go:11-51` | Money model hardcodes 2 decimal places. No ISO 4217 exponent table, no high-precision decimal, no Half-Even rounding policy for percentage/tier fees. | Cross-currency mispricing, regulatory remediation |
| `bill/activities.go:71`, `bill/workflow.go:54` | Logs include bill totals and identifiers. No log classification/redaction policy for financial data. | Sensitive financial data leakage, compliance violation |
| `bill/idempotency.go:65-71` | On mutation error, idempotency record is deleted. If side effect succeeded but response completion failed or context canceled, retry can execute a second mutation. | Duplicate charges, ledger inconsistency |

## Medium Findings

| Location | Finding | Financial Risk Impact |
|---|---|---|
| `bill/idempotency.go:35-60`, `bill/migrations/2_create_idempotency_records.up.sql:1-13` | `in_progress` idempotency records have no expiry/recovery owner. Crash can block retries indefinitely, returning `idempotency_in_progress`. | Payment processing outage, SLA breach |
| `bill/repository.go:115-121`, `bill/bill.go:307-327` | `ListBills` is unpaginated. Large tenants can trigger slow queries/large responses and exceed latency budget. | 50ms latency breach, availability risk |
| `bill/migrations/1_create_tables.up.sql:15-22`, `bill/bill.go:28-33` | `description`, `customer-id`, and `Idempotency-Key` have no length limits. Large inputs can bloat DB rows, indexes, logs, and Temporal history. | DoS risk, latency spike, storage cost |
| `bill/bill.go:68-76,293-299` | `GetBill` returns `customer_id` in response. Tenant identifiers should not be exposed unless required and authorized. | PII/tenant metadata leakage |
| Entire codebase | No override/manual adjustment flow with mandatory `reason_code` and `operator_id`. | Audit gap, SOX/PCI control failure |

## Precision Summary

No direct `float` or `double` currency math was found; current totals use integer minor units. Main precision gap is missing decimal and rounding support for percentage or tiered fee calculations.
