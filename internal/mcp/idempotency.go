package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"skarb/internal/domain"
	"skarb/internal/service"
)

func idempotentOut[T any](ctx context.Context, svc *service.Service, actor domain.Actor, scope, key string, request any, fn func() (T, error)) (T, error) {
	var zero T
	if key == "" {
		return fn()
	}
	raw, err := json.Marshal(request)
	if err != nil {
		return zero, err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	rec, err := svc.Store.GetIdempotency(ctx, scope, key)
	if err == nil {
		if rec.RequestHash != hash {
			return zero, fmt.Errorf("%w: idempotency_conflict", domain.ErrConflict)
		}
		if err := json.Unmarshal([]byte(rec.ResponseJSON), &zero); err != nil {
			return zero, err
		}
		return zero, nil
	}
	if !errors.Is(err, domain.ErrNotFound) {
		return zero, err
	}
	out, err := fn()
	if err != nil {
		return zero, err
	}
	response, err := json.Marshal(out)
	if err != nil {
		return zero, err
	}
	if err := svc.Store.PutIdempotency(ctx, nil, scope, key, actor.UserID, hash, string(response)); err != nil {
		return zero, err
	}
	return out, nil
}
