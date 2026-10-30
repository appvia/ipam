// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package azblob

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/appvia/ipam/storage"
)

const azuriteConnectionString = "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;" +
	"AccountKey=" + azuriteAccountKey + ";BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;"

type staticCredential struct{}

func (staticCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type failingCredential struct{}

func (failingCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{}, errors.New("token acquisition failed")
}

// failingTransport fails the test on any request: construction must stay offline.
type failingTransport struct {
	t     *testing.T
	calls atomic.Int32
}

func (f *failingTransport) Do(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	f.t.Errorf("unexpected network request: %s %s", req.Method, req.URL)

	return nil, errors.New("network access is not allowed in this test")
}

func fastRetry(o *policy.RetryOptions) {
	o.RetryDelay = time.Millisecond
	o.MaxRetryDelay = 5 * time.Millisecond
}

// inProcessTransport serves handler synchronously and reads and closes the
// whole request body before answering. No transport goroutine outlives an
// attempt, so azcore can rewind the body for a retry without racing a writer
// (a real server answering early with a 1ms retry delay races net/http's
// trailing body read).
type inProcessTransport struct {
	handler  http.HandlerFunc
	requests atomic.Int32
}

func (p *inProcessTransport) Do(req *http.Request) (*http.Response, error) {
	p.requests.Add(1)

	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	var body []byte

	if req.Body != nil {
		data, readErr := io.ReadAll(req.Body)
		if err := errors.Join(readErr, req.Body.Close()); err != nil {
			return nil, err
		}

		body = data
	}

	served := req.Clone(req.Context())
	served.Body = io.NopCloser(bytes.NewReader(body))

	recorder := httptest.NewRecorder()
	p.handler(recorder, served)

	resp := recorder.Result()
	resp.Request = req

	return resp, nil
}

// newTestStore builds a store over an inProcessTransport and counts requests.
func newTestStore(t *testing.T, handler http.HandlerFunc, configure ...func(*Config)) (*Store, *atomic.Int32) {
	t.Helper()

	transport := &inProcessTransport{handler: handler}

	cfg := Config{
		AccountURL: "https://account.blob.core.windows.net",
		Container:  "ipam",
		Prefix:     "pfx",
		Credential: staticCredential{},
		Transport:  transport,
		retry:      fastRetry,
	}
	for _, c := range configure {
		c(&cfg)
	}

	store, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return store, &transport.requests
}

func writeAzureError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("x-ms-error-code", code)
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><Error><Code>%s</Code><Message>test</Message></Error>`, code)
}

func writeCreated(w http.ResponseWriter, etag string) {
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
}

func TestCreateIfAbsentSendsIfNoneMatchAny(t *testing.T) {
	t.Parallel()

	store, count := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/ipam/pfx/pools/a.json" {
			t.Errorf("request = %s %s, want PUT /ipam/pfx/pools/a.json", r.Method, r.URL.Path)
		}

		if got := r.Header.Get("If-None-Match"); got != "*" {
			t.Errorf("If-None-Match = %q, want *", got)
		}

		if got := r.Header.Get("If-Match"); got != "" {
			t.Errorf("If-Match = %q, want none", got)
		}

		body, _ := io.ReadAll(r.Body)
		if string(body) != "data" {
			t.Errorf("body = %q, want data", body)
		}

		writeCreated(w, `"0x8DC1"`)
	})

	version, err := store.CreateIfAbsent(t.Context(), "pools/a.json", []byte("data"))
	if err != nil {
		t.Fatalf("CreateIfAbsent() error = %v", err)
	}

	if version != `"0x8DC1"` {
		t.Fatalf("CreateIfAbsent() version = %q, want the ETag verbatim", version)
	}

	if count.Load() != 1 {
		t.Fatalf("requests = %d, want 1", count.Load())
	}
}

func TestPutIfMatchSendsIfMatch(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-Match"); got != `"0x1"` {
			t.Errorf("If-Match = %q, want \"0x1\"", got)
		}

		if got := r.Header.Get("If-None-Match"); got != "" {
			t.Errorf("If-None-Match = %q, want none", got)
		}

		writeCreated(w, `"0x2"`)
	})

	version, err := store.PutIfMatch(t.Context(), "k", []byte("v2"), `"0x1"`)
	if err != nil {
		t.Fatalf("PutIfMatch() error = %v", err)
	}

	if version != `"0x2"` {
		t.Fatalf("PutIfMatch() version = %q, want \"0x2\"", version)
	}
}

func TestDeleteIfMatchSendsIfMatch(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/ipam/pfx/k" {
			t.Errorf("request = %s %s, want DELETE /ipam/pfx/k", r.Method, r.URL.Path)
		}

		if got := r.Header.Get("If-Match"); got != `"0x1"` {
			t.Errorf("If-Match = %q, want \"0x1\"", got)
		}

		w.WriteHeader(http.StatusAccepted)
	})

	if err := store.DeleteIfMatch(t.Context(), "k", `"0x1"`); err != nil {
		t.Fatalf("DeleteIfMatch() error = %v", err)
	}
}

func TestEmptyOrWildcardVersionIsRejectedWithoutRequest(t *testing.T) {
	t.Parallel()

	store, count := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	for _, version := range []storage.Version{"", "*"} {
		_, err := store.PutIfMatch(t.Context(), "k", []byte("x"), version)
		if !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Errorf("PutIfMatch(%q) error = %v, want %v", version, err, storage.ErrPreconditionFailed)
		}

		err = store.DeleteIfMatch(t.Context(), "k", version)
		if !errors.Is(err, storage.ErrPreconditionFailed) {
			t.Errorf("DeleteIfMatch(%q) error = %v, want %v", version, err, storage.ErrPreconditionFailed)
		}
	}

	if count.Load() != 0 {
		t.Fatalf("requests = %d, want 0", count.Load())
	}
}

func TestGetReturnsDataAndETagVerbatim(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/ipam/pfx/k" {
			t.Errorf("request = %s %s, want GET /ipam/pfx/k", r.Method, r.URL.Path)
		}

		w.Header().Set("ETag", `"0x8DC1ABC"`)
		w.Header().Set("Content-Length", "5")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})

	data, version, err := store.Get(t.Context(), "k")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if string(data) != "hello" || version != `"0x8DC1ABC"` {
		t.Fatalf("Get() = %q, %q; want hello, \"0x8DC1ABC\"", data, version)
	}
}

func TestErrorMapping(t *testing.T) {
	t.Parallel()

	type op func(*Store, context.Context) error

	get := func(s *Store, ctx context.Context) error { _, _, err := s.Get(ctx, "k"); return err }
	create := func(s *Store, ctx context.Context) error {
		_, err := s.CreateIfAbsent(ctx, "k", []byte("x"))
		return err
	}
	put := func(s *Store, ctx context.Context) error {
		_, err := s.PutIfMatch(ctx, "k", []byte("x"), `"0x1"`)
		return err
	}
	del := func(s *Store, ctx context.Context) error { return s.DeleteIfMatch(ctx, "k", `"0x1"`) }
	list := func(s *Store, ctx context.Context) error { _, err := s.List(ctx, ""); return err }

	tests := []struct {
		name   string
		op     op
		status int
		code   string
		want   error
	}{
		{"create over existing blob (409 BlobAlreadyExists)", create, http.StatusConflict, "BlobAlreadyExists", storage.ErrPreconditionFailed},
		{"create 412 ConditionNotMet", create, http.StatusPreconditionFailed, "ConditionNotMet", storage.ErrPreconditionFailed},
		{"put stale ETag (412 ConditionNotMet)", put, http.StatusPreconditionFailed, "ConditionNotMet", storage.ErrPreconditionFailed},
		{"put missing blob (404 BlobNotFound)", put, http.StatusNotFound, "BlobNotFound", storage.ErrNotFound},
		{"delete stale ETag (412 ConditionNotMet)", del, http.StatusPreconditionFailed, "ConditionNotMet", storage.ErrPreconditionFailed},
		{"delete missing blob (404 BlobNotFound)", del, http.StatusNotFound, "BlobNotFound", storage.ErrNotFound},
		{"get missing blob (404 BlobNotFound)", get, http.StatusNotFound, "BlobNotFound", storage.ErrNotFound},
		{"get 403 AuthorizationPermissionMismatch", get, http.StatusForbidden, "AuthorizationPermissionMismatch", storage.ErrAccessDenied},
		{"create 403 AuthorizationFailure", create, http.StatusForbidden, "AuthorizationFailure", storage.ErrAccessDenied},
		{"put 403 AuthorizationPermissionMismatch", put, http.StatusForbidden, "AuthorizationPermissionMismatch", storage.ErrAccessDenied},
		{"delete 403 AuthorizationFailure", del, http.StatusForbidden, "AuthorizationFailure", storage.ErrAccessDenied},
		{"list 403 AuthorizationPermissionMismatch", list, http.StatusForbidden, "AuthorizationPermissionMismatch", storage.ErrAccessDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store, count := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
				writeAzureError(w, tt.status, tt.code)
			})

			err := tt.op(store, t.Context())

			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}

			if errors.Is(err, storage.ErrUnknownOutcome) {
				t.Fatalf("error = %v, must not be %v", err, storage.ErrUnknownOutcome)
			}

			if count.Load() != 1 {
				t.Fatalf("requests = %d, want exactly 1 (never retried)", count.Load())
			}
		})
	}
}

func TestContainerNotFoundIsNotObjectNotFound(t *testing.T) {
	t.Parallel()

	store, _ := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
		writeAzureError(w, http.StatusNotFound, "ContainerNotFound")
	})

	_, _, err := store.Get(t.Context(), "k")
	if err == nil || errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Get() error = %v, want a non-ErrNotFound error", err)
	}
}

func TestAccessPropagationRetry(t *testing.T) {
	t.Parallel()

	handler := func(count *atomic.Int32) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if count.Add(1) <= 2 {
				writeAzureError(w, http.StatusForbidden, "AuthorizationPermissionMismatch")

				return
			}

			writeCreated(w, `"0x1"`)
		}
	}

	t.Run("retried with the propagation flag", func(t *testing.T) {
		t.Parallel()

		var responses atomic.Int32

		store, count := newTestStore(t, handler(&responses))

		_, err := store.CreateIfAbsent(storage.WithAccessPropagationRetry(t.Context()), "k", []byte("x"))
		if err != nil {
			t.Fatalf("CreateIfAbsent() error = %v", err)
		}

		if count.Load() != 3 {
			t.Fatalf("requests = %d, want 3", count.Load())
		}
	})

	t.Run("not retried without the flag", func(t *testing.T) {
		t.Parallel()

		var responses atomic.Int32

		store, count := newTestStore(t, handler(&responses))

		_, err := store.CreateIfAbsent(t.Context(), "k", []byte("x"))
		if !errors.Is(err, storage.ErrAccessDenied) {
			t.Fatalf("CreateIfAbsent() error = %v, want %v", err, storage.ErrAccessDenied)
		}

		if count.Load() != 1 {
			t.Fatalf("requests = %d, want exactly 1", count.Load())
		}
	})
}

func TestPreconditionFailedIsNeverRetried(t *testing.T) {
	t.Parallel()

	store, count := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
		writeAzureError(w, http.StatusPreconditionFailed, "ConditionNotMet")
	})

	ctx := storage.WithAccessPropagationRetry(t.Context())

	_, err := store.PutIfMatch(ctx, "k", []byte("x"), `"0x1"`)
	if !errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("PutIfMatch() error = %v, want %v", err, storage.ErrPreconditionFailed)
	}

	if count.Load() != 1 {
		t.Fatalf("requests = %d, want exactly 1", count.Load())
	}
}

func TestShouldRetry(t *testing.T) {
	t.Parallel()

	withFlag := storage.WithAccessPropagationRetry(context.Background())

	response := func(ctx context.Context, status int, code string) *http.Response {
		req := httptest.NewRequestWithContext(ctx, http.MethodPut, "https://example.invalid/c/k", nil)
		resp := &http.Response{StatusCode: status, Header: http.Header{}, Request: req}

		if code != "" {
			resp.Header.Set("x-ms-error-code", code)
		}

		return resp
	}

	tests := []struct {
		name string
		resp *http.Response
		err  error
		want bool
	}{
		{"transport error", nil, errors.New("connection reset"), true},
		{"408", response(context.Background(), 408, ""), nil, true},
		{"429", response(context.Background(), 429, ""), nil, true},
		{"500", response(context.Background(), 500, ""), nil, true},
		{"502", response(context.Background(), 502, ""), nil, true},
		{"503", response(context.Background(), 503, ""), nil, true},
		{"504", response(context.Background(), 504, ""), nil, true},
		{"201", response(context.Background(), 201, ""), nil, false},
		{"404", response(withFlag, 404, "BlobNotFound"), nil, false},
		{"409", response(withFlag, 409, "BlobAlreadyExists"), nil, false},
		{"412", response(withFlag, 412, "ConditionNotMet"), nil, false},
		{"501", response(context.Background(), 501, ""), nil, false},
		{"403 permission mismatch with flag", response(withFlag, 403, "AuthorizationPermissionMismatch"), nil, true},
		{"403 authorization failure with flag", response(withFlag, 403, "AuthorizationFailure"), nil, true},
		{"403 permission mismatch without flag", response(context.Background(), 403, "AuthorizationPermissionMismatch"), nil, false},
		{"403 authorization failure without flag", response(context.Background(), 403, "AuthorizationFailure"), nil, false},
		{"403 authentication failed with flag", response(withFlag, 403, "AuthenticationFailed"), nil, false},
		{"403 without code with flag", response(withFlag, 403, ""), nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := shouldRetry(tt.resp, tt.err); got != tt.want {
				t.Fatalf("shouldRetry() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestRetryOptionsMatchThePlan(t *testing.T) {
	t.Parallel()

	got := clientOptions(Config{}).Retry
	if got.MaxRetries != 10 || got.RetryDelay != 2*time.Second || got.MaxRetryDelay != 30*time.Second {
		t.Fatalf("retry options = %+v, want MaxRetries 10, RetryDelay 2s, MaxRetryDelay 30s", got)
	}

	if got.ShouldRetry == nil {
		t.Fatal("ShouldRetry is nil, want shouldRetry")
	}
}

type writeOp struct {
	name string
	run  func(*Store, context.Context) error
}

var writeOps = []writeOp{
	{"create", func(s *Store, ctx context.Context) error {
		_, err := s.CreateIfAbsent(ctx, "k", []byte("x"))
		return err
	}},
	{"put", func(s *Store, ctx context.Context) error {
		_, err := s.PutIfMatch(ctx, "k", []byte("x"), `"0x1"`)
		return err
	}},
	{"delete", func(s *Store, ctx context.Context) error { return s.DeleteIfMatch(ctx, "k", `"0x1"`) }},
}

// Runs over real net/http so the WroteHeaders trace fires; a single attempt
// means azcore never rewinds a body a previous writer may still be reading.
func TestUnknownOutcomeWhenTheServerHangsPastTryTimeout(t *testing.T) {
	t.Parallel()

	for _, op := range writeOps {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			var count atomic.Int32

			release := make(chan struct{})

			server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				count.Add(1)

				_, _ = io.Copy(io.Discard, r.Body)

				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			t.Cleanup(server.Close)
			t.Cleanup(func() { close(release) })

			store, err := New(t.Context(), Config{
				AccountURL: server.URL, Container: "ipam", Credential: staticCredential{}, Transport: server.Client(),
				retry: func(o *policy.RetryOptions) {
					o.MaxRetries = -1
					o.TryTimeout = 100 * time.Millisecond
				},
			})
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			err = op.run(store, ctx)
			if !errors.Is(err, storage.ErrUnknownOutcome) {
				t.Fatalf("error = %v, want %v", err, storage.ErrUnknownOutcome)
			}

			if count.Load() != 1 {
				t.Fatalf("requests = %d, want 1 (single attempt)", count.Load())
			}
		})
	}
}

func TestUnknownOutcomeAfterFinal5xx(t *testing.T) {
	t.Parallel()

	for _, op := range writeOps {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			store, count := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
				writeAzureError(w, http.StatusServiceUnavailable, "ServerBusy")
			}, func(c *Config) {
				c.retry = func(o *policy.RetryOptions) {
					fastRetry(o)
					o.MaxRetries = 2
				}
			})

			err := op.run(store, t.Context())
			if !errors.Is(err, storage.ErrUnknownOutcome) {
				t.Fatalf("error = %v, want %v", err, storage.ErrUnknownOutcome)
			}

			if count.Load() != 3 {
				t.Fatalf("requests = %d, want 3 (MaxRetries 2)", count.Load())
			}
		})
	}
}

// A 412 after an attempt that may have applied could be the answer to our own write.
func TestUnknownOutcomeWhenAnEarlierAttemptMayHaveApplied(t *testing.T) {
	t.Parallel()

	var responses atomic.Int32

	store, _ := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
		if responses.Add(1) == 1 {
			writeAzureError(w, http.StatusInternalServerError, "InternalError")

			return
		}

		writeAzureError(w, http.StatusPreconditionFailed, "ConditionNotMet")
	})

	_, err := store.PutIfMatch(t.Context(), "k", []byte("x"), `"0x1"`)
	if !errors.Is(err, storage.ErrUnknownOutcome) {
		t.Fatalf("PutIfMatch() error = %v, want %v", err, storage.ErrUnknownOutcome)
	}

	if errors.Is(err, storage.ErrPreconditionFailed) {
		t.Fatalf("PutIfMatch() error = %v, must not claim a definite precondition failure", err)
	}
}

func TestPreSendErrorsAreNotUnknownOutcome(t *testing.T) {
	t.Parallel()

	t.Run("token acquisition failure", func(t *testing.T) {
		t.Parallel()

		store, count := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
			writeCreated(w, `"0x1"`)
		}, func(c *Config) { c.Credential = failingCredential{} })

		for _, op := range writeOps {
			err := op.run(store, t.Context())
			if err == nil || errors.Is(err, storage.ErrUnknownOutcome) {
				t.Errorf("%s error = %v, want a non-unknown-outcome error", op.name, err)
			}
		}

		if count.Load() != 0 {
			t.Fatalf("requests = %d, want 0", count.Load())
		}
	})

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewTLSServer(http.NotFoundHandler())
		url, client := server.URL, server.Client()
		server.Close()

		store, err := New(t.Context(), Config{
			AccountURL: url, Container: "ipam", Credential: staticCredential{}, Transport: client,
			retry: func(o *policy.RetryOptions) { o.MaxRetries = -1 },
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		for _, op := range writeOps {
			err := op.run(store, t.Context())
			if err == nil || errors.Is(err, storage.ErrUnknownOutcome) {
				t.Errorf("%s error = %v, want a non-unknown-outcome error", op.name, err)
			}
		}
	})
}

func TestListPagesStripsPrefixAndSorts(t *testing.T) {
	t.Parallel()

	const page = `<?xml version="1.0" encoding="utf-8"?>` +
		`<EnumerationResults ServiceEndpoint="https://x/" ContainerName="ipam"><Prefix>pfx/pools/</Prefix>` +
		`<Blobs>%s</Blobs><NextMarker>%s</NextMarker></EnumerationResults>`

	blob := func(name string) string {
		return `<Blob><Name>` + name + `</Name><Properties><Etag>"0x1"</Etag></Properties></Blob>`
	}

	store, count := newTestStore(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("restype") != "container" || q.Get("comp") != "list" || q.Get("prefix") != "pfx/pools/" {
			t.Errorf("query = %v, want a container list with prefix pfx/pools/", q)
		}

		w.Header().Set("Content-Type", "application/xml")

		switch q.Get("marker") {
		case "":
			_, _ = fmt.Fprintf(w, page, blob("pfx/pools/z")+blob("pfx/pools/m"), "next")
		case "next":
			_, _ = fmt.Fprintf(w, page, blob("pfx/pools/a"), "")
		default:
			t.Errorf("unexpected marker %q", q.Get("marker"))
		}
	})

	keys, err := store.List(t.Context(), "pools/")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}

	if want := []string{"pools/a", "pools/m", "pools/z"}; !slices.Equal(keys, want) {
		t.Fatalf("List() = %v, want %v", keys, want)
	}

	if count.Load() != 2 {
		t.Fatalf("requests = %d, want 2 pages", count.Load())
	}
}

func TestNewValidatesConfig(t *testing.T) {
	const (
		wkKey   = azuriteAccountKey
		realKey = "c2VjcmV0a2V5"
	)

	tests := []struct {
		name string
		conn string
		cfg  Config
	}{
		{"missing container", "", Config{AccountURL: "https://a.blob.core.windows.net", Credential: staticCredential{}}},
		{"account URL without scheme", "", Config{AccountURL: "a.blob.core.windows.net", Container: "c", Credential: staticCredential{}}},
		{"plain http account URL", "", Config{AccountURL: "http://a.blob.core.windows.net", Container: "c", Credential: staticCredential{}}},
		{"account URL carrying a SAS token", "", Config{AccountURL: "https://a.blob.core.windows.net/?sig=secret", Container: "c", Credential: staticCredential{}}},
		{"empty account URL", "", Config{Container: "c", Credential: staticCredential{}}},
		{"no credential and no connection string", "", Config{AccountURL: "https://a.blob.core.windows.net", Container: "c"}},
		{
			"account URL disagreeing with the connection string", azuriteConnectionString,
			Config{AccountURL: "https://a.blob.core.windows.net", Container: "c"},
		},
	}

	for _, conn := range []struct{ name, conn string }{
		{"real account", "DefaultEndpointsProtocol=https;AccountName=prod;AccountKey=" + realKey + ";EndpointSuffix=core.windows.net"},
		{"non-loopback blob endpoint", "DefaultEndpointsProtocol=https;AccountName=prod;AccountKey=" + realKey + ";BlobEndpoint=https://prod.blob.core.windows.net/"},
		{"loopback-looking hostname", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1.evil.example/devstoreaccount1"},
		{"malformed", "not a connection string"},
		{"lower-case blobendpoint key falls back to the real account", "AccountName=prodacct;AccountKey=" + realKey + ";blobendpoint=http://127.0.0.1:10000/devstoreaccount1"},
		{"space before BlobEndpoint falls back to the real account", "AccountName=prodacct;AccountKey=" + realKey + "; BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1"},
		{"upper-case BLOBENDPOINT falls back to the real account", "AccountName=prodacct;AccountKey=" + realKey + ";BLOBENDPOINT=http://localhost:10000/x"},
		{"dev account with differently-cased BlobEndpoint", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";blobEndpoint=http://127.0.0.1:10000/devstoreaccount1"},
		{"differently-cased AccountName key", "accountname=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1"},
		{"space before AccountName key", " AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1"},
		{"duplicate BlobEndpoint, loopback last", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=https://evil.blob.core.windows.net;BlobEndpoint=http://127.0.0.1:10000/x"},
		{"duplicate BlobEndpoint, loopback first", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1:10000/x;BlobEndpoint=https://evil.blob.core.windows.net"},
		{"duplicate AccountKey", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";AccountKey=" + realKey + ";BlobEndpoint=http://127.0.0.1:10000/x"},
		{"loopback endpoint with a real account name", "AccountName=prodacct;AccountKey=" + realKey + ";BlobEndpoint=http://127.0.0.1:10000/prodacct"},
		{"loopback endpoint with the dev account but another key", "AccountName=devstoreaccount1;AccountKey=" + realKey + ";BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1"},
		{"dev account without BlobEndpoint, https protocol", "DefaultEndpointsProtocol=https;AccountName=devstoreaccount1;AccountKey=" + wkKey},
		{"dev account without BlobEndpoint, http protocol", "DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=" + wkKey + ";EndpointSuffix=core.windows.net"},
		{"differently-cased DefaultEndpointsProtocol key", "defaultendpointsprotocol=http;AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1:10000/x"},
		{"SAS instead of the dev key", "BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;SharedAccessSignature=sv=2024&sig=abc"},
		{"trailing whitespace segment", azuriteConnectionString + " "},
		{"empty segment in the middle", "AccountName=devstoreaccount1;;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1:10000/x"},
		{"IPv4-mapped IPv6 loopback", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://[::ffff:127.0.0.1]:10000/x"},
		{"upper-case LOCALHOST", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://LOCALHOST:10000/x"},
		{"unspecified address", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://0.0.0.0:10000/x"},
		{"userinfo trick", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1@evil.example/x"},
		{"non-http scheme", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=ftp://127.0.0.1:10000/x"},
		{"UseDevelopmentStorage shorthand", "UseDevelopmentStorage=true"},
		{"localhost suffix trick", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://localhost.evil:10000/x"},
		{"localhost prefix trick", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://xlocalhost:10000/x"},
		{"userinfo with password", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://user:pw@127.0.0.1:10000/x"},
		{"fragment-at trick", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://evil.example#@127.0.0.1:10000/x"},
		{"dev key plus SAS", "AccountName=devstoreaccount1;AccountKey=" + wkKey + ";BlobEndpoint=http://127.0.0.1:10000/x;SharedAccessSignature=sv=2024&sig=abc"},
	} {
		tests = append(tests, struct {
			name string
			conn string
			cfg  Config
		}{"connection string: " + conn.name, conn.conn, Config{Container: "c"}})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(connectionStringEnv, tt.conn)

			transport := &failingTransport{t: t}
			cfg := tt.cfg
			cfg.Transport = transport
			cfg.retry = func(o *policy.RetryOptions) { o.MaxRetries = -1 }

			store, err := New(t.Context(), cfg)
			if err == nil {
				_, _, _ = store.Get(t.Context(), "probe")
			}

			if !errors.Is(err, ErrInvalidArgument) {
				t.Errorf("New() error = %v, want %v", err, ErrInvalidArgument)
			}

			if transport.calls.Load() != 0 {
				t.Errorf("requests = %d, want 0", transport.calls.Load())
			}
		})
	}
}

func TestNewAcceptsAzuriteConnectionString(t *testing.T) {
	transport := &failingTransport{t: t}

	for _, conn := range []string{
		azuriteConnectionString,
		strings.TrimSuffix(azuriteConnectionString, ";"),
		"AccountName=devstoreaccount1;AccountKey=" + azuriteAccountKey + ";BlobEndpoint=http://localhost:10000/devstoreaccount1",
		"DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey=" + azuriteAccountKey + ";BlobEndpoint=http://[::1]:10000/devstoreaccount1",
		"DefaultEndpointsProtocol=https;AccountName=devstoreaccount1;AccountKey=" + azuriteAccountKey + ";BlobEndpoint=https://127.0.0.2:10000/devstoreaccount1",
	} {
		t.Setenv(connectionStringEnv, conn)

		if _, err := New(t.Context(), Config{Container: "c", Transport: transport}); err != nil {
			t.Fatalf("New() with %q error = %v", conn, err)
		}
	}

	if transport.calls.Load() != 0 {
		t.Fatalf("requests = %d, want 0", transport.calls.Load())
	}
}

func TestPreCancelledContextIsNotUnknownOutcome(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()

	for _, op := range writeOps {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			store, count := newTestStore(t, func(w http.ResponseWriter, _ *http.Request) {
				writeCreated(w, `"0x1"`)
			})

			for _, tc := range []struct {
				ctx  context.Context
				want error
			}{{cancelled, context.Canceled}, {expired, context.DeadlineExceeded}} {
				err := op.run(store, tc.ctx)
				if !errors.Is(err, tc.want) || errors.Is(err, storage.ErrUnknownOutcome) {
					t.Errorf("error = %v, want %v and not %v", err, tc.want, storage.ErrUnknownOutcome)
				}
			}

			if count.Load() != 0 {
				t.Fatalf("requests = %d, want 0", count.Load())
			}
		})
	}
}

func TestTLSHandshakeFailureIsNotUnknownOutcome(t *testing.T) {
	t.Parallel()

	var handled atomic.Int32

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		handled.Add(1)
		writeCreated(w, `"0x1"`)
	}))
	t.Cleanup(server.Close)

	untrusting := &http.Client{Transport: &http.Transport{}}

	store, err := New(t.Context(), Config{
		AccountURL: server.URL, Container: "ipam", Credential: staticCredential{}, Transport: untrusting,
		retry: func(o *policy.RetryOptions) { o.MaxRetries = -1 },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, op := range writeOps {
		err := op.run(store, t.Context())
		if err == nil || errors.Is(err, storage.ErrUnknownOutcome) {
			t.Errorf("%s error = %v, want a non-unknown-outcome error", op.name, err)
		}
	}

	if handled.Load() != 0 {
		t.Fatalf("requests handled = %d, want 0", handled.Load())
	}
}

type transporterFunc func(*http.Request) (*http.Response, error)

func (f transporterFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

func TestUntraceableTransportErrorsStayUnknownOutcome(t *testing.T) {
	t.Parallel()

	store, err := New(t.Context(), Config{
		AccountURL: "https://account.blob.core.windows.net", Container: "ipam", Credential: staticCredential{},
		Transport: transporterFunc(func(*http.Request) (*http.Response, error) { return nil, io.ErrUnexpectedEOF }),
		retry:     func(o *policy.RetryOptions) { fastRetry(o); o.MaxRetries = 1 },
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	for _, op := range writeOps {
		if err := op.run(store, t.Context()); !errors.Is(err, storage.ErrUnknownOutcome) {
			t.Errorf("%s error = %v, want %v", op.name, err, storage.ErrUnknownOutcome)
		}
	}
}

func TestConstructionDoesNotTouchTheNetwork(t *testing.T) {
	t.Setenv(connectionStringEnv, "")

	transport := &failingTransport{t: t}

	auth := AuthConfig{UseMSI: ptr(true), UseCLI: ptr(false)}
	auth.clientOptions.Transport = transport

	credential, err := auth.Credential(nil)
	if err != nil {
		t.Fatalf("Credential() error = %v", err)
	}

	_, err = New(t.Context(), Config{
		AccountURL: "https://account.blob.core.windows.net",
		Container:  "ipam",
		Prefix:     "env",
		Credential: credential,
		Transport:  transport,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if transport.calls.Load() != 0 {
		t.Fatalf("requests = %d, want 0", transport.calls.Load())
	}
}

func TestIsLoopback(t *testing.T) {
	t.Parallel()

	for host, want := range map[string]bool{
		"localhost":        true,
		"127.0.0.1":        true,
		"127.255.255.254":  true,
		"::1":              true,
		"localhost.evil":   false,
		"xlocalhost":       false,
		"localhostx":       false,
		"LOCALHOST":        false,
		"::ffff:127.0.0.1": false,
		"0.0.0.0":          false,
		"10.0.0.1":         false,
		"":                 false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %t, want %t", host, got, want)
		}
	}
}

func TestRequireLoopbackURL(t *testing.T) {
	t.Parallel()

	for raw, wantOK := range map[string]bool{
		"http://127.0.0.1:10000/devstoreaccount1":         true,
		"https://localhost:10000/devstoreaccount1":        true,
		"http://[::1]:10000/devstoreaccount1/":            true,
		"http://user:pw@127.0.0.1:10000/devstoreaccount1": false,
		"http://user@localhost:10000/devstoreaccount1":    false,
		"http://evil.example#@127.0.0.1:10000/x":          false,
		"http://localhost.evil:10000/x":                   false,
		"ftp://127.0.0.1:10000/x":                         false,
		"https://account.blob.core.windows.net/":          false,
	} {
		err := requireLoopbackURL(raw)
		if wantOK != (err == nil) || (err != nil && !errors.Is(err, ErrInvalidArgument)) {
			t.Errorf("requireLoopbackURL(%q) error = %v, want ok=%t", raw, err, wantOK)
		}
	}
}

// The parser layer is checked directly: through New the resolved-URL layer
// would mask a missing check here.
func TestParseAzuriteConnectionStringChecksEndpoint(t *testing.T) {
	t.Parallel()

	dev := "AccountName=devstoreaccount1;AccountKey=" + azuriteAccountKey + ";BlobEndpoint="

	for conn, wantOK := range map[string]bool{
		dev + "http://127.0.0.1:10000/devstoreaccount1":                             true,
		dev + "https://devstoreaccount1.blob.core.windows.net":                      false,
		dev + "http://user:pw@127.0.0.1:10000/devstoreaccount1":                     false,
		dev + "http://localhost.evil:10000/x":                                       false,
		dev + "http://127.0.0.1:10000/x;SharedAccessSignature=sv=2024&sig=abc":      false,
		"SharedAccessSignature=sv=2024&sig=abc;" + dev + "http://127.0.0.1:10000/x": false,
	} {
		endpoint, err := parseAzuriteConnectionString(conn)
		if wantOK != (err == nil) || (err != nil && !errors.Is(err, ErrInvalidArgument)) {
			t.Errorf("parseAzuriteConnectionString(%q) = %q, %v; want ok=%t", conn, endpoint, err, wantOK)
		}
	}
}

// The resolved-URL layer is checked with a parser that wrongly approves a
// connection string the SDK resolves to a real account.
func TestNewRejectsNonLoopbackResolvedURL(t *testing.T) {
	t.Setenv(connectionStringEnv, "DefaultEndpointsProtocol=https;AccountName=devstoreaccount1;AccountKey="+
		azuriteAccountKey+";BlobEndpoint=https://devstoreaccount1.blob.core.windows.net")

	transport := &failingTransport{t: t}

	store, err := New(t.Context(), Config{
		Container: "c",
		Transport: transport,
		retry:     func(o *policy.RetryOptions) { o.MaxRetries = -1 },
		parseConnectionString: func(string) (string, error) {
			return "http://127.0.0.1:10000/devstoreaccount1", nil
		},
	})
	if err == nil {
		_, _, _ = store.Get(t.Context(), "probe")
	}

	if !errors.Is(err, ErrInvalidArgument) {
		t.Errorf("New() error = %v, want %v", err, ErrInvalidArgument)
	}

	if transport.calls.Load() != 0 {
		t.Errorf("requests = %d, want 0", transport.calls.Load())
	}
}
