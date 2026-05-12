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

func lineItemResponses(records []LineItemRecord) []LineItemResponse {
	lineItems := make([]LineItemResponse, 0, len(records))
	for _, record := range records {
		lineItems = append(lineItems, LineItemResponse{
			ID:          record.ID,
			Description: record.Description,
			Quantity:    record.Quantity,
			UnitPrice:   formatMoneyAmount(record.UnitPrice),
			Amount:      formatMoneyAmount(int64(record.Quantity) * record.UnitPrice),
			CreatedAt:   record.CreatedAt,
		})
	}
	return lineItems
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

	repo := NewRepository(db)
	exists, err := repo.BillExistsForCustomer(ctx, id, customerID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errs.WrapCode(errors.New("bill not found"), errs.NotFound, "bill_not_found")
	}

	return withIdempotency(ctx, customerID, "add_line_item:"+id, req.IdempotencyKey, payload, func() (*AddLineItemResponse, error) {
		status, err := repo.GetBillStatusForCustomer(ctx, id, customerID)
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
	repo := NewRepository(db)
	status, err := repo.GetBillStatusForCustomer(ctx, id, customerID)
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

	bill, err := repo.GetClosedBillForCustomer(ctx, id, customerID)
	if err != nil {
		return nil, err
	}

	items, err := repo.ListLineItems(ctx, id)
	if err != nil {
		return nil, err
	}
	lineItems := lineItemResponses(items)

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
	repo := NewRepository(db)
	bill, err := repo.GetBillForCustomer(ctx, id, customerID)
	if err != nil {
		return nil, errs.WrapCode(errors.New("bill not found"), errs.NotFound, "bill_not_found")
	}

	items, err := repo.ListLineItems(ctx, id)
	if err != nil {
		return nil, err
	}
	lineItems := lineItemResponses(items)

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
	records, err := NewRepository(db).ListBillsForCustomer(ctx, customerID)
	if err != nil {
		return nil, err
	}

	var bills []BillSummary
	for _, bill := range records {
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
