// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"bytes"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/appvia/ipam/internal/document"
	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

func policySpec(id string, policy PoolPolicy) PoolSpec {
	return PoolSpec{
		ID:     id,
		CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
		Policy: policy,
	}
}

func storedBytes(t *testing.T, store storage.Storage, key string) []byte {
	t.Helper()

	data, _, err := store.Get(t.Context(), key)
	assertNoError(t, err)

	return data
}

func assertContains(t *testing.T, data []byte, wants ...string) {
	t.Helper()

	for _, want := range wants {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("document does not contain %s:\n%s", want, data)
		}
	}
}

func assertPolicy(t *testing.T, got, want PoolPolicy) {
	t.Helper()

	if got != want {
		t.Fatalf("policy = %+v, want %+v", got, want)
	}
}

func TestPolicyDefaultCreateStoresBestFitV2PoolAndV1Reservations(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)

	created, err := client.CreatePool(t.Context(), policySpec("prod", PoolPolicy{}))
	assertNoError(t, err)

	want := PoolPolicy{Strategy: StrategyBestFit}
	assertPolicy(t, created.Policy, want)

	assertContains(t, storedBytes(t, base, poolKey("prod")), `"schema_version": 2`, `"strategy": "best-fit"`)

	reservations := storedBytes(t, base, reservationsKey("prod"))
	assertContains(t, reservations, `"schema_version": 1`)

	if bytes.Contains(reservations, []byte(`"released"`)) || bytes.Contains(reservations, []byte(`"cooldown"`)) {
		t.Fatalf("v1 reservations carry v2 fields:\n%s", reservations)
	}

	got, err := client.GetPool(t.Context(), "prod")
	assertNoError(t, err)
	assertPolicy(t, got.Policy, want)
}

func TestPolicyCreateWithCooldownStoresV2Reservations(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)
	want := PoolPolicy{
		Strategy: StrategyLowest,
		Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour},
	}

	created, err := client.CreatePool(t.Context(), policySpec("prod", want))
	assertNoError(t, err)
	assertPolicy(t, created.Policy, want)

	// The pool document follows the strategy alone, so lowest stays v1.
	assertContains(t, storedBytes(t, base, poolKey("prod")), `"schema_version": 1`)
	assertContains(t, storedBytes(t, base, reservationsKey("prod")),
		`"schema_version": 2`, `"mode": "strict"`, `"seconds": 3600`, `"released": {}`)

	got, err := client.GetPool(t.Context(), "prod")
	assertNoError(t, err)
	assertPolicy(t, got.Policy, want)
}

func TestPolicyExplicitLowestCreateStoresV1Bytes(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)

	created, err := client.CreatePool(t.Context(), PoolSpec{
		ID:          "prod",
		CIDRs:       []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
		Description: "production",
		Labels:      map[string]string{"team": "platform"},
		Policy:      PoolPolicy{Strategy: StrategyLowest},
	})
	assertNoError(t, err)
	assertPolicy(t, created.Policy, PoolPolicy{Strategy: StrategyLowest})

	poolData := storedBytes(t, base, poolKey("prod"))

	stored, err := document.DecodePool(poolData, "prod")
	assertNoError(t, err)

	// The pre-policy encoder wrote exactly this v1 document.
	wantPool, err := document.EncodePool(document.Pool{
		SchemaVersion: document.SchemaVersionV1,
		Kind:          "pool",
		PoolID:        "prod",
		Incarnation:   stored.Incarnation,
		Family:        4,
		CIDRs:         []string{"10.0.0.0/16"},
		Description:   "production",
		Labels:        map[string]string{"team": "platform"},
	})
	assertNoError(t, err)

	if !bytes.Equal(poolData, wantPool) {
		t.Fatalf("pool document =\n%s\nwant\n%s", poolData, wantPool)
	}

	wantReservations, err := document.EncodeReservations(document.Reservations{
		SchemaVersion: document.SchemaVersionV1,
		Kind:          "reservations",
		PoolID:        "prod",
		Incarnation:   stored.Incarnation,
		Allocations:   map[string]document.Allocation{},
	})
	assertNoError(t, err)

	if reservations := storedBytes(t, base, reservationsKey("prod")); !bytes.Equal(reservations, wantReservations) {
		t.Fatalf("reservations document =\n%s\nwant\n%s", reservations, wantReservations)
	}
}

func TestPolicyCreateRejectsInvalidPolicies(t *testing.T) {
	t.Parallel()

	tests := map[string]PoolPolicy{
		"mode without duration": {Cooldown: Cooldown{Mode: CooldownStrict}},
		"duration without mode": {Cooldown: Cooldown{Duration: time.Hour}},
		"fractional seconds":    {Cooldown: Cooldown{Mode: CooldownFitAware, Duration: 1500 * time.Millisecond}},
		"above 8760h":           {Cooldown: Cooldown{Mode: CooldownStrict, Duration: 9000 * time.Hour}},
		"below 1s":              {Cooldown: Cooldown{Mode: CooldownStrict, Duration: -time.Second}},
		"unknown strategy":      {Strategy: "random"},
		"unknown mode":          {Cooldown: Cooldown{Mode: "forever", Duration: time.Hour}},
	}

	for name, policy := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			recorder := &recordingStore{Storage: memory.New()}

			_, err := newTestClient(recorder).CreatePool(t.Context(), policySpec("prod", policy))
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("CreatePool() error = %v, want ErrInvalidArgument", err)
			}

			if writes := recorder.writeKeys(); len(writes) != 0 {
				t.Fatalf("CreatePool() writes = %v, want none", writes)
			}
		})
	}
}

func TestPolicyValidateRequiresStrategyOutsideCreate(t *testing.T) {
	t.Parallel()

	if err := validatePolicy(PoolPolicy{}, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("validatePolicy(default, false) = %v, want ErrInvalidArgument", err)
	}

	limits := PoolPolicy{Strategy: StrategyBestFit, Cooldown: Cooldown{Mode: CooldownStrict, Duration: 8760 * time.Hour}}
	if err := validatePolicy(limits, false); err != nil {
		t.Fatalf("validatePolicy(8760h) = %v, want nil", err)
	}

	limits.Cooldown.Duration = time.Second
	if err := validatePolicy(limits, false); err != nil {
		t.Fatalf("validatePolicy(1s) = %v, want nil", err)
	}
}

// createV1Pool creates a pool whose documents have the pre-policy v1 shape.
func createV1Pool(t *testing.T, store storage.Storage) {
	t.Helper()

	_, err := newTestClient(store).CreatePool(t.Context(), policySpec("prod", PoolPolicy{Strategy: StrategyLowest}))
	assertNoError(t, err)

	assertContains(t, storedBytes(t, store, poolKey("prod")), `"schema_version": 1`)
	assertContains(t, storedBytes(t, store, reservationsKey("prod")), `"schema_version": 1`)
}

func TestPolicyGetPoolOnV1DocumentsReturnsLowestWithoutCooldown(t *testing.T) {
	t.Parallel()

	base := memory.New()
	createV1Pool(t, base)

	recorder := &recordingStore{Storage: base}

	got, err := newTestClient(recorder).GetPool(t.Context(), "prod")
	assertNoError(t, err)
	assertPolicy(t, got.Policy, PoolPolicy{Strategy: StrategyLowest})

	pools, err := newTestClient(recorder).ListPools(t.Context())
	assertNoError(t, err)

	if len(pools) != 1 {
		t.Fatalf("ListPools() = %v, want one pool", pools)
	}

	assertPolicy(t, pools[0].Policy, PoolPolicy{Strategy: StrategyLowest})

	if writes := recorder.writeKeys(); len(writes) != 0 {
		t.Fatalf("reads wrote %v, want none", writes)
	}
}

func TestPolicyIdenticalCreateOnV1PoolDoesNotWrite(t *testing.T) {
	t.Parallel()

	base := memory.New()
	createV1Pool(t, base)

	poolBefore := storedBytes(t, base, poolKey("prod"))
	reservationsBefore := storedBytes(t, base, reservationsKey("prod"))

	for name, policy := range map[string]PoolPolicy{
		"unset policy":    {},
		"explicit lowest": {Strategy: StrategyLowest},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &recordingStore{Storage: base}

			got, err := newTestClient(recorder).CreatePool(t.Context(), policySpec("prod", policy))
			assertNoError(t, err)
			assertPolicy(t, got.Policy, PoolPolicy{Strategy: StrategyLowest})

			if writes := recorder.writeKeys(); len(writes) != 0 {
				t.Fatalf("CreatePool() writes = %v, want none", writes)
			}
		})
	}

	if !bytes.Equal(storedBytes(t, base, poolKey("prod")), poolBefore) ||
		!bytes.Equal(storedBytes(t, base, reservationsKey("prod")), reservationsBefore) {
		t.Fatal("identical CreatePool changed the v1 documents")
	}
}

func TestPolicyDifferentCreateOnExistingPoolIsPoolExists(t *testing.T) {
	t.Parallel()

	base := memory.New()
	createV1Pool(t, base)

	for name, policy := range map[string]PoolPolicy{
		"strict cooldown": {Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour}},
		"best-fit":        {Strategy: StrategyBestFit},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &recordingStore{Storage: base}

			_, err := newTestClient(recorder).CreatePool(t.Context(), policySpec("prod", policy))
			if !errors.Is(err, ErrConflict) || !conflictReason(err, "pool_exists") {
				t.Fatalf("CreatePool() error = %v, want pool_exists", err)
			}

			if writes := recorder.writeKeys(); len(writes) != 0 {
				t.Fatalf("CreatePool() writes = %v, want none", writes)
			}
		})
	}
}

func TestPolicyCreateMatchesUnsetFieldsAgainstStoredCooldown(t *testing.T) {
	t.Parallel()

	base := memory.New()
	want := PoolPolicy{
		Strategy: StrategyBestFit,
		Cooldown: Cooldown{Mode: CooldownFitAware, Duration: time.Minute},
	}

	_, err := newTestClient(base).CreatePool(t.Context(), policySpec("prod", want))
	assertNoError(t, err)

	for name, policy := range map[string]PoolPolicy{
		"unset policy":      {},
		"strategy only":     {Strategy: StrategyBestFit},
		"cooldown only":     {Cooldown: want.Cooldown},
		"full policy match": want,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := &recordingStore{Storage: base}

			got, createErr := newTestClient(recorder).CreatePool(t.Context(), policySpec("prod", policy))
			assertNoError(t, createErr)
			assertPolicy(t, got.Policy, want)

			if writes := recorder.writeKeys(); len(writes) != 0 {
				t.Fatalf("CreatePool() writes = %v, want none", writes)
			}
		})
	}

	_, err = newTestClient(base).CreatePool(t.Context(), policySpec("prod", PoolPolicy{
		Cooldown: Cooldown{Mode: CooldownFitAware, Duration: time.Hour},
	}))
	if !conflictReason(err, "pool_exists") {
		t.Fatalf("CreatePool(different cooldown) error = %v, want pool_exists", err)
	}
}

func TestPolicyChildDoesNotInheritParentPolicy(t *testing.T) {
	t.Parallel()

	base := memory.New()
	client := newTestClient(base)

	_, err := client.CreatePool(t.Context(), policySpec("root", PoolPolicy{
		Strategy: StrategyLowest,
		Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour},
	}))
	assertNoError(t, err)

	child, err := client.CreatePool(t.Context(), PoolSpec{
		ID:         "child",
		ParentPool: "root",
		Request:    Request{Kind: RequestNext, PrefixLength: 24},
	})
	assertNoError(t, err)
	assertPolicy(t, child.Policy, PoolPolicy{Strategy: StrategyBestFit})

	got, err := client.GetPool(t.Context(), "child")
	assertNoError(t, err)
	assertPolicy(t, got.Policy, PoolPolicy{Strategy: StrategyBestFit})
	assertContains(t, storedBytes(t, base, reservationsKey("child")), `"schema_version": 1`)
}

func replaceStored(t *testing.T, store storage.Storage, key, old, replacement string) {
	t.Helper()

	data, version, err := store.Get(t.Context(), key)
	assertNoError(t, err)

	if !bytes.Contains(data, []byte(old)) {
		t.Fatalf("document %s does not contain %s:\n%s", key, old, data)
	}

	_, err = store.PutIfMatch(t.Context(), key, bytes.Replace(data, []byte(old), []byte(replacement), 1), version)
	assertNoError(t, err)
}

func TestPolicyGetPoolRejectsUnknownStoredStrategy(t *testing.T) {
	t.Parallel()

	base := memory.New()
	_, err := newTestClient(base).CreatePool(t.Context(), policySpec("prod", PoolPolicy{}))
	assertNoError(t, err)

	replaceStored(t, base, poolKey("prod"), `"strategy": "best-fit"`, `"strategy": "random"`)

	_, err = newTestClient(base).GetPool(t.Context(), "prod")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("GetPool() error = %v, want ErrInvalidArgument", err)
	}
}

func TestPolicyGetPoolRejectsInvalidStoredCooldownState(t *testing.T) {
	t.Parallel()

	tests := map[string][2]string{
		"unknown mode":      {`"mode": "strict"`, `"mode": "forever"`},
		"empty mode":        {`"mode": "strict"`, `"mode": ""`},
		"zero seconds":      {`"seconds": 3600`, `"seconds": 0`},
		"seconds too large": {`"seconds": 3600`, `"seconds": 31536001`},
		"non-canonical key": {`"released": {}`, `"released": {"10.0.0.1/24": "2026-10-30T12:00:00Z"}`},
		"bad expiry":        {`"released": {}`, `"released": {"10.0.0.0/24": "tomorrow"}`},
	}

	for name, replacement := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			base := memory.New()
			_, err := newTestClient(base).CreatePool(t.Context(), policySpec("prod", PoolPolicy{
				Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour},
			}))
			assertNoError(t, err)

			replaceStored(t, base, reservationsKey("prod"), replacement[0], replacement[1])

			_, err = newTestClient(base).GetPool(t.Context(), "prod")
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("GetPool() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestPolicyReservationsReleaseRecordsRoundTrip(t *testing.T) {
	t.Parallel()

	expiry := time.Date(2026, time.October, 30, 12, 0, 0, 500, time.UTC)
	cidr := netip.MustParsePrefix("10.0.1.0/24")
	stored := reservationsDocument{
		PoolID:      "prod",
		Incarnation: "0123456789abcdef0123456789abcdef",
		Allocations: map[string]Allocation{},
		Released:    map[netip.Prefix]time.Time{cidr: expiry},
	}

	data, err := encodeReservationsDocument(stored)
	assertNoError(t, err)
	assertContains(t, data, `"schema_version": 2`, `"cooldown": null`, `"10.0.1.0/24": "2026-10-30T12:00:00.0000005Z"`)

	decoded, err := decodeReservationsDocument(data, "prod")
	assertNoError(t, err)

	if decoded.Cooldown != (Cooldown{}) || len(decoded.Released) != 1 || !decoded.Released[cidr].Equal(expiry) {
		t.Fatalf("decoded = %+v, want records only", decoded)
	}

	// Without cooldown and records the document returns to v1.
	decoded.Released = nil

	data, err = encodeReservationsDocument(decoded)
	assertNoError(t, err)

	if !strings.Contains(string(data), `"schema_version": 1`) || strings.Contains(string(data), "released") {
		t.Fatalf("encode without records =\n%s\nwant v1", data)
	}

	stored.Released = map[netip.Prefix]time.Time{netip.MustParsePrefix("10.0.1.1/24"): expiry}
	if _, err := encodeReservationsDocument(stored); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("encode non-canonical record error = %v, want ErrInvalidArgument", err)
	}
}
