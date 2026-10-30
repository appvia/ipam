// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package s3

import (
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// WithoutRetryDelay makes every SDK retry of store immediate. The retryer
// itself (attempt limit, retryable errors, retry quota) stays the one the
// backend resolves, so tests exercise the real retry decisions without
// waiting for the backoff.
func WithoutRetryDelay(store *Store) *Store {
	store.clientOptions = append(store.clientOptions, func(o *s3.Options) {
		retryer, ok := o.Retryer.(aws.RetryerV2)
		if !ok {
			panic("s3 test: the resolved retryer is not an aws.RetryerV2")
		}

		o.Retryer = noRetryDelay{retryer}
	})

	return store
}

// WithSpareOptionCapacity gives store's per-call options spare capacity, so a
// call that appended to them in place would share memory with concurrent calls.
func WithSpareOptionCapacity(store *Store) *Store {
	store.clientOptions = slices.Grow(store.clientOptions, 8)

	return store
}

type noRetryDelay struct{ aws.RetryerV2 }

func (noRetryDelay) RetryDelay(int, error) (time.Duration, error) { return 0, nil }
