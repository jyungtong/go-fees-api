UPDATE bills
SET customer_id = 'legacy'
WHERE customer_id IS NULL;

ALTER TABLE bills ALTER COLUMN customer_id SET NOT NULL;

CREATE INDEX idx_bills_customer_created_at ON bills (customer_id, created_at DESC);
