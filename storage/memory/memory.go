// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package memory provides an in-memory storage backend.
package memory

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/appvia/ipam/storage"
)

type object struct {
	data    []byte
	version storage.Version
}

// Store is a concurrency-safe in-memory storage backend.
type Store struct {
	BeforeWrite func(op, key string)

	mu      sync.RWMutex
	objects map[string]object
	version uint64
}

// New creates an empty in-memory store.
func New() *Store {
	return &Store{
		objects: make(map[string]object),
	}
}

// Get returns a copy of an object's data and its current version.
func (s *Store) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", fmt.Errorf("memory get %q: %w", key, err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	value, exists := s.objects[key]
	if !exists {
		return nil, "", fmt.Errorf("memory get %q: %w", key, storage.ErrNotFound)
	}

	return append([]byte(nil), value.data...), value.version, nil
}

// CreateIfAbsent creates an object only when its key does not exist.
func (s *Store) CreateIfAbsent(
	ctx context.Context,
	key string,
	data []byte,
) (storage.Version, error) {
	s.beforeWrite("create", key)

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("memory create %q: %w", key, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.objects[key]; exists {
		return "", fmt.Errorf("memory create %q: %w", key, storage.ErrPreconditionFailed)
	}

	version := s.nextVersion()
	s.objects[key] = object{
		data:    append([]byte(nil), data...),
		version: version,
	}

	return version, nil
}

// PutIfMatch replaces an object only when its version matches.
func (s *Store) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	s.beforeWrite("put", key)

	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("memory put %q: %w", key, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.objects[key]
	if !exists {
		return "", fmt.Errorf("memory put %q: %w", key, storage.ErrNotFound)
	}

	if current.version != version {
		return "", fmt.Errorf("memory put %q: %w", key, storage.ErrPreconditionFailed)
	}

	nextVersion := s.nextVersion()
	s.objects[key] = object{
		data:    append([]byte(nil), data...),
		version: nextVersion,
	}

	return nextVersion, nil
}

// DeleteIfMatch deletes an object only when its version matches.
func (s *Store) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	s.beforeWrite("delete", key)

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("memory delete %q: %w", key, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current, exists := s.objects[key]
	if !exists {
		return fmt.Errorf("memory delete %q: %w", key, storage.ErrNotFound)
	}

	if current.version != version {
		return fmt.Errorf("memory delete %q: %w", key, storage.ErrPreconditionFailed)
	}

	delete(s.objects, key)
	s.nextVersion()

	return nil
}

// List returns sorted keys with the requested prefix.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("memory list %q: %w", prefix, err)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0)

	for key := range s.objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}

	sort.Strings(keys)

	return keys, nil
}

func (s *Store) beforeWrite(op, key string) {
	if s.BeforeWrite != nil {
		s.BeforeWrite(op, key)
	}
}

func (s *Store) nextVersion() storage.Version {
	s.version++

	return storage.Version(strconv.FormatUint(s.version, 10))
}

// Inspect reports Unknown versioning and soft delete (the in-memory store keeps no prior versions) and no
// hierarchical namespace. It makes no calls.
func (s *Store) Inspect(context.Context) (storage.Info, error) {
	return storage.Info{
		Versioning:            storage.Unknown,
		SoftDelete:            storage.Unknown,
		HierarchicalNamespace: storage.No,
	}, nil
}
