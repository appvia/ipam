// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package s3_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/aws/smithy-go"

	"github.com/appvia/ipam/storage"
	s3store "github.com/appvia/ipam/storage/s3"
)

// newRetryingStore is newStore with the backend's own SDK retryer, minus the
// backoff delay.
func newRetryingStore(t *testing.T, endpoint string) storage.Storage {
	t.Helper()

	store, err := s3store.New(t.Context(), s3store.Config{
		Bucket:       testBucket,
		Region:       "us-east-1",
		Endpoint:     endpoint,
		UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return s3store.WithoutRetryDelay(store)
}

// resetConnection drops the connection with a RST after the request has been
// read in full, as if the server applied the write and the answer was lost.
func resetConnection(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		panic(err)
	}

	if tcp, isTCP := conn.(*net.TCPConn); isTCP {
		_ = tcp.SetLinger(0)
	}

	_ = conn.Close()
}

var writeOps = []operation{opCreate, opPut, opDelete}

func TestRetryAfterPossiblyAppliedAttemptIsUnknownOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		first func(w http.ResponseWriter)
		code  string
		final int
	}{
		{"reset after the request was written, then 412", resetConnection,
			"PreconditionFailed", http.StatusPreconditionFailed},
		{"500, then 412", func(w http.ResponseWriter) { writeError(w, http.StatusInternalServerError, "InternalError") },
			"PreconditionFailed", http.StatusPreconditionFailed},
		{"408 RequestTimeout, then 412",
			func(w http.ResponseWriter) { writeError(w, http.StatusRequestTimeout, "RequestTimeout") },
			"PreconditionFailed", http.StatusPreconditionFailed},
		{"503, then 409 ConditionalRequestConflict",
			func(w http.ResponseWriter) { writeError(w, http.StatusServiceUnavailable, "ServiceUnavailable") },
			"ConditionalRequestConflict", http.StatusConflict},
	}

	for _, tt := range tests {
		for _, op := range writeOps {
			t.Run(tt.name+"/"+op.name, func(t *testing.T) {
				t.Parallel()

				fake := newFake(t, func(w http.ResponseWriter, _ *http.Request, n int) {
					if n == 1 {
						tt.first(w)
						return
					}

					writeError(w, tt.final, tt.code)
				})
				store := newRetryingStore(t, fake.server.URL)

				err := op.call(t.Context(), store)
				if !errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, want %v: the 412/409 may answer our own earlier write", err, storage.ErrUnknownOutcome)
				}

				if errors.Is(err, storage.ErrPreconditionFailed) {
					t.Fatalf("error = %v, must not also be %v", err, storage.ErrPreconditionFailed)
				}

				var apiErr smithy.APIError
				if !errors.As(err, &apiErr) || apiErr.ErrorCode() != tt.code {
					t.Fatalf("error = %v, want the final SDK error %s kept in the chain", err, tt.code)
				}

				if got := fake.count(); got != 2 {
					t.Fatalf("requests = %d, want 2 (one SDK retry)", got)
				}
			})
		}
	}
}

func TestFirstAttemptPreconditionFailureIsNotUnknown(t *testing.T) {
	t.Parallel()

	for _, code := range []struct {
		status int
		code   string
	}{{http.StatusPreconditionFailed, "PreconditionFailed"}, {http.StatusConflict, "ConditionalRequestConflict"}} {
		for _, op := range writeOps {
			t.Run(code.code+"/"+op.name, func(t *testing.T) {
				t.Parallel()

				fake := newFake(t, status(code.status, code.code))
				store := newRetryingStore(t, fake.server.URL)

				err := op.call(t.Context(), store)
				if !errors.Is(err, storage.ErrPreconditionFailed) || errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, want %v and not an unknown outcome", err, storage.ErrPreconditionFailed)
				}

				if got := fake.count(); got != 1 {
					t.Fatalf("requests = %d, want exactly 1", got)
				}
			})
		}
	}
}

func TestBackendRetryerIsExercised(t *testing.T) {
	t.Parallel()

	final := map[string]func(http.ResponseWriter, *http.Request, int){
		"500 InternalError":  status(http.StatusInternalServerError, "InternalError"),
		"408 RequestTimeout": status(http.StatusRequestTimeout, "RequestTimeout"),
	}

	for name, respond := range final {
		for _, op := range writeOps {
			t.Run(name+"/"+op.name, func(t *testing.T) {
				t.Parallel()

				fake := newFake(t, respond)
				store := newRetryingStore(t, fake.server.URL)

				if err := op.call(t.Context(), store); !errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, want %v", err, storage.ErrUnknownOutcome)
				}

				if got := fake.count(); got != 3 {
					t.Fatalf("requests = %d, want the SDK default of 3 attempts", got)
				}
			})
		}
	}
}

func tlsHandshakeFailures() map[string]func(t *testing.T) (string, *atomic.Int64) {
	return map[string]func(t *testing.T) (string, *atomic.Int64){
		"untrusted certificate": func(t *testing.T) (string, *atomic.Int64) {
			var requests atomic.Int64

			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(server.Close)

			return server.URL, &requests
		},
		"connection reset during the handshake": func(t *testing.T) (string, *atomic.Int64) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}

			done := make(chan struct{})

			go func() {
				defer close(done)

				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}

					if tcp, isTCP := conn.(*net.TCPConn); isTCP {
						_ = tcp.SetLinger(0)
					}

					_ = conn.Close()
				}
			}()

			t.Cleanup(func() {
				_ = listener.Close()

				<-done
			})

			return "https://" + listener.Addr().String(), new(atomic.Int64)
		},
	}
}

func TestPreSendFailuresAreNotUnknown(t *testing.T) {
	t.Parallel()

	for name, start := range tlsHandshakeFailures() {
		for _, op := range writeOps {
			t.Run(name+"/"+op.name, func(t *testing.T) {
				t.Parallel()

				endpoint, requests := start(t)
				store := newRetryingStore(t, endpoint)

				err := op.call(t.Context(), store)
				if err == nil {
					t.Fatal("error = nil, want a TLS handshake failure")
				}

				if errors.Is(err, storage.ErrUnknownOutcome) {
					t.Fatalf("error = %v, must not be an unknown outcome: nothing was sent", err)
				}

				if got := requests.Load(); got != 0 {
					t.Fatalf("requests = %d, want 0", got)
				}
			})
		}
	}
}

func TestCancelledContextReturnsContextError(t *testing.T) {
	t.Parallel()

	for _, op := range writeOps {
		t.Run(op.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, ok(`"e"`))
			store := newRetryingStore(t, fake.server.URL)

			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := op.call(ctx, store)
			if err != context.Canceled { //nolint:errorlint // the context error must be returned unwrapped
				t.Fatalf("error = %v, want context.Canceled unwrapped", err)
			}

			if got := fake.count(); got != 0 {
				t.Fatalf("requests = %d, want 0", got)
			}
		})
	}
}
