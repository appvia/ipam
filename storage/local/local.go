// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package local implements storage.Storage on a directory of plain files.
//
// It is intended for development, tests and single-host use on LOCAL DISKS
// ONLY. Never point it at a network filesystem (NFS, SMB, FUSE mounts,
// cloud-synced folders): advisory file locks and atomic renames are not
// reliable there, so the compare-and-swap guarantees do not hold.
//
// Each key is a file under the store directory ("/" becomes nested
// directories) holding the object bytes verbatim. The version of an object is
// the hex sha256 of its content. Every conditional operation holds an
// exclusive lock on <dir>/.lock, which makes it atomic across goroutines and
// processes sharing the directory; the lock is released by the operating
// system if a holder crashes, so a leftover .lock file is harmless. Writes go
// to a temporary file that is synced and renamed into place, so readers never
// observe partial content.
//
// Limitation: the backend never deletes .lock itself. If an outside actor
// deletes or replaces .lock while it is held, two holders can lock different
// files and updates can be lost silently. The check made after locking narrows
// that window but cannot close it, so do not remove .lock while the store is
// in use.
//
// All file access goes through os.Root, so symlinks inside the directory can
// not be used to read or write outside it.
package local

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/appvia/ipam/storage"
)

// testHookAfterOpen is set only by tests. It runs after the lock file is
// opened and before it is locked.
var testHookAfterOpen func()

const (
	lockName     = ".lock"
	tempPrefix   = ".ipam-tmp-"
	lockAttempts = 100
)

// ErrInvalidKey identifies a key that is empty, absolute, contains "." or
// ".." segments, or uses a name reserved by the backend.
var ErrInvalidKey = errors.New("local: invalid key (invalid argument)")

// Store is a storage.Storage backed by a local directory.
type Store struct {
	root *os.Root
}

var _ storage.Storage = (*Store)(nil)

// New returns a Store rooted at dir, creating the directory when missing.
// dir must be on a local disk.
func New(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("local: directory must not be empty")
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("local: create directory: %w", err)
	}

	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("local: open directory: %w", err)
	}

	store := &Store{root: root}

	release, err := store.acquire()
	if err != nil {
		_ = root.Close()

		return nil, err
	}

	release()

	return store, nil
}

// Close releases the directory handle. The Store must not be used afterwards.
// Windows cannot delete a directory while the Store still holds it open.
func (s *Store) Close() error {
	return s.root.Close()
}

// Get returns the content and version of key.
func (s *Store) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	if err := validateKey(key); err != nil {
		return nil, "", err
	}

	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	data, err := s.read(key)
	if err != nil {
		return nil, "", mapError("get", key, err)
	}

	return data, versionOf(data), nil
}

// CreateIfAbsent creates key, failing with storage.ErrPreconditionFailed when
// it already exists.
func (s *Store) CreateIfAbsent(ctx context.Context, key string, data []byte) (storage.Version, error) {
	const op = "create"

	if err := validateKey(key); err != nil {
		return "", err
	}

	var version storage.Version

	err := s.locked(ctx, func() error {
		if _, err := s.root.Lstat(filepath.FromSlash(key)); err == nil {
			return fmt.Errorf("local: create %q: %w", key, storage.ErrPreconditionFailed)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return mapError(op, key, err)
		}

		if err := s.write(key, data); err != nil {
			return mapError(op, key, err)
		}

		version = versionOf(data)

		return nil
	})

	return version, err
}

// PutIfMatch replaces key when its current version equals version.
func (s *Store) PutIfMatch(ctx context.Context, key string, data []byte, version storage.Version) (storage.Version, error) {
	const op = "put"

	if err := validateKey(key); err != nil {
		return "", err
	}

	var next storage.Version

	err := s.locked(ctx, func() error {
		if err := s.check(op, key, version); err != nil {
			return err
		}

		if err := s.write(key, data); err != nil {
			return mapError(op, key, err)
		}

		next = versionOf(data)

		return nil
	})

	return next, err
}

// DeleteIfMatch removes key when its current version equals version.
func (s *Store) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	const op = "delete"

	if err := validateKey(key); err != nil {
		return err
	}

	return s.locked(ctx, func() error {
		if err := s.check(op, key, version); err != nil {
			return err
		}

		if err := s.root.Remove(filepath.FromSlash(key)); err != nil {
			return mapError(op, key, err)
		}

		return nil
	})
}

// List returns the sorted keys that start with prefix. Lock, temporary and
// non-regular files (including symlinks) are ignored.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var keys []string

	err := fs.WalkDir(s.root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		if !d.Type().IsRegular() || name == lockName || strings.HasPrefix(d.Name(), tempPrefix) {
			return nil
		}

		if strings.HasPrefix(name, prefix) {
			keys = append(keys, name)
		}

		return ctx.Err()
	})
	if err != nil {
		return nil, mapError("list", prefix, err)
	}

	slices.Sort(keys)

	return keys, nil
}

func (s *Store) locked(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	release, err := s.acquire()
	if err != nil {
		return err
	}

	defer release()

	return fn()
}

// acquire takes the exclusive store lock. The lock file is opened without
// O_CREATE first because os.Root can report ENOENT for a concurrent
// O_CREATE of a missing file; creation uses O_EXCL and any ENOENT or EEXIST
// simply retries. After locking, the descriptor must still be the file at
// <dir>/.lock, otherwise the file was deleted or replaced after it was opened
// and the lock is discarded and taken again. This does not protect a lock that
// is held while .lock is deleted; see the package documentation.
func (s *Store) acquire() (func(), error) {
	var last error

	for range lockAttempts {
		f, err := s.openLockFile()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrExist) {
				return nil, mapError("lock", lockName, err)
			}

			last = err

			continue
		}

		if testHookAfterOpen != nil {
			testHookAfterOpen()
		}

		if err := lockFile(f); err != nil {
			_ = f.Close()

			return nil, mapError("lock", lockName, err)
		}

		if s.isCurrentLock(f) {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}

		_ = unlockFile(f)
		_ = f.Close()

		last = errors.New("lock file was replaced while waiting")
	}

	return nil, fmt.Errorf("local: acquire lock %q: gave up after %d attempts: %v", lockName, lockAttempts, last)
}

func (s *Store) openLockFile() (*os.File, error) {
	f, err := s.root.OpenFile(lockName, os.O_RDWR, 0o644)
	if !errors.Is(err, fs.ErrNotExist) {
		return f, err
	}

	return s.root.OpenFile(lockName, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o644)
}

func (s *Store) isCurrentLock(f *os.File) bool {
	opened, err := f.Stat()
	if err != nil {
		return false
	}

	current, err := s.root.Lstat(lockName)
	if err != nil {
		return false
	}

	return os.SameFile(opened, current)
}

func (s *Store) check(op, key string, version storage.Version) error {
	data, err := s.read(key)
	if err != nil {
		return mapError(op, key, err)
	}

	if versionOf(data) != version {
		return fmt.Errorf("local: %s %q: %w", op, key, storage.ErrPreconditionFailed)
	}

	return nil
}

func (s *Store) read(key string) ([]byte, error) {
	f, err := s.root.Open(filepath.FromSlash(key))
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file: %w", fs.ErrNotExist)
	}

	return io.ReadAll(f)
}

func (s *Store) write(key string, data []byte) error {
	rel := filepath.FromSlash(key)

	if err := s.root.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
		return err
	}

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}

	tmp := filepath.Join(filepath.Dir(rel), tempPrefix+hex.EncodeToString(suffix))

	f, err := s.root.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}

	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		_ = f.Close()
		_ = s.root.Remove(tmp)

		return err
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = s.root.Remove(tmp)

		return err
	}

	if err := f.Close(); err != nil {
		_ = s.root.Remove(tmp)

		return err
	}

	if err := s.root.Rename(tmp, rel); err != nil {
		_ = s.root.Remove(tmp)

		return err
	}

	s.syncDir(filepath.Dir(rel))

	return nil
}

// syncDir flushes a directory entry to disk after a rename. It is best
// effort: platforms and filesystems that cannot sync directories are ignored.
func (s *Store) syncDir(dir string) {
	d, err := s.root.Open(dir)
	if err != nil {
		return
	}

	_ = d.Sync()
	_ = d.Close()
}

func validateKey(key string) error {
	if key == "" {
		return fmt.Errorf("%w: empty key", ErrInvalidKey)
	}

	if strings.ContainsAny(key, "\x00\\:") || strings.HasPrefix(key, "/") || path.IsAbs(key) {
		return fmt.Errorf("%w: %q must be a relative slash-separated path", ErrInvalidKey, key)
	}

	for seg := range strings.SplitSeq(key, "/") {
		switch {
		case seg == "" || seg == "." || seg == "..":
			return fmt.Errorf("%w: %q has an empty, '.' or '..' segment", ErrInvalidKey, key)
		case strings.HasPrefix(seg, tempPrefix):
			return fmt.Errorf("%w: %q uses reserved prefix %q", ErrInvalidKey, key, tempPrefix)
		}
	}

	if key == lockName {
		return fmt.Errorf("%w: %q is reserved", ErrInvalidKey, key)
	}

	return nil
}

func versionOf(data []byte) storage.Version {
	sum := sha256.Sum256(data)

	return storage.Version(hex.EncodeToString(sum[:]))
}

func mapError(op, key string, err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("local: %s %q: %w: %w", op, key, storage.ErrNotFound, err)
	case errors.Is(err, syscall.ENAMETOOLONG) || strings.Contains(err.Error(), "path escapes from parent"):
		return fmt.Errorf("local: %s %q: %w: %v", op, key, ErrInvalidKey, err)
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("local: %s %q: %w: %w", op, key, storage.ErrAccessDenied, err)
	default:
		return fmt.Errorf("local: %s %q: %w", op, key, err)
	}
}

// Inspect reports Unknown versioning and soft delete (a local folder has no versioning or soft delete) and no
// hierarchical namespace. It makes no calls.
func (s *Store) Inspect(context.Context) (storage.Info, error) {
	return storage.Info{
		Versioning:            storage.Unknown,
		SoftDelete:            storage.Unknown,
		HierarchicalNamespace: storage.No,
	}, nil
}
