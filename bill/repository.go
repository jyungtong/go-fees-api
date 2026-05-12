package bill

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"encore.dev/storage/sqldb"
	"github.com/jackc/pgx/v5"
)

type Repository struct {
	db *sqldb.Database
}

type BillRecord struct {
	ID         string
	Status     string
	Currency   string
	CustomerID *string
	Total      *int64
	CreatedAt  time.Time
	ClosedAt   *time.Time
}

type BillSummaryRecord struct {
	ID        string
	Status    string
	Currency  string
	Total     *int64
	CreatedAt time.Time
	ClosedAt  *time.Time
}

type ClosedBillRecord struct {
	Currency string
	Total    *int64
	ClosedAt time.Time
}

type IdempotencyRecord struct {
	RequestHash string
	State       string
	Body        []byte
}

func NewRepository(db *sqldb.Database) *Repository {
	return &Repository{db: db}
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

func (r *Repository) InsertBill(ctx context.Context, params BillParams, workflowID string) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO bills (id, status, currency, customer_id, workflow_id, created_at)
		VALUES ($1, 'open', $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING
	`, params.BillID, params.Currency, params.CustomerID, workflowID, params.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert bill: %w", err)
	}
	return nil
}

func (r *Repository) BillExistsForCustomer(ctx context.Context, billID, customerID string) (bool, error) {
	var exists int
	err := r.db.QueryRow(ctx, `SELECT 1 FROM bills WHERE id = $1 AND customer_id = $2`, billID, customerID).Scan(&exists)
	if err != nil {
		if isNoRows(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (r *Repository) GetBillStatusForCustomer(ctx context.Context, billID, customerID string) (string, error) {
	var status string
	err := r.db.QueryRow(ctx, `SELECT status FROM bills WHERE id = $1 AND customer_id = $2`, billID, customerID).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

func (r *Repository) GetClosedBillForCustomer(ctx context.Context, billID, customerID string) (*ClosedBillRecord, error) {
	var bill ClosedBillRecord
	err := r.db.QueryRow(ctx, `
		SELECT currency, total_amount, closed_at
		FROM bills WHERE id = $1 AND customer_id = $2
	`, billID, customerID).Scan(&bill.Currency, &bill.Total, &bill.ClosedAt)
	if err != nil {
		return nil, err
	}
	return &bill, nil
}

func (r *Repository) GetBillForCustomer(ctx context.Context, billID, customerID string) (*BillRecord, error) {
	var bill BillRecord
	err := r.db.QueryRow(ctx, `
		SELECT status, currency, customer_id, total_amount, created_at, closed_at
		FROM bills WHERE id = $1 AND customer_id = $2
	`, billID, customerID).Scan(&bill.Status, &bill.Currency, &bill.CustomerID, &bill.Total, &bill.CreatedAt, &bill.ClosedAt)
	if err != nil {
		return nil, err
	}
	bill.ID = billID
	return &bill, nil
}

func (r *Repository) ListBillsForCustomer(ctx context.Context, customerID string) ([]BillSummaryRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, status, currency, total_amount, created_at, closed_at
		FROM bills
		WHERE customer_id = $1
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bills []BillSummaryRecord
	for rows.Next() {
		var bill BillSummaryRecord
		if err := rows.Scan(&bill.ID, &bill.Status, &bill.Currency, &bill.Total, &bill.CreatedAt, &bill.ClosedAt); err != nil {
			return nil, err
		}
		bills = append(bills, bill)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return bills, nil
}

func (r *Repository) ListLineItems(ctx context.Context, billID string) ([]LineItemRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, description, quantity, unit_price, created_at
		FROM line_items WHERE bill_id = $1
		ORDER BY created_at
	`, billID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lineItems []LineItemRecord
	for rows.Next() {
		var item LineItemRecord
		if err := rows.Scan(&item.ID, &item.Description, &item.Quantity, &item.UnitPrice, &item.CreatedAt); err != nil {
			return nil, err
		}
		lineItems = append(lineItems, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return lineItems, nil
}

func (r *Repository) AddLineItem(ctx context.Context, billID string, signal LineItemSignal) error {
	tx, err := r.db.Begin(ctx)
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
	return nil
}

func (r *Repository) CloseBill(ctx context.Context, billID string) (*CloseBillResult, error) {
	tx, err := r.db.Begin(ctx)
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
	return &CloseBillResult{Total: total}, nil
}

func (r *Repository) TryCreateIdempotencyRecord(ctx context.Context, customerID, scope, key, requestHash string) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO idempotency_records (customer_id, scope, key, request_hash, state)
		VALUES ($1, $2, $3, $4, 'in_progress')
		ON CONFLICT (customer_id, scope, key) DO NOTHING
	`, customerID, scope, key, requestHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repository) DeleteIdempotencyRecord(ctx context.Context, customerID, scope, key string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM idempotency_records WHERE customer_id = $1 AND scope = $2 AND key = $3`, customerID, scope, key)
	return err
}

func (r *Repository) CompleteIdempotencyRecord(ctx context.Context, customerID, scope, key string, body []byte) error {
	_, err := r.db.Exec(ctx, `
		UPDATE idempotency_records
		SET state = 'completed', response_status = 200, response_body = $1, updated_at = now(), completed_at = now()
		WHERE customer_id = $2 AND scope = $3 AND key = $4
	`, body, customerID, scope, key)
	return err
}

func (r *Repository) GetIdempotencyRecord(ctx context.Context, customerID, scope, key string) (*IdempotencyRecord, error) {
	var record IdempotencyRecord
	err := r.db.QueryRow(ctx, `
		SELECT request_hash, state, response_body
		FROM idempotency_records WHERE customer_id = $1 AND scope = $2 AND key = $3
	`, customerID, scope, key).Scan(&record.RequestHash, &record.State, &record.Body)
	if err != nil {
		return nil, err
	}
	return &record, nil
}
