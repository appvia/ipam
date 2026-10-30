// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package azblob

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/google/uuid"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/storagetest"
)

func newAzuriteContainer(t *testing.T) string {
	t.Helper()

	conn := os.Getenv(connectionStringEnv)
	if conn == "" {
		t.Skipf("%s is not set; start Azurite with scripts/emulators/start-azurite.sh", connectionStringEnv)
	}

	name := "ipam-it-" + uuid.NewString()

	client, err := container.NewClientFromConnectionString(conn, name, nil)
	if err != nil {
		t.Fatalf("container client: %v", err)
	}

	if _, err := client.Create(t.Context(), nil); err != nil {
		t.Fatalf("create container %s: %v", name, err)
	}

	t.Cleanup(func() {
		if _, err := client.Delete(context.Background(), nil); err != nil {
			t.Errorf("delete container %s: %v", name, err)
		}
	})

	return name
}

func newAzuriteStore(t *testing.T, containerName string) storage.Storage {
	t.Helper()

	store, err := New(t.Context(), Config{Container: containerName, Prefix: "t-" + uuid.NewString()})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return store
}

func TestAzuriteContract(t *testing.T) {
	containerName := newAzuriteContainer(t)

	storagetest.Run(t, func(t *testing.T) storage.Storage {
		return newAzuriteStore(t, containerName)
	})
}

func TestAzuriteConditionalFailures(t *testing.T) {
	store := newAzuriteStore(t, newAzuriteContainer(t))
	ctx := t.Context()

	first, err := store.CreateIfAbsent(ctx, "object", []byte("v1"))
	if err != nil {
		t.Fatalf("CreateIfAbsent() error = %v", err)
	}

	_, err = store.CreateIfAbsent(ctx, "object", []byte("again"))
	t.Logf("create over an existing blob: %v", err)

	if !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("second CreateIfAbsent() error = %v, want %v", err, storage.ErrPreconditionFailed)
	}

	if _, err := store.PutIfMatch(ctx, "object", []byte("v2"), first); err != nil {
		t.Fatalf("PutIfMatch() error = %v", err)
	}

	_, err = store.PutIfMatch(ctx, "object", []byte("stale"), first)
	t.Logf("stale ETag put: %v", err)

	if !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("stale PutIfMatch() error = %v, want %v", err, storage.ErrPreconditionFailed)
	}

	err = store.DeleteIfMatch(ctx, "object", first)
	t.Logf("stale ETag delete: %v", err)

	if !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("stale DeleteIfMatch() error = %v, want %v", err, storage.ErrPreconditionFailed)
	}
}
