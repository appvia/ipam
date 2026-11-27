// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

const supportedBackendsURL = "https://github.com/appvia/ipam#supported-backends"

func TestSupportedBackendsDocumentation(t *testing.T) {
	readme := readDocumentation(t, "README.md")
	support := markdownSection(t, readme, "## Supported backends")

	t.Run("AC-19: support policy and consent", func(t *testing.T) {
		for _, phrase := range []string{
			"`s3://<bucket>/<prefix>` with no `endpoint=` override | **Verified**",
			"S3 with an endpoint hostname matching `*.amazonaws.com` or `*.amazonaws.com.cn` | **Verified**",
			"`azblob://<account>.blob.core.windows.net/<container>/<prefix>` | **Verified**",
			"`file://` or `mem://` | **Verified**",
			"S3 with any other endpoint hostname | **Unverified**",
			"`azblob://<container>/<prefix>` | **Unverified**",
			"`--allow-unverified-backend`",
			"`IPAM_ALLOW_UNVERIFIED_BACKEND=true`",
			"`--allow-unverified-backend=false` takes precedence over the environment",
			"`WithAllowUnverifiedBackend`",
			"`Open` and `OpenStorage`",
			"`ClassifyBackend(rawURL)`",
			"loopback",
			"Operators are responsible for evaluating the endpoint",
			"storage contract suite against it, including conditional deletion",
			"does not prove an endpoint is safe",
		} {
			requireDocumentation(t, support, phrase)
		}
	})

	t.Run("AC-20: conditional-write safety", func(t *testing.T) {
		for _, phrase := range []string{
			"Conditional creation must reject an existing object",
			"Conditional replacement and deletion must reject a version mismatch",
			"concurrent writers can corrupt pool and reservation documents",
			"Amazon S3 is the reference target and the only verified S3 service",
			"Doctor cannot prove conditional deletion",
			"can change data on a non-enforcing backend",
		} {
			requireDocumentation(t, support, phrase)
		}
	})
}

func TestUnverifiedBackendMigrationDocumentation(t *testing.T) {
	changelog := readDocumentation(t, "CHANGELOG.md")

	unreleased := markdownSection(t, changelog, "## [Unreleased]")
	for _, phrase := range []string{
		"Breaking change",
		"unverified custom endpoints",
		"`--allow-unverified-backend`",
		"`IPAM_ALLOW_UNVERIFIED_BACKEND=true`",
		"`WithAllowUnverifiedBackend`",
		"do not make an endpoint safe or verified",
	} {
		requireDocumentation(t, unreleased, phrase)
	}
}

func TestAC1AllocationsMigrationContract(t *testing.T) {
	spec := readDocumentation(t, "specs/BASE-0007-document-format/spec.md")
	decision := readDocumentation(t, "specs/BASE-0007-document-format/decisions/ADR-0001-allocations-storage-migration.md")
	changelog := readDocumentation(t, "CHANGELOG.md")

	for _, phrase := range []string{
		"`reservations/<pool-id>.json`",
		"`allocations/<pool-id>.json`",
		"`schema_version: 3`",
		"`kind: allocations`",
		"`ipam migrate allocations`",
		"conditional writes",
		"idempotent",
		"fail closed",
	} {
		requireDocumentation(t, spec, phrase)
	}

	for _, phrase := range []string{
		"store created by the previous release",
		"cross-version test",
		"TestMigrateAllocationsPreviousReleaseStore",
		"TestMigrateAllocationsCrossVersionSafety",
		"compatibility window",
		"coordinated provider release",
		"CreateIfAbsent",
		"DeleteIfMatch",
		"unknown outcome",
	} {
		requireDocumentation(t, decision, phrase)
	}

	unreleased := markdownSection(t, changelog, "## [Unreleased]")
	for _, phrase := range []string{
		"coordinated provider release",
		"`reservations/` to `allocations/`",
		"migration is explicit",
	} {
		requireDocumentation(t, unreleased, phrase)
	}
}

func TestMigrateAllocationsPreviousReleaseStore(t *testing.T) {
	tests := []struct {
		name    string
		policy  PoolPolicy
		release bool
	}{
		{
			name:   "v1",
			policy: PoolPolicy{Strategy: StrategyLowest},
		},
		{
			name: "v2",
			policy: PoolPolicy{
				Strategy: StrategyBestFit,
				Cooldown: Cooldown{Mode: CooldownStrict, Duration: time.Hour},
			},
			release: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := memory.New()
			previous := newTestClient(store)
			pool, err := previous.CreatePool(t.Context(), PoolSpec{
				ID:     "tenant-a",
				CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
				Policy: test.policy,
			})
			assertNoError(t, err)

			first, err := previous.Allocate(t.Context(), pool.ID, AllocationSpec{
				ID:           testAllocationID,
				PrefixLength: 26,
			})
			assertNoError(t, err)

			if test.release {
				second, allocateErr := previous.Allocate(t.Context(), pool.ID, AllocationSpec{
					ID:           "01900000-0000-7000-8000-000000000002",
					PrefixLength: 26,
				})
				assertNoError(t, allocateErr)
				assertNoError(t, previous.Release(t.Context(), pool.ID, second.ID))
			}

			poolBefore := getObject(t, store, poolKey(pool.ID))
			legacyBefore := getObject(t, store, reservationsKey(pool.ID))

			assertNoError(t, migrateAllocationsReference(t.Context(), store, pool.ID))

			if got := getObject(t, store, poolKey(pool.ID)); !reflect.DeepEqual(got.data, poolBefore.data) {
				t.Fatal("migration changed the pool document")
			}

			_, _, err = store.Get(t.Context(), reservationsKey(pool.ID))
			if !errors.Is(err, storage.ErrNotFound) {
				t.Fatalf("legacy document after migration = %v, want ErrNotFound", err)
			}

			migrated := getObject(t, store, allocationsKeyForContract(pool.ID))
			assertMigrationDocument(t, legacyBefore.data, migrated.data, pool.ID, first.ID)

			version := migrated.version

			assertNoError(t, migrateAllocationsReference(t.Context(), store, pool.ID))

			if got := getObject(t, store, allocationsKeyForContract(pool.ID)); got.version != version {
				t.Fatal("idempotent rerun rewrote the migrated document")
			}
		})
	}

	t.Run("AC-1: conditional delete conflict preserves allocation state", func(t *testing.T) {
		store := memory.New()
		previous := newTestClient(store)
		_, err := previous.CreatePool(t.Context(), PoolSpec{
			ID:     "tenant-a",
			CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
			Policy: PoolPolicy{Strategy: StrategyLowest},
		})
		assertNoError(t, err)
		_, err = previous.Allocate(t.Context(), "tenant-a", AllocationSpec{
			ID:           testAllocationID,
			PrefixLength: 26,
		})
		assertNoError(t, err)

		legacy := reservationsKey("tenant-a")
		original := getObject(t, store, legacy)
		changed := setFirstAllocationDescription(t, original.data, "previous-writer-update")
		injected := false
		store.BeforeWrite = func(op, key string) {
			if op != "create" || key != allocationsKeyForContract("tenant-a") || injected {
				return
			}

			injected = true

			if _, putErr := store.PutIfMatch(t.Context(), legacy, changed, original.version); putErr != nil {
				t.Fatalf("inject concurrent legacy write: %v", putErr)
			}
		}

		err = migrateAllocationsReference(t.Context(), store, "tenant-a")
		if !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Fatalf("migration conflict = %v, want ErrPreconditionFailed", err)
		}

		if got := getObject(t, store, legacy).data; string(got) != string(changed) {
			t.Fatal("migration deleted or replaced the concurrently updated legacy state")
		}

		getObject(t, store, allocationsKeyForContract("tenant-a"))
		store.BeforeWrite = nil

		if err := migrateAllocationsReference(t.Context(), store, "tenant-a"); !errors.Is(err, errReferenceConflict) {
			t.Fatalf("rerun after conflicting cutover = %v, want %v", err, errReferenceConflict)
		}
	})
}

func TestMigrateAllocationsCrossVersionSafety(t *testing.T) {
	t.Run("AC-1: previous bridge and migrated clients share the authoritative key", func(t *testing.T) {
		store := memory.New()
		previous := newTestClient(store)
		_, err := previous.CreatePool(t.Context(), PoolSpec{
			ID:     "tenant-a",
			CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
			Policy: PoolPolicy{Strategy: StrategyLowest},
		})
		assertNoError(t, err)
		_, err = previous.Allocate(t.Context(), "tenant-a", AllocationSpec{
			ID:           testAllocationID,
			PrefixLength: 26,
		})
		assertNoError(t, err)

		assertNoError(t, mutateReferenceClient(
			t.Context(), store, "tenant-a", referenceBridgeClient, "bridge-before",
		))
		assertReferenceDescription(t, store, "tenant-a", referencePreviousClient, "bridge-before")

		stale := getObject(t, store, reservationsKey("tenant-a"))
		assertNoError(t, migrateAllocationsReference(t.Context(), store, "tenant-a"))

		staleBytes := setFirstAllocationDescription(t, stale.data, "stale-previous-writer")

		_, err = store.PutIfMatch(
			t.Context(), reservationsKey("tenant-a"), staleBytes, stale.version,
		)
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("stale previous-client write = %v, want ErrNotFound", err)
		}

		assertReferenceDescription(t, store, "tenant-a", referenceBridgeClient, "bridge-before")
		assertReferenceDescription(t, store, "tenant-a", referenceMigratedClient, "bridge-before")
		assertNoError(t, mutateReferenceClient(
			t.Context(), store, "tenant-a", referenceBridgeClient, "bridge-after",
		))
		assertReferenceDescription(t, store, "tenant-a", referenceMigratedClient, "bridge-after")
		assertNoError(t, mutateReferenceClient(
			t.Context(), store, "tenant-a", referenceMigratedClient, "migrated-after",
		))
		assertReferenceDescription(t, store, "tenant-a", referenceBridgeClient, "migrated-after")

		_, _, err = store.Get(t.Context(), reservationsKey("tenant-a"))
		if !errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("legacy key after cutover = %v, want ErrNotFound", err)
		}
	})

	t.Run("AC-1: bridge fails closed when both keys exist", func(t *testing.T) {
		store := memory.New()
		previous := newTestClient(store)
		_, err := previous.CreatePool(t.Context(), PoolSpec{
			ID:     "tenant-a",
			CIDRs:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")},
			Policy: PoolPolicy{Strategy: StrategyLowest},
		})
		assertNoError(t, err)
		_, err = previous.Allocate(t.Context(), "tenant-a", AllocationSpec{
			ID:           testAllocationID,
			PrefixLength: 26,
		})
		assertNoError(t, err)

		legacy := getObject(t, store, reservationsKey("tenant-a"))
		target, err := migrateDocumentReference(legacy.data)
		assertNoError(t, err)
		_, err = store.CreateIfAbsent(t.Context(), allocationsKeyForContract("tenant-a"), target)
		assertNoError(t, err)

		beforeLegacy := getObject(t, store, reservationsKey("tenant-a"))
		beforeTarget := getObject(t, store, allocationsKeyForContract("tenant-a"))

		err = mutateReferenceClient(
			t.Context(), store, "tenant-a", referenceBridgeClient, "must-not-write",
		)
		if !errors.Is(err, errReferenceBothKeys) {
			t.Fatalf("bridge write with both keys = %v, want %v", err, errReferenceBothKeys)
		}

		if got := getObject(t, store, reservationsKey("tenant-a")); got.version != beforeLegacy.version {
			t.Fatal("bridge changed legacy state while both keys existed")
		}

		if got := getObject(t, store, allocationsKeyForContract("tenant-a")); got.version != beforeTarget.version {
			t.Fatal("bridge changed migrated state while both keys existed")
		}
	})
}

type referenceClientKind string

const (
	referencePreviousClient referenceClientKind = "previous"
	referenceBridgeClient   referenceClientKind = "bridge"
	referenceMigratedClient referenceClientKind = "migrated"
)

var (
	errReferenceBothKeys = errors.New("both allocation-state keys exist")
	errReferenceConflict = errors.New("allocation-state documents differ")
)

type referenceObject struct {
	data    []byte
	version storage.Version
}

type referenceEnvelope struct {
	SchemaVersion int    `json:"schema_version"`
	Kind          string `json:"kind"`
	PoolID        string `json:"pool_id"`
	Incarnation   string `json:"incarnation"`
}

func allocationsKeyForContract(poolID string) string {
	return "allocations/" + poolID + documentSuffix
}

func migrateAllocationsReference(ctx context.Context, store storage.Storage, poolID string) error {
	poolObject, err := readObject(ctx, store, poolKey(poolID))
	if err != nil {
		return fmt.Errorf("read pool: %w", err)
	}

	var poolEnvelope referenceEnvelope
	if err := json.Unmarshal(poolObject.data, &poolEnvelope); err != nil {
		return fmt.Errorf("decode pool: %w", err)
	}

	legacyKey := reservationsKey(poolID)
	targetKey := allocationsKeyForContract(poolID)
	legacy, legacyErr := readObject(ctx, store, legacyKey)
	target, targetErr := readObject(ctx, store, targetKey)

	if legacyErr != nil && !errors.Is(legacyErr, storage.ErrNotFound) {
		return fmt.Errorf("read legacy: %w", legacyErr)
	}

	if targetErr != nil && !errors.Is(targetErr, storage.ErrNotFound) {
		return fmt.Errorf("read target: %w", targetErr)
	}

	switch {
	case legacyErr == nil && targetErr == nil:
		expected, err := migrateDocumentReference(legacy.data)
		if err != nil {
			return err
		}

		if err := validateMigratedReference(expected, poolID, poolEnvelope.Incarnation); err != nil {
			return err
		}

		if err := validateMigratedReference(target.data, poolID, poolEnvelope.Incarnation); err != nil {
			return err
		}

		if !equalJSON(expected, target.data) {
			return errReferenceConflict
		}

		return store.DeleteIfMatch(ctx, legacyKey, legacy.version)
	case legacyErr == nil:
		targetData, err := migrateDocumentReference(legacy.data)
		if err != nil {
			return err
		}

		if err := validateMigratedReference(targetData, poolID, poolEnvelope.Incarnation); err != nil {
			return err
		}

		if _, err := store.CreateIfAbsent(ctx, targetKey, targetData); err != nil {
			return fmt.Errorf("create migrated document: %w", err)
		}

		return store.DeleteIfMatch(ctx, legacyKey, legacy.version)
	case targetErr == nil:
		return validateMigratedReference(target.data, poolID, poolEnvelope.Incarnation)
	default:
		return fmt.Errorf("pool %s has no allocation-state document: %w", poolID, storage.ErrNotFound)
	}
}

func migrateDocumentReference(legacy []byte) ([]byte, error) {
	var source map[string]json.RawMessage
	if err := json.Unmarshal(legacy, &source); err != nil {
		return nil, fmt.Errorf("decode legacy allocation state: %w", err)
	}

	var envelope referenceEnvelope
	if err := json.Unmarshal(legacy, &envelope); err != nil {
		return nil, fmt.Errorf("decode legacy envelope: %w", err)
	}

	if envelope.SchemaVersion != 1 && envelope.SchemaVersion != 2 {
		return nil, fmt.Errorf("legacy schema version %d", envelope.SchemaVersion)
	}

	if envelope.Kind != "reservations" {
		return nil, fmt.Errorf("legacy kind %q", envelope.Kind)
	}

	target := make(map[string]json.RawMessage, 7)

	for _, field := range []string{"pool_id", "incarnation", "allocations", "cooldown", "released"} {
		if value, ok := source[field]; ok {
			target[field] = value
		}
	}

	if target["pool_id"] == nil || target["incarnation"] == nil || target["allocations"] == nil {
		return nil, errors.New("legacy allocation state is incomplete")
	}

	if target["cooldown"] == nil {
		target["cooldown"] = json.RawMessage("null")
	}

	if target["released"] == nil {
		target["released"] = json.RawMessage("{}")
	}

	target["schema_version"] = json.RawMessage("3")
	target["kind"] = json.RawMessage(`"allocations"`)

	data, err := json.Marshal(target)
	if err != nil {
		return nil, fmt.Errorf("encode migrated allocation state: %w", err)
	}

	return data, nil
}

func validateMigratedReference(data []byte, poolID, incarnation string) error {
	var envelope referenceEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode migrated allocation state: %w", err)
	}

	if envelope.SchemaVersion != 3 || envelope.Kind != "allocations" {
		return fmt.Errorf("invalid migrated version or kind: %d/%s", envelope.SchemaVersion, envelope.Kind)
	}

	if envelope.PoolID != poolID || envelope.Incarnation != incarnation {
		return fmt.Errorf("migrated pool identity differs from %s/%s", poolID, incarnation)
	}

	return nil
}

func readObject(ctx context.Context, store storage.Storage, key string) (referenceObject, error) {
	data, version, err := store.Get(ctx, key)

	return referenceObject{data: data, version: version}, err
}

func getObject(t *testing.T, store storage.Storage, key string) referenceObject {
	t.Helper()

	object, err := readObject(t.Context(), store, key)
	assertNoError(t, err)

	return object
}

func assertMigrationDocument(t *testing.T, legacy, migrated []byte, poolID, allocationID string) {
	t.Helper()

	var before, after map[string]json.RawMessage
	assertNoError(t, json.Unmarshal(legacy, &before))
	assertNoError(t, json.Unmarshal(migrated, &after))

	var envelope referenceEnvelope
	assertNoError(t, json.Unmarshal(migrated, &envelope))

	if envelope.SchemaVersion != 3 || envelope.Kind != "allocations" || envelope.PoolID != poolID {
		t.Fatalf("migrated envelope = %#v", envelope)
	}

	for _, field := range []string{"pool_id", "incarnation", "allocations"} {
		if !equalJSON(before[field], after[field]) {
			t.Errorf("migrated %s changed: before %s, after %s", field, before[field], after[field])
		}
	}

	for _, field := range []string{"cooldown", "released"} {
		if before[field] != nil && !equalJSON(before[field], after[field]) {
			t.Errorf("migrated %s changed: before %s, after %s", field, before[field], after[field])
		}
	}

	if before["cooldown"] == nil && string(after["cooldown"]) != "null" {
		t.Errorf("v1 migration cooldown = %s, want null", after["cooldown"])
	}

	if before["released"] == nil && string(after["released"]) != "{}" {
		t.Errorf("v1 migration released = %s, want {}", after["released"])
	}

	var allocations map[string]json.RawMessage
	assertNoError(t, json.Unmarshal(after["allocations"], &allocations))

	if _, ok := allocations[allocationID]; !ok {
		t.Errorf("migrated allocations do not contain %s", allocationID)
	}
}

func equalJSON(left, right []byte) bool {
	var leftValue, rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}

	return reflect.DeepEqual(leftValue, rightValue)
}

func readReferenceClient(
	ctx context.Context,
	store storage.Storage,
	poolID string,
	kind referenceClientKind,
) (string, referenceObject, error) {
	legacyKey := reservationsKey(poolID)
	targetKey := allocationsKeyForContract(poolID)

	switch kind {
	case referencePreviousClient:
		object, err := readObject(ctx, store, legacyKey)

		return legacyKey, object, err
	case referenceMigratedClient:
		object, err := readObject(ctx, store, targetKey)

		return targetKey, object, err
	case referenceBridgeClient:
		legacy, legacyErr := readObject(ctx, store, legacyKey)
		target, targetErr := readObject(ctx, store, targetKey)

		switch {
		case legacyErr == nil && targetErr == nil:
			return "", referenceObject{}, errReferenceBothKeys
		case legacyErr == nil:
			if targetErr != nil && !errors.Is(targetErr, storage.ErrNotFound) {
				return "", referenceObject{}, targetErr
			}

			return legacyKey, legacy, nil
		case targetErr == nil:
			if legacyErr != nil && !errors.Is(legacyErr, storage.ErrNotFound) {
				return "", referenceObject{}, legacyErr
			}

			return targetKey, target, nil
		default:
			return "", referenceObject{}, errors.Join(legacyErr, targetErr)
		}
	default:
		return "", referenceObject{}, fmt.Errorf("unknown reference client %q", kind)
	}
}

func mutateReferenceClient(
	ctx context.Context,
	store storage.Storage,
	poolID string,
	kind referenceClientKind,
	description string,
) error {
	key, object, err := readReferenceClient(ctx, store, poolID, kind)
	if err != nil {
		return err
	}

	changed, err := setFirstAllocationDescriptionJSON(object.data, description)
	if err != nil {
		return err
	}

	_, err = store.PutIfMatch(ctx, key, changed, object.version)

	return err
}

func setFirstAllocationDescriptionJSON(data []byte, description string) ([]byte, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}

	var allocations map[string]map[string]json.RawMessage
	if err := json.Unmarshal(document["allocations"], &allocations); err != nil {
		return nil, err
	}

	for _, allocation := range allocations {
		encoded, err := json.Marshal(description)
		if err != nil {
			return nil, err
		}

		allocation["description"] = encoded

		break
	}

	encodedAllocations, err := json.Marshal(allocations)
	if err != nil {
		return nil, err
	}

	document["allocations"] = encodedAllocations

	return json.Marshal(document)
}

func setFirstAllocationDescription(t *testing.T, data []byte, description string) []byte {
	t.Helper()

	changed, err := setFirstAllocationDescriptionJSON(data, description)
	assertNoError(t, err)

	return changed
}

func assertReferenceDescription(
	t *testing.T,
	store storage.Storage,
	poolID string,
	kind referenceClientKind,
	want string,
) {
	t.Helper()

	_, object, err := readReferenceClient(t.Context(), store, poolID, kind)
	assertNoError(t, err)

	var document struct {
		Allocations map[string]struct {
			Description string `json:"description"`
		} `json:"allocations"`
	}
	assertNoError(t, json.Unmarshal(object.data, &document))

	for _, allocation := range document.Allocations {
		if allocation.Description != want {
			t.Errorf("%s client description = %q, want %q", kind, allocation.Description, want)
		}

		return
	}

	t.Errorf("%s client observed no allocation", kind)
}

func TestBackendSupportLinks(t *testing.T) {
	readme := readDocumentation(t, "README.md")

	backendURLs := markdownSection(t, readme, "## Backend URLs and credentials")
	if strings.Count(readme, "## Supported backends") != 1 {
		t.Fatal("README must have exactly one Supported backends section")
	}

	requireDocumentation(t, backendURLs, supportedBackendsURL)

	s3Source := readDocumentation(t, "storage/s3/s3.go")

	start := strings.Index(s3Source, "// Endpoint overrides")
	if start < 0 {
		t.Fatal("missing S3 Endpoint documentation comment")
	}

	end := strings.Index(s3Source[start:], "\n\tEndpoint string")
	if end < 0 {
		t.Fatal("could not find the end of the S3 Endpoint documentation comment")
	}

	endpointComment := s3Source[start : start+end]
	requireDocumentation(t, endpointComment, supportedBackendsURL)
}

func readDocumentation(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	return string(contents)
}

func markdownSection(t *testing.T, markdown, heading string) string {
	t.Helper()

	start := strings.Index(markdown, heading)
	if start < 0 {
		t.Fatalf("missing heading %q", heading)
	}

	section := markdown[start:]

	if next := strings.Index(section[len(heading):], "\n## "); next >= 0 {
		section = section[:len(heading)+next]
	}

	return section
}

func requireDocumentation(t *testing.T, text, phrase string) {
	t.Helper()

	if !strings.Contains(strings.Join(strings.Fields(text), " "), phrase) {
		t.Errorf("documentation is missing %q", phrase)
	}
}
