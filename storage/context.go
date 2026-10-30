// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package storage

import "context"

type accessPropagationRetryKey struct{}

// WithAccessPropagationRetry marks a context to allow access-denied retries.
func WithAccessPropagationRetry(ctx context.Context) context.Context {
	return context.WithValue(ctx, accessPropagationRetryKey{}, true)
}

// AccessPropagationRetry reports whether access-denied retries are enabled.
func AccessPropagationRetry(ctx context.Context) bool {
	enabled, _ := ctx.Value(accessPropagationRetryKey{}).(bool)

	return enabled
}
