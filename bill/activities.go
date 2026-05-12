package bill

import (
	"context"
	"time"

	"go.temporal.io/sdk/activity"
)

type BillParams struct {
	BillID     string
	Currency   string
	CustomerID string
	CreatedAt  time.Time
}

type LineItemSignal struct {
	ID          string
	Description string
	Quantity    int
	UnitPrice   int64
}

type LineItemRecord struct {
	ID          string
	Description string
	Quantity    int
	UnitPrice   int64
	CreatedAt   time.Time
}

type CreateBillResult struct {
	BillID string
}

type CloseBillResult struct {
	Total int64
}

func CreateBillActivity(ctx context.Context, params BillParams) (*CreateBillResult, error) {
	logger := activity.GetLogger(ctx)
	info := activity.GetInfo(ctx)

	if err := NewRepository(db).InsertBill(ctx, params, info.WorkflowExecution.ID); err != nil {
		return nil, err
	}

	logger.Info("bill persisted", "billID", params.BillID)
	return &CreateBillResult{BillID: params.BillID}, nil
}

func AddLineItemActivity(ctx context.Context, billID string, signal LineItemSignal) error {
	logger := activity.GetLogger(ctx)

	if err := NewRepository(db).AddLineItem(ctx, billID, signal); err != nil {
		return err
	}

	logger.Info("line item persisted", "billID", billID, "itemID", signal.ID)
	return nil
}

func CloseBillActivity(ctx context.Context, billID string) (*CloseBillResult, error) {
	logger := activity.GetLogger(ctx)

	result, err := NewRepository(db).CloseBill(ctx, billID)
	if err != nil {
		return nil, err
	}

	logger.Info("bill closed", "billID", billID, "total", result.Total)
	return result, nil
}
