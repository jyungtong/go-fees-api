package bill

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"encore.dev/beta/errs"
)

func createBillWithKey(t *testing.T, ctx context.Context, svc *Service, key string, req *CreateBillRequest) *CreateBillResponse {
	t.Helper()
	req.IdempotencyKey = key
	return createBill(t, ctx, svc, req)
}

func addLineItemWithKey(t *testing.T, ctx context.Context, svc *Service, billID, key string, req *AddLineItemRequest, wantAmount string) *AddLineItemResponse {
	t.Helper()
	req.IdempotencyKey = key
	return addLineItem(t, ctx, svc, billID, req, wantAmount)
}

func countIdempotencyRecords(t *testing.T, ctx context.Context, customerID, scope, key string) int {
	t.Helper()
	return countRows(t, ctx, `SELECT COUNT(*) FROM idempotency_records WHERE customer_id = $1 AND scope = $2 AND key = $3`, customerID, scope, key)
}

func requireCompletedIdempotencyRecord(t *testing.T, ctx context.Context, customerID, scope, key string) map[string]any {
	t.Helper()
	var requestHash string
	var state string
	var responseStatus int
	var body []byte
	err := db.QueryRow(ctx, `
		SELECT request_hash, state, response_status, response_body
		FROM idempotency_records WHERE customer_id = $1 AND scope = $2 AND key = $3
	`, customerID, scope, key).Scan(&requestHash, &state, &responseStatus, &body)
	if err != nil {
		t.Fatalf("query idempotency record: %v", err)
	}
	if requestHash == "" {
		t.Fatal("request hash is empty")
	}
	if state != "completed" {
		t.Fatalf("state = %q, want completed", state)
	}
	if responseStatus != 200 {
		t.Fatalf("response status = %d, want 200", responseStatus)
	}
	var response map[string]any
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
	return response
}

func TestCreateBillWithoutIdempotencyKeyCreatesNewBills(t *testing.T) {
	ctx, svc := setupIntegration(t)

	first := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD", CustomerID: "same"})
	second := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD", CustomerID: "same"})

	if first.ID == second.ID {
		t.Fatalf("bill IDs matched without idempotency key: %s", first.ID)
	}
}

func TestCreateBillIdempotencyReplay(t *testing.T) {
	ctx, svc := setupIntegration(t)

	first := createBillWithKey(t, ctx, svc, "create-replay", &CreateBillRequest{Currency: "USD", CustomerID: "acme"})
	second := createBillWithKey(t, ctx, svc, "create-replay", &CreateBillRequest{Currency: "USD", CustomerID: "acme"})

	if *first != *second {
		t.Fatalf("replay response differs\nfirst:  %#v\nsecond: %#v", first, second)
	}
	if got := countRows(t, ctx, `SELECT COUNT(*) FROM bills WHERE id = $1`, first.ID); got != 1 {
		t.Fatalf("bill count = %d, want 1", got)
	}
	if got := countIdempotencyRecords(t, ctx, "acme", "create_bill", "create-replay"); got != 1 {
		t.Fatalf("idempotency record count = %d, want 1", got)
	}
}

func TestCreateBillIdempotencyKeyScopedByCustomer(t *testing.T) {
	ctx, svc := setupIntegration(t)

	first := createBillWithKey(t, ctx, svc, "shared-key", &CreateBillRequest{Currency: "USD", CustomerID: "user1"})
	second := createBillWithKey(t, ctx, svc, "shared-key", &CreateBillRequest{Currency: "GEL", CustomerID: "user2"})

	if first.ID == second.ID {
		t.Fatalf("bill IDs matched across customers: %s", first.ID)
	}
	if got := countIdempotencyRecords(t, ctx, "user1", "create_bill", "shared-key"); got != 1 {
		t.Fatalf("user1 idempotency record count = %d, want 1", got)
	}
	if got := countIdempotencyRecords(t, ctx, "user2", "create_bill", "shared-key"); got != 1 {
		t.Fatalf("user2 idempotency record count = %d, want 1", got)
	}
}

func TestCreateBillIdempotencyConflict(t *testing.T) {
	ctx, svc := setupIntegration(t)

	first := createBillWithKey(t, ctx, svc, "create-conflict", &CreateBillRequest{Currency: "USD", CustomerID: "acme"})
	_, err := svc.CreateBill(ctx, &CreateBillRequest{Currency: "GEL", CustomerID: "acme", IdempotencyKey: "create-conflict"})
	assertErrCode(t, err, errs.Aborted)

	if got := countRows(t, ctx, `SELECT COUNT(*) FROM bills WHERE id = $1`, first.ID); got != 1 {
		t.Fatalf("original bill count = %d, want 1", got)
	}
}

func TestCreateBillIdempotencyDoesNotCacheValidationError(t *testing.T) {
	ctx, svc := setupIntegration(t)

	_, err := svc.CreateBill(ctx, &CreateBillRequest{Currency: "EUR", CustomerID: defaultTestCustomerID, IdempotencyKey: "create-validation"})
	assertErrCode(t, err, errs.InvalidArgument)
	if got := countIdempotencyRecords(t, ctx, defaultTestCustomerID, "create_bill", "create-validation"); got != 0 {
		t.Fatalf("idempotency record count after validation error = %d, want 0", got)
	}

	createBillWithKey(t, ctx, svc, "create-validation", &CreateBillRequest{Currency: "USD"})
}

func TestConcurrentCreateBillIdempotency(t *testing.T) {
	ctx, svc := setupIntegration(t)

	const workers = 8
	results := make(chan *CreateBillResponse, workers)
	errsCh := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.CreateBill(ctx, &CreateBillRequest{Currency: "USD", CustomerID: "concurrent", IdempotencyKey: "create-concurrent"})
			if err != nil {
				errsCh <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)
	close(errsCh)
	for err := range errsCh {
		t.Fatalf("concurrent create bill: %v", err)
	}

	var first *CreateBillResponse
	for res := range results {
		if first == nil {
			first = res
			continue
		}
		if *first != *res {
			t.Fatalf("concurrent response differs\nfirst: %#v\nnext:  %#v", first, res)
		}
	}
	if first == nil {
		t.Fatal("no concurrent create results")
	}
	if got := countRows(t, ctx, `SELECT COUNT(*) FROM bills WHERE customer_id = $1`, "concurrent"); got != 1 {
		t.Fatalf("bill count = %d, want 1", got)
	}
}

func TestAddLineItemWithoutIdempotencyKeyCreatesNewItems(t *testing.T) {
	ctx, svc := setupIntegration(t)
	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})

	first := addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "same", Quantity: 1, UnitPrice: "1.00"}, "1.00")
	second := addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "same", Quantity: 1, UnitPrice: "1.00"}, "1.00")

	if first.ID == second.ID {
		t.Fatalf("line item IDs matched without idempotency key: %s", first.ID)
	}
	waitForLineItemCount(t, ctx, svc, created.ID, 2)
}

func TestAddLineItemIdempotencyReplay(t *testing.T) {
	ctx, svc := setupIntegration(t)
	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})

	first := addLineItemWithKey(t, ctx, svc, created.ID, "add-replay", &AddLineItemRequest{Description: "apple", Quantity: 2, UnitPrice: "1.25"}, "2.50")
	second := addLineItemWithKey(t, ctx, svc, created.ID, "add-replay", &AddLineItemRequest{Description: "apple", Quantity: 2, UnitPrice: "1.25"}, "2.50")

	if *first != *second {
		t.Fatalf("replay response differs\nfirst:  %#v\nsecond: %#v", first, second)
	}
	waitForLineItemCount(t, ctx, svc, created.ID, 1)
}

func TestAddLineItemIdempotencyConflict(t *testing.T) {
	ctx, svc := setupIntegration(t)
	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})

	addLineItemWithKey(t, ctx, svc, created.ID, "add-conflict", &AddLineItemRequest{Description: "apple", Quantity: 1, UnitPrice: "1.00"}, "1.00")
	_, err := svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "pear", Quantity: 1, UnitPrice: "1.00", CustomerID: billCustomerID(t, ctx, created.ID), IdempotencyKey: "add-conflict"})
	assertErrCode(t, err, errs.Aborted)
	waitForLineItemCount(t, ctx, svc, created.ID, 1)
}

func TestAddLineItemReplayAfterBillClose(t *testing.T) {
	ctx, svc := setupIntegration(t)
	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})

	first := addLineItemWithKey(t, ctx, svc, created.ID, "add-after-close", &AddLineItemRequest{Description: "apple", Quantity: 1, UnitPrice: "1.00"}, "1.00")
	waitForLineItemCount(t, ctx, svc, created.ID, 1)
	closeBill(t, ctx, svc, created.ID)
	second := addLineItemWithKey(t, ctx, svc, created.ID, "add-after-close", &AddLineItemRequest{Description: "apple", Quantity: 1, UnitPrice: "1.00"}, "1.00")

	if *first != *second {
		t.Fatalf("replay after close differs\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func TestAddLineItemNewKeyAfterBillClose(t *testing.T) {
	ctx, svc := setupIntegration(t)
	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})
	closeBill(t, ctx, svc, created.ID)

	_, err := svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "late", Quantity: 1, UnitPrice: "1.00", CustomerID: billCustomerID(t, ctx, created.ID), IdempotencyKey: "new-key-after-close"})
	assertErrCode(t, err, errs.Aborted)
	if got := countIdempotencyRecords(t, ctx, billCustomerID(t, ctx, created.ID), "add_line_item:"+created.ID, "new-key-after-close"); got != 0 {
		t.Fatalf("idempotency record count after closed-bill error = %d, want 0", got)
	}
}

func TestAddLineItemIdempotencyDoesNotCacheValidationError(t *testing.T) {
	ctx, svc := setupIntegration(t)
	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})

	_, err := svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "bad", Quantity: 0, UnitPrice: "1.00", CustomerID: billCustomerID(t, ctx, created.ID), IdempotencyKey: "add-validation"})
	assertErrCode(t, err, errs.InvalidArgument)
	if got := countIdempotencyRecords(t, ctx, billCustomerID(t, ctx, created.ID), "add_line_item:"+created.ID, "add-validation"); got != 0 {
		t.Fatalf("idempotency record count after validation error = %d, want 0", got)
	}
	addLineItemWithKey(t, ctx, svc, created.ID, "add-validation", &AddLineItemRequest{Description: "good", Quantity: 1, UnitPrice: "1.00"}, "1.00")
}

func TestAddLineItemIdempotencyDoesNotCacheMissingBill(t *testing.T) {
	ctx, svc := setupIntegration(t)
	missingID := "00000000-0000-0000-0000-000000000000"

	_, err := svc.AddLineItem(ctx, missingID, &AddLineItemRequest{Description: "missing", Quantity: 1, UnitPrice: "1.00", CustomerID: defaultTestCustomerID, IdempotencyKey: "add-missing"})
	assertErrCode(t, err, errs.NotFound)
	if got := countIdempotencyRecords(t, ctx, defaultTestCustomerID, "add_line_item:"+missingID, "add-missing"); got != 0 {
		t.Fatalf("idempotency record count after missing-bill error = %d, want 0", got)
	}
}

func TestAddLineItemIdempotencyKeyScopedByBillCustomer(t *testing.T) {
	ctx, svc := setupIntegration(t)
	firstBill := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD", CustomerID: "user1"})
	secondBill := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD", CustomerID: "user2"})

	first := addLineItemWithKey(t, ctx, svc, firstBill.ID, "shared-add", &AddLineItemRequest{Description: "apple", Quantity: 1, UnitPrice: "1.00"}, "1.00")
	second := addLineItemWithKey(t, ctx, svc, secondBill.ID, "shared-add", &AddLineItemRequest{Description: "apple", Quantity: 1, UnitPrice: "1.00"}, "1.00")

	if first.ID == second.ID {
		t.Fatalf("line item IDs matched across customers: %s", first.ID)
	}
	waitForLineItemCount(t, ctx, svc, firstBill.ID, 1)
	waitForLineItemCount(t, ctx, svc, secondBill.ID, 1)
}

func TestConcurrentAddLineItemIdempotency(t *testing.T) {
	ctx, svc := setupIntegration(t)
	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})

	const workers = 8
	results := make(chan *AddLineItemResponse, workers)
	errsCh := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "concurrent", Quantity: 1, UnitPrice: "2.00", CustomerID: billCustomerID(t, ctx, created.ID), IdempotencyKey: "add-concurrent"})
			if err != nil {
				errsCh <- err
				return
			}
			results <- res
		}()
	}
	wg.Wait()
	close(results)
	close(errsCh)
	for err := range errsCh {
		t.Fatalf("concurrent add line item: %v", err)
	}

	var first *AddLineItemResponse
	for res := range results {
		if first == nil {
			first = res
			continue
		}
		if *first != *res {
			t.Fatalf("concurrent response differs\nfirst: %#v\nnext:  %#v", first, res)
		}
	}
	waitForLineItemCount(t, ctx, svc, created.ID, 1)
}

func TestIdempotencyRecordStoresRequestHashAndResponse(t *testing.T) {
	ctx, svc := setupIntegration(t)

	created := createBillWithKey(t, ctx, svc, "record-storage", &CreateBillRequest{Currency: "USD", CustomerID: "storage"})
	response := requireCompletedIdempotencyRecord(t, ctx, "storage", "create_bill", "record-storage")
	if response["id"] != created.ID {
		t.Fatalf("stored response id = %v, want %s", response["id"], created.ID)
	}
}

func TestIdempotencyIncompleteRecordNotReturnedAsSuccess(t *testing.T) {
	ctx, svc := setupIntegration(t)

	_, err := db.Exec(ctx, `
		INSERT INTO idempotency_records (customer_id, scope, key, request_hash, state)
		VALUES ($1, $2, $3, $4, 'in_progress')
	`, defaultTestCustomerID, "create_bill", "incomplete", "stuck-hash")
	if err != nil {
		t.Fatalf("insert incomplete record: %v", err)
	}

	_, err = svc.CreateBill(ctx, &CreateBillRequest{Currency: "USD", CustomerID: defaultTestCustomerID, IdempotencyKey: "incomplete"})
	assertErrCode(t, err, errs.Aborted)
	if got := countRows(t, ctx, `SELECT COUNT(*) FROM bills`); got != 0 {
		t.Fatalf("bill count after incomplete record = %d, want 0", got)
	}
}
