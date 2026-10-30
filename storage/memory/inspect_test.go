// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"testing"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

func TestInspectReportsNoDataProtection(t *testing.T) {
	var inspector storage.Inspector = memory.New()

	info, err := inspector.Inspect(t.Context())
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}

	want := storage.Info{Versioning: storage.Unknown, SoftDelete: storage.Unknown, HierarchicalNamespace: storage.No}
	if info != want {
		t.Fatalf("Inspect() = %+v, want %+v", info, want)
	}
}
