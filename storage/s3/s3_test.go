// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package s3_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/appvia/ipam/storage"
	s3store "github.com/appvia/ipam/storage/s3"
)

const testBucket = "bucket"

func TestMain(m *testing.M) {
	// Isolate the SDK from the developer's AWS configuration: static test
	// credentials from the environment, no shared config files and no IMDS.
	// An integration run (build tag + IPAM_TEST_S3_* set) keeps the caller's
	// credentials, which the fake servers accept as well.
	if os.Getenv("IPAM_TEST_S3_ENDPOINT") != "" || os.Getenv("IPAM_TEST_S3_BUCKET") != "" {
		os.Exit(m.Run())
	}

	for key, value := range map[string]string{
		"AWS_ACCESS_KEY_ID":           "test",
		"AWS_SECRET_ACCESS_KEY":       "test",
		"AWS_SESSION_TOKEN":           "",
		"AWS_PROFILE":                 "",
		"AWS_CONFIG_FILE":             os.DevNull,
		"AWS_SHARED_CREDENTIALS_FILE": os.DevNull,
		"AWS_EC2_METADATA_DISABLED":   "true",
	} {
		if err := os.Setenv(key, value); err != nil {
			panic(err)
		}
	}

	os.Exit(m.Run())
}

// recorded is one request seen by the fake S3 server.
type recorded struct {
	Method string
	Path   string
	Query  string
	Header http.Header
	Body   string
}

// fakeS3 records every request and answers it with respond.
type fakeS3 struct {
	mu       sync.Mutex
	requests []recorded
	respond  func(w http.ResponseWriter, r *http.Request, n int)
	server   *httptest.Server
}

func newFake(t *testing.T, respond func(w http.ResponseWriter, r *http.Request, n int)) *fakeS3 {
	t.Helper()

	f := &fakeS3{respond: respond}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		f.mu.Lock()
		f.requests = append(f.requests, recorded{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Header: r.Header.Clone(),
			Body:   string(body),
		})
		n := len(f.requests)
		f.mu.Unlock()

		f.respond(w, r, n)
	}))
	t.Cleanup(f.server.Close)

	return f
}

func (f *fakeS3) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.requests)
}

func (f *fakeS3) request(t *testing.T, i int) recorded {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	if i >= len(f.requests) {
		t.Fatalf("request %d not recorded; got %d requests", i, len(f.requests))
	}

	return f.requests[i]
}

func newStore(t *testing.T, endpoint, prefix string) storage.Storage {
	t.Helper()

	store, err := s3store.New(t.Context(), s3store.Config{
		Bucket:       testBucket,
		Prefix:       prefix,
		Region:       "us-east-1",
		Endpoint:     endpoint,
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return store
}

func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w,
		`<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code><Message>%s</Message><RequestId>r</RequestId></Error>`,
		code, code)
}

func ok(etag string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, r *http.Request, _ int) {
		if etag != "" {
			w.Header().Set("ETag", etag)
		}

		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		w.WriteHeader(http.StatusOK)

		if r.Method == http.MethodGet {
			_, _ = io.WriteString(w, "payload")
		}
	}
}

func status(code int, errCode string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		writeError(w, code, errCode)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	t.Parallel()

	if _, err := s3store.New(t.Context(), s3store.Config{Region: "us-east-1"}); err == nil {
		t.Fatal("New() without a bucket succeeded, want an error")
	}
}

func TestConditionalHeaders(t *testing.T) {
	t.Parallel()

	const etag = `"abc123"`

	tests := []struct {
		name       string
		call       func(ctx context.Context, s storage.Storage) error
		method     string
		wantHeader map[string]string
		noHeaders  []string
		wantBody   string
	}{
		{
			name: "create sends If-None-Match star",
			call: func(ctx context.Context, s storage.Storage) error {
				v, err := s.CreateIfAbsent(ctx, "pools/a.json", []byte("data"))
				if err == nil && v != storage.Version(etag) {
					return fmt.Errorf("version = %q, want %q", v, etag)
				}

				return err
			},
			method:     http.MethodPut,
			wantHeader: map[string]string{"If-None-Match": "*"},
			noHeaders:  []string{"If-Match"},
			wantBody:   "data",
		},
		{
			name: "put sends If-Match with the version verbatim",
			call: func(ctx context.Context, s storage.Storage) error {
				v, err := s.PutIfMatch(ctx, "pools/a.json", []byte("next"), storage.Version(`"old"`))
				if err == nil && v != storage.Version(etag) {
					return fmt.Errorf("version = %q, want %q", v, etag)
				}

				return err
			},
			method:     http.MethodPut,
			wantHeader: map[string]string{"If-Match": `"old"`},
			noHeaders:  []string{"If-None-Match"},
			wantBody:   "next",
		},
		{
			name: "delete sends If-Match with the version verbatim",
			call: func(ctx context.Context, s storage.Storage) error {
				return s.DeleteIfMatch(ctx, "pools/a.json", storage.Version(`"old"`))
			},
			method:     http.MethodDelete,
			wantHeader: map[string]string{"If-Match": `"old"`},
			noHeaders:  []string{"If-None-Match"},
		},
		{
			name: "get sends no conditional headers and returns the ETag verbatim",
			call: func(ctx context.Context, s storage.Storage) error {
				data, v, err := s.Get(ctx, "pools/a.json")
				if err != nil {
					return err
				}

				if string(data) != "payload" || v != storage.Version(etag) {
					return fmt.Errorf("Get() = %q, %q; want payload, %q", data, v, etag)
				}

				return nil
			},
			method:    http.MethodGet,
			noHeaders: []string{"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, ok(etag))
			store := newStore(t, fake.server.URL, "root")

			if err := tt.call(t.Context(), store); err != nil {
				t.Fatalf("call error = %v", err)
			}

			if got := fake.count(); got != 1 {
				t.Fatalf("requests = %d, want 1", got)
			}

			req := fake.request(t, 0)
			if req.Method != tt.method {
				t.Fatalf("method = %s, want %s", req.Method, tt.method)
			}

			if want := "/" + testBucket + "/root/pools/a.json"; req.Path != want {
				t.Fatalf("path = %s, want %s (keys stored under the prefix)", req.Path, want)
			}

			for name, want := range tt.wantHeader {
				if got := req.Header.Get(name); got != want {
					t.Fatalf("header %s = %q, want %q", name, got, want)
				}
			}

			for _, name := range tt.noHeaders {
				if got, present := req.Header[http.CanonicalHeaderKey(name)]; present {
					t.Fatalf("header %s = %q, want absent", name, got)
				}
			}

			if strings.Contains(req.Query, "uploads") || req.Header.Get("x-amz-copy-source") != "" {
				t.Fatalf("unexpected multipart or copy request: %s %s", req.Query, req.Header)
			}

			if tt.wantBody != "" && !strings.Contains(req.Body, tt.wantBody) {
				t.Fatalf("body = %q, want it to contain %q", req.Body, tt.wantBody)
			}
		})
	}
}

// operation invokes one Storage method against store.
type operation struct {
	name string
	call func(ctx context.Context, s storage.Storage) error
}

var (
	opGet = operation{"Get", func(ctx context.Context, s storage.Storage) error {
		_, _, err := s.Get(ctx, "k")
		return err
	}}
	opCreate = operation{"CreateIfAbsent", func(ctx context.Context, s storage.Storage) error {
		_, err := s.CreateIfAbsent(ctx, "k", []byte("d"))
		return err
	}}
	opPut = operation{"PutIfMatch", func(ctx context.Context, s storage.Storage) error {
		_, err := s.PutIfMatch(ctx, "k", []byte("d"), storage.Version(`"v"`))
		return err
	}}
	opDelete = operation{"DeleteIfMatch", func(ctx context.Context, s storage.Storage) error {
		return s.DeleteIfMatch(ctx, "k", storage.Version(`"v"`))
	}}
	opList = operation{"List", func(ctx context.Context, s storage.Storage) error {
		_, err := s.List(ctx, "pools/")
		return err
	}}
)

func TestStatusMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		status    int
		code      string
		ops       []operation
		want      error
		wantCount int
	}{
		{"412 PreconditionFailed is not retried", 412, "PreconditionFailed",
			[]operation{opCreate, opPut, opDelete}, storage.ErrPreconditionFailed, 1},
		{"409 ConditionalRequestConflict", 409, "ConditionalRequestConflict",
			[]operation{opCreate, opPut, opDelete}, storage.ErrPreconditionFailed, 1},
		{"404 NoSuchKey", 404, "NoSuchKey",
			[]operation{opGet, opPut, opDelete}, storage.ErrNotFound, 1},
		{"403 AccessDenied fails immediately without the propagation flag", 403, "AccessDenied",
			[]operation{opGet, opCreate, opPut, opDelete, opList}, storage.ErrAccessDenied, 1},
	}

	for _, tt := range tests {
		for _, op := range tt.ops {
			t.Run(tt.name+"/"+op.name, func(t *testing.T) {
				t.Parallel()

				fake := newFake(t, status(tt.status, tt.code))
				store := newStore(t, fake.server.URL, "")

				err := op.call(t.Context(), store)

				if !errors.Is(err, tt.want) {
					t.Fatalf("error = %v, want errors.Is(_, %v)", err, tt.want)
				}

				if errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, must not be an unknown outcome", err)
				}

				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != tt.code {
					t.Fatalf("error = %v, want the SDK error with code %s kept in the chain", err, tt.code)
				}

				if got := fake.count(); got != tt.wantCount {
					t.Fatalf("requests = %d, want %d", got, tt.wantCount)
				}
			})
		}
	}
}

func TestMissingBucketIsPropagatedUnchanged(t *testing.T) {
	t.Parallel()

	for _, op := range []operation{opGet, opCreate, opPut, opDelete, opList} {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, status(http.StatusNotFound, "NoSuchBucket"))
			store := newStore(t, fake.server.URL, "")

			err := op.call(t.Context(), store)

			for _, sentinel := range []error{
				storage.ErrNotFound, storage.ErrPreconditionFailed,
				storage.ErrAccessDenied, storage.ErrUnknownOutcome,
			} {
				if errors.Is(err, sentinel) {
					t.Fatalf("error = %v, must not map to %v", err, sentinel)
				}
			}

			var noBucket *types.NoSuchBucket

			var apiErr smithy.APIError
			if !errors.As(err, &noBucket) && (!errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchBucket") {
				t.Fatalf("error = %v, want the NoSuchBucket SDK error", err)
			}
		})
	}
}

func TestAccessPropagationRetry(t *testing.T) {
	t.Parallel()

	// 403, 403, then success.
	respond := func(etag string) func(http.ResponseWriter, *http.Request, int) {
		return func(w http.ResponseWriter, r *http.Request, n int) {
			if n <= 2 {
				writeError(w, http.StatusForbidden, "AccessDenied")
				return
			}

			ok(etag)(w, r, n)
		}
	}

	for _, op := range []operation{opGet, opCreate, opPut, opDelete} {
		t.Run(op.name+"/with flag succeeds after 3 requests", func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, respond(`"e"`))
			store := newStore(t, fake.server.URL, "")

			err := op.call(storage.WithAccessPropagationRetry(t.Context()), store)
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}

			if got := fake.count(); got != 3 {
				t.Fatalf("requests = %d, want 3", got)
			}
		})

		t.Run(op.name+"/without flag fails after 1 request", func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, respond(`"e"`))
			store := newStore(t, fake.server.URL, "")

			err := op.call(t.Context(), store)
			if !errors.Is(err, storage.ErrAccessDenied) {
				t.Fatalf("error = %v, want %v", err, storage.ErrAccessDenied)
			}

			if got := fake.count(); got != 1 {
				t.Fatalf("requests = %d, want 1", got)
			}
		})
	}

	t.Run("with flag still does not retry 412", func(t *testing.T) {
		t.Parallel()

		fake := newFake(t, status(http.StatusPreconditionFailed, "PreconditionFailed"))
		store := newStore(t, fake.server.URL, "")

		err := opPut.call(storage.WithAccessPropagationRetry(t.Context()), store)
		if !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Fatalf("error = %v, want %v", err, storage.ErrPreconditionFailed)
		}

		if got := fake.count(); got != 1 {
			t.Fatalf("requests = %d, want 1", got)
		}
	})
}

func TestUnknownOutcome(t *testing.T) {
	t.Parallel()

	writes := []operation{opCreate, opPut, opDelete}

	t.Run("server hangs past the client timeout", func(t *testing.T) {
		t.Parallel()

		for _, op := range writes {
			t.Run(op.name, func(t *testing.T) {
				t.Parallel()

				release := make(chan struct{})
				fake := newFake(t, func(http.ResponseWriter, *http.Request, int) { <-release })
				t.Cleanup(func() { close(release) })

				store := newStore(t, fake.server.URL, "")
				ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)

				defer cancel()

				err := op.call(ctx, store)
				if !errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, want %v", err, storage.ErrUnknownOutcome)
				}

				if fake.count() == 0 {
					t.Fatal("the request never reached the server")
				}
			})
		}
	})

	t.Run("connection reset after the request was sent", func(t *testing.T) {
		t.Parallel()

		for _, op := range writes {
			t.Run(op.name, func(t *testing.T) {
				t.Parallel()

				fake := newFake(t, func(w http.ResponseWriter, _ *http.Request, _ int) {
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						panic(err)
					}

					if tcp, isTCP := conn.(*net.TCPConn); isTCP {
						_ = tcp.SetLinger(0) // RST instead of FIN
					}

					_ = conn.Close()
				})
				store := newStore(t, fake.server.URL, "")

				err := op.call(t.Context(), store)
				if !errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, want %v", err, storage.ErrUnknownOutcome)
				}
			})
		}
	})

	t.Run("final 5xx after SDK retries", func(t *testing.T) {
		t.Parallel()

		for _, op := range writes {
			t.Run(op.name, func(t *testing.T) {
				t.Parallel()

				fake := newFake(t, status(http.StatusInternalServerError, "InternalError"))
				store := newStore(t, fake.server.URL, "")

				err := op.call(t.Context(), store)
				if !errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, want %v", err, storage.ErrUnknownOutcome)
				}

				if got := fake.count(); got < 2 {
					t.Fatalf("requests = %d, want the SDK default retries (>1) before giving up", got)
				}
			})
		}
	})

	t.Run("reads are never unknown outcomes", func(t *testing.T) {
		t.Parallel()

		fake := newFake(t, status(http.StatusInternalServerError, "InternalError"))
		store := newStore(t, fake.server.URL, "")

		if err := opGet.call(t.Context(), store); err == nil || errors.Is(err, storage.ErrUnknownOutcome) {
			t.Fatalf("Get() error = %v, want a plain error", err)
		}
	})

	t.Run("errors before sending are not unknown outcomes", func(t *testing.T) {
		t.Parallel()

		// A listener that is closed straight away: connections are refused.
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		refused := "http://" + listener.Addr().String()
		_ = listener.Close()

		endpoints := map[string]string{
			"connection refused": refused,
			"dns failure":        "http://ipam-test-host.invalid",
		}

		for name, endpoint := range endpoints {
			for _, op := range writes {
				t.Run(name+"/"+op.name, func(t *testing.T) {
					t.Parallel()

					store := newStore(t, endpoint, "")

					err := op.call(t.Context(), store)
					if err == nil {
						t.Fatal("error = nil, want a send failure")
					}

					if errors.Is(err, storage.ErrUnknownOutcome) {
						t.Fatalf("error = %v, must not be an unknown outcome", err)
					}
				})
			}
		}

		t.Run("empty version is rejected without sending", func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, ok(`"e"`))
			store := newStore(t, fake.server.URL, "")

			_, putErr := store.PutIfMatch(t.Context(), "k", []byte("d"), "")
			delErr := store.DeleteIfMatch(t.Context(), "k", "")

			for _, err := range []error{putErr, delErr} {
				if !errors.Is(err, storage.ErrPreconditionFailed) || errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, want %v", err, storage.ErrPreconditionFailed)
				}
			}

			if got := fake.count(); got != 0 {
				t.Fatalf("requests = %d, want 0 (an empty If-Match would be an unconditional write)", got)
			}
		})
	})
}

func TestMissingETag(t *testing.T) {
	t.Parallel()

	t.Run("get without an ETag is an error", func(t *testing.T) {
		t.Parallel()

		fake := newFake(t, ok(""))
		store := newStore(t, fake.server.URL, "")

		_, _, err := store.Get(t.Context(), "k")
		if err == nil || errors.Is(err, storage.ErrNotFound) {
			t.Fatalf("Get() error = %v, want a missing-ETag error", err)
		}
	})

	for _, op := range []operation{opCreate, opPut} {
		t.Run(op.name+" without an ETag is an unknown outcome", func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, ok(""))
			store := newStore(t, fake.server.URL, "")

			if err := op.call(t.Context(), store); !errors.Is(err, storage.ErrUnknownOutcome) {
				t.Fatalf("error = %v, want %v", err, storage.ErrUnknownOutcome)
			}
		})
	}
}

func TestListPaginatesSortsAndStripsPrefix(t *testing.T) {
	t.Parallel()

	pages := map[string][]string{
		"":   {"root/pools/z.json", "root/pools/b.json"},
		"t1": {"root/pools/a.json"},
		"t2": {"root/pools/m.json"},
	}
	next := map[string]string{"": "t1", "t1": "t2"}

	fake := newFake(t, func(w http.ResponseWriter, r *http.Request, _ int) {
		query := r.URL.Query()
		if query.Get("list-type") != "2" {
			writeError(w, http.StatusBadRequest, "InvalidRequest")
			return
		}

		if query.Get("prefix") != "root/pools/" {
			writeError(w, http.StatusBadRequest, "UnexpectedPrefix")
			return
		}

		token := query.Get("continuation-token")

		var body strings.Builder

		body.WriteString(`<?xml version="1.0" encoding="UTF-8"?><ListBucketResult><Name>bucket</Name><Prefix>root/pools/</Prefix>`)

		for _, key := range pages[token] {
			fmt.Fprintf(&body, `<Contents><Key>%s</Key><ETag>"e"</ETag><Size>1</Size></Contents>`, key)
		}

		if n, more := next[token]; more {
			fmt.Fprintf(&body, `<IsTruncated>true</IsTruncated><NextContinuationToken>%s</NextContinuationToken>`, n)
		} else {
			body.WriteString(`<IsTruncated>false</IsTruncated>`)
		}

		body.WriteString(`</ListBucketResult>`)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, body.String())
	})

	store := newStore(t, fake.server.URL, "root/")

	keys, err := store.List(t.Context(), "pools/")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	want := []string{"pools/a.json", "pools/b.json", "pools/m.json", "pools/z.json"}
	if !slices.Equal(keys, want) {
		t.Fatalf("List() = %v, want %v", keys, want)
	}

	if got := fake.count(); got != 3 {
		t.Fatalf("requests = %d, want 3 pages", got)
	}
}

func TestPrefixNormalisation(t *testing.T) {
	t.Parallel()

	for _, prefix := range []string{"root", "root/", "/root/"} {
		t.Run(prefix, func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, ok(`"e"`))
			store := newStore(t, fake.server.URL, prefix)

			if _, _, err := store.Get(t.Context(), "k"); err != nil {
				t.Fatalf("Get() error = %v", err)
			}

			if got, want := fake.request(t, 0).Path, "/"+testBucket+"/root/k"; got != want {
				t.Fatalf("path = %s, want %s", got, want)
			}
		})
	}
}
