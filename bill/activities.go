package bill

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

func CreateBillActivity(ctx context.Context, params BillParams) (*CreateBillResult, error) {
	logger := activity.GetLogger(ctx)
	info := activity.GetInfo(ctx)

	_, err := db.Exec(ctx, `
		INSERT INTO bills (id, status, currency, customer_id, workflow_id, created_at)
		VALUES ($1, 'open', $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING
	`, params.BillID, params.Currency, params.CustomerID, info.WorkflowExecution.ID, params.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert bill: %w", err)
	}

	logger.Info("bill persisted", "billID", params.BillID)
	return &CreateBillResult{BillID: params.BillID}, nil
}

func AddLineItemActivity(ctx context.Context, billID string, signal LineItemSignal) error {
	logger := activity.GetLogger(ctx)

	tx, err := db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var existing struct {
		BillID      string
		Description string
		Quantity    int
		UnitPrice   int64
	}
	err = tx.QueryRow(ctx, `
		SELECT bill_id, description, quantity, unit_price
		FROM line_items WHERE id = $1
	`, signal.ID).Scan(&existing.BillID, &existing.Description, &existing.Quantity, &existing.UnitPrice)
	if err == nil {
		if existing.BillID != billID || existing.Description != signal.Description || existing.Quantity != signal.Quantity || existing.UnitPrice != signal.UnitPrice {
			return fmt.Errorf("line item %s already exists with different payload", signal.ID)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit tx: %w", err)
		}
		logger.Info("line item already persisted", "billID", billID, "itemID", signal.ID)
		return nil
	}
	if !isNoRows(err) {
		return fmt.Errorf("query line item: %w", err)
	}

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM bills WHERE id = $1 FOR UPDATE`, billID).Scan(&status)
	if err != nil {
		return fmt.Errorf("query bill: %w", err)
	}
	if status != "open" {
		return fmt.Errorf("bill %s is already closed", billID)
	}

	tag, err := tx.Exec(ctx, `
		INSERT INTO line_items (id, bill_id, description, quantity, unit_price)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING
	`, signal.ID, billID, signal.Description, signal.Quantity, signal.UnitPrice)
	if err != nil {
		return fmt.Errorf("insert line item: %w", err)
	}
	if tag.RowsAffected() == 0 {
		err = tx.QueryRow(ctx, `
			SELECT bill_id, description, quantity, unit_price
			FROM line_items WHERE id = $1
		`, signal.ID).Scan(&existing.BillID, &existing.Description, &existing.Quantity, &existing.UnitPrice)
		if err != nil {
			return fmt.Errorf("query conflicted line item: %w", err)
		}
		if existing.BillID != billID || existing.Description != signal.Description || existing.Quantity != signal.Quantity || existing.UnitPrice != signal.UnitPrice {
			return fmt.Errorf("line item %s already exists with different payload", signal.ID)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	logger.Info("line item persisted", "billID", billID, "itemID", signal.ID)
	return nil
}

func CloseBillActivity(ctx context.Context, billID string) (*CloseBillResult, error) {
	logger := activity.GetLogger(ctx)

	tx, err := db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var status string
	var existingTotal *int64
	err = tx.QueryRow(ctx, `SELECT status, total_amount FROM bills WHERE id = $1 FOR UPDATE`, billID).Scan(&status, &existingTotal)
	if err != nil {
		return nil, fmt.Errorf("query bill: %w", err)
	}
	if status != "open" {
		if existingTotal == nil {
			return nil, fmt.Errorf("closed bill %s has no total", billID)
		}
		if err := tx.Commit(); err != nil {
			return nil, fmt.Errorf("commit tx: %w", err)
		}
		logger.Info("bill already closed", "billID", billID, "total", *existingTotal)
		return &CloseBillResult{Total: *existingTotal}, nil
	}

	var total int64
	err = tx.QueryRow(ctx, `
		SELECT COALESCE(SUM(quantity * unit_price), 0)
		FROM line_items WHERE bill_id = $1
	`, billID).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("sum line items: %w", err)
	}

	now := time.Now()
	tag, err := tx.Exec(ctx, `
		UPDATE bills
		SET status = 'closed', total_amount = $1, closed_at = $2, updated_at = $3
		WHERE id = $4 AND status = 'open'
	`, total, now, now, billID)
	if err != nil {
		return nil, fmt.Errorf("close bill: %w", err)
	}
	if tag.RowsAffected() == 0 {
		err = tx.QueryRow(ctx, `SELECT total_amount FROM bills WHERE id = $1`, billID).Scan(&existingTotal)
		if err != nil {
			return nil, fmt.Errorf("query closed total: %w", err)
		}
		if existingTotal == nil {
			return nil, fmt.Errorf("closed bill %s has no total", billID)
		}
		total = *existingTotal
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %w", err)
	}

	logger.Info("bill closed", "billID", billID, "total", total)
	return &CloseBillResult{Total: total}, nil
}
