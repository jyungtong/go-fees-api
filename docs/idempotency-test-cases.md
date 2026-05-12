# User-Facing Idempotency Test Cases

Planned tests for `Idempotency-Key` support on client-facing mutation APIs.

## Scope

- `POST /bills`
- `POST /bills/:id/line-items`

## Create Bill

### `TestCreateBillWithoutIdempotencyKeyCreatesNewBills`

- Call `POST /bills` twice without `Idempotency-Key`, same payload.
- Expect two `200` responses.
- Expect different bill IDs.
- Expect two `bills` rows.

### `TestCreateBillIdempotencyReplay`

- Call `POST /bills` with `Idempotency-Key: create-1`.
- Retry with same key and same payload.
- Expect second response to match original `id`, `workflow_id`, `created_at`, `currency`, `status`.
- Expect one `bills` row for that key.
- Expect one completed idempotency record.

### `TestCreateBillIdempotencyConflict`

- Call `POST /bills` with `Idempotency-Key: create-conflict` and `{ "currency": "USD" }`.
- Retry same key with `{ "currency": "GEL" }`.
- Expect `409 Conflict`.
- Expect one bill only.

### `TestCreateBillIdempotencyDoesNotCacheValidationError`

- Call `POST /bills` with key and invalid currency.
- Expect `400 Bad Request`.
- Retry same key with valid payload.
- Expect `200`.
- Expect one bill only.

### `TestConcurrentCreateBillIdempotency`

- Start two concurrent `POST /bills` requests with same key and same payload.
- Expect both responses to resolve to same bill ID.
- Expect one bill row and one workflow.

## Add Line Item

### `TestAddLineItemWithoutIdempotencyKeyCreatesNewItems`

- Create bill.
- Call `POST /bills/:id/line-items` twice without key, same payload.
- Expect two `200` responses.
- Expect different item IDs.
- Expect two `line_items` rows.

### `TestAddLineItemIdempotencyReplay`

- Create bill.
- Add item with `Idempotency-Key: item-1`.
- Retry same key and same payload.
- Expect same `id`, `bill_id`, `amount`, `created_at`.
- Expect one `line_items` row.

### `TestAddLineItemIdempotencyConflict`

- Create bill.
- Add item with key and payload A.
- Retry same key with payload B.
- Expect `409 Conflict`.
- Expect one `line_items` row.

### `TestAddLineItemReplayAfterBillClose`

- Create bill.
- Add item with key.
- Wait until item persists.
- Close bill.
- Retry same add-item key and same payload.
- Expect original item response, not closed-bill `409`.
- Expect one `line_items` row.

### `TestAddLineItemNewKeyAfterBillClose`

- Create bill.
- Close bill.
- Add item with a new key.
- Expect `409 Conflict`.
- Expect no new `line_items` row.

### `TestAddLineItemIdempotencyDoesNotCacheValidationError`

- Create bill.
- Add invalid item with key, e.g. quantity `0`.
- Expect `400 Bad Request`.
- Retry same key with valid payload.
- Expect `200`.
- Expect one `line_items` row.

### `TestAddLineItemIdempotencyDoesNotCacheMissingBill`

- Add item to missing bill with key.
- Expect `404 Not Found`.
- Retry same scoped key against a valid bill.
- Expect success if key scope includes bill ID.

### `TestConcurrentAddLineItemIdempotency`

- Create bill.
- Start two concurrent add-item requests with same key and same payload.
- Expect both responses to resolve to same item ID.
- Expect one `line_items` row.

## Storage Behavior

### `TestIdempotencyRecordStoresRequestHashAndResponse`

- Perform successful keyed mutation.
- Query idempotency storage.
- Expect scoped key, request hash, response status/body, and completed state.

### `TestIdempotencyIncompleteRecordNotReturnedAsSuccess`

- Seed or simulate reserved idempotency row without response.
- Retry same key.
- Expect chosen behavior: wait/poll until complete, or return in-progress error.
- Do not return fake success.

## Minimum Regression Set

- `TestCreateBillIdempotencyReplay`
- `TestCreateBillIdempotencyConflict`
- `TestCreateBillIdempotencyDoesNotCacheValidationError`
- `TestAddLineItemIdempotencyReplay`
- `TestAddLineItemIdempotencyConflict`
- `TestAddLineItemReplayAfterBillClose`
- `TestAddLineItemNewKeyAfterBillClose`
- `TestAddLineItemIdempotencyDoesNotCacheValidationError`
