// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	calls       []string
	contexts    []context.Context
	propagation bool
}

func (r *recordingStore) note(ctx context.Context, write string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if storage.AccessPropagationRetry(ctx) {
		r.propagation = true
	}

	r.calls = append(r.calls, write)
	r.contexts = append(r.contexts, ctx)

	if strings.HasPrefix(write, "create ") || strings.HasPrefix(write, "put ") || strings.HasPrefix(write, "delete ") {
		r.writes = append(r.writes, write)
	}
}

func (r *recordingStore) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	r.note(ctx, "get "+key)

	if err := r.getErr[key]; err != nil {
		return nil, "", err
	}

	return r.Storage.Get(ctx, key)
}

func (r *recordingStore) List(ctx context.Context, prefix string) ([]string, error) {
	r.note(ctx, "list "+prefix)

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
	i.note(ctx, "inspect")

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
	h := &harness{t: t, backend: fileURL(dir), env: map[string]string{}, open: openClient}
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

const (
	verifiedClassificationDetail = "verified support policy; not evidence of connectivity, permission or conditional-write enforcement"
	allowedClassificationDetail  = "unverified; explicit consent permits use but does not prove conditional-write enforcement. " +
		"Consent: --allow-unverified-backend or IPAM_ALLOW_UNVERIFIED_BACKEND=true. See https://github.com/appvia/ipam#supported-backends"
	refusedClassificationDetail = "unverified; no explicit consent; ordinary use refused. Use --allow-unverified-backend or " +
		"IPAM_ALLOW_UNVERIFIED_BACKEND=true after evaluating conditional-write safety. See https://github.com/appvia/ipam#supported-backends"
	probeSkipDetail = "write probes skipped: backend is unverified and no opt-in was supplied"
)

// assertDoctorRows compares the shared row contract in either output format.
func assertDoctorRows(t *testing.T, res result, format, overall string, code int, rows ...doctorCheck) {
	t.Helper()

	if res.code != code {
		t.Fatalf("exit = %d, want %d: %+v", res.code, code, res)
	}

	if format == "json" {
		report := decodeDoctor(t, res.stdout)
		if report.Result != overall || len(report.Checks) < len(rows) {
			t.Fatalf("report = %+v, want result %s and rows %+v", report, overall, rows)
		}

		for i, row := range rows {
			if got := report.find(t, row.Check, row.Pool); got != row {
				t.Errorf("row = %+v, want %+v", got, row)
			}

			if i == 0 && report.Checks[0] != row {
				t.Errorf("first row = %+v, want %+v", report.Checks[0], row)
			}
		}

		var wire struct {
			Checks []map[string]json.RawMessage `json:"checks"`
		}

		if err := json.Unmarshal([]byte(res.stdout), &wire); err != nil {
			t.Fatal(err)
		}

		if len(wire.Checks[0]) != 3 || wire.Checks[0]["pool"] != nil || wire.Checks[0]["stats"] != nil {
			t.Fatalf("classification row has unexpected fields: %v", wire.Checks[0])
		}

		return
	}

	lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
	if !strings.Contains(lines[0], "CHECK") || lines[len(lines)-1] != "RESULT: "+overall {
		t.Fatalf("invalid table: %s", res.stdout)
	}

	for i, row := range rows {
		pool := row.Pool
		if pool == "" {
			pool = "-"
		}

		want := strings.Fields(strings.Join([]string{row.Check, pool, row.Status, row.Detail}, " "))
		found := false

		for _, line := range lines[1:] {
			if slices.Equal(strings.Fields(line), want) {
				found = true
			}
		}

		if !found || (i == 0 && !slices.Equal(strings.Fields(lines[1]), want)) {
			t.Errorf("missing or misplaced row %+v: %s", row, res.stdout)
		}
	}
}

func TestDoctorVerifiedClassification(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		for _, state := range []struct {
			name, result string
			info         storage.Info
			code         int
		}{
			{"healthy", "pass", protected(), exitOK},
			{"unknown", "warn", storage.Info{}, exitOK},
			{"unprotected", "fail", storage.Info{Versioning: storage.No, SoftDelete: storage.No}, exitDoctorFailed},
		} {
			t.Run("AC-13/"+format+"/"+state.name, func(t *testing.T) {
				store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: state.info}
				res := runDoctor(t, store, "-o", format)
				assertDoctorRows(t, res, format, state.result, state.code,
					doctorCheck{"backend-classification", "", statusPass, verifiedClassificationDetail})
			})
		}
	}
}

func TestDoctorAllowedUnverifiedClassification(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run("AC-14/"+format, func(t *testing.T) {
			store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}
			res := runDoctor(t, store, "--backend", testUnverifiedBackend, "--allow-unverified-backend", "-o", format)
			assertDoctorRows(t, res, format, "warn", exitOK,
				doctorCheck{"backend-classification", "", statusWarn, allowedClassificationDetail})
			assertSupportWarning(t, res, 1)
		})
	}
}

func TestDoctorRefusedUnverifiedClassification(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run("AC-15/"+format+"/explicit false overrides environment", func(t *testing.T) {
			h := newPolicyHarness(t)
			res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend, "IPAM_ALLOW_UNVERIFIED_BACKEND": "true"},
				"doctor", "--allow-unverified-backend=false", "-o", format)
			assertDoctorRows(t, res, format, "fail", exitDoctorFailed,
				doctorCheck{"backend-classification", "", statusFail, refusedClassificationDetail},
				doctorCheck{"write-probe", "", statusWarn, probeSkipDetail})
			assertSupportWarning(t, res, 0)
			assertOpeningConsent(t, h, true)
		})

		t.Run("AC-15/"+format, func(t *testing.T) {
			store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}
			res := runDoctor(t, store, "--backend", testUnverifiedBackend, "-o", format)
			assertDoctorRows(t, res, format, "fail", exitDoctorFailed,
				doctorCheck{"backend-classification", "", statusFail, refusedClassificationDetail})
			assertSupportWarning(t, res, 0)
		})
	}
}

func TestDoctorRefusalKeepsReadOnlyChecks(t *testing.T) {
	for _, state := range []string{"readable", "list denied", "pool denied", "inspect denied", "reservations denied"} {
		t.Run("AC-16/"+state, func(t *testing.T) {
			store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}

			switch state {
			case "list denied":
				store.listErr = storage.ErrAccessDenied
			case "pool denied":
				store.getErr = map[string]error{"pools/p.json": storage.ErrAccessDenied}
			case "inspect denied":
				store.err = storage.ErrAccessDenied
			case "reservations denied":
				store.getErr = map[string]error{"reservations/p.json": storage.ErrAccessDenied}
			}

			res := runDoctor(t, store, "--backend", testUnverifiedBackend, "-o", "json")
			assertDoctorRows(t, res, "json", "fail", exitDoctorFailed,
				doctorCheck{"backend-classification", "", statusFail, refusedClassificationDetail})
			report := decodeDoctor(t, res.stdout)

			if !slices.Contains(store.calls, "list pools/") {
				t.Fatal("backend was not read")
			}

			if state == "list denied" || state == "pool denied" {
				if got := report.find(t, "backend", ""); got.Status != statusFail || !strings.Contains(got.Detail, "access denied") {
					t.Fatalf("real reachability failure lost: %+v", got)
				}

				return
			}

			for _, call := range []string{"get pools/p.json", "inspect", "get reservations/p.json"} {
				if !slices.Contains(store.calls, call) {
					t.Errorf("read-only call %q absent from %q", call, store.calls)
				}
			}

			for _, check := range []string{"backend", "data-protection", "hierarchical-namespace", "fragmentation"} {
				pool := ""
				if check == "fragmentation" {
					pool = "p"
				}

				got := report.find(t, check, pool)
				if state == "readable" && got.Status != statusPass {
					t.Errorf("available check did not pass: %+v", got)
				}
			}

			if state == "inspect denied" && report.find(t, "data-protection", "").Status != statusFail {
				t.Fatal("inspection failure lost")
			}

			if state == "reservations denied" && report.find(t, "fragmentation", "p").Status != statusWarn {
				t.Fatal("unreadable statistics lost")
			}
		})
	}
}

func TestDoctorUnverifiedWithoutConsentNeverWrites(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		for _, expectation := range []string{"--expect-write", "--expect-no-write"} {
			for _, scenario := range []string{"discovered", "union", "expectation-only", "no-pools", "list-denied", "pool-denied"} {
				t.Run("AC-28/"+format+"/"+expectation+"/"+scenario, func(t *testing.T) {
					store := &inspectingStore{recordingStore: &recordingStore{Storage: memory.New()}, info: protected()}
					args := []string{"--backend", testUnverifiedBackend, "-o", format}
					pools := []string{""}

					switch scenario {
					case "discovered", "union", "pool-denied":
						store.Storage = newPoolStore(t)

						args = append(args, expectation, "p")
						pools = []string{"p"}

						if scenario == "pool-denied" {
							store.getErr = map[string]error{"pools/p.json": storage.ErrAccessDenied}
						}

						if scenario == "union" {
							args = append(args, expectation, "z", expectation, "a", expectation, "p")
							pools = []string{"a", "p", "z"}
						}
					case "expectation-only", "list-denied":
						args = append(args, expectation, "z", expectation, "a")
						pools = []string{"a", "z"}

						if scenario == "list-denied" {
							store.listErr = storage.ErrAccessDenied
						}
					}

					res := runDoctor(t, store, args...)

					rows := []doctorCheck{{"backend-classification", "", statusFail, refusedClassificationDetail}}
					for _, pool := range pools {
						rows = append(rows, doctorCheck{"write-probe", pool, statusWarn, probeSkipDetail})
					}

					assertDoctorRows(t, res, format, "fail", exitDoctorFailed, rows...)

					if writes := store.recordedWrites(); len(writes) != 0 {
						t.Fatalf("create/put/delete calls = %q, want exactly zero", writes)
					}

					var probePools []string

					if format == "json" {
						for _, check := range decodeDoctor(t, res.stdout).Checks {
							if check.Check == "write-probe" {
								probePools = append(probePools, check.Pool)
							}
						}
					} else {
						for _, line := range strings.Split(res.stdout, "\n") {
							fields := strings.Fields(line)
							if len(fields) > 1 && fields[0] == "write-probe" {
								pool := fields[1]
								if pool == "-" {
									pool = ""
								}

								probePools = append(probePools, pool)
							}
						}
					}

					if !slices.Equal(probePools, pools) {
						t.Fatalf("skip pools = %q, want sorted union %q", probePools, pools)
					}
				})
			}
		}
	}
}

func TestDoctorConsentRetainsExistingProbe(t *testing.T) {
	for _, backend := range []string{"mem://", testUnverifiedBackend} {
		for _, allowed := range []bool{false, true} {
			t.Run("AC-17/AC-27/AC-28/"+backend+"/"+fmt.Sprint(allowed), func(t *testing.T) {
				parent := t.Context()
				store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}

				before, version, err := store.Storage.Get(parent, "reservations/p.json")
				if err != nil {
					t.Fatal(err)
				}

				var stdout, stderr bytes.Buffer

				args := []string{"doctor", "--backend", backend, "-o", "json"}
				if allowed {
					args = append(args, "--allow-unverified-backend")
				}

				opened := 0
				code := runCLI(parent, args, &stdout, &stderr, func(string) string { return "" }, openClient,
					func(ctx context.Context, raw string) (storage.Storage, error) {
						opened++

						want := parent
						if allowed || backend == testUnverifiedBackend {
							want = ipam.WithAllowUnverifiedBackend(parent)
						}

						if raw != backend || !reflect.DeepEqual(ctx, want) || len(store.calls) != 0 {
							t.Fatal("diagnostic construction changed selection, consent or performed I/O")
						}

						return store, nil
					})

				wantCode := exitOK
				if backend == testUnverifiedBackend && !allowed {
					wantCode = exitDoctorFailed
				}

				if code != wantCode || opened != 1 {
					t.Fatalf("exit %d, opens %d: %s %s", code, opened, stdout.String(), stderr.String())
				}

				for _, ctx := range store.contexts {
					if ctx != parent {
						t.Fatal("derived opening consent leaked into checks")
					}
				}

				if wantCode == exitOK {
					wantCalls := []string{"list pools/", "get pools/p.json", "inspect", "get reservations/p.json",
						"put reservations/p.json " + string(probeVersion), "get pools/p.json", "get reservations/p.json"}
					if !slices.Equal(store.calls, wantCalls) {
						t.Fatalf("probe/read order = %q, want %q", store.calls, wantCalls)
					}
				}

				after, afterVersion, err := store.Storage.Get(parent, "reservations/p.json")
				if err != nil || version != afterVersion || !bytes.Equal(before, after) {
					t.Fatal("probe bytes or stored version changed")
				}
			})
		}
	}
}

func runDoctorOpener(t *testing.T, backend, consent, format string, opener openStorageFunc) result {
	t.Helper()

	var stdout, stderr bytes.Buffer

	code := runCLI(t.Context(), []string{"doctor", "--backend", backend, "--allow-unverified-backend=" + consent, "-o", format},
		&stdout, &stderr, func(string) string { return "" }, openClient, opener)

	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func testDoctorPolicyRedaction(t *testing.T) {
	t.Helper()

	backend := "s3://private-bucket-marker/private-prefix-marker?endpoint=https://private-endpoint-marker.example.test"

	for _, consent := range []string{"false", "true"} {
		for _, format := range []string{"table", "json"} {
			for _, constructionFails := range []bool{false, true} {
				t.Run("AC-24/doctor/"+consent+"/"+format+"/"+fmt.Sprint(constructionFails), func(t *testing.T) {
					res := runDoctorOpener(t, backend, consent, format, func(context.Context, string) (storage.Storage, error) {
						if constructionFails {
							return nil, errors.New("private-credential-marker " + backend)
						}

						return &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}, nil
					})

					status, detail, overall, code := statusFail, refusedClassificationDetail, "fail", exitDoctorFailed
					if consent == "true" {
						status, detail = statusWarn, allowedClassificationDetail

						if !constructionFails {
							overall, code = "warn", exitOK
						}
					}

					rows := []doctorCheck{{"backend-classification", "", status, detail}}
					if constructionFails {
						rows = append(rows, doctorCheck{"backend", "", statusFail, "cannot construct unverified backend for diagnosis"})

						if consent == "false" {
							rows = append(rows, doctorCheck{"write-probe", "", statusWarn, probeSkipDetail})
						}
					}

					assertDoctorRows(t, res, format, overall, code, rows...)

					for _, marker := range []string{backend, "private-bucket-marker", "private-prefix-marker", "private-endpoint-marker", "private-credential-marker"} {
						if strings.Contains(res.stdout+res.stderr, marker) {
							t.Fatalf("doctor disclosed synthetic configuration marker %q", marker)
						}
					}
				})
			}
		}
	}
}

func TestAzureContainerFormUnverifiedPolicy(t *testing.T) {
	for _, consent := range []string{"false", "true"} {
		for _, format := range []string{"table", "json"} {
			t.Run("AC-15/AC-23/AC-24/AC-28/doctor/"+consent+"/"+format, func(t *testing.T) {
				store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}
				res := runDoctorOpener(t, "azblob://fictional-container/private-prefix-marker", consent, format,
					func(context.Context, string) (storage.Storage, error) { return store, nil })

				status, detail, overall, code, warnings := statusFail, refusedClassificationDetail, "fail", exitDoctorFailed, 0
				if consent == "true" {
					status, detail, overall, code, warnings = statusWarn, allowedClassificationDetail, "warn", exitOK, 1
				}

				assertDoctorRows(t, res, format, overall, code, doctorCheck{"backend-classification", "", status, detail})
				assertSupportWarning(t, res, warnings)

				if consent == "false" && len(store.recordedWrites()) != 0 {
					t.Fatal("container-form Azure probed without consent")
				}

				for _, connection := range []string{"", "synthetic-invalid-connection-marker",
					"AccountName=private-account-marker;AccountKey=private-key-marker;BlobEndpoint=https://private-endpoint-marker.example.test"} {
					t.Setenv("AZURE_STORAGE_CONNECTION_STRING", connection)

					res = runDoctorOpener(t, "azblob://fictional-container/private-prefix-marker", consent, format, ipam.OpenStorage)
					assertDoctorRows(t, res, format, "fail", exitDoctorFailed,
						doctorCheck{"backend-classification", "", status, detail},
						doctorCheck{"backend", "", statusFail, "cannot construct unverified backend for diagnosis"})

					for _, marker := range []string{"private-account-marker", "private-key-marker", "private-endpoint-marker", "private-prefix-marker", "synthetic-invalid-connection-marker"} {
						if strings.Contains(res.stdout+res.stderr, marker) {
							t.Fatalf("constructor diagnostic disclosed %q", marker)
						}
					}
				}
			})
		}
	}

	t.Run("AC-13/AC-27/doctor/account form", func(t *testing.T) {
		store := &inspectingStore{recordingStore: &recordingStore{Storage: memory.New()}, info: protected()}
		res := runDoctor(t, store, "--backend", "azblob://fictionalaccount.blob.core.windows.net/fictional-container", "-o", "json")
		assertDoctorRows(t, res, "json", "pass", exitOK,
			doctorCheck{"backend-classification", "", statusPass, verifiedClassificationDetail})
	})
}

func TestDoctorVerifiedErrorsPreserved(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		for _, cause := range []error{errors.New("synthetic constructor failure"), ipam.ErrInvalidArgument} {
			t.Run("AC-27/"+format+"/"+cause.Error(), func(t *testing.T) {
				var stdout, stderr bytes.Buffer

				c := &cli{stdout: &stdout, getenv: func(string) string { return "" },
					openStorage: func(context.Context, string) (storage.Storage, error) { return nil, cause }}
				cmd := c.rootCommand()
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				cmd.SetArgs([]string{"doctor", "--backend", "mem://", "-o", format})

				if err := cmd.ExecuteContext(t.Context()); !errors.Is(err, cause) || stdout.Len() != 0 {
					t.Fatalf("verified constructor failure changed: %v, stdout %q", err, stdout.String())
				}
			})
		}
	}
}

func TestDoctorDiagnosticConstructionHasNoIO(t *testing.T) {
	for _, consent := range []string{"false", "true"} {
		t.Run("AC-17/"+consent, func(t *testing.T) {
			for key, value := range map[string]string{
				"AWS_ACCESS_KEY_ID": "dummy-key", "AWS_SECRET_ACCESS_KEY": "dummy-secret",
				"AWS_SESSION_TOKEN": "", "AWS_PROFILE": "", "AWS_CONFIG_FILE": "/dev/null",
				"AWS_SHARED_CREDENTIALS_FILE": "/dev/null", "AWS_EC2_METADATA_DISABLED": "true",
			} {
				t.Setenv(key, value)
			}

			var requests atomic.Int64

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			t.Cleanup(server.Close)

			backend := "s3://fictional-bucket/prefix?region=us-east-1&path_style=true&endpoint=" + url.QueryEscape(server.URL)
			store := &inspectingStore{recordingStore: &recordingStore{Storage: newPoolStore(t)}, info: protected()}
			opened := 0
			res := runDoctorOpener(t, backend, consent, "json", func(ctx context.Context, raw string) (storage.Storage, error) {
				opened++

				handle, err := ipam.OpenStorage(ctx, raw)
				if err != nil || handle == nil {
					t.Fatalf("diagnostic construction failed: %v", err)
				}

				if requests.Load() != 0 || len(store.calls) != 0 {
					t.Fatal("construction issued requests or diagnostic storage operations")
				}

				return store, nil
			})

			wantCode, wantWrites := exitDoctorFailed, 0
			if consent == "true" {
				wantCode, wantWrites = exitOK, 1
			}

			if res.code != wantCode || opened != 1 || requests.Load() != 0 ||
				len(store.calls) == 0 || len(store.recordedWrites()) != wantWrites {
				t.Fatalf("construction and later diagnosis not separated: result %+v, opens %d, requests %d, calls %q",
					res, opened, requests.Load(), store.calls)
			}
		})
	}
}
