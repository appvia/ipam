// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package local_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/local"
	"github.com/appvia/ipam/storage/storagetest"
)

const (
	childDirEnv    = "IPAM_LOCAL_CROSS_PROCESS_DIR"
	crossProcesses = 4
	crossIncrement = 25
	counterKey     = "counter/value"

	freshDirEnv   = "IPAM_LOCAL_FRESH_DIR"
	freshChildren = 12
	freshRounds   = 30
	raceWorkers   = 16
	raceRounds    = 300
	wonMarker     = "WON-THE-CREATE"
)

func newStore(t *testing.T) *local.Store {
	t.Helper()

	store, err := local.New(t.TempDir())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	return store
}

func TestLocalContract(t *testing.T) {
	storagetest.Run(t, func(t *testing.T) storage.Storage {
		return newStore(t)
	})
}

func TestInvalidKeys(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	absolute := filepath.Join(t.TempDir(), "x")
	keys := []string{"../x", "a/../../x", "a/..", "..", "/etc/passwd", absolute, "", "a//b", "a/", ".lock", "a/.ipam-tmp-1", "a\x00b", ".", "a/./b"}

	for _, key := range keys {
		ctx := context.Background()

		_, _, err := store.Get(ctx, key)
		assertInvalidKey(t, "Get", key, err)

		_, err = store.CreateIfAbsent(ctx, key, []byte("x"))
		assertInvalidKey(t, "CreateIfAbsent", key, err)

		_, err = store.PutIfMatch(ctx, key, []byte("x"), "v")
		assertInvalidKey(t, "PutIfMatch", key, err)

		err = store.DeleteIfMatch(ctx, key, "v")
		assertInvalidKey(t, "DeleteIfMatch", key, err)
	}
}

func assertInvalidKey(t *testing.T, op, key string, err error) {
	t.Helper()

	if !errors.Is(err, local.ErrInvalidKey) {
		t.Errorf("%s(%q) error = %v, want ErrInvalidKey", op, key, err)
	}
}

func TestStoredFileIsPlainContentAtPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	content := []byte(`{"hello":"world"}`)

	version, err := store.CreateIfAbsent(t.Context(), "pools/prod.json", content)
	if err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "pools", "prod.json"))
	if err != nil {
		t.Fatal(err)
	}

	if string(got) != string(content) {
		t.Fatalf("file content = %q, want %q", got, content)
	}

	sum := sha256.Sum256(content)
	if want := hex.EncodeToString(sum[:]); string(version) != want {
		t.Fatalf("version = %q, want %q", version, want)
	}
}

func TestStaleStateIsIgnored(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, ".lock"), "stale holder from a crashed run")
	mustWrite(t, filepath.Join(dir, ".ipam-tmp-deadbeef"), "partial")
	mustWrite(t, filepath.Join(dir, "pools", ".ipam-tmp-cafe"), "partial")
	mustWrite(t, filepath.Join(dir, "pools", "a.json"), "a")

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	version, err := store.CreateIfAbsent(t.Context(), "pools/b.json", []byte("b"))
	if err != nil {
		t.Fatalf("CreateIfAbsent() with stale state error = %v", err)
	}

	if _, err := store.PutIfMatch(t.Context(), "pools/b.json", []byte("b2"), version); err != nil {
		t.Fatalf("PutIfMatch() with stale state error = %v", err)
	}

	keys, err := store.List(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"pools/a.json", "pools/b.json"}
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] {
		t.Fatalf("List() = %v, want %v", keys, want)
	}

	assertNoNewTemp(t, dir)
}

// assertNoNewTemp checks that operations leave no temp files of their own
// behind: only the two seeded leftovers may exist.
func assertNoNewTemp(t *testing.T, dir string) {
	t.Helper()

	var temps []string

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && len(d.Name()) >= 10 && d.Name()[:10] == ".ipam-tmp-" {
			temps = append(temps, d.Name())
		}

		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(temps) != 2 {
		t.Fatalf("temp files = %v, want only the 2 seeded leftovers", temps)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSymlinkEscapeIsBlocked(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}

	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret"), "secret")

	dir := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(dir, "filelink")); err != nil {
		t.Fatal(err)
	}

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()

	if data, _, err := store.Get(ctx, "link/secret"); err == nil {
		t.Fatalf("Get() through symlink returned %q, want error", data)
	}

	if data, _, err := store.Get(ctx, "filelink"); err == nil {
		t.Fatalf("Get() through file symlink returned %q, want error", data)
	}

	if _, err := store.CreateIfAbsent(ctx, "link/new", []byte("x")); err == nil {
		t.Fatal("CreateIfAbsent() through symlink succeeded, want error")
	}

	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Fatal("file was written outside the store directory")
	}

	if err := store.DeleteIfMatch(ctx, "link/secret", "v"); err == nil {
		t.Fatal("DeleteIfMatch() through symlink succeeded, want error")
	}

	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatalf("outside file affected: %v", err)
	}

	keys, err := store.List(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(keys) != 0 {
		t.Fatalf("List() = %v, want symlinks ignored", keys)
	}
}

func TestPermissionDeniedMapsToAccessDenied(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits not enforced here")
	}

	dir := t.TempDir()

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.CreateIfAbsent(t.Context(), "locked/obj", []byte("x")); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Join(dir, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o755) })

	_, _, err = store.Get(t.Context(), "locked/obj")
	if !errors.Is(err, storage.ErrAccessDenied) {
		t.Fatalf("Get() error = %v, want ErrAccessDenied", err)
	}
}

func TestCrossProcess(t *testing.T) {
	if dir := os.Getenv(childDirEnv); dir != "" {
		runChild(t, dir)

		return
	}

	dir := t.TempDir()

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.CreateIfAbsent(t.Context(), counterKey, []byte("0")); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	var wg sync.WaitGroup

	outputs := make([][]byte, crossProcesses)
	errs := make([]error, crossProcesses)

	for i := range crossProcesses {
		wg.Add(1)

		go func() {
			defer wg.Done()

			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrossProcess$", "-test.count=1")

			cmd.Env = append(os.Environ(), childDirEnv+"="+dir)
			outputs[i], errs[i] = cmd.CombinedOutput()
		}()
	}

	wg.Wait()

	for i := range crossProcesses {
		if errs[i] != nil {
			t.Fatalf("child %d failed: %v\n%s", i, errs[i], outputs[i])
		}
	}

	data, _, err := store.Get(t.Context(), counterKey)
	if err != nil {
		t.Fatal(err)
	}

	if want := strconv.Itoa(crossProcesses * crossIncrement); string(data) != want {
		t.Fatalf("counter = %s, want %s", data, want)
	}
}

func runChild(t *testing.T, dir string) {
	t.Helper()

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()
	deadline := time.Now().Add(time.Minute)

	for range crossIncrement {
		for {
			if time.Now().After(deadline) {
				t.Fatal("child timed out")
			}

			data, version, err := store.Get(ctx, counterKey)
			if err != nil {
				t.Fatal(err)
			}

			n, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}

			_, err = store.PutIfMatch(ctx, counterKey, []byte(strconv.Itoa(n+1)), version)
			if err == nil {
				break
			}

			if !errors.Is(err, storage.ErrPreconditionFailed) {
				t.Fatal(err)
			}
		}
	}
}

func TestFirstOperationsOnFreshDirectoryDoNotFail(t *testing.T) {
	t.Parallel()

	for round := range raceRounds {
		dir := t.TempDir()

		store, err := local.New(dir)
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = store.Close() })

		if err := os.Remove(filepath.Join(dir, ".lock")); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}

		errs := make([]error, raceWorkers)

		var wg sync.WaitGroup

		for i := range raceWorkers {
			wg.Add(1)

			go func() {
				defer wg.Done()

				_, errs[i] = store.CreateIfAbsent(t.Context(), "k", []byte(strconv.Itoa(i)))
			}()
		}

		wg.Wait()

		winners := 0

		for i, err := range errs {
			switch {
			case err == nil:
				winners++
			case !errors.Is(err, storage.ErrPreconditionFailed):
				t.Fatalf("round %d worker %d: error = %v, want nil or ErrPreconditionFailed", round, i, err)
			}
		}

		if winners != 1 {
			t.Fatalf("round %d: %d winners, want exactly 1", round, winners)
		}
	}
}

func TestMissingLockFileIsTolerated(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	lockPath := filepath.Join(dir, ".lock")
	ctx := t.Context()

	removeLock := func() {
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}

	removeLock()

	version, err := store.CreateIfAbsent(ctx, "a", []byte("1"))
	if err != nil {
		t.Fatalf("CreateIfAbsent() without .lock error = %v", err)
	}

	removeLock()

	next, err := store.PutIfMatch(ctx, "a", []byte("2"), version)
	if err != nil {
		t.Fatalf("PutIfMatch() without .lock error = %v", err)
	}

	removeLock()

	if err := store.DeleteIfMatch(ctx, "a", next); err != nil {
		t.Fatalf("DeleteIfMatch() without .lock error = %v", err)
	}

	if _, _, err := store.Get(ctx, "a"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestEscapeAndOverlongKeysAreInvalidKeys(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on Windows")
	}

	outside := t.TempDir()
	dir := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	store, err := local.New(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = store.Close() })

	ctx := t.Context()

	_, _, err = store.Get(ctx, "link/secret")
	assertInvalidKey(t, "Get(symlink escape)", "link/secret", err)

	_, err = store.CreateIfAbsent(ctx, "link/new", []byte("x"))
	assertInvalidKey(t, "CreateIfAbsent(symlink escape)", "link/new", err)

	long := strings.Repeat("a", 300)
	_, err = store.CreateIfAbsent(ctx, long, []byte("x"))
	assertInvalidKey(t, "CreateIfAbsent(overlong)", "300 x a", err)
}

func TestFreshDirectoryCrossProcess(t *testing.T) {
	if dir := os.Getenv(freshDirEnv); dir != "" {
		runFreshChild(t, dir)

		return
	}

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()

	for round := range freshRounds {
		dir := t.TempDir()
		cmds := make([]*exec.Cmd, freshChildren)
		stdins := make([]io.WriteCloser, freshChildren)
		outputs := make([]*strings.Builder, freshChildren)

		for i := range freshChildren {
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFreshDirectoryCrossProcess$", "-test.count=1")

			cmd.Env = append(os.Environ(), freshDirEnv+"="+dir)
			outputs[i] = &strings.Builder{}
			cmd.Stdout = outputs[i]
			cmd.Stderr = outputs[i]

			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}

			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}

			cmds[i], stdins[i] = cmd, stdin
		}

		for _, stdin := range stdins {
			if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
		}

		winners := 0

		for i, cmd := range cmds {
			if err := cmd.Wait(); err != nil {
				t.Fatalf("round %d child %d failed: %v\n%s", round, i, err, outputs[i])
			}

			winners += strings.Count(outputs[i].String(), wonMarker)
		}

		if winners != 1 {
			t.Fatalf("round %d: %d winners, want exactly 1", round, winners)
		}
	}
}

func runFreshChild(t *testing.T, dir string) {
	t.Helper()

	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}

	store, err := local.New(dir)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	t.Cleanup(func() { _ = store.Close() })

	_, err = store.CreateIfAbsent(t.Context(), "k", []byte(strconv.Itoa(os.Getpid())))

	switch {
	case err == nil:
		fmt.Println(wonMarker)
	case !errors.Is(err, storage.ErrPreconditionFailed):
		t.Fatalf("CreateIfAbsent() error = %v, want nil or ErrPreconditionFailed", err)
	}
}
