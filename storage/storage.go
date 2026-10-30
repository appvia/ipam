// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package storage

import "context"

// Version is an opaque object version that backends round-trip unchanged.
type Version string

// Storage stores objects using conditional writes and deletes.
type Storage interface {
	Get(ctx context.Context, key string) ([]byte, Version, error)
	CreateIfAbsent(ctx context.Context, key string, data []byte) (Version, error)
	PutIfMatch(ctx context.Context, key string, data []byte, version Version) (Version, error)
	DeleteIfMatch(ctx context.Context, key string, version Version) error
	List(ctx context.Context, prefix string) ([]string, error) // keys, sorted
}
