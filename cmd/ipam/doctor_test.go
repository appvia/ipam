// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

// recordingStore wraps a store, records every write and can inject failures.
type recordingStore struct {
	storage.Storage

	listErr        error
	getErr         map[string]error
	putErr         error
	ignoreVersions bool

	mu          sync.Mutex
	writes      []string
	propagation bool
}

func (r *recordingStore) note(ctx context.Context, write string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if storage.AccessPropagationRetry(ctx) {
		r.propagation = true
	}

	if write != "" {
		r.writes = append(r.writes, write)
	}
}

func (r *recordingStore) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	r.note(ctx, "")

	if err := r.getErr[key]; err != nil {
		return nil, "", err
	}

	return r.Storage.Get(ctx, key)
}

func (r *recordingStore) List(ctx context.Context, prefix string) ([]string, error) {
	r.note(ctx, "")

	if r.listErr != nil {
		return nil, r.listErr
	}

	return r.Storage.List(ctx, prefix)
}

func (r *recordingStore) CreateIfAbsent(ctx context.Context, key string, data []byte) (storage.Version, error) {
	r.note(ctx, "create "+key)

	return r.Storage.CreateIfAbsent(ctx, key, data)
}

func (r *recordingStore) PutIfMatch(
	ctx context.Context, key string, data []byte, version storage.Version,
) (storage.Version, error) {
	r.note(ctx, "put "+key+" "+string(version))

	if r.putErr != nil {
		return "", r.putErr
	}

	if r.ignoreVersions {
		_, current, err := r.Storage.Get(ctx, key)
		if err != nil {
			return "", err
		}

		version = current
	}

	return r.Storage.PutIfMatch(ctx, key, data, version)
}

func (r *recordingStore) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	r.note(ctx, "delete "+key)

	return r.Storage.DeleteIfMatch(ctx, key, version)
}

func (r *recordingStore) recordedWrites() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.writes...)
}

// inspectingStore adds storage.Inspector to a recordingStore.
type inspectingStore struct {
	*recordingStore

	info storage.Info
	err  error
}

func (i *inspectingStore) Inspect(ctx context.Context) (storage.Info, error) {
	i.note(ctx, "")

	return i.info, i.err
}

type doctorCheck struct {
	Check  string `json:"check"`
	Pool   string `json:"pool"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type doctorOutput struct {
	Result string        `json:"result"`
	Checks []doctorCheck `json:"checks"`
}

// newPoolStore returns a memory store holding pool p (10.0.0.0/16) with one
// allocation, so reservations/p.json exists.
func newPoolStore(t *testing.T) *memory.Store {
	t.Helper()

	store := memory.New()
	client := ipam.New(store)

	if _, err := client.CreatePool(t.Context(), ipam.PoolSpec{
		ID: "p", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/16")},
	}); err != nil {
		t.Fatalf("CreatePool: %v", err)
	}

	if _, err := client.Allocate(t.Context(), "p", ipam.AllocationSpec{PrefixLength: 24}); err != nil {
		t.Fatalf("Allocate: %v", err)
	}

	return store
}

func protected() storage.Info {
	return storage.Info{Versioning: storage.Yes, SoftDelete: storage.Unknown, HierarchicalNamespace: storage.No}
}

func runDoctor(t *testing.T, store storage.Storage, args ...string) result {
	t.Helper()

	var stdout, stderr bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	openStore := func(context.Context, string) (storage.Storage, error) { return store, nil }
	env := map[string]string{"IPAM_BACKEND": "mem://"}

	code := runCLI(ctx, append([]string{"doctor"}, args...), &stdout, &stderr,
		func(key string) string { return env[key] }, openClient, openStore)

	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func decodeDoctor(t *testing.T, out string) doctorOutput {
	t.Helper()

	var report doctorOutput
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("doctor stdout is not JSON: %v\n%s", err, out)
	}

	return report
}

func (d doctorOutput) find(t *testing.T, check, pool string) doctorCheck {
	t.Helper()

	for _, c := range d.Checks {
		if c.Check == check && c.Pool == pool {
			return c
		}
	}

	t.Fatalf("no %s check for pool %q in %+v", check, pool, d.Checks)

	return doctorCheck{}
}

func TestDoctorHealthyBackendPassesWithoutChangingData(t *testing.T) {
	mem := newPoolStore(t)
	store := &inspectingStore{recordingStore: &recordingStore{Storage: mem}, info: protected()}

	beforeData, beforeVersion, err := mem.Get(t.Context(), "reservations/p.json")
	if err != nil {
		t.Fatalf("Get before: %v", err)
	}

	res := runDoctor(t, store, "--expect-write", "p", "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d, want 0; stdout %s stderr %s", res.code, res.stdout, res.stderr)
	}

	report := decodeDoctor(t, res.stdout)
	if report.Result != "pass" {
		t.Fatalf("result = %q, want pass: %+v", report.Result, report.Checks)
	}

	for _, check := range []struct{ name, pool string }{
		{"backend", ""}, {"data-protection", ""}, {"hierarchical-namespace", ""}, {"write-probe", "p"},
	} {
		if got := report.find(t, check.name, check.pool); got.Status != "PASS" {
			t.Fatalf("%s = %+v, want PASS", check.name, got)
		}
	}

	afterData, afterVersion, err := mem.Get(t.Context(), "reservations/p.json")
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}

	if afterVersion != beforeVersion || !bytes.Equal(afterData, beforeData) {
		t.Fatalf("reservations changed: version %q -> %q", beforeVersion, afterVersion)
	}

	writes := store.recordedWrites()
	if want := "put reservations/p.json " + string(probeVersion); len(writes) != 1 || writes[0] != want {
		t.Fatalf("writes = %q, want exactly %q", writes, want)
	}

	if store.propagation {
		t.Fatal("doctor enabled access-propagation retries")
	}
}

func TestDoctorFailsWhenConditionalWritesAreIgnored(t *testing.T) {
	mem := newPoolStore(t)
	store := &inspectingStore{recordingStore: &recordingStore{Storage: mem, ignoreVersions: true}, info: protected()}

	before, _, err := mem.Get(t.Context(), "reservations/p.json")
	if err != nil {
		t.Fatal(err)
	}

	res := runDoctor(t, store, "-o", "json")
	if res.code != exitDoctorFailed {
		t.Fatalf("exit %d, want %d; stdout %s", res.code, exitDoctorFailed, res.stdout)
	}

	probe := decodeDoctor(t, res.stdout).find(t, "write-probe", "p")
	if probe.Status != "FAIL" || !strings.Contains(probe.Detail, "conditional writes ignored") {
		t.Fatalf("write probe = %+v, want FAIL conditional writes ignored", probe)
	}

	after, _, err := mem.Get(t.Context(), "reservations/p.json")
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("reservations bytes changed (err %v)", err)
	}

	if !strings.Contains(res.stderr, "failed") {
		t.Fatalf("stderr %q does not report the failure", res.stderr)
	}
}

func TestDoctorWriteExpectations(t *testing.T) {
	cases := []struct {
		name   string
		putErr error
		args   []string
		code   int
		status string
	}{
		{"denied but write expected", storage.ErrAccessDenied, []string{"--expect-write", "p"}, exitDoctorFailed, "FAIL"},
		{"denied and no write expected", storage.ErrAccessDenied, []string{"--expect-no-write", "p"}, exitOK, "PASS"},
		{"writable but no write expected", nil, []string{"--expect-no-write", "p"}, exitDoctorFailed, "FAIL"},
		{"writable and write expected", nil, []string{"--expect-write", "p"}, exitOK, "PASS"},
		{"denied without expectation", storage.ErrAccessDenied, nil, exitOK, "PASS"},
		{"probe error", errors.New("boom"), nil, exitDoctorFailed, "FAIL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &inspectingStore{
				recordingStore: &recordingStore{Storage: newPoolStore(t), putErr: tc.putErr},
				info:           protected(),
			}

			res := runDoctor(t, store, append(tc.args, "-o", "json")...)
			if res.code != tc.code {
				t.Fatalf("exit %d, want %d; stdout %s stderr %s", res.code, tc.code, res.stdout, res.stderr)
			}

			if probe := decodeDoctor(t, res.stdout).find(t, "write-probe", "p"); probe.Status != tc.status {
				t.Fatalf("write probe = %+v, want %s", probe, tc.status)
			}
		})
	}
}

func TestDoctorUnreadableReservationsWarnsWithoutWriting(t *testing.T) {
	mem := newPoolStore(t)
	store := &inspectingStore{
		recordingStore: &recordingStore{Storage: mem, getErr: map[string]error{
			"reservations/p.json": storage.ErrAccessDenied,
		}},
		info: protected(),
	}

	res := runDoctor(t, store, "--expect-write", "p", "--expect-write", "q", "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d, want 0; stdout %s stderr %s", res.code, res.stdout, res.stderr)
	}

	report := decodeDoctor(t, res.stdout)
	if report.Result != "warn" {
		t.Fatalf("result = %q, want warn", report.Result)
	}

	for _, pool := range []string{"p", "q"} {
		if probe := report.find(t, "write-probe", pool); probe.Status != "WARN" {
			t.Fatalf("write probe %s = %+v, want WARN", pool, probe)
		}
	}

	if writes := store.recordedWrites(); len(writes) != 0 {
		t.Fatalf("writes = %q, want none", writes)
	}
}

func TestDoctorDataProtectionAndNamespace(t *testing.T) {
	cases := []struct {
		name   string
		info   storage.Info
		check  string
		status string
		code   int
	}{
		{"versioning on", storage.Info{Versioning: storage.Yes, SoftDelete: storage.No}, "data-protection", "PASS", exitOK},
		{"soft delete on", storage.Info{Versioning: storage.No, SoftDelete: storage.Yes}, "data-protection", "PASS", exitOK},
		{"both off", storage.Info{Versioning: storage.No, SoftDelete: storage.No}, "data-protection", "FAIL", exitDoctorFailed},
		{"unknown", storage.Info{Versioning: storage.No, SoftDelete: storage.Unknown}, "data-protection", "WARN", exitOK},
		{"hns on", storage.Info{Versioning: storage.Yes, HierarchicalNamespace: storage.Yes}, "hierarchical-namespace", "FAIL", exitDoctorFailed},
		{"hns unknown", storage.Info{Versioning: storage.Yes, HierarchicalNamespace: storage.Unknown}, "hierarchical-namespace", "WARN", exitOK},
		{"hns off", storage.Info{Versioning: storage.Yes, HierarchicalNamespace: storage.No}, "hierarchical-namespace", "PASS", exitOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: tc.info}

			res := runDoctor(t, store, "-o", "json")
			if res.code != tc.code {
				t.Fatalf("exit %d, want %d; stdout %s", res.code, tc.code, res.stdout)
			}

			if got := decodeDoctor(t, res.stdout).find(t, tc.check, ""); got.Status != tc.status {
				t.Fatalf("%s = %+v, want %s", tc.check, got, tc.status)
			}
		})
	}
}

func TestDoctorInspectErrorFails(t *testing.T) {
	store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, err: errors.New("inspect broke")}

	res := runDoctor(t, store, "-o", "json")
	if res.code != exitDoctorFailed {
		t.Fatalf("exit %d, want %d; stdout %s", res.code, exitDoctorFailed, res.stdout)
	}

	if got := decodeDoctor(t, res.stdout).find(t, "data-protection", ""); got.Status != "FAIL" {
		t.Fatalf("data-protection = %+v, want FAIL", got)
	}
}

func TestDoctorStoreWithoutInspectorWarns(t *testing.T) {
	store := &recordingStore{Storage: newPoolStore(t)}

	res := runDoctor(t, store, "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d, want 0; stdout %s", res.code, res.stdout)
	}

	report := decodeDoctor(t, res.stdout)
	for _, check := range []string{"data-protection", "hierarchical-namespace"} {
		if got := report.find(t, check, ""); got.Status != "WARN" {
			t.Fatalf("%s = %+v, want WARN", check, got)
		}
	}
}

func TestDoctorUnreachableBackendFails(t *testing.T) {
	for name, listErr := range map[string]error{
		"network": errors.New("dial tcp 127.0.0.1:1: connect: connection refused"),
		"denied":  storage.ErrAccessDenied,
	} {
		t.Run(name, func(t *testing.T) {
			store := &inspectingStore{
				recordingStore: &recordingStore{Storage: newPoolStore(t), listErr: listErr},
				info:           protected(),
			}

			res := runDoctor(t, store, "--expect-write", "p", "-o", "json")
			if res.code != exitDoctorFailed {
				t.Fatalf("exit %d, want %d; stdout %s", res.code, exitDoctorFailed, res.stdout)
			}

			report := decodeDoctor(t, res.stdout)
			if got := report.find(t, "backend", ""); got.Status != "FAIL" {
				t.Fatalf("backend = %+v, want FAIL", got)
			}

			if writes := store.recordedWrites(); len(writes) != 0 {
				t.Fatalf("writes = %q, want none", writes)
			}
		})
	}
}

func TestDoctorUnreadablePoolDocumentFails(t *testing.T) {
	store := &inspectingStore{
		recordingStore: &recordingStore{Storage: newPoolStore(t), getErr: map[string]error{
			"pools/p.json": storage.ErrAccessDenied,
		}},
		info: protected(),
	}

	res := runDoctor(t, store, "-o", "json")
	if res.code != exitDoctorFailed {
		t.Fatalf("exit %d, want %d; stdout %s", res.code, exitDoctorFailed, res.stdout)
	}

	if got := decodeDoctor(t, res.stdout).find(t, "backend", ""); got.Status != "FAIL" {
		t.Fatalf("backend = %+v, want FAIL", got)
	}
}

func TestDoctorFileBackendNotApplicable(t *testing.T) {
	dir := t.TempDir()
	h := &harness{t: t, backend: "file://" + dir, env: map[string]string{}, open: openClient}
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/16")

	res := h.run("doctor", "--expect-write", "p", "-o", "json")
	if res.code != exitOK {
		t.Fatalf("exit %d; stdout %s stderr %s", res.code, res.stdout, res.stderr)
	}

	report := decodeDoctor(t, res.stdout)

	protection := report.find(t, "data-protection", "")
	if protection.Status != "WARN" || !strings.Contains(protection.Detail, "not applicable to file:// backends") {
		t.Fatalf("data-protection = %+v", protection)
	}

	if probe := report.find(t, "write-probe", "p"); probe.Status != "PASS" {
		t.Fatalf("write probe = %+v, want PASS", probe)
	}
}

func TestDoctorTableOutput(t *testing.T) {
	store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}

	res := runDoctor(t, store)
	if res.code != exitOK {
		t.Fatalf("exit %d; stderr %s", res.code, res.stderr)
	}

	for _, want := range []string{"CHECK", "STATUS", "DETAIL", "write-probe", "PASS"} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("table lacks %q:\n%s", want, res.stdout)
		}
	}
}

func TestDoctorUsageErrors(t *testing.T) {
	store := &recordingStore{Storage: newPoolStore(t)}

	for name, args := range map[string][]string{
		"pool in both lists": {"--expect-write", "p", "--expect-no-write", "p"},
		"bad output":         {"-o", "yaml"},
		"extra argument":     {"p"},
		"pool with a slash":  {"--expect-write", "../pools/p"},
	} {
		t.Run(name, func(t *testing.T) {
			if res := runDoctor(t, store, args...); res.code != exitUsage {
				t.Fatalf("exit %d, want %d; stderr %s", res.code, exitUsage, res.stderr)
			}
		})
	}

	h := newMemHarness(t)
	if res := h.runEnv(map[string]string{}, "doctor"); res.code != exitUsage {
		t.Fatalf("doctor without backend: exit %d, want %d", res.code, exitUsage)
	}

	if writes := store.recordedWrites(); len(writes) != 0 {
		t.Fatalf("writes = %q, want none", writes)
	}
}
