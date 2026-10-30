// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package memory_test

import (
	"testing"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
	"github.com/appvia/ipam/storage/storagetest"
)

func TestMemoryContract(t *testing.T) {
	storagetest.Run(t, func(*testing.T) storage.Storage {
		return memory.New()
	})
}
