package bill

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"encore.dev/beta/errs"
)

const idempotencyPollTimeout = 2 * time.Second

func hashCanonicalPayload(payload any) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(body)
	return hex.EncodeToString(hash[:]), nil
}

func withIdempotency[T any](ctx context.Context, customerID, scope, key string, payload any, fn func() (*T, error)) (*T, error) {
	if key == "" {
		return fn()
	}

	requestHash, err := hashCanonicalPayload(payload)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(idempotencyPollTimeout)
	repo := NewRepository(db)
	for {
		created, err := repo.TryCreateIdempotencyRecord(ctx, customerID, scope, key, requestHash)
		if err != nil {
			return nil, err
		}
		if created {
			return completeIdempotentMutation(ctx, customerID, scope, key, fn)
		}

		res, done, err := replayIdempotentMutation[T](ctx, customerID, scope, key, requestHash)
		if err != nil {
			return nil, err
		}
		if done {
			return res, nil
		}
		if time.Now().After(deadline) {
			return nil, errs.WrapCode(errors.New("idempotency request still in progress"), errs.Aborted, "idempotency_in_progress")
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func completeIdempotentMutation[T any](ctx context.Context, customerID, scope, key string, fn func() (*T, error)) (*T, error) {
	res, err := fn()
	if err != nil {
		if deleteErr := NewRepository(db).DeleteIdempotencyRecord(ctx, customerID, scope, key); deleteErr != nil {
			return nil, deleteErr
		}
		return nil, err
	}

	body, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	if err := NewRepository(db).CompleteIdempotencyRecord(ctx, customerID, scope, key, body); err != nil {
		return nil, err
	}
	return res, nil
}

func replayIdempotentMutation[T any](ctx context.Context, customerID, scope, key, requestHash string) (*T, bool, error) {
	record, err := NewRepository(db).GetIdempotencyRecord(ctx, customerID, scope, key)
	if err != nil {
		if isNoRows(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if record.RequestHash != requestHash {
		return nil, false, errs.WrapCode(errors.New("same idempotency key used with different payload"), errs.Aborted, "idempotency_conflict")
	}
	if record.State != "completed" {
		return nil, false, nil
	}

	var res T
	if err := json.Unmarshal(record.Body, &res); err != nil {
		return nil, false, err
	}
	return &res, true, nil
}
