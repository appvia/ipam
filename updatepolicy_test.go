// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

var (
	errInjectedPolicyWrite = errors.New("injected policy write failure")

	strictThirtyMinutes = PoolPolicy{
		Strategy: StrategyBestFit,
		Cooldown: Cooldown{Mode: CooldownStrict, Duration: 30 * time.Minute},
	}
	lowestNoCooldown = PoolPolicy{Strategy: StrategyLowest}
)

type policyFaultMode int

const (
	policyWriteLanded policyFaultMode = iota
	policyWriteDropped
	policyWriteFails
	policyWriteConflicts
)

type policyFaultStore struct {
	storage.Storage
	mu        sync.Mutex
	key       string
	mode      policyFaultMode
	remaining int
}

func (store *policyFaultStore) inject(key string, mode policyFaultMode, count int) {
	store.mu.Lock()
	defer store.mu.Unlock()

	store.key, store.mode, store.remaining = key, mode, count
}

func (store *policyFaultStore) take(key string) (policyFaultMode, bool) {
	store.mu.Lock()
	defer store.mu.Unlock()

	if key != store.key || store.remaining <= 0 {
		return 0, false
	}

	store.remaining--

	return store.mode, true
}

func (store *policyFaultStore) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	mode, inject := store.take(key)
	if !inject {
		return store.Storage.PutIfMatch(ctx, key, data, version)
	}

	switch mode {
	case policyWriteLanded:
		if _, err := store.Storage.PutIfMatch(ctx, key, data, version); err != nil {
			return "", err
		}

		return "", fmt.Errorf("lost response: %w", storage.ErrUnknownOutcome)
	case policyWriteDropped:
		return "", fmt.Errorf("lost request: %w", storage.ErrUnknownOutcome)
	case policyWriteConflicts:
		return "", errors.Join(ErrConflict, errInjectedPolicyWrite)
	default:
		return "", errInjectedPolicyWrite
	}
}

func newPolicyPool(t *testing.T, store storage.Storage, policy PoolPolicy) *Client {
	t.Helper()

	client := newTestClient(store)
	_, err := client.CreatePool(t.Context(), policySpec("prod", policy))
	assertNoError(t, err)

	return client
}

func readReservations(t *testing.T, store storage.Storage) reservationsDocument {
	t.Helper()

	document, err := decodeReservationsDocument(storedBytes(t, store, reservationsKey("prod")), "prod")
	assertNoError(t, err)

	return document
}

func allocateAndRelease(t *testing.T, client *Client) {
	t.Helper()

	allocation, err := client.Allocate(t.Context(), "prod", AllocationSpec{PrefixLength: 28})
	assertNoError(t, err)
	assertNoError(t, client.Release(t.Context(), "prod", allocation.ID))
}

func assertPoolPolicy(t *testing.T, client *Client, want PoolPolicy) {
	t.Helper()

	pool, err := client.GetPool(t.Context(), "prod")
	assertNoError(t, err)
	assertPolicy(t, pool.Policy, want)
}

func TestUpdatePoolPolicyUpgradeAndDowngradeRewriteDocuments(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newPolicyPool(t, base, lowestNoCooldown)
	assertContains(t, storedBytes(t, base, poolKey("prod")), `"schema_version": 1`)
	assertContains(t, storedBytes(t, base, reservationsKey("prod")), `"schema_version": 1`)

	assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", strictThirtyMinutes))
	assertPoolPolicy(t, client, strictThirtyMinutes)
	assertContains(t, storedBytes(t, base, poolKey("prod")), `"schema_version": 2`, `"strategy": "best-fit"`)
	assertContains(t, storedBytes(t, base, reservationsKey("prod")), `"schema_version": 2`, `"cooldown"`)

	allocateAndRelease(t, client)

	if records := readReservations(t, base).Released; len(records) != 1 {
		t.Fatalf("released records = %v, want one", records)
	}

	assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", lowestNoCooldown))
	assertPoolPolicy(t, client, lowestNoCooldown)

	poolBytes := storedBytes(t, base, poolKey("prod"))
	assertContains(t, poolBytes, `"schema_version": 1`)

	if bytes.Contains(poolBytes, []byte(`"strategy"`)) {
		t.Fatalf("downgraded pool document is not v1 bytes:\n%s", poolBytes)
	}

	reservationBytes := storedBytes(t, base, reservationsKey("prod"))
	assertContains(t, reservationBytes, `"schema_version": 1`)

	if bytes.Contains(reservationBytes, []byte(`"released"`)) || bytes.Contains(reservationBytes, []byte(`"cooldown"`)) {
		t.Fatalf("downgraded reservations are not v1 bytes:\n%s", reservationBytes)
	}

	if records := readReservations(t, base).Released; len(records) != 0 {
		t.Fatalf("records after cooldown off = %v, want none", records)
	}
}

func TestUpdatePoolPolicyUnknownPoolIsNotFound(t *testing.T) {
	t.Parallel()

	client := newTestClient(memory.New())

	err := client.UpdatePoolPolicy(t.Context(), "missing", lowestNoCooldown)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdatePoolPolicy(missing) = %v, want ErrNotFound", err)
	}
}

func TestUpdatePoolPolicyRequiresExplicitStrategy(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newPolicyPool(t, base, lowestNoCooldown)
	before := storedBytes(t, base, poolKey("prod"))

	err := client.UpdatePoolPolicy(t.Context(), "prod", PoolPolicy{})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("UpdatePoolPolicy(empty strategy) = %v, want ErrInvalidArgument", err)
	}

	if !bytes.Equal(before, storedBytes(t, base, poolKey("prod"))) {
		t.Fatal("rejected policy modified the pool document")
	}
}

func TestUpdatePoolPolicyUnknownOutcome(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		key  string
		mode policyFaultMode
	}{
		{"strategy landed", poolKey("prod"), policyWriteLanded},
		{"strategy dropped", poolKey("prod"), policyWriteDropped},
		{"cooldown landed", reservationsKey("prod"), policyWriteLanded},
		{"cooldown dropped", reservationsKey("prod"), policyWriteDropped},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			faults := &policyFaultStore{Storage: memory.New()}
			client := newPolicyPool(t, faults, lowestNoCooldown)
			faults.inject(test.key, test.mode, 1)

			assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", strictThirtyMinutes))
			assertPoolPolicy(t, client, strictThirtyMinutes)
		})
	}
}

func TestUpdatePoolPolicyLandedUnknownOutcomeIsNotRewrittenAgain(t *testing.T) {
	t.Parallel()

	faults := &policyFaultStore{Storage: memory.New()}
	client := newPolicyPool(t, faults, lowestNoCooldown)
	faults.inject(poolKey("prod"), policyWriteLanded, 1)

	assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", PoolPolicy{Strategy: StrategyBestFit}))

	faults.mu.Lock()
	defer faults.mu.Unlock()

	if faults.remaining != 0 {
		t.Fatalf("remaining faults = %d, want 0", faults.remaining)
	}
}

func TestUpdatePoolPolicyRetryCompletesPartialUpdate(t *testing.T) {
	t.Parallel()

	faults := &policyFaultStore{Storage: memory.New()}
	client := newPolicyPool(t, faults, lowestNoCooldown)
	faults.inject(reservationsKey("prod"), policyWriteConflicts, 1)

	err := client.UpdatePoolPolicy(t.Context(), "prod", strictThirtyMinutes)
	if !errors.Is(err, errInjectedPolicyWrite) || !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdatePoolPolicy = %v, want the injected cooldown failure wrapping ErrConflict", err)
	}

	if errors.Unwrap(err) == nil {
		t.Fatalf("UpdatePoolPolicy = %v, want the partial-update context wrapped around the cause", err)
	}

	assertPoolPolicy(t, client, PoolPolicy{Strategy: StrategyBestFit})

	assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", strictThirtyMinutes))
	assertPoolPolicy(t, client, strictThirtyMinutes)

	faults.inject(poolKey("prod"), policyWriteFails, 1)
	assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", strictThirtyMinutes))

	faults.mu.Lock()
	defer faults.mu.Unlock()

	if faults.remaining != 1 {
		t.Fatal("a converged policy wrote to the pool document")
	}
}

func TestUpdatePoolPolicyCooldownFailureWithEqualStrategyIsReportedPlainly(t *testing.T) {
	t.Parallel()

	base := memory.New()
	faults := &policyFaultStore{Storage: base}
	client := newPolicyPool(t, faults, lowestNoCooldown)
	poolBefore := storedBytes(t, base, poolKey("prod"))

	faults.inject(reservationsKey("prod"), policyWriteFails, 1)

	wanted := PoolPolicy{Strategy: StrategyLowest, Cooldown: strictThirtyMinutes.Cooldown}

	err := client.UpdatePoolPolicy(t.Context(), "prod", wanted)
	if !errors.Is(err, errInjectedPolicyWrite) {
		t.Fatalf("UpdatePoolPolicy = %v, want the injected cooldown failure", err)
	}

	if errors.Unwrap(err) != nil {
		t.Fatalf("UpdatePoolPolicy = %v, want the plain cause: no strategy was written", err)
	}

	if !bytes.Equal(poolBefore, storedBytes(t, base, poolKey("prod"))) {
		t.Fatal("an unchanged strategy rewrote the pool document")
	}

	assertPoolPolicy(t, client, lowestNoCooldown)

	assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", wanted))
	assertPoolPolicy(t, client, wanted)
}

func TestUpdatePoolPolicyDurationChangeKeepsRecordExpiry(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newPolicyPool(t, base, strictThirtyMinutes)
	allocateAndRelease(t, client)

	before := readReservations(t, base).Released
	if len(before) != 1 {
		t.Fatalf("released records = %v, want one", before)
	}

	hourly := PoolPolicy{
		Strategy: StrategyBestFit,
		Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour},
	}
	assertNoError(t, client.UpdatePoolPolicy(t.Context(), "prod", hourly))
	assertPoolPolicy(t, client, hourly)

	after := readReservations(t, base).Released
	for cidr, expiry := range before {
		if got, ok := after[cidr]; !ok || !got.Equal(expiry) {
			t.Fatalf("record %s expiry = %v (present %v), want %v", cidr, got, ok, expiry)
		}
	}
}

func TestUpdatePoolPolicyRefusesOtherIncarnation(t *testing.T) {
	t.Parallel()

	base := memory.New()
	_ = newPolicyPool(t, base, lowestNoCooldown)
	other := New(base, WithClock(func() time.Time { return time.Unix(1, 0).UTC() }),
		WithSleeper(noSleep), WithMaxAttempts(2))

	data, version, err := base.Get(t.Context(), reservationsKey("prod"))
	assertNoError(t, err)

	document, err := decodeReservationsDocument(data, "prod")
	assertNoError(t, err)

	document.Incarnation = "00000000000000000000000000000001"
	if document.Incarnation == poolIncarnation(t, base) {
		t.Fatal("test incarnation collides with the pool incarnation")
	}

	mismatched, err := encodeReservationsDocument(document)
	assertNoError(t, err)

	_, err = base.PutIfMatch(t.Context(), reservationsKey("prod"), mismatched, version)
	assertNoError(t, err)

	poolBefore := storedBytes(t, base, poolKey("prod"))

	err = other.UpdatePoolPolicy(t.Context(), "prod", strictThirtyMinutes)
	assertPoolIncomplete(t, err)

	if !bytes.Equal(poolBefore, storedBytes(t, base, poolKey("prod"))) ||
		!bytes.Equal(mismatched, storedBytes(t, base, reservationsKey("prod"))) {
		t.Fatal("UpdatePoolPolicy rewrote a document of another incarnation")
	}
}

func poolIncarnation(t *testing.T, store storage.Storage) string {
	t.Helper()

	pool, err := decodePoolDocument(storedBytes(t, store, poolKey("prod")), "prod")
	assertNoError(t, err)

	return pool.Incarnation
}

func assertPoolIncomplete(t *testing.T, err error) {
	t.Helper()

	var operationError *OpError
	if !errors.As(err, &operationError) || operationError.Reason != "pool_incomplete" ||
		!errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want pool_incomplete wrapping ErrNotFound", err)
	}
}
