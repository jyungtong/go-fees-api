package bill

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/workflow"
)

func BillWorkflow(ctx workflow.Context, params BillParams) (*CloseBillResult, error) {
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Second,
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var createResult CreateBillResult
	err := workflow.ExecuteActivity(ctx, CreateBillActivity, params).Get(ctx, &createResult)
	if err != nil {
		logger.Error("create bill activity failed", "error", err)
		return nil, fmt.Errorf("create bill: %w", err)
	}
	logger.Info("bill created", "billID", params.BillID)

	addCh := workflow.GetSignalChannel(ctx, "AddLineItem")
	closeCh := workflow.GetSignalChannel(ctx, "CloseBill")
	closed := false

	for !closed {
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(addCh, func(c workflow.ReceiveChannel, more bool) {
			var signal LineItemSignal
			c.Receive(ctx, &signal)
			logger.Info("received AddLineItem signal", "billID", params.BillID, "itemID", signal.ID)
			err := workflow.ExecuteActivity(ctx, AddLineItemActivity, params.BillID, signal).Get(ctx, nil)
			if err != nil {
				logger.Error("add line item activity failed", "error", err, "itemID", signal.ID)
			}
		})
		selector.AddReceive(closeCh, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, nil)
			logger.Info("received CloseBill signal", "billID", params.BillID)
			closed = true
		})
		selector.Select(ctx)
	}

	var closeResult CloseBillResult
	err = workflow.ExecuteActivity(ctx, CloseBillActivity, params.BillID).Get(ctx, &closeResult)
	if err != nil {
		logger.Error("close bill activity failed", "error", err)
		return nil, fmt.Errorf("close bill: %w", err)
	}
	logger.Info("bill closed", "billID", params.BillID, "total", closeResult.Total)

	return &closeResult, nil
}
