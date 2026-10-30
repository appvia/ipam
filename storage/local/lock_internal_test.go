// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package local

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The tests in this file set testHookAfterOpen, so they must not run in
// parallel.

func replaceLockFile(t *testing.T, path string) {
	t.Helper()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireRetriesWhenLockFileReplacedBeforeLocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("removing an open file needs FILE_SHARE_DELETE on Windows")
	}

	dir := t.TempDir()
	lockPath := filepath.Join(dir, lockName)

	calls := 0

	testHookAfterOpen = func() {
		calls++
		if calls == 1 {
			replaceLockFile(t, lockPath)
		}
	}

	t.Cleanup(func() { testHookAfterOpen = nil })

	store, err := New(dir)
	if err != nil {
		t.Fatalf("New() error = %v, want the replaced lock to be retried", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	if calls != 2 {
		t.Fatalf("lock opened %d times, want 2 (replaced file detected and retried)", calls)
	}
}

func TestAcquireGiveUpErrorDoesNotReportNil(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("removing an open file needs FILE_SHARE_DELETE on Windows")
	}

	dir := t.TempDir()
	lockPath := filepath.Join(dir, lockName)

	testHookAfterOpen = func() { replaceLockFile(t, lockPath) }

	t.Cleanup(func() { testHookAfterOpen = nil })

	_, err := New(dir)
	if err == nil {
		t.Fatal("New() error = nil, want give-up error when the lock is replaced on every attempt")
	}

	if strings.Contains(err.Error(), "<nil>") {
		t.Fatalf("New() error = %q, must not contain <nil>", err)
	}
}

func TestNewFailsWhenLockPathIsDirectory(t *testing.T) {
	dir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, lockName), 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := New(dir)
	if err == nil {
		t.Fatal("New() error = nil, want error when .lock is a directory")
	}

	if store != nil {
		t.Fatal("New() returned a store alongside an error")
	}
}
