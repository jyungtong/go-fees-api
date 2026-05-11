package bill

import (
	"context"
	"errors"
	"strings"
	"time"

	"encore.dev/beta/errs"
	"github.com/google/uuid"
	"go.temporal.io/sdk/client"
)

type CreateBillRequest struct {
	Currency       string `json:"currency"`
	CustomerID     string `header:"customer-id"`
	IdempotencyKey string `header:"Idempotency-Key"`
}

type CreateBillResponse struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	Currency   string    `json:"currency"`
	WorkflowID string    `json:"workflow_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type AddLineItemRequest struct {
	Description    string `json:"description"`
	Quantity       int    `json:"quantity"`
	UnitPrice      string `json:"unit_price"`
	CustomerID     string `header:"customer-id"`
	IdempotencyKey string `header:"Idempotency-Key"`
}

type TenantRequest struct {
	CustomerID string `header:"customer-id"`
}

type AddLineItemResponse struct {
	ID          string    `json:"id"`
	BillID      string    `json:"bill_id"`
	Description string    `json:"description"`
	Quantity    int       `json:"quantity"`
	UnitPrice   string    `json:"unit_price"`
	Amount      string    `json:"amount"`
	CreatedAt   time.Time `json:"created_at"`
}

type LineItemResponse struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Quantity    int       `json:"quantity"`
	UnitPrice   string    `json:"unit_price"`
	Amount      string    `json:"amount"`
	CreatedAt   time.Time `json:"created_at"`
}

type CloseBillResponse struct {
	ID        string             `json:"id"`
	Status    string             `json:"status"`
	Currency  string             `json:"currency"`
	Total     string             `json:"total"`
	LineItems []LineItemResponse `json:"line_items"`
	ClosedAt  time.Time          `json:"closed_at"`
}

type BillResponse struct {
	ID         string             `json:"id"`
	Status     string             `json:"status"`
	Currency   string             `json:"currency"`
	CustomerID string             `json:"customer_id,omitempty"`
	Total      *string            `json:"total,omitempty"`
	LineItems  []LineItemResponse `json:"line_items"`
	CreatedAt  time.Time          `json:"created_at"`
	ClosedAt   *time.Time         `json:"closed_at,omitempty"`
}

type ListBillsResponse struct {
	Bills []BillSummary `json:"bills"`
}

type BillSummary struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Currency  string     `json:"currency"`
	Total     *string    `json:"total,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
}

func requireCustomerID(customerID string) (string, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return "", errs.WrapCode(errors.New("missing customer-id"), errs.Unauthenticated, "missing_customer_id")
	}
	return customerID, nil
}

//encore:api public method=POST path=/bills
func (s *Service) CreateBill(ctx context.Context, req *CreateBillRequest) (*CreateBillResponse, error) {
	customerID, err := requireCustomerID(req.CustomerID)
	if err != nil {
		return nil, err
	}
	if req.Currency != "USD" && req.Currency != "GEL" {
		return nil, errs.WrapCode(errors.New("currency must be USD or GEL"), errs.InvalidArgument, "invalid_currency")
	}

	payload := struct {
		Currency string `json:"currency"`
	}{Currency: req.Currency}

	return withIdempotency(ctx, customerID, "create_bill", req.IdempotencyKey, payload, func() (*CreateBillResponse, error) {
		billID := uuid.NewString()
		createdAt := time.Now()

		params := BillParams{
			BillID:     billID,
			Currency:   req.Currency,
			CustomerID: customerID,
			CreatedAt:  createdAt,
		}

		workflowOptions := client.StartWorkflowOptions{
			ID:        billID,
			TaskQueue: taskQueue,
		}

		_, err := s.temporalClient.ExecuteWorkflow(ctx, workflowOptions, BillWorkflow, params)
		if err != nil {
			return nil, err
		}

		return &CreateBillResponse{
			ID:         billID,
			Status:     "open",
			Currency:   req.Currency,
			WorkflowID: billID,
			CreatedAt:  createdAt,
		}, nil
	})
}

//encore:api public method=POST path=/bills/:id/line-items
func (s *Service) AddLineItem(ctx context.Context, id string, req *AddLineItemRequest) (*AddLineItemResponse, error) {
	customerID, err := requireCustomerID(req.CustomerID)
	if err != nil {
		return nil, err
	}
	if req.Quantity <= 0 {
		return nil, errs.WrapCode(errors.New("quantity must be greater than 0"), errs.InvalidArgument, "invalid_quantity")
	}
	unitPriceMinor, err := parseMoneyAmount(req.UnitPrice)
	if err != nil {
		return nil, errs.WrapCode(err, errs.InvalidArgument, "invalid_unit_price")
	}

	payload := struct {
		Description string `json:"description"`
		Quantity    int    `json:"quantity"`
		UnitPrice   int64  `json:"unit_price_minor"`
	}{Description: req.Description, Quantity: req.Quantity, UnitPrice: unitPriceMinor}

	var exists int
	err = db.QueryRow(ctx, `SELECT 1 FROM bills WHERE id = $1 AND customer_id = $2`, id, customerID).Scan(&exists)
	if err != nil {
		return nil, errs.WrapCode(errors.New("bill not found"), errs.NotFound, "bill_not_found")
	}

	return withIdempotency(ctx, customerID, "add_line_item:"+id, req.IdempotencyKey, payload, func() (*AddLineItemResponse, error) {
		var status string
		err = db.QueryRow(ctx, `SELECT status FROM bills WHERE id = $1 AND customer_id = $2`, id, customerID).Scan(&status)
		if err != nil {
			return nil, errs.WrapCode(errors.New("bill not found"), errs.NotFound, "bill_not_found")
		}
		if status != "open" {
			return nil, errs.WrapCode(errors.New("bill is already closed"), errs.Aborted, "bill_closed")
		}

		itemID := uuid.NewString()
		signal := LineItemSignal{
			ID:          itemID,
			Description: req.Description,
			Quantity:    req.Quantity,
			UnitPrice:   unitPriceMinor,
		}

		err = s.temporalClient.SignalWorkflow(ctx, id, "", "AddLineItem", signal)
		if err != nil {
			return nil, err
		}

		return &AddLineItemResponse{
			ID:          itemID,
			BillID:      id,
			Description: req.Description,
			Quantity:    req.Quantity,
			UnitPrice:   formatMoneyAmount(unitPriceMinor),
			Amount:      formatMoneyAmount(int64(req.Quantity) * unitPriceMinor),
			CreatedAt:   time.Now(),
		}, nil
	})
}

//encore:api public method=POST path=/bills/:id/close
func (s *Service) CloseBill(ctx context.Context, id string, req *TenantRequest) (*CloseBillResponse, error) {
	customerID, err := requireCustomerID(req.CustomerID)
	if err != nil {
		return nil, err
	}
	var status string
	err = db.QueryRow(ctx, `SELECT status FROM bills WHERE id = $1 AND customer_id = $2`, id, customerID).Scan(&status)
	if err != nil {
		return nil, errs.WrapCode(errors.New("bill not found"), errs.NotFound, "bill_not_found")
	}
	if status != "open" {
		return nil, errs.WrapCode(errors.New("bill is already closed"), errs.Aborted, "bill_closed")
	}

	err = s.temporalClient.SignalWorkflow(ctx, id, "", "CloseBill", nil)
	if err != nil {
		return nil, err
	}

	run := s.temporalClient.GetWorkflow(ctx, id, "")
	var result CloseBillResult
	err = run.Get(ctx, &result)
	if err != nil {
		return nil, err
	}

	var bill struct {
		Currency string
		Total    *int64
		ClosedAt time.Time
	}
	err = db.QueryRow(ctx, `
		SELECT currency, total_amount, closed_at
		FROM bills WHERE id = $1 AND customer_id = $2
	`, id, customerID).Scan(&bill.Currency, &bill.Total, &bill.ClosedAt)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(ctx, `
		SELECT id, description, quantity, unit_price, created_at
		FROM line_items WHERE bill_id = $1
		ORDER BY created_at
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lineItems []LineItemResponse
	for rows.Next() {
		var item LineItemResponse
		var unitPriceMinor int64
		err := rows.Scan(&item.ID, &item.Description, &item.Quantity, &unitPriceMinor, &item.CreatedAt)
		if err != nil {
			return nil, err
		}
		item.UnitPrice = formatMoneyAmount(unitPriceMinor)
		item.Amount = formatMoneyAmount(int64(item.Quantity) * unitPriceMinor)
		lineItems = append(lineItems, item)
	}

	return &CloseBillResponse{
		ID:        id,
		Status:    "closed",
		Currency:  bill.Currency,
		Total:     formatMoneyAmount(*bill.Total),
		LineItems: lineItems,
		ClosedAt:  bill.ClosedAt,
	}, nil
}

//encore:api public method=GET path=/bills/:id
func (s *Service) GetBill(ctx context.Context, id string, req *TenantRequest) (*BillResponse, error) {
	customerID, err := requireCustomerID(req.CustomerID)
	if err != nil {
		return nil, err
	}
	var bill struct {
		Status     string
		Currency   string
		CustomerID *string
		Total      *int64
		CreatedAt  time.Time
		ClosedAt   *time.Time
	}
	err = db.QueryRow(ctx, `
		SELECT status, currency, customer_id, total_amount, created_at, closed_at
		FROM bills WHERE id = $1 AND customer_id = $2
	`, id, customerID).Scan(&bill.Status, &bill.Currency, &bill.CustomerID, &bill.Total, &bill.CreatedAt, &bill.ClosedAt)
	if err != nil {
		return nil, errs.WrapCode(errors.New("bill not found"), errs.NotFound, "bill_not_found")
	}

	rows, err := db.Query(ctx, `
		SELECT id, description, quantity, unit_price, created_at
		FROM line_items WHERE bill_id = $1
		ORDER BY created_at
	`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var lineItems []LineItemResponse
	for rows.Next() {
		var item LineItemResponse
		var unitPriceMinor int64
		err := rows.Scan(&item.ID, &item.Description, &item.Quantity, &unitPriceMinor, &item.CreatedAt)
		if err != nil {
			return nil, err
		}
		item.UnitPrice = formatMoneyAmount(unitPriceMinor)
		item.Amount = formatMoneyAmount(int64(item.Quantity) * unitPriceMinor)
		lineItems = append(lineItems, item)
	}

	return &BillResponse{
		ID:       id,
		Status:   bill.Status,
		Currency: bill.Currency,
		CustomerID: func() string {
			if bill.CustomerID != nil {
				return *bill.CustomerID
			}
			return ""
		}(),
		Total:     formatOptionalMoneyAmount(bill.Total),
		LineItems: lineItems,
		CreatedAt: bill.CreatedAt,
		ClosedAt:  bill.ClosedAt,
	}, nil
}

//encore:api public method=GET path=/bills
func (s *Service) ListBills(ctx context.Context, req *TenantRequest) (*ListBillsResponse, error) {
	customerID, err := requireCustomerID(req.CustomerID)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `
		SELECT id, status, currency, total_amount, created_at, closed_at
		FROM bills
		WHERE customer_id = $1
		ORDER BY created_at DESC
	`, customerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bills []BillSummary
	for rows.Next() {
		var bill struct {
			ID        string
			Status    string
			Currency  string
			Total     *int64
			CreatedAt time.Time
			ClosedAt  *time.Time
		}
		err := rows.Scan(&bill.ID, &bill.Status, &bill.Currency, &bill.Total, &bill.CreatedAt, &bill.ClosedAt)
		if err != nil {
			return nil, err
		}
		bills = append(bills, BillSummary{
			ID:        bill.ID,
			Status:    bill.Status,
			Currency:  bill.Currency,
			Total:     formatOptionalMoneyAmount(bill.Total),
			CreatedAt: bill.CreatedAt,
			ClosedAt:  bill.ClosedAt,
		})
	}

	return &ListBillsResponse{Bills: bills}, nil
}
