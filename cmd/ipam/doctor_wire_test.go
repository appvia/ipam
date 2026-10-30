// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/appvia/ipam/storage"
	azstore "github.com/appvia/ipam/storage/azblob"
	s3store "github.com/appvia/ipam/storage/s3"
)

// wellFormedETag matches an RFC 9110 entity-tag: optional W/, then a quoted
// string of etagc characters.
var wellFormedETag = regexp.MustCompile(`^(W/)?"[\x21\x23-\x7E\x80-\xFF]*"$`)

// azureETag matches the If-Match values real Azure accepts as well-formed;
// it answered both doctor-probe and "doctor-probe" with 412 for a reader.
var azureETag = regexp.MustCompile(`^"0x[0-9A-Fa-f]+"$`)

const (
	fakeETag     = `"0x8DE0123456789AB"`
	reservations = `{"reservations":[]}`
)

// conditionalFake models the order in which real Azure Blob Storage (and,
// conservatively, S3) evaluates a conditional PUT, as observed against a real
// account: a malformed If-Match is answered 412 before authorization; a
// well-formed one is authorized first (403 when the caller may not write),
// then compared with the current ETag (412 when it differs).
type conditionalFake struct {
	wellFormed *regexp.Regexp
	authorized bool
	forbidden  func(http.ResponseWriter)
	refused    func(http.ResponseWriter)

	mu       sync.Mutex
	ifMatch  []string
	accepted int
}

func (f *conditionalFake) serve(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Header().Set("ETag", fakeETag)
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", fmt.Sprint(len(reservations)))
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, reservations)
	case http.MethodPut:
		ifMatch := r.Header.Get("If-Match")

		f.mu.Lock()
		f.ifMatch = append(f.ifMatch, ifMatch)
		f.mu.Unlock()

		switch {
		case !f.wellFormed.MatchString(ifMatch):
			f.refused(w)
		case !f.authorized:
			f.forbidden(w)
		case ifMatch != fakeETag:
			f.refused(w)
		default:
			f.mu.Lock()
			f.accepted++
			f.mu.Unlock()

			w.Header().Set("ETag", fakeETag)
			w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
			w.WriteHeader(http.StatusCreated)
		}
	default:
		http.Error(w, "unexpected "+r.Method, http.StatusMethodNotAllowed)
	}
}

func (f *conditionalFake) sent(t *testing.T) string {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.ifMatch) != 1 || f.accepted != 0 {
		t.Fatalf("PUTs with If-Match %q (%d accepted), want exactly one refused PUT", f.ifMatch, f.accepted)
	}

	return f.ifMatch[0]
}

func xmlError(status int, code string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("x-ms-error-code", code)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>%s</Code>`+
			`<Message>%s</Message><RequestId>r</RequestId></Error>`, code, code)
	}
}

type testCredential struct{}

func (testCredential) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

type handlerTransport struct{ handler http.HandlerFunc }

func (h handlerTransport) Do(req *http.Request) (*http.Response, error) {
	var body []byte

	if req.Body != nil {
		data, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}

		body = data
	}

	served := req.Clone(req.Context())
	served.Body = io.NopCloser(bytes.NewReader(body))

	recorder := httptest.NewRecorder()
	h.handler(recorder, served)

	resp := recorder.Result()
	resp.Request = req

	return resp, nil
}

func newAzureProbeStore(t *testing.T, fake *conditionalFake) storage.Storage {
	t.Helper()

	store, err := azstore.New(t.Context(), azstore.Config{
		AccountURL: "https://account.blob.core.windows.net",
		Container:  "ipam",
		Credential: testCredential{},
		Transport:  handlerTransport{handler: fake.serve},
	})
	if err != nil {
		t.Fatalf("azblob.New: %v", err)
	}

	return store
}

func newS3ProbeStore(t *testing.T, fake *conditionalFake) storage.Storage {
	t.Helper()

	for key, value := range map[string]string{
		"AWS_ACCESS_KEY_ID":           "test",
		"AWS_SECRET_ACCESS_KEY":       "test",
		"AWS_SESSION_TOKEN":           "",
		"AWS_PROFILE":                 "",
		"AWS_CONFIG_FILE":             "/dev/null",
		"AWS_SHARED_CREDENTIALS_FILE": "/dev/null",
		"AWS_EC2_METADATA_DISABLED":   "true",
	} {
		t.Setenv(key, value)
	}

	server := httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(server.Close)

	store, err := s3store.New(t.Context(), s3store.Config{
		Bucket: "bucket", Region: "us-east-1", Endpoint: server.URL, UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("s3.New: %v", err)
	}

	return store
}

// TestProbeWriteSendsWellFormedETag runs the doctor write probe against
// backends whose servers, like real Azure, answer a malformed If-Match with
// 412 before checking permissions. A malformed probe version would report a
// reader as "write allowed".
func TestProbeWriteSendsWellFormedETag(t *testing.T) {
	backends := []struct {
		name       string
		wellFormed *regexp.Regexp
		forbidden  func(http.ResponseWriter)
		refused    func(http.ResponseWriter)
		open       func(*testing.T, *conditionalFake) storage.Storage
	}{
		{
			"azblob",
			azureETag,
			xmlError(http.StatusForbidden, "AuthorizationPermissionMismatch"),
			xmlError(http.StatusPreconditionFailed, "ConditionNotMet"),
			newAzureProbeStore,
		},
		{
			"s3",
			wellFormedETag,
			xmlError(http.StatusForbidden, "AccessDenied"),
			xmlError(http.StatusPreconditionFailed, "PreconditionFailed"),
			newS3ProbeStore,
		},
	}

	identities := []struct {
		name       string
		authorized bool
		wantWrite  bool
		expected   bool
		status     string
		detail     string
	}{
		{"reader", false, false, false, statusPass, "write denied"},
		{"reader expect-no-write", false, false, true, statusPass, "write denied"},
		{"reader expect-write", false, true, true, statusFail, "write denied, but --expect-write was given"},
		{"contributor", true, false, false, statusPass, "write allowed; conditional writes honoured"},
		{"contributor expect-write", true, true, true, statusPass, "write allowed; conditional writes honoured"},
		{"contributor expect-no-write", true, false, true, statusFail, "write allowed, but --expect-no-write was given"},
	}

	for _, backend := range backends {
		for _, identity := range identities {
			t.Run(backend.name+"/"+identity.name, func(t *testing.T) {
				fake := &conditionalFake{
					wellFormed: backend.wellFormed, authorized: identity.authorized, forbidden: backend.forbidden, refused: backend.refused,
				}
				store := backend.open(t, fake)

				got := probeWrite(t.Context(), store, "p", identity.wantWrite, identity.expected)
				if got.Status != identity.status || got.Detail != identity.detail {
					t.Fatalf("probe = %s %q, want %s %q", got.Status, got.Detail, identity.status, identity.detail)
				}

				sent := fake.sent(t)
				if !wellFormedETag.MatchString(sent) || strings.HasPrefix(sent, "W/") {
					t.Fatalf("If-Match = %q, want a quoted strong entity-tag", sent)
				}

				if sent == fakeETag {
					t.Fatalf("If-Match = %q equals the current ETag", sent)
				}
			})
		}
	}
}
