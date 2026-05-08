# Understanding Fees API concept

**Session ID:** ses_1fd33ef21ffeoAc782dyOeXM5g
**Created:** 5/7/2026, 10:16:28 PM
**Updated:** 5/8/2026, 10:31:17 PM

---

## User

i'm working on an assignment to create a Fees API

```
The Challenge

Your challenge is to build a Fees API in Encore (encore.dev) that uses a Temporal (temporal.io) workflow started at the beginning of a fee period, and allows for the progressive accrual of fees. At the end of the billing period, the total invoice and bill summation should be available.



Requirements





Create New Bill; Able to initiate a new bill/workflow at the start of a period 



Add Line Items: Able to add line items to an existing open bill 



Close Active Bill: 

a. Indicate total amount being charged

b. Indicate all line items being charged



State Integrity; Reject line item addition of the bill is closed (bill already charged)



Multi-currency: Able to handle different types of currency, for the sake of simplicity, assume GEL and USD 

You are free to design the RESTFul API that will be used by other services. The above requirements are not exhaustive, you’re free to add in requirements that you think are helpful in making the Fees API more flexible and complete.
```

before jumping straight to solution, i wanted to understand, what can be a fees api means 

---

## Assistant (Plan · DeepSeek V4 Pro · 30.0s)

_Thinking:_

The user is asking me to help them understand what a "Fees API" means in the context of their assignment. They're NOT asking me to build it yet - they're in the planning/understanding phase.

Let me think about this:

A "Fees API" in this context is a billing/invoicing service. Let me break down what the requirements are telling us:

1. **Fee Period/Billing Cycle**: There's a concept of a billing period that has a start and end. A workflow begins at the start.

2. **Bill/Invoice Management**: The system manages bills - creating them, adding line items, closing them.

3. **Workflow (Temporal)**: Temporal is a workflow orchestration engine. The idea is that when a billing period starts, a Temporal workflow is kicked off. This workflow:
   - Stays alive for the duration of the billing period
   - Accepts "signals" (line item additions) during the open period
   - At the end, produces a final invoice/total

4. **Progressive Accrual**: Fees accumulate over time during the billing period. Line items can be added progressively.

5. **Multi-currency**: Supports at least GEL (Georgian Lari) and USD.

The assignment is really about building a billing/invoicing microservice. The "Fees API" is:

- **A REST API** that lets other services:
  - Start a new billing period (creates a bill + Temporal workflow)
  - Add line items/charges to an open bill
  - Close a bill (finalize charges, calculate total)
  - Query bill status and details

- **A Temporal Workflow** that:
  - Represents an active billing period
  - Receives line items as signals
  - Handles the lifecycle (open → closed)
  - Computes totals at closure

The term "Fees API" is somewhat abstract - it's essentially a service that tracks and accumulates charges/fees for a customer or account during a billing period, then produces an invoice at the end. Think of it like how AWS accumulates usage charges during a month and then bills you at month-end.

This is a common pattern in:
- Telecom billing (call records added throughout month, bill generated at end)
- Cloud usage billing (usage events accumulate)
- Subscription platforms (add-on charges during month)
- Financial services (management fees, transaction fees)

The key architectural insight is that Temporal workflows are **long-running** - they're perfect for representing a billing period that might last 30 days, staying alive the whole time to accept signals for new line items.

Should I help the user explore the project structure or discuss implementation approaches? They seem to want conceptual understanding first.

Fees API = **billing/invoicing service**. Think of it like:

- A **bill** represents one customer's charges for one billing period
- **Line items** are individual charges accrued progressively (like AWS adding EC2 hours throughout the month)
- The **Temporal workflow** is the "open bill" — it sits alive for the period, receiving line items via Temporal **signals**, then finalizes at closure
- At close, it computes the total and locks the bill (no more additions)

Real-world analogs: telecom billing (calls accumulate, invoice at month-end), cloud usage billing, subscription platforms with add-on charges.

The unique insight is that Temporal workflows are **long-running** — you'd have a workflow alive for potentially 30 days, receiving signals (line items) the whole time, then closing at period end. That's the "progressive accrual" piece.

Want me to sketch out the API design, data model, and Temporal workflow structure next?

---

## User

i wanted to visualize my understanding, do cmiiw

assume food supply vendor:

1. january billing started
2. purchased 1x brocolli 1/1/2026
3. purchased 2x chili 2/1/2026
4. purchased 3x orange juice 3/1/2026
5. january ended
6. bill generated and issued to client

is that what a fees api looks like?

---

## Assistant (Plan · DeepSeek V4 Pro · 13.8s)

_Thinking:_

The user is asking me to confirm their understanding of what a Fees API would look like, using a food supply vendor example. They're in plan mode, so I should not make any changes. Let me just validate their understanding with a concise response.

Yes, correct. That's the core flow:

```
Billing Period Open (Temporal workflow alive)
  │
  ├─ 1/1  signal: add line item (1x broccoli)
  ├─ 2/1  signal: add line item (2x chili)
  ├─ 3/1  signal: add line item (3x orange juice)
  │
  ▼
Period End → workflow closes → compute total → lock bill → ready for invoicing
```

Key nuance: Temporal workflow is the **bill lifecycle** itself. It stays alive for the full month, waiting for signals. No polling, no cron jobs checking "did we get a new line item?" — the workflow **blocks on signal reception** until closed.

The API would have endpoints like:

- `POST /bills` → starts workflow (new billing period)
- `POST /bills/:id/line-items` → signals the workflow (add charge)
- `POST /bills/:id/close` → closes workflow → computes total

CMIIW check: your understanding is solid.

---

## User

how does the temporal setup looks like in this scenario

---

## Assistant (Plan · DeepSeek V4 Pro · 8.7s)

_Thinking:_

The user is in plan mode, so I cannot make any edits or run non-readonly commands. They want to understand what the Temporal setup looks like for this Fees API scenario. Let me explain the Temporal workflow architecture conceptually for this billing scenario.

I should explain:
1. The Temporal worker setup
2. The workflow definition (long-running, awaiting signals)
3. The activity definitions
4. How Encore integrates with Temporal

Let me look at the project directory first to understand what they have.

**Tool: read**

**Input:**
```json
{
  "filePath": "/Users/jyung/Documents/dev/personal/go-fees-api"
}
```

**Output:**
```
<path>/Users/jyung/Documents/dev/personal/go-fees-api</path>
<type>directory</type>
<entries>


(0 entries)
</entries>
```

---

## Assistant (Plan · DeepSeek V4 Pro · 7.9s)

_Thinking:_

Empty project. Let me explain the Temporal setup conceptually.

For a Fees API with Encore + Temporal, the setup looks like:

1. **Temporal Worker** - runs inside Encore service, registers workflows and activities
2. **Workflow Definition** - the bill lifecycle (long-running, signal-based)
3. **Activities** - side-effect-free operations (e.g., save to DB, compute totals)
4. **Signal Handlers** - how the workflow receives line items

Let me also check if they have Go setup or anything.

**Tool: glob**

**Input:**
```json
{
  "pattern": "**/*",
  "path": "/Users/jyung/Documents/dev/personal/go-fees-api"
}
```

**Output:**
```
No files found
```

---

## Assistant (Plan · DeepSeek V4 Pro · 38.2s)

_Thinking:_

Empty project. Let me explain the Temporal architecture conceptually. Since we're in plan mode, I'll draw out the structure.

The Temporal setup for this Fees API:

```
encore.app
├── bill/                    # Encore service
│   ├── bill.go             # Encore API endpoints
│   ├── workflow.go         # Temporal workflow definition
│   └── activities.go       # Temporal activities
├── temporal/               # Temporal worker orchestration
│   └── worker.go           # Worker registration
└── migrations/
    └── 1_bill.sql
```

Key concepts:

1. **Workflow** = the bill itself. It's a Go function that:
   - Receives bill metadata on start (customer, currency, period)
   - Awaits signals (line items) via `workflow.GetSignalChannel`
   - Awaits close signal
   - On close, runs activity to compute total
   - Rejects signals after close

2. **Activities** = DB operations, calculations. Workflows call activities (they never touch DB directly):
   - `CreateBillActivity` - persist bill to DB
   - `AddLineItemActivity` - persist line item
   - `CloseBillActivity` - compute total, lock bill

3. **Signals** = how external API endpoints communicate with running workflows:
   - `AddLineItem` signal → workflow receives and calls AddLineItemActivity
   - `CloseBill` signal → workflow exits wait loop, computes total

4. **Encore integration**: Encore has built-in Temporal support. You define workflows as Encore services with `encore:service` annotation and use `encore.dev/temporal` package.

Let me sketch this more concretely.

Empty project. Let me sketch the Temporal architecture:

```
┌─────────────────────────────────────────────────┐
│ Encore API (bill.go)                             │
│  POST /bills          → start workflow           │
│  POST /bills/:id/items → signal workflow         │
│  POST /bills/:id/close → signal workflow (close) │
└──────────────┬──────────────────────────────────┘
               │ client.StartWorkflow / SignalWorkflow
┌──────────────▼──────────────────────────────────┐
│ Temporal Workflow (bill_workflow.go)             │
│  Long-running, survives restarts                  │
│                                                   │
│  func BillWorkflow(ctx, params) {                 │
│    // 1. Persist bill via activity                │
│    err := CreateBillActivity(ctx, bill)           │
│                                                   │
│    // 2. Wait loop — receive line items            │
│    for {                                          │
│      selector.Select(ctx)                         │
│      case addLineItemSig:                         │
│        if closed → reject                          │
│        else → AddLineItemActivity(ctx, item)      │
│      case closeSig:                               │
│        goto COMPUTE                                │
│    }                                              │
│                                                   │
│    COMPUTE:                                       │
│    // 3. Close — compute + lock                    │
│    total := CloseBillActivity(ctx, billID)         │
│    return total                                   │
│  }                                                │
└──────────────┬──────────────────────────────────┘
               │ calls activities (never touches DB directly)
┌──────────────▼──────────────────────────────────┐
│ Activities (activities.go)                        │
│  CreateBillActivity → INSERT bill (status=OPEN)   │
│  AddLineItemActivity → INSERT line_item           │
│  CloseBillActivity → SUM items, UPDATE status=CLOSED│
└─────────────────────────────────────────────────┘
```

**Flow for your broccoli/chili example:**

```
POST /bills {currency:"USD", period_start:"2026-01-01"}
  → starts BillWorkflow
  → workflow runs CreateBillActivity → bill #1 in DB (status=OPEN)
  → workflow enters selector loop (alive, waiting)

POST /bills/1/items {item:"broccoli", qty:1}
  → signals workflow with AddLineItem
  → workflow calls AddLineItemActivity → inserts line_item row

POST /bills/1/items {item:"chili", qty:2}
  → same signal path

POST /bills/1/close
  → signals workflow with CloseBill
  → workflow breaks loop, calls CloseBillActivity
  → computes SUM(amount) across line_items
  → updates bill status=CLOSED, total=$X

POST /bills/1/items {item:"orange juice"}  ← REJECTED (bill closed)
```

**Why Temporal instead of a simple state machine?**
- Workflow survives server restarts (Temporal replays history)
- No need for cron/cleanup if close never happens
- Built-in retry for activities
- Can handle `ContinueAsNew` for very long-running bills

**Encore-specific setup**: Encore has a temporal package where you declare workflows with `encore:workflow` annotation and the worker auto-registers. No manual worker/spinning up required.

That's the high-level shape. Want me to plan out the full implementation with file structure and actual Go code?

---

