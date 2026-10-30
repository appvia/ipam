// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package storage_test

import (
	"context"
	"testing"

	"github.com/appvia/ipam/storage"
)

func TestAccessPropagationRetry_returns_false_when_not_enabled(t *testing.T) {
	if storage.AccessPropagationRetry(t.Context()) {
		t.Fatal("AccessPropagationRetry() = true, want false")
	}
}

func TestAccessPropagationRetry_returns_true_when_enabled(t *testing.T) {
	ctx := storage.WithAccessPropagationRetry(context.WithValue(t.Context(), testContextKey{}, "value"))

	if !storage.AccessPropagationRetry(ctx) {
		t.Fatal("AccessPropagationRetry() = false, want true")
	}

	if got := ctx.Value(testContextKey{}); got != "value" {
		t.Fatalf("preserved context value = %v, want value", got)
	}
}

type testContextKey struct{}
