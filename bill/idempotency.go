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

func withIdempotency[T any](ctx context.Context, scope, key string, payload any, fn func() (*T, error)) (*T, error) {
	if key == "" {
		return fn()
	}

	requestHash, err := hashCanonicalPayload(payload)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(idempotencyPollTimeout)
	for {
		tag, err := db.Exec(ctx, `
			INSERT INTO idempotency_records (scope, key, request_hash, state)
			VALUES ($1, $2, $3, 'in_progress')
			ON CONFLICT (scope, key) DO NOTHING
		`, scope, key, requestHash)
		if err != nil {
			return nil, err
		}
		if tag.RowsAffected() == 1 {
			return completeIdempotentMutation(ctx, scope, key, fn)
		}

		res, done, err := replayIdempotentMutation[T](ctx, scope, key, requestHash)
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

func completeIdempotentMutation[T any](ctx context.Context, scope, key string, fn func() (*T, error)) (*T, error) {
	res, err := fn()
	if err != nil {
		_, deleteErr := db.Exec(ctx, `DELETE FROM idempotency_records WHERE scope = $1 AND key = $2`, scope, key)
		if deleteErr != nil {
			return nil, deleteErr
		}
		return nil, err
	}

	body, err := json.Marshal(res)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(ctx, `
		UPDATE idempotency_records
		SET state = 'completed', response_status = 200, response_body = $1, updated_at = now(), completed_at = now()
		WHERE scope = $2 AND key = $3
	`, body, scope, key)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func replayIdempotentMutation[T any](ctx context.Context, scope, key, requestHash string) (*T, bool, error) {
	var existingHash string
	var state string
	var body []byte
	err := db.QueryRow(ctx, `
		SELECT request_hash, state, response_body
		FROM idempotency_records WHERE scope = $1 AND key = $2
	`, scope, key).Scan(&existingHash, &state, &body)
	if err != nil {
		if isNoRows(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if existingHash != requestHash {
		return nil, false, errs.WrapCode(errors.New("same idempotency key used with different payload"), errs.Aborted, "idempotency_conflict")
	}
	if state != "completed" {
		return nil, false, nil
	}

	var res T
	if err := json.Unmarshal(body, &res); err != nil {
		return nil, false, err
	}
	return &res, true, nil
}
