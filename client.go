// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/appvia/ipam/storage"
)

const (
	defaultMaxAttempts = 8
	defaultBackoffBase = 10 * time.Millisecond
	defaultBackoffCap  = 250 * time.Millisecond
)

// Clock supplies the creation time recorded on allocations.
type Clock func() time.Time

// Sleeper waits for a retry delay and must return promptly when ctx is done.
type Sleeper func(ctx context.Context, delay time.Duration) error

// IDGenerator supplies allocation IDs.
type IDGenerator func() string

// Option configures a Client.
type Option func(*Client)

// Client coordinates IPAM documents through conditional storage operations.
type Client struct {
	store       storage.Storage
	maxAttempts int
	backoffBase time.Duration
	backoffCap  time.Duration
	clock       Clock
	random      io.Reader
	sleeper     Sleeper
	idGenerator IDGenerator

	randomMu sync.Mutex
	locksMu  sync.Mutex
	locks    map[string]*poolMutex
	noLocks  bool
}

type poolMutex struct {
	token    chan struct{}
	disabled bool
}

func newPoolMutex() *poolMutex {
	lock := &poolMutex{token: make(chan struct{}, 1)}
	lock.token <- struct{}{}

	return lock
}

func (lock *poolMutex) LockContext(ctx context.Context) error {
	if lock.disabled {
		return ctx.Err()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-lock.token:
		return nil
	}
}

func (lock *poolMutex) Lock() {
	if lock.disabled {
		return
	}

	<-lock.token
}

func (lock *poolMutex) Unlock() {
	if lock.disabled {
		return
	}

	lock.token <- struct{}{}
}

// New constructs a client over store.
func New(store storage.Storage, options ...Option) *Client {
	client := &Client{
		store:       store,
		maxAttempts: defaultMaxAttempts,
		backoffBase: defaultBackoffBase,
		backoffCap:  defaultBackoffCap,
		clock:       time.Now,
		random:      rand.Reader,
		sleeper:     sleepContext,
		idGenerator: NewAllocationID,
		locks:       make(map[string]*poolMutex),
	}

	for _, option := range options {
		if option != nil {
			option(client)
		}
	}

	return client
}

// WithMaxAttempts bounds retriable conditional writes. Zero retries until ctx ends.
func WithMaxAttempts(attempts int) Option {
	return func(client *Client) {
		client.maxAttempts = attempts
	}
}

// WithBackoff configures exponential full-jitter retry delays.
func WithBackoff(base, cap time.Duration) Option {
	return func(client *Client) {
		client.backoffBase = base
		client.backoffCap = cap
	}
}

// WithClock injects the allocation clock.
func WithClock(clock Clock) Option {
	return func(client *Client) {
		if clock != nil {
			client.clock = clock
		}
	}
}

// WithRand injects the entropy source used for incarnations and retry jitter.
func WithRand(random io.Reader) Option {
	return func(client *Client) {
		if random != nil {
			client.random = random
		}
	}
}

// WithSleeper injects the cancellable retry sleeper.
func WithSleeper(sleeper Sleeper) Option {
	return func(client *Client) {
		if sleeper != nil {
			client.sleeper = sleeper
		}
	}
}

// WithIDGenerator injects allocation ID generation.
func WithIDGenerator(generator IDGenerator) Option {
	return func(client *Client) {
		if generator != nil {
			client.idGenerator = generator
		}
	}
}

// withoutPoolLocks disables the in-process contention optimisation in tests.
// Correctness must continue to come entirely from storage CAS operations.
func withoutPoolLocks() Option {
	return func(client *Client) {
		client.noLocks = true
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (client *Client) poolLock(poolID string) *poolMutex {
	if client.noLocks {
		return &poolMutex{disabled: true}
	}

	client.locksMu.Lock()
	defer client.locksMu.Unlock()

	lock := client.locks[poolID]
	if lock == nil {
		lock = newPoolMutex()
		client.locks[poolID] = lock
	}

	return lock
}

func (client *Client) newIncarnation() (string, error) {
	var value [16]byte

	client.randomMu.Lock()
	_, err := io.ReadFull(client.random, value[:])
	client.randomMu.Unlock()

	if err != nil {
		return "", fmt.Errorf("generate incarnation: %w", err)
	}

	return hex.EncodeToString(value[:]), nil
}

func (client *Client) waitRetry(ctx context.Context, failedAttempt int) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if client.backoffBase <= 0 || client.backoffCap <= 0 {
		return client.sleeper(ctx, 0)
	}

	limit := client.backoffBase
	for range failedAttempt {
		if limit >= client.backoffCap || limit > client.backoffCap/2 {
			limit = client.backoffCap
			break
		}

		limit *= 2
	}

	if limit > client.backoffCap {
		limit = client.backoffCap
	}

	var raw [8]byte

	client.randomMu.Lock()
	_, err := io.ReadFull(client.random, raw[:])
	client.randomMu.Unlock()

	if err != nil {
		return fmt.Errorf("retry jitter: %w", err)
	}

	delay := time.Duration(binary.LittleEndian.Uint64(raw[:]) % uint64(limit+1))

	return client.sleeper(ctx, delay)
}

func (client *Client) attemptsExhausted(attempt int) bool {
	return client.maxAttempts > 0 && attempt >= client.maxAttempts
}

func contentionError(op, poolID, id string, last error) error {
	if conflictReason(last, "incarnation_mismatch") {
		// The pool and reservations documents kept different incarnations for
		// every attempt: this is residue of an interrupted create or delete,
		// which is not a usable pool.
		return &OpError{
			Op: op, Pool: poolID, ID: id, Reason: "pool_incomplete",
			Err: fmt.Errorf("pool documents have different incarnations: %w", ErrNotFound),
		}
	}

	return &OpError{
		Op:     op,
		Pool:   poolID,
		ID:     id,
		Reason: "contention",
		Err:    errors.Join(ErrConflict, last),
	}
}

func mapStorageError(op, poolID, id string, err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, storage.ErrNotFound) {
		return &OpError{Op: op, Pool: poolID, ID: id, Err: ErrNotFound}
	}

	return err
}

func conflictReason(err error, reason string) bool {
	var operationError *OpError

	return errors.As(err, &operationError) &&
		errors.Is(err, ErrConflict) &&
		operationError.Reason == reason
}

func validateClient(client *Client) error {
	if client == nil || client.store == nil {
		return fmt.Errorf("client storage: %w", ErrInvalidArgument)
	}

	if client.maxAttempts < 0 {
		return fmt.Errorf("max attempts must not be negative: %w", ErrInvalidArgument)
	}

	return nil
}
