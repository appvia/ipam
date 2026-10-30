// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/appvia/ipam"
	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/memory"
)

// harness runs the root command in-process against one backend URL. For
// mem:// every invocation shares one store, so command sequences behave like
// they would against a persistent backend.
type harness struct {
	t       *testing.T
	backend string
	env     map[string]string
	open    func(context.Context, string) (*ipam.Client, error)
}

type result struct {
	stdout string
	stderr string
	code   int
}

func newFileHarness(t *testing.T) *harness {
	t.Helper()

	return &harness{t: t, backend: fileURL(t.TempDir()), env: map[string]string{}, open: openClient}
}

func newMemHarness(t *testing.T) *harness {
	t.Helper()

	var (
		once  sync.Once
		store storage.Storage
		err   error
	)

	open := func(ctx context.Context, rawURL string) (*ipam.Client, error) {
		once.Do(func() { store, err = ipam.OpenStorage(ctx, rawURL) })

		if err != nil {
			return nil, err
		}

		return ipam.New(store), nil
	}

	return &harness{t: t, backend: "mem://", env: map[string]string{}, open: open}
}

func backends(t *testing.T) map[string]*harness {
	t.Helper()

	return map[string]*harness{"mem": newMemHarness(t), "file": newFileHarness(t)}
}

func (h *harness) run(args ...string) result {
	h.t.Helper()

	env := map[string]string{"IPAM_BACKEND": h.backend}
	for key, value := range h.env {
		env[key] = value
	}

	return h.runEnv(env, args...)
}

func (h *harness) runEnv(env map[string]string, args ...string) result {
	h.t.Helper()

	var stdout, stderr bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	code := run(ctx, args, &stdout, &stderr, func(key string) string { return env[key] }, h.open)

	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func (h *harness) ok(args ...string) string {
	h.t.Helper()

	res := h.run(args...)
	if res.code != exitOK {
		h.t.Fatalf("ipam %s: exit %d, stderr %q", strings.Join(args, " "), res.code, res.stderr)
	}

	return res.stdout
}

func decodeObject(t *testing.T, out string) map[string]any {
	t.Helper()

	var object map[string]any
	if err := json.Unmarshal([]byte(out), &object); err != nil {
		t.Fatalf("stdout is not a JSON object: %v\n%s", err, out)
	}

	return object
}

func decodeArray(t *testing.T, out string) []map[string]any {
	t.Helper()

	var array []map[string]any
	if err := json.Unmarshal([]byte(out), &array); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, out)
	}

	return array
}

func assertKeys(t *testing.T, object map[string]any, want ...string) {
	t.Helper()

	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}

	slices.Sort(got)
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("JSON keys = %v, want %v", got, want)
	}
}

var (
	poolKeys       = []string{"id", "family", "cidrs", "description", "labels"}
	childPoolKeys  = []string{"id", "family", "cidrs", "parent", "description", "labels"}
	allocationKeys = []string{"id", "pool_id", "cidr", "request", "description", "labels", "created_at"}
)

func TestEverySubcommandJSON(t *testing.T) {
	for name, h := range backends(t) {
		t.Run(name, func(t *testing.T) {
			pool := decodeObject(t, h.ok("pool", "create", "p", "--cidr", "10.0.0.0/16",
				"--description", "root", "--label", "env=dev", "-o", "json"))
			assertKeys(t, pool, poolKeys...)

			if pool["id"] != "p" || pool["family"] != float64(4) || pool["description"] != "root" {
				t.Fatalf("pool create = %v", pool)
			}

			if cidrs := pool["cidrs"].([]any); len(cidrs) != 1 || cidrs[0] != "10.0.0.0/16" {
				t.Fatalf("pool cidrs = %v", cidrs)
			}

			if labels := pool["labels"].(map[string]any); labels["env"] != "dev" {
				t.Fatalf("pool labels = %v", labels)
			}

			assertKeys(t, decodeObject(t, h.ok("pool", "get", "p", "-o", "json")), poolKeys...)

			allocation := decodeObject(t, h.ok("alloc", "create", "p", "--prefix-length", "24", "-o", "json"))
			assertKeys(t, allocation, allocationKeys...)

			if labels, ok := allocation["labels"].(map[string]any); !ok || len(labels) != 0 {
				t.Fatalf("unlabelled allocation labels = %#v, want {}", allocation["labels"])
			}

			if allocation["cidr"] != "10.0.0.0/24" || allocation["pool_id"] != "p" {
				t.Fatalf("alloc create = %v", allocation)
			}

			id, err := uuid.Parse(allocation["id"].(string))
			if err != nil || id.Version() != 7 || id.String() != allocation["id"] {
				t.Fatalf("alloc id %v is not a canonical UUIDv7 (%v)", allocation["id"], err)
			}

			request := allocation["request"].(map[string]any)
			if request["kind"] != "next" || request["prefix_length"] != float64(24) {
				t.Fatalf("alloc request = %v", request)
			}

			if _, err := time.Parse(time.RFC3339Nano, allocation["created_at"].(string)); err != nil {
				t.Fatalf("created_at: %v", err)
			}

			allocID := allocation["id"].(string)

			got := decodeObject(t, h.ok("alloc", "get", "p", allocID, "-o", "json"))
			assertKeys(t, got, allocationKeys...)

			if got["cidr"] != "10.0.0.0/24" {
				t.Fatalf("alloc get = %v", got)
			}

			explicit := decodeObject(t, h.ok("alloc", "create", "p", "--cidr", "10.0.9.0/24", "-o", "json"))
			if explicit["cidr"] != "10.0.9.0/24" || explicit["request"].(map[string]any)["kind"] != "explicit" {
				t.Fatalf("explicit alloc = %v", explicit)
			}

			updated := decodeObject(t, h.ok("alloc", "update", "p", allocID,
				"--description", "web", "--label", "team=net", "-o", "json"))
			assertKeys(t, updated, allocationKeys...)

			if updated["description"] != "web" || updated["cidr"] != "10.0.0.0/24" ||
				updated["labels"].(map[string]any)["team"] != "net" {
				t.Fatalf("alloc update = %v", updated)
			}

			// Labels only: the description is kept.
			labelsOnly := decodeObject(t, h.ok("alloc", "update", "p", allocID, "--label", "team=ops", "-o", "json"))
			if labelsOnly["description"] != "web" || labelsOnly["labels"].(map[string]any)["team"] != "ops" {
				t.Fatalf("alloc update labels only = %v", labelsOnly)
			}

			list := decodeArray(t, h.ok("alloc", "list", "p", "-o", "json"))
			if len(list) != 2 {
				t.Fatalf("alloc list = %v", list)
			}

			for _, item := range list {
				assertKeys(t, item, allocationKeys...)
			}

			child := decodeObject(t, h.ok("pool", "create", "c", "--parent", "p", "--prefix-length", "20", "-o", "json"))
			assertKeys(t, child, childPoolKeys...)

			parent := child["parent"].(map[string]any)
			assertKeys(t, parent, "pool_id", "allocation_id")

			if parent["pool_id"] != "p" || !strings.HasPrefix(parent["allocation_id"].(string), "pool:c:") {
				t.Fatalf("child parent = %v", parent)
			}

			if cidrs := child["cidrs"].([]any); len(cidrs) != 1 || cidrs[0] != "10.0.16.0/20" {
				t.Fatalf("child cidrs = %v", cidrs)
			}

			pinned := decodeObject(t, h.ok("pool", "create", "e", "--parent", "p", "--cidr", "10.0.128.0/24", "-o", "json"))
			if pinned["cidrs"].([]any)[0] != "10.0.128.0/24" {
				t.Fatalf("explicit child = %v", pinned)
			}

			pools := decodeArray(t, h.ok("pool", "list", "-o", "json"))
			if len(pools) != 3 {
				t.Fatalf("pool list = %v", pools)
			}

			poolUpdate := decodeObject(t, h.ok("pool", "update", "p", "--description", "renamed", "-o", "json"))
			assertKeys(t, poolUpdate, poolKeys...)

			if poolUpdate["description"] != "renamed" || poolUpdate["labels"].(map[string]any)["env"] != "dev" {
				t.Fatalf("pool update = %v", poolUpdate)
			}

			released := decodeObject(t, h.ok("alloc", "release", "p", allocID, "-o", "json"))
			assertKeys(t, released, "pool_id", "id", "released")

			if res := h.run("alloc", "get", "p", allocID); res.code != exitNotFound {
				t.Fatalf("alloc get after release: exit %d", res.code)
			}

			for _, id := range []string{"c", "e"} {
				deleted := decodeObject(t, h.ok("pool", "delete", id, "-o", "json"))
				assertKeys(t, deleted, "id", "deleted")
			}

			h.ok("alloc", "release", "p", explicit["id"].(string))
			h.ok("pool", "delete", "p")

			if pools := decodeArray(t, h.ok("pool", "list", "-o", "json")); len(pools) != 0 {
				t.Fatalf("pool list after delete = %v", pools)
			}

			if allocs := h.run("alloc", "list", "p", "-o", "json"); allocs.code != exitNotFound {
				t.Fatalf("alloc list of deleted pool: exit %d", allocs.code)
			}
		})
	}
}

func TestEmptyListsAreJSONArrays(t *testing.T) {
	h := newMemHarness(t)
	if out := strings.TrimSpace(h.ok("pool", "list", "-o", "json")); out != "[]" {
		t.Fatalf("empty pool list = %q", out)
	}

	h.ok("pool", "create", "p", "--cidr", "fd00::/48")

	if out := strings.TrimSpace(h.ok("alloc", "list", "p", "-o", "json")); out != "[]" {
		t.Fatalf("empty alloc list = %q", out)
	}
}

func TestTableOutput(t *testing.T) {
	h := newMemHarness(t)
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/16", "--cidr", "10.1.0.0/16", "--label", "env=dev")

	out := h.ok("pool", "list")
	if !strings.Contains(out, "ID") || !strings.Contains(out, "10.0.0.0/16,10.1.0.0/16") ||
		!strings.Contains(out, "env=dev") {
		t.Fatalf("pool list table:\n%s", out)
	}

	out = h.ok("alloc", "create", "p", "--prefix-length", "24", "--description", "web")
	if !strings.Contains(out, "10.0.0.0/24") || !strings.Contains(out, "CREATED_AT") {
		t.Fatalf("alloc create table:\n%s", out)
	}

	out = h.ok("alloc", "list", "p")
	for _, column := range []string{"ID", "CIDR", "CREATED_AT", "DESCRIPTION", "LABELS", "web"} {
		if !strings.Contains(out, column) {
			t.Fatalf("alloc list table lacks %q:\n%s", column, out)
		}
	}
}

func TestSuppliedIDIsIdempotent(t *testing.T) {
	h := newMemHarness(t)
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/16")

	id := ipam.NewAllocationID()

	first := decodeObject(t, h.ok("alloc", "create", "p", "--prefix-length", "24", "--id", id, "-o", "json"))
	second := decodeObject(t, h.ok("alloc", "create", "p", "--prefix-length", "24", "--id", id, "-o", "json"))

	if first["id"] != id || second["id"] != id || first["cidr"] != second["cidr"] {
		t.Fatalf("same id twice: %v then %v", first, second)
	}

	res := h.run("alloc", "create", "p", "--prefix-length", "25", "--id", id)
	if res.code != exitConflict || !strings.Contains(res.stderr, "id_reused") {
		t.Fatalf("id reuse: exit %d stderr %q", res.code, res.stderr)
	}
}

func TestExitCodes(t *testing.T) {
	h := newMemHarness(t)
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/24")
	allocID := decodeObject(t, h.ok("alloc", "create", "p", "--prefix-length", "25", "-o", "json"))["id"].(string)

	cases := []struct {
		name   string
		args   []string
		code   int
		stderr string
	}{
		{"pool not found", []string{"pool", "get", "missing"}, exitNotFound, "not found"},
		{"alloc not found", []string{"alloc", "get", "p", ipam.NewAllocationID()}, exitNotFound, "not found"},
		{"pool not empty", []string{"pool", "delete", "p"}, exitConflict, "pool_not_empty"},
		{"pool exists", []string{"pool", "create", "p", "--cidr", "10.9.0.0/24"}, exitConflict, "pool_exists"},
		{"overlap", []string{"alloc", "create", "p", "--cidr", "10.0.0.0/26"}, exitConflict, "overlap"},
		{"exhausted", []string{"alloc", "create", "p", "--prefix-length", "24"}, exitExhausted, "exhausted"},
		{"unknown command", []string{"bogus"}, exitUsage, ""},
		{"unknown subcommand", []string{"pool", "bogus"}, exitUsage, ""},
		{"unknown flag", []string{"pool", "list", "--bogus"}, exitUsage, ""},
		{"missing args", []string{"pool", "get"}, exitUsage, ""},
		{"extra args", []string{"pool", "list", "x"}, exitUsage, ""},
		{"bad output", []string{"pool", "list", "-o", "yaml"}, exitUsage, "output"},
		{"no cidr", []string{"pool", "create", "q"}, exitUsage, ""},
		{"cidr and parent prefix", []string{"pool", "create", "q", "--parent", "p", "--prefix-length", "26", "--cidr", "10.0.0.0/26"}, exitUsage, ""},
		{"prefix without parent", []string{"pool", "create", "q", "--prefix-length", "26"}, exitUsage, ""},
		{"root cidr with prefix", []string{"pool", "create", "q", "--cidr", "10.5.0.0/24", "--prefix-length", "26"}, exitUsage, ""},
		{"two child cidrs", []string{"pool", "create", "q", "--parent", "p", "--cidr", "10.0.0.128/26", "--cidr", "10.0.0.192/26"}, exitUsage, ""},
		{"alloc both", []string{"alloc", "create", "p", "--prefix-length", "26", "--cidr", "10.0.0.128/26"}, exitUsage, ""},
		{"alloc neither", []string{"alloc", "create", "p"}, exitUsage, ""},
		{"bad cidr", []string{"alloc", "create", "p", "--cidr", "10.0.0.300/26"}, exitUsage, "cidr"},
		{"bad label", []string{"alloc", "create", "p", "--prefix-length", "26", "--label", "novalue"}, exitUsage, "label"},
		{"duplicate label", []string{"pool", "update", "p", "--label", "a=1", "--label", "a=2"}, exitUsage, "label"},
		{"reserved id", []string{"alloc", "create", "p", "--prefix-length", "26", "--id", "pool:x"}, exitUsage, ""},
		{"update nothing", []string{"alloc", "update", "p", allocID}, exitUsage, ""},
		{"bad pool id", []string{"pool", "get", "Bad/ID"}, exitUsage, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := h.run(tc.args...)
			if res.code != tc.code {
				t.Fatalf("exit %d, want %d; stderr %q", res.code, tc.code, res.stderr)
			}

			if tc.code != exitOK && res.stderr == "" {
				t.Fatal("failure printed nothing on stderr")
			}

			if !strings.Contains(strings.ToLower(res.stderr), tc.stderr) {
				t.Fatalf("stderr %q lacks %q", res.stderr, tc.stderr)
			}
		})
	}
}

func TestBackendSelection(t *testing.T) {
	h := newFileHarness(t)

	res := h.runEnv(map[string]string{}, "pool", "list")
	if res.code != exitUsage || !strings.Contains(res.stderr, "IPAM_BACKEND") {
		t.Fatalf("missing backend: exit %d stderr %q", res.code, res.stderr)
	}

	res = h.runEnv(map[string]string{}, "pool", "list", "--backend", "ftp://x")
	if res.code != exitUsage {
		t.Fatalf("unsupported scheme: exit %d stderr %q", res.code, res.stderr)
	}

	res = h.runEnv(map[string]string{"IPAM_BACKEND": "ftp://x"}, "pool", "list", "--backend", h.backend, "-o", "json")
	if res.code != exitOK || strings.TrimSpace(res.stdout) != "[]" {
		t.Fatalf("flag over env: exit %d stdout %q stderr %q", res.code, res.stdout, res.stderr)
	}
}

func TestGenericErrorExitsOne(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "pools"), 0o755); err != nil {
		t.Fatal(err)
	}

	doc := `{"schema_version":2,"id":"p"}`
	if err := os.WriteFile(filepath.Join(dir, "pools", "p.json"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	h := &harness{t: t, backend: fileURL(dir), env: map[string]string{}, open: openClient}

	res := h.run("pool", "get", "p")
	if res.code != exitError || !strings.Contains(res.stderr, "schema") {
		t.Fatalf("unsupported schema: exit %d stderr %q", res.code, res.stderr)
	}
}

func TestVersionNeedsNoBackend(t *testing.T) {
	h := newMemHarness(t)

	res := h.runEnv(map[string]string{}, "version")
	if res.code != exitOK || strings.TrimSpace(res.stdout) != version {
		t.Fatalf("version: exit %d stdout %q stderr %q", res.code, res.stdout, res.stderr)
	}

	res = h.runEnv(map[string]string{}, "version", "-o", "json")
	if res.code != exitOK {
		t.Fatalf("version json: exit %d", res.code)
	}

	assertKeys(t, decodeObject(t, res.stdout), "version")
}

func TestHelpExitsZero(t *testing.T) {
	h := newMemHarness(t)

	for _, args := range [][]string{{"--help"}, {"pool"}, {"alloc", "release", "--help"}} {
		if res := h.runEnv(map[string]string{}, args...); res.code != exitOK || res.stdout == "" {
			t.Fatalf("ipam %v: exit %d stdout %q stderr %q", args, res.code, res.stdout, res.stderr)
		}
	}
}

func TestBackendSecretsAreNeverEchoed(t *testing.T) {
	const secret = "sEcReTvAlUe42"

	urls := []string{
		"s3://AKIAEXAMPLEKEY:" + secret + "@bucket/x?region=us-east-1",
		"s3://bucket/x?region=us-east-1&endpoint=https://user:" + secret + "@minio.example.com",
		"s3://bucket/x?region=us-east-1&aws_secret_access_key=" + secret,
		"azblob://acct.blob.core.windows.net/ctr/p?sv=2024-01-01&sig=" + secret,
		"azblob://ctr/p?AccountKey=" + secret,
		"file://user:" + secret + "@/tmp/ipam",
		"mem://?token=" + secret,
	}

	commands := [][]string{
		{"pool", "list"},
		{"pool", "get", "p"},
		{"alloc", "create", "p", "--prefix-length", "24"},
		{"doctor"},
	}

	h := newFileHarness(t)

	for _, raw := range urls {
		for _, args := range commands {
			viaEnv := h.runEnv(map[string]string{"IPAM_BACKEND": raw}, args...)
			viaFlag := h.runEnv(map[string]string{}, append(args, "--backend", raw)...)

			for source, res := range map[string]result{"IPAM_BACKEND": viaEnv, "--backend": viaFlag} {
				if res.code != exitUsage || res.stderr == "" {
					t.Fatalf("%s %v: exit %d stderr %q, want a usage error", source, args, res.code, res.stderr)
				}

				output := strings.ToLower(res.stdout + res.stderr)
				for _, leaked := range []string{strings.ToLower(secret), "akiaexamplekey"} {
					if strings.Contains(output, leaked) {
						t.Fatalf("%s %v leaked %q: stdout %q stderr %q", source, args, leaked, res.stdout, res.stderr)
					}
				}
			}
		}
	}
}

func TestLibraryValidationErrorsExitTwo(t *testing.T) {
	store := memory.New()
	client := ipam.New(store)
	open := func(context.Context, string) (*ipam.Client, error) { return client, nil }
	h := &harness{t: t, backend: "mem://", env: map[string]string{}, open: open}

	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/24")

	cases := []struct {
		name     string
		args     []string
		sentinel error
		library  func() error
	}{
		{
			"cidr outside the pool", []string{"alloc", "create", "p", "--cidr", "192.168.0.0/26"}, ipam.ErrInvalidCIDR,
			func() error {
				_, err := client.Allocate(t.Context(), "p",
					ipam.AllocationSpec{CIDR: netip.MustParsePrefix("192.168.0.0/26")})

				return err
			},
		},
		{
			"prefix length beyond the family", []string{"alloc", "create", "p", "--prefix-length", "33"}, ipam.ErrInvalidCIDR,
			func() error {
				_, err := client.Allocate(t.Context(), "p", ipam.AllocationSpec{PrefixLength: 33})

				return err
			},
		},
		{
			"allocation id is not a UUID", []string{"alloc", "create", "p", "--prefix-length", "26", "--id", "not-a-uuid"},
			ipam.ErrInvalidArgument,
			func() error {
				_, err := client.Allocate(t.Context(), "p", ipam.AllocationSpec{ID: "not-a-uuid", PrefixLength: 26})

				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The library itself must classify the input with this sentinel;
			// otherwise the CLI exit code below would prove nothing.
			if err := tc.library(); !errors.Is(err, tc.sentinel) {
				t.Fatalf("library error = %v, want %v", err, tc.sentinel)
			}

			res := h.run(tc.args...)
			if res.code != exitUsage {
				t.Fatalf("exit %d, want %d; stderr %q", res.code, exitUsage, res.stderr)
			}

			if !strings.Contains(res.stderr, "ipam: ") {
				t.Fatalf("stderr %q lacks the error", res.stderr)
			}
		})
	}
}

// fileURL turns a directory into a file URL that parses on every OS: on Windows
// C:\dir becomes file:///C:/dir.
func fileURL(dir string) string {
	path := filepath.ToSlash(dir)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return (&url.URL{Scheme: "file", Path: path}).String()
}
