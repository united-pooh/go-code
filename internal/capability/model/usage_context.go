package model

import (
	"context"
	"crypto/rand"
)

type usageRequestKey struct{}

func WithUsageRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, usageRequestKey{}, requestID)
}

func usageRequestID(ctx context.Context) string {
	if id, _ := ctx.Value(usageRequestKey{}).(string); id != "" {
		return id
	}
	return rand.Text()
}
