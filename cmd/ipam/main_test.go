// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

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

const testUnverifiedBackend = "s3://fictional-bucket/private-prefix?endpoint=https://store.example.test&region=us-east-1"

// policyStore records operation contexts independently of the opening context.
type policyStore struct {
	storage.Storage
	t          *testing.T
	parent     context.Context
	operations int
}

func (s *policyStore) List(ctx context.Context, prefix string) ([]string, error) {
	if ctx != s.parent {
		s.t.Fatal("opening consent leaked into the operation context")
	}

	s.operations++

	return s.Storage.List(ctx, prefix)
}

type policyHarness struct {
	t      *testing.T
	parent context.Context
	store  *policyStore
	opened []context.Context
	urls   []string
	stderr io.Writer
}

func newPolicyHarness(t *testing.T) *policyHarness {
	t.Helper()

	parent, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	return &policyHarness{
		t: t, parent: parent,
		store: &policyStore{Storage: memory.New(), t: t, parent: parent},
	}
}

func (h *policyHarness) recordOpen(ctx context.Context, raw string) {
	h.opened = append(h.opened, ctx)
	h.urls = append(h.urls, raw)
}

func (h *policyHarness) run(env map[string]string, args ...string) result {
	h.t.Helper()

	var stdout, stderr bytes.Buffer

	errWriter := h.stderr
	if errWriter == nil {
		errWriter = &stderr
	}

	code := runCLI(h.parent, args, &stdout, errWriter, func(key string) string { return env[key] },
		func(ctx context.Context, raw string) (*ipam.Client, error) {
			h.recordOpen(ctx, raw)

			return ipam.New(h.store), nil
		}, func(ctx context.Context, raw string) (storage.Storage, error) {
			h.recordOpen(ctx, raw)

			return h.store, nil
		})

	return result{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func assertOpeningConsent(t *testing.T, h *policyHarness, want bool) {
	t.Helper()

	if len(h.opened) == 0 {
		t.Fatal("opener was not called")
	}

	expected := h.parent
	if want {
		expected = ipam.WithAllowUnverifiedBackend(h.parent)
	}

	for _, ctx := range h.opened {
		if !reflect.DeepEqual(ctx, expected) {
			t.Fatalf("opening context does not carry effective consent %v", want)
		}
	}
}

func assertSupportWarning(t *testing.T, res result, count int) {
	t.Helper()

	if got := strings.Count(res.stderr, "ipam: warning: unverified backend;"); got != count {
		t.Fatalf("support-risk warnings = %d, want %d; stderr %q", got, count, res.stderr)
	}

	if count > 0 && (strings.Count(res.stderr, "\n") != count || !strings.HasSuffix(res.stderr, "\n")) {
		t.Fatalf("warning is not exactly one newline-terminated line: %q", res.stderr)
	}
}

func TestCLIUnverifiedFlagConsent(t *testing.T) {
	for _, command := range []string{"pool", "doctor"} {
		for _, selector := range []string{"flag", "environment"} {
			for _, value := range []string{"", "false", "private-malformed-consent"} {
				t.Run("AC-9/"+command+"/"+selector+"/"+value, func(t *testing.T) {
					h := newPolicyHarness(t)
					env := map[string]string{"IPAM_ALLOW_UNVERIFIED_BACKEND": value, "IPAM_BACKEND": testUnverifiedBackend}

					args := []string{command}
					if command == "pool" {
						args = append(args, "list")
					}

					args = append(args, "--allow-unverified-backend", "-o", "json")

					if selector == "flag" {
						env["IPAM_BACKEND"] = "invalid-environment-backend"

						args = append(args, "--backend", testUnverifiedBackend)
					}

					res := h.run(env, args...)
					if res.code != exitOK || h.store.operations == 0 || !slices.Equal(h.urls, []string{testUnverifiedBackend}) {
						t.Fatalf("command did not operate on selected backend: %+v, URLs %v", res, h.urls)
					}

					assertOpeningConsent(t, h, true)
					assertSupportWarning(t, res, 1)
				})
			}
		}
	}
}

func TestCLIUnverifiedEnvironmentConsent(t *testing.T) {
	for _, selector := range []string{"flag", "environment"} {
		for _, value := range []string{"1", "t", "T", "TRUE", "true", "True"} {
			t.Run("AC-10/"+selector+"/"+value, func(t *testing.T) {
				h := newPolicyHarness(t)
				env := map[string]string{"IPAM_BACKEND": testUnverifiedBackend, "IPAM_ALLOW_UNVERIFIED_BACKEND": value}

				args := []string{"pool", "list", "-o", "json"}
				if selector == "flag" {
					args = append(args, "--backend", testUnverifiedBackend)
				}

				res := h.run(env, args...)
				if res.code != exitOK || h.store.operations == 0 {
					t.Fatalf("command did not operate: %+v", res)
				}

				decodeArray(t, res.stdout)
				assertOpeningConsent(t, h, true)
				assertSupportWarning(t, res, 1)
			})
		}
	}
}

func TestCLIExplicitFalseOverridesEnvironment(t *testing.T) {
	for _, command := range []string{"pool", "doctor"} {
		for _, value := range []string{"true", "private-malformed-consent"} {
			t.Run(command+"/"+value, func(t *testing.T) {
				h := newPolicyHarness(t)

				args := []string{command}
				if command == "pool" {
					args = append(args, "list")
				}

				res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend, "IPAM_ALLOW_UNVERIFIED_BACKEND": value},
					append(args, "--allow-unverified-backend=false")...)
				if res.code != exitOK {
					t.Fatalf("pre-enforcement command: %+v", res)
				}

				assertOpeningConsent(t, h, false)
				assertSupportWarning(t, res, 0)
			})
		}
	}
}

func TestCLIConsentDefaultsClosed(t *testing.T) {
	for _, command := range []string{"pool", "doctor"} {
		for _, value := range []string{"absent", "", "0", "f", "F", "FALSE", "false", "False", "private-malformed-consent"} {
			t.Run(command+"/"+value, func(t *testing.T) {
				h := newPolicyHarness(t)

				args := []string{command}
				if command == "pool" {
					args = append(args, "list")
				}

				env := map[string]string{"IPAM_BACKEND": testUnverifiedBackend}
				if value != "absent" {
					env["IPAM_ALLOW_UNVERIFIED_BACKEND"] = value
				}

				res := h.run(env, args...)
				if value == "private-malformed-consent" {
					if res.code != exitUsage || len(h.opened) != 0 || h.store.operations != 0 || strings.Contains(res.stderr, value) {
						t.Fatalf("malformed consent: %+v", res)
					}
				} else {
					if res.code != exitOK {
						t.Fatalf("pre-enforcement command: %+v", res)
					}

					assertOpeningConsent(t, h, false)
				}

				assertSupportWarning(t, res, 0)
			})
		}
	}
}

func TestCLIUnverifiedWarningOncePerInvocation(t *testing.T) {
	t.Run("AC-12/independent invocations and JSON", func(t *testing.T) {
		h := newPolicyHarness(t)
		for range 2 {
			res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend}, "pool", "list", "--allow-unverified-backend", "-o", "json")
			if res.code != exitOK {
				t.Fatalf("command: %+v", res)
			}

			decodeArray(t, res.stdout)
			assertSupportWarning(t, res, 1)
		}
	})
	t.Run("AC-12/repeated openings", func(t *testing.T) {
		var stdout, stderr bytes.Buffer

		c := &cli{stdout: &stdout, getenv: func(string) string { return "" }, open: func(context.Context, string) (*ipam.Client, error) {
			if strings.Count(stderr.String(), "ipam: warning: unverified backend;") != 1 {
				t.Fatal("opener ran before the support-risk warning was written")
			}

			return ipam.New(memory.New()), nil
		}}
		defer c.closeAll()

		root := c.rootCommand()
		root.SetErr(&stderr)
		root.AddCommand(&cobra.Command{Use: "repeat", RunE: c.leaf(func(cmd *cobra.Command, _ []string) error {
			for range 3 {
				if _, err := c.client(cmd); err != nil {
					return err
				}
			}

			return writeJSON(c.stdout, map[string]bool{"ok": true})
		})})
		root.SetArgs([]string{"repeat", "--backend", testUnverifiedBackend, "--allow-unverified-backend"})

		if err := root.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}

		decodeObject(t, stdout.String())
		assertSupportWarning(t, result{stderr: stderr.String()}, 1)
	})
	t.Run("AC-12/verified silence and AC-18/shared classifier", func(t *testing.T) {
		for _, backend := range []string{"mem://", "file:///fictional", "s3://fictional-bucket", "s3://fictional-bucket?endpoint=https://s3.amazonaws.com", "azblob://fictional.blob.core.windows.net/container", "azblob://container/prefix", "s3://fictional-bucket?endpoint=https://amazonaws.com.example.test"} {
			h := newPolicyHarness(t)

			classification, err := ipam.ClassifyBackend(backend)
			if err != nil {
				t.Fatal(err)
			}

			for _, consent := range []string{"false", "true", "true", "false"} {
				res := h.run(map[string]string{"IPAM_BACKEND": backend, "IPAM_ALLOW_UNVERIFIED_BACKEND": consent}, "pool", "list")
				if res.code != exitOK {
					t.Fatalf("command: %+v", res)
				}

				want := 0
				if classification == ipam.BackendUnverified && consent == "true" {
					want = 1
				}

				assertSupportWarning(t, res, want)

				after, err := ipam.ClassifyBackend(backend)
				if err != nil || after != classification {
					t.Fatalf("consent or operation changed classification: %v, %v", after, err)
				}
			}
		}
	})
	t.Run("AC-12/warning writer failure", func(t *testing.T) {
		for _, args := range [][]string{{"pool", "list", "--allow-unverified-backend"}, {"doctor", "--allow-unverified-backend"}} {
			h := newPolicyHarness(t)
			h.stderr = failingPolicyWriter{}

			res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend}, args...)
			if res.code != exitError || h.store.operations != 0 || len(h.opened) != 0 || res.stdout != "" {
				t.Fatalf("writer failure allowed opening or operations: %+v", res)
			}
		}
	})
	t.Run("AC-12/failed writes do not record success", func(t *testing.T) {
		var c cli

		if err := c.warnUnverified(failingPolicyWriter{}); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("warning error = %v, want closed pipe", err)
		}

		if c.warnedUnverified {
			t.Fatal("failed warning write marked as successful")
		}

		var stderr bytes.Buffer

		if err := c.warnUnverified(&stderr); err != nil {
			t.Fatal(err)
		}

		assertSupportWarning(t, result{stderr: stderr.String()}, 1)
	})
	t.Run("AC-12/short warning write", func(t *testing.T) {
		var c cli

		if err := c.warnUnverified(shortPolicyWriter{}); !errors.Is(err, io.ErrShortWrite) || c.warnedUnverified {
			t.Fatalf("short warning write recorded success or lost its error: %v", err)
		}
	})
	t.Run("AC-12/unrelated warnings remain visible", func(t *testing.T) {
		h := newPolicyHarness(t)
		if _, err := h.store.CreateIfAbsent(h.parent, "pools/fictional-pool.json", []byte(`{"schema_version":99,"id":"fictional-pool"}`)); err != nil {
			t.Fatal(err)
		}

		res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend}, "pool", "list", "--allow-unverified-backend", "-o", "json")
		if res.code != exitOK || strings.Count(res.stderr, "ipam: warning: unverified backend;") != 1 ||
			!strings.Contains(res.stderr, "ipam: warning: skipped pools with an unsupported schema") {
			t.Fatalf("unrelated warning was suppressed: %+v", res)
		}

		decodeArray(t, res.stdout)
	})
	t.Run("AC-12/operation errors remain visible", func(t *testing.T) {
		h := newPolicyHarness(t)

		res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend}, "pool", "get", "fictional-pool", "--allow-unverified-backend")
		if res.code != exitNotFound || !strings.Contains(res.stderr, "not found") ||
			strings.Count(res.stderr, "ipam: warning: unverified backend;") != 1 {
			t.Fatalf("operation error or warning missing: %+v", res)
		}
	})
}

type failingPolicyWriter struct{}

func (failingPolicyWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}

type shortPolicyWriter struct{}

func (shortPolicyWriter) Write(p []byte) (int, error) {
	return len(p) - 1, nil
}

func TestBackendPolicyDiagnosticsAreRedacted(t *testing.T) {
	t.Run("AC-24/malformed flag", func(t *testing.T) {
		h := newPolicyHarness(t)

		res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend}, "pool", "list", "--allow-unverified-backend=private-consent-marker")
		if res.code != exitUsage || len(h.opened) != 0 || h.store.operations != 0 ||
			strings.Contains(res.stderr, "private-consent-marker") || !strings.Contains(res.stderr, "--allow-unverified-backend") {
			t.Fatalf("consent flag diagnostic is not static and redacted: %+v", res)
		}
	})

	for _, consent := range []string{"true", "private-consent-marker"} {
		t.Run("AC-24/"+consent, func(t *testing.T) {
			h := newPolicyHarness(t)
			backend := "s3://private-bucket-marker/private-prefix-marker?endpoint=https://private-endpoint-marker.example.test"

			res := h.run(map[string]string{"IPAM_BACKEND": backend, "IPAM_ALLOW_UNVERIFIED_BACKEND": consent}, "pool", "list")
			for _, marker := range []string{backend, "private-bucket-marker", "private-prefix-marker", "private-endpoint-marker", "private-consent-marker"} {
				if strings.Contains(res.stderr+res.stdout, marker) {
					t.Fatalf("diagnostic disclosed %q", marker)
				}
			}

			if consent == "true" {
				assertSupportWarning(t, res, 1)
			} else if res.code != exitUsage || !strings.Contains(res.stderr, "IPAM_ALLOW_UNVERIFIED_BACKEND") {
				t.Fatalf("missing static consent usage diagnostic: %+v", res)
			}
		})
	}
}

func TestBackendSupportLinks(t *testing.T) {
	t.Run("AC-21/AC-12/static warning", func(t *testing.T) {
		h := newPolicyHarness(t)

		res := h.run(map[string]string{"IPAM_BACKEND": testUnverifiedBackend}, "pool", "list", "--allow-unverified-backend")
		for _, text := range []string{"conditional creation", "replacement", "deletion", "S3", "PutObject", "DeleteObject", "If-None-Match: *", "If-Match: <etag>", "corruption", "opening", "--allow-unverified-backend", "IPAM_ALLOW_UNVERIFIED_BACKEND=true", "https://github.com/appvia/ipam#supported-backends"} {
			if !strings.Contains(res.stderr, text) {
				t.Errorf("warning missing %q: %q", text, res.stderr)
			}
		}
	})
}

func TestConsentNeverBypassesURLValidation(t *testing.T) {
	for _, backend := range []string{"s3://private-user:private-password@fictional-bucket", "mem://#private-fragment", "s3://fictional-bucket/a/../b", "s3://fictional-bucket?unknown=private-query", "s3://fictional-bucket?endpoint=ftp://private-endpoint"} {
		for _, command := range []string{"pool", "doctor"} {
			t.Run("AC-23/"+command+"/"+backend, func(t *testing.T) {
				h := newPolicyHarness(t)

				args := []string{command}
				if command == "pool" {
					args = append(args, "list")
				}

				res := h.run(map[string]string{"IPAM_BACKEND": backend}, append(args, "--allow-unverified-backend")...)
				if res.code != exitUsage || len(h.opened) != 0 || h.store.operations != 0 || !strings.Contains(res.stderr, "invalid argument") {
					t.Fatalf("invalid URL reached opener or lost category: %+v", res)
				}

				if strings.Contains(res.stderr, "private-") {
					t.Fatalf("AC-24: invalid URL diagnostic leaked a private marker: %+v", res)
				}
			})
		}
	}

	for _, args := range [][]string{{"--help"}, {"doctor", "--help"}, {"version"}} {
		h := newPolicyHarness(t)

		res := h.run(map[string]string{"IPAM_BACKEND": "invalid", "IPAM_ALLOW_UNVERIFIED_BACKEND": "private-malformed-consent"}, args...)
		if res.code != exitOK || len(h.opened) != 0 || res.stderr != "" {
			t.Fatalf("help/version required backend or consent: %+v", res)
		}
	}
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
	poolKeys       = []string{"id", "family", "cidrs", "description", "labels", "strategy"}
	childPoolKeys  = []string{"id", "family", "cidrs", "parent", "description", "labels", "strategy"}
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

	doc := `{"schema_version":3,"id":"p"}`
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

	allocation, err := client.Allocate(t.Context(), "p", ipam.AllocationSpec{PrefixLength: 26})
	if err != nil {
		t.Fatal(err)
	}

	longDescription := strings.Repeat("a", 1025)
	longLabelValue := strings.Repeat("v", 64)
	controlDescription := "a\x01b"

	cases := []struct {
		name     string
		args     []string
		sentinel error
		rejected string
		library  func() error
	}{
		{
			"cidr outside the pool", []string{"alloc", "create", "p", "--cidr", "192.168.0.0/26"}, ipam.ErrInvalidCIDR,
			"",
			func() error {
				_, err := client.Allocate(t.Context(), "p",
					ipam.AllocationSpec{CIDR: netip.MustParsePrefix("192.168.0.0/26")})

				return err
			},
		},
		{
			"prefix length beyond the family", []string{"alloc", "create", "p", "--prefix-length", "33"}, ipam.ErrInvalidCIDR,
			"",
			func() error {
				_, err := client.Allocate(t.Context(), "p", ipam.AllocationSpec{PrefixLength: 33})

				return err
			},
		},
		{
			"allocation id is not a UUID", []string{"alloc", "create", "p", "--prefix-length", "26", "--id", "not-a-uuid"},
			ipam.ErrInvalidArgument, "",
			func() error {
				_, err := client.Allocate(t.Context(), "p", ipam.AllocationSpec{ID: "not-a-uuid", PrefixLength: 26})

				return err
			},
		},
		{
			"AC-5 pool create rejects oversized description",
			[]string{"pool", "create", "q", "--cidr", "10.1.0.0/24", "--description", longDescription},
			ipam.ErrInvalidArgument, longDescription,
			func() error {
				_, err := client.CreatePool(t.Context(), ipam.PoolSpec{
					ID: "q", CIDRs: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/24")}, Description: longDescription,
				})

				return err
			},
		},
		{
			"AC-5 alloc create rejects invalid label key",
			[]string{"alloc", "create", "p", "--prefix-length", "26", "--label", "-bad=v"},
			ipam.ErrInvalidArgument, "-bad",
			func() error {
				_, err := client.Allocate(t.Context(), "p", ipam.AllocationSpec{
					PrefixLength: 26, Labels: map[string]string{"-bad": "v"},
				})

				return err
			},
		},
		{
			"AC-5 pool update rejects oversized label value",
			[]string{"pool", "update", "p", "--label", "k=" + longLabelValue},
			ipam.ErrInvalidArgument, longLabelValue,
			func() error {
				return client.UpdatePoolMetadata(t.Context(), "p", "", map[string]string{"k": longLabelValue})
			},
		},
		{
			"AC-5 alloc update rejects control character description",
			[]string{"alloc", "update", "p", allocation.ID, "--description", controlDescription},
			ipam.ErrInvalidArgument, controlDescription,
			func() error {
				return client.UpdateAllocationMetadata(t.Context(), "p", allocation.ID, controlDescription, nil)
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

			if tc.rejected != "" && strings.Contains(res.stderr, tc.rejected) {
				t.Fatal("stderr contains rejected metadata")
			}
		})
	}

	t.Run("AC-5 valid label exits zero", func(t *testing.T) {
		res := h.run("pool", "update", "p", "--label", "team=a")
		if res.code != exitOK {
			t.Fatalf("exit %d, want %d; stderr %q", res.code, exitOK, res.stderr)
		}
	})
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
