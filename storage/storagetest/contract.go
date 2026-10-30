// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package storagetest provides a reusable contract suite for storage backends.
package storagetest

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/appvia/ipam/storage"
)

// Options adjusts RunWithOptions for a backend under test.
type Options struct {
	// SkipConditionalDelete, when non-empty, skips the subtests that need
	// DeleteIfMatch to reject a stale or missing version, and is reported as
	// the skip reason. Use it only for emulators known to ignore If-Match on
	// delete; the real backend must run them.
	SkipConditionalDelete string
}

// Run tests a Storage implementation against the backend contract.
//
// Subtests run in parallel and reuse fixed keys such as "object", so newStore
// must return an isolated store (its own prefix or container) on every call.
func Run(t *testing.T, newStore func(t *testing.T) storage.Storage) {
	t.Helper()

	RunWithOptions(t, newStore, Options{})
}

// RunWithOptions is Run with Options. With the zero Options it is identical
// to Run.
func RunWithOptions(t *testing.T, newStore func(t *testing.T) storage.Storage, opts Options) {
	t.Helper()

	skipConditionalDelete := func(t *testing.T) {
		t.Helper()

		if opts.SkipConditionalDelete != "" {
			t.Skip("SKIPPED conditional-delete contract case: " + opts.SkipConditionalDelete)
		}
	}

	t.Run("get on a missing key returns not found", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)

		_, _, err := store.Get(t.Context(), "missing")

		assertErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("create then get round trips data and version", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)
		input := []byte("first")

		createdVersion, err := store.CreateIfAbsent(t.Context(), "object", input)
		assertNoError(t, err)

		input[0] = 'X'
		data, readVersion, err := store.Get(t.Context(), "object")

		assertNoError(t, err)
		assertBytesEqual(t, data, []byte("first"))

		if createdVersion != readVersion {
			t.Fatalf("Get() version = %q, want %q", readVersion, createdVersion)
		}

		data[0] = 'X'
		unchanged, _, err := store.Get(t.Context(), "object")
		assertNoError(t, err)
		assertBytesEqual(t, unchanged, []byte("first"))
	})

	t.Run("second create returns precondition failed", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)
		_, err := store.CreateIfAbsent(t.Context(), "object", []byte("first"))
		assertNoError(t, err)

		_, err = store.CreateIfAbsent(t.Context(), "object", []byte("second"))

		assertErrorIs(t, err, storage.ErrPreconditionFailed)
	})

	t.Run("put with a stale version preserves data", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)
		version, err := store.CreateIfAbsent(t.Context(), "object", []byte("first"))
		assertNoError(t, err)
		currentVersion, err := store.PutIfMatch(t.Context(), "object", []byte("second"), version)
		assertNoError(t, err)

		_, err = store.PutIfMatch(t.Context(), "object", []byte("stale"), version)

		assertErrorIs(t, err, storage.ErrPreconditionFailed)
		data, gotVersion, err := store.Get(t.Context(), "object")
		assertNoError(t, err)
		assertBytesEqual(t, data, []byte("second"))

		if gotVersion != currentVersion {
			t.Fatalf("Get() version = %q, want %q", gotVersion, currentVersion)
		}
	})

	t.Run("put with the current version returns a new version", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)
		version, err := store.CreateIfAbsent(t.Context(), "object", []byte("first"))
		assertNoError(t, err)

		nextVersion, err := store.PutIfMatch(t.Context(), "object", []byte("second"), version)

		assertNoError(t, err)

		if nextVersion == version {
			t.Fatalf("PutIfMatch() version = %q, want a new version", nextVersion)
		}

		data, readVersion, err := store.Get(t.Context(), "object")
		assertNoError(t, err)
		assertBytesEqual(t, data, []byte("second"))

		if readVersion != nextVersion {
			t.Fatalf("Get() version = %q, want %q", readVersion, nextVersion)
		}
	})

	t.Run("delete with a stale version preserves the object", func(t *testing.T) {
		t.Parallel()
		skipConditionalDelete(t)

		store := newStore(t)
		version, err := store.CreateIfAbsent(t.Context(), "object", []byte("first"))
		assertNoError(t, err)
		_, err = store.PutIfMatch(t.Context(), "object", []byte("second"), version)
		assertNoError(t, err)

		err = store.DeleteIfMatch(t.Context(), "object", version)

		assertErrorIs(t, err, storage.ErrPreconditionFailed)
		data, _, err := store.Get(t.Context(), "object")
		assertNoError(t, err)
		assertBytesEqual(t, data, []byte("second"))
	})

	t.Run("delete on a missing key does not affect other objects", func(t *testing.T) {
		t.Parallel()
		skipConditionalDelete(t)

		store := newStore(t)
		version, err := store.CreateIfAbsent(t.Context(), "existing", []byte("data"))
		assertNoError(t, err)

		err = store.DeleteIfMatch(t.Context(), "missing", version)

		assertOneOfErrors(t, err, storage.ErrNotFound, storage.ErrPreconditionFailed)
		data, _, err := store.Get(t.Context(), "existing")
		assertNoError(t, err)
		assertBytesEqual(t, data, []byte("data"))
	})

	t.Run("put on a missing key never creates it", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)

		_, err := store.PutIfMatch(t.Context(), "missing", []byte("data"), storage.Version("1"))

		assertOneOfErrors(t, err, storage.ErrNotFound, storage.ErrPreconditionFailed)
		_, _, err = store.Get(t.Context(), "missing")
		assertErrorIs(t, err, storage.ErrNotFound)
	})

	t.Run("delete with the current version removes the object", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)
		version, err := store.CreateIfAbsent(t.Context(), "object", []byte("first"))
		assertNoError(t, err)

		err = store.DeleteIfMatch(t.Context(), "object", version)

		assertNoError(t, err)
		_, _, err = store.Get(t.Context(), "object")
		assertErrorIs(t, err, storage.ErrNotFound)

		recreated, err := store.CreateIfAbsent(t.Context(), "object", []byte("second"))
		assertNoError(t, err)

		if recreated == version {
			t.Fatalf("CreateIfAbsent() version = %q, want a version different from the deleted object", recreated)
		}
	})

	t.Run("list returns matching keys in sorted order", func(t *testing.T) {
		t.Parallel()

		store := newStore(t)
		for _, key := range []string{"pools/z", "other/a", "pools/a", "pools/m"} {
			_, err := store.CreateIfAbsent(t.Context(), key, []byte(key))
			assertNoError(t, err)
		}

		keys, err := store.List(t.Context(), "pools/")

		assertNoError(t, err)

		want := []string{"pools/a", "pools/m", "pools/z"}
		if !slices.Equal(keys, want) {
			t.Fatalf("List() = %v, want %v", keys, want)
		}
	})

	t.Run("concurrent puts with one version allow exactly one writer", func(t *testing.T) {
		t.Parallel()

		const writerCount = 8

		store := newStore(t)
		version, err := store.CreateIfAbsent(t.Context(), "object", []byte("first"))
		assertNoError(t, err)

		start := make(chan struct{})
		results := make(chan error, writerCount)

		var ready sync.WaitGroup
		ready.Add(writerCount)

		for range writerCount {
			go func() {
				ready.Done()
				<-start

				_, putErr := store.PutIfMatch(
					context.Background(),
					"object",
					[]byte("writer"),
					version,
				)
				results <- putErr
			}()
		}

		ready.Wait()
		close(start)

		successes := 0
		preconditionFailures := 0

		for range writerCount {
			result := <-results
			switch {
			case result == nil:
				successes++
			case errors.Is(result, storage.ErrPreconditionFailed):
				preconditionFailures++
			default:
				t.Fatalf("PutIfMatch() error = %v, want nil or %v", result, storage.ErrPreconditionFailed)
			}
		}

		if successes != 1 {
			t.Fatalf("successful PutIfMatch() calls = %d, want 1", successes)
		}

		if preconditionFailures != writerCount-1 {
			t.Fatalf("precondition failures = %d, want %d", preconditionFailures, writerCount-1)
		}
	})
}

func assertNoError(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()

	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(_, %v)", err, target)
	}
}

func assertOneOfErrors(t *testing.T, err error, targets ...error) {
	t.Helper()

	for _, target := range targets {
		if errors.Is(err, target) {
			return
		}
	}

	t.Fatalf("error = %v, want one of %v", err, targets)
}

func assertBytesEqual(t *testing.T, got, want []byte) {
	t.Helper()

	if !bytes.Equal(got, want) {
		t.Fatalf("data = %q, want %q", got, want)
	}
}
