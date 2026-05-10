package bill

import (
	"context"
	"errors"
	"testing"
	"time"

	"encore.dev/beta/errs"
	"encore.dev/et"
)

func setupIntegration(t *testing.T) (context.Context, *Service) {
	t.Helper()

	ctx := context.Background()
	testDB, err := et.NewTestDatabase(ctx, "fees_db")
	if err != nil {
		t.Skipf("Encore test database unavailable: %v", err)
	}

	originalDB := db
	db = testDB
	t.Cleanup(func() {
		db = originalDB
	})

	svc, err := initService()
	if err != nil {
		t.Fatalf("init service: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		svc.Shutdown(shutdownCtx)
	})

	return ctx, svc
}

func assertErrCode(t *testing.T, err error, want errs.ErrCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want %s", want)
	}
	var encoreErr *errs.Error
	if !errors.As(err, &encoreErr) {
		t.Fatalf("error = %T %v, want Encore error code %s", err, err, want)
	}
	if encoreErr.Code != want {
		t.Fatalf("error code = %s, want %s; err = %v", encoreErr.Code, want, err)
	}
}

func waitForBill(t *testing.T, ctx context.Context, svc *Service, id string) *BillResponse {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		bill, err := svc.GetBill(ctx, id)
		if err == nil {
			return bill
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("bill %s was not readable: %v", id, lastErr)
	return nil
}

func waitForLineItemCount(t *testing.T, ctx context.Context, svc *Service, id string, want int) *BillResponse {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	var last *BillResponse
	var lastErr error
	for time.Now().Before(deadline) {
		bill, err := svc.GetBill(ctx, id)
		if err == nil {
			last = bill
			if len(bill.LineItems) == want {
				return bill
			}
		} else {
			lastErr = err
		}
		time.Sleep(100 * time.Millisecond)
	}
	if lastErr != nil {
		t.Fatalf("line items for bill %s not readable: %v", id, lastErr)
	}
	t.Fatalf("line item count for bill %s = %d, want %d", id, len(last.LineItems), want)
	return nil
}

func createBill(t *testing.T, ctx context.Context, svc *Service, req *CreateBillRequest) *CreateBillResponse {
	t.Helper()
	bill, err := svc.CreateBill(ctx, req)
	if err != nil {
		t.Fatalf("create bill: %v", err)
	}
	if bill.ID == "" {
		t.Fatal("create bill returned empty id")
	}
	if bill.WorkflowID != bill.ID {
		t.Fatalf("workflow id = %q, want bill id %q", bill.WorkflowID, bill.ID)
	}
	waitForBill(t, ctx, svc, bill.ID)
	return bill
}

func addLineItem(t *testing.T, ctx context.Context, svc *Service, billID string, req *AddLineItemRequest, wantAmount string) *AddLineItemResponse {
	t.Helper()
	item, err := svc.AddLineItem(ctx, billID, req)
	if err != nil {
		t.Fatalf("add line item: %v", err)
	}
	if item.Amount != wantAmount {
		t.Fatalf("line item amount = %q, want %q", item.Amount, wantAmount)
	}
	return item
}

func closeBill(t *testing.T, ctx context.Context, svc *Service, billID string) *CloseBillResponse {
	t.Helper()
	bill, err := svc.CloseBill(ctx, billID)
	if err != nil {
		t.Fatalf("close bill: %v", err)
	}
	if bill.Status != "closed" {
		t.Fatalf("closed bill status = %q, want closed", bill.Status)
	}
	return bill
}

func TestHealth(t *testing.T) {
	ctx, svc := setupIntegration(t)

	health, err := svc.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if health.Status != "ok" {
		t.Fatalf("health status = %q, want ok", health.Status)
	}
	if !health.TemporalReachable {
		t.Fatal("temporal should be reachable")
	}
}

func TestUSDBillLifecycle(t *testing.T) {
	ctx, svc := setupIntegration(t)

	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD", CustomerID: "acme-1"})
	if created.Status != "open" {
		t.Fatalf("created bill status = %q, want open", created.Status)
	}
	if created.Currency != "USD" {
		t.Fatalf("created bill currency = %q, want USD", created.Currency)
	}

	addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "widget", Quantity: 2, UnitPrice: "3.50"}, "7.00")
	addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "gadget", Quantity: 1, UnitPrice: "10.00"}, "10.00")
	addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "service", Quantity: 3, UnitPrice: "5.00"}, "15.00")
	waitForLineItemCount(t, ctx, svc, created.ID, 3)

	closed := closeBill(t, ctx, svc, created.ID)
	if closed.Currency != "USD" {
		t.Fatalf("closed bill currency = %q, want USD", closed.Currency)
	}
	if closed.Total != "32.00" {
		t.Fatalf("closed bill total = %q, want 32.00", closed.Total)
	}
	if len(closed.LineItems) != 3 {
		t.Fatalf("closed bill line items = %d, want 3", len(closed.LineItems))
	}

	fetched, err := svc.GetBill(ctx, created.ID)
	if err != nil {
		t.Fatalf("get bill: %v", err)
	}
	if fetched.Status != "closed" {
		t.Fatalf("fetched status = %q, want closed", fetched.Status)
	}
	if fetched.Total == nil || *fetched.Total != "32.00" {
		t.Fatalf("fetched total = %v, want 32.00", fetched.Total)
	}
	if len(fetched.LineItems) != 3 {
		t.Fatalf("fetched line items = %d, want 3", len(fetched.LineItems))
	}

	list, err := svc.ListBills(ctx)
	if err != nil {
		t.Fatalf("list bills: %v", err)
	}
	found := false
	for _, bill := range list.Bills {
		if bill.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("list bills did not include %s", created.ID)
	}
}

func TestGELBillLifecycle(t *testing.T) {
	ctx, svc := setupIntegration(t)

	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "GEL"})
	if created.Currency != "GEL" {
		t.Fatalf("created bill currency = %q, want GEL", created.Currency)
	}

	addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "khachapuri", Quantity: 1, UnitPrice: "1.00"}, "1.00")
	addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "lobiani", Quantity: 1, UnitPrice: "2.00"}, "2.00")
	waitForLineItemCount(t, ctx, svc, created.ID, 2)

	closed := closeBill(t, ctx, svc, created.ID)
	if closed.Currency != "GEL" {
		t.Fatalf("closed bill currency = %q, want GEL", closed.Currency)
	}
	if closed.Total != "3.00" {
		t.Fatalf("closed bill total = %q, want 3.00", closed.Total)
	}
}

func TestValidation(t *testing.T) {
	ctx, svc := setupIntegration(t)

	_, err := svc.CreateBill(ctx, &CreateBillRequest{Currency: "EUR"})
	assertErrCode(t, err, errs.InvalidArgument)

	_, err = svc.CreateBill(ctx, &CreateBillRequest{Currency: ""})
	assertErrCode(t, err, errs.InvalidArgument)

	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})

	_, err = svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "bad", Quantity: 0, UnitPrice: "1.00"})
	assertErrCode(t, err, errs.InvalidArgument)

	_, err = svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "bad", Quantity: -1, UnitPrice: "1.00"})
	assertErrCode(t, err, errs.InvalidArgument)

	_, err = svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "bad", Quantity: 1, UnitPrice: "0"})
	assertErrCode(t, err, errs.InvalidArgument)

	_, err = svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "bad", Quantity: 1, UnitPrice: "-1.00"})
	assertErrCode(t, err, errs.InvalidArgument)
}

func TestStateIntegrity(t *testing.T) {
	ctx, svc := setupIntegration(t)

	missingID := "00000000-0000-0000-0000-000000000000"

	_, err := svc.AddLineItem(ctx, missingID, &AddLineItemRequest{Description: "missing", Quantity: 1, UnitPrice: "1.00"})
	assertErrCode(t, err, errs.NotFound)

	_, err = svc.CloseBill(ctx, missingID)
	assertErrCode(t, err, errs.NotFound)

	_, err = svc.GetBill(ctx, missingID)
	assertErrCode(t, err, errs.NotFound)

	created := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})
	addLineItem(t, ctx, svc, created.ID, &AddLineItemRequest{Description: "item", Quantity: 1, UnitPrice: "1.00"}, "1.00")
	waitForLineItemCount(t, ctx, svc, created.ID, 1)
	closeBill(t, ctx, svc, created.ID)

	_, err = svc.AddLineItem(ctx, created.ID, &AddLineItemRequest{Description: "too late", Quantity: 1, UnitPrice: "1.00"})
	assertErrCode(t, err, errs.Aborted)

	_, err = svc.CloseBill(ctx, created.ID)
	assertErrCode(t, err, errs.Aborted)
}

func TestEdgeCases(t *testing.T) {
	ctx, svc := setupIntegration(t)

	zero := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})
	closedZero := closeBill(t, ctx, svc, zero.ID)
	if closedZero.Total != "0.00" {
		t.Fatalf("zero-item total = %q, want 0.00", closedZero.Total)
	}
	if len(closedZero.LineItems) != 0 {
		t.Fatalf("zero-item line items = %d, want 0", len(closedZero.LineItems))
	}

	large := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})
	addLineItem(t, ctx, svc, large.ID, &AddLineItemRequest{Description: "large", Quantity: 999, UnitPrice: "9999999.99"}, "9989999990.01")
	waitForLineItemCount(t, ctx, svc, large.ID, 1)
	closedLarge := closeBill(t, ctx, svc, large.ID)
	if closedLarge.Total != "9989999990.01" {
		t.Fatalf("large bill total = %q, want 9989999990.01", closedLarge.Total)
	}

	open := createBill(t, ctx, svc, &CreateBillRequest{Currency: "USD"})
	addLineItem(t, ctx, svc, open.ID, &AddLineItemRequest{Description: "open-item", Quantity: 1, UnitPrice: "1.23"}, "1.23")
	fetched := waitForLineItemCount(t, ctx, svc, open.ID, 1)
	if fetched.Status != "open" {
		t.Fatalf("open bill status = %q, want open", fetched.Status)
	}
	if fetched.Total != nil {
		t.Fatalf("open bill total = %q, want nil", *fetched.Total)
	}
}
