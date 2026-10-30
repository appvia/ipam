// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package azblob

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/appvia/ipam/storage"
)

// fakeAccount answers the read-only requests Inspect makes. Each field is the
// HTTP status for that request; 0 means 200.
type fakeAccount struct {
	accountInfo    int
	hns            string
	serviceProps   int
	softDelete     string
	list           int
	blobs          []string
	head           int
	versionID      string
	mu             sync.Mutex
	methodsAndURLs []string
}

func (f *fakeAccount) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.methodsAndURLs = append(f.methodsAndURLs, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()

	query := r.URL.Query()

	switch {
	case query.Get("comp") == "properties" && query.Get("restype") == "account":
		if f.accountInfo != 0 {
			writeAzureError(w, f.accountInfo, "AuthorizationPermissionMismatch")
			return
		}

		if f.hns != "" {
			w.Header().Set("x-ms-is-hns-enabled", f.hns)
		}

		w.WriteHeader(http.StatusOK)
	case query.Get("comp") == "properties" && query.Get("restype") == "service":
		if f.serviceProps != 0 {
			writeAzureError(w, f.serviceProps, "AuthorizationPermissionMismatch")
			return
		}

		w.Header().Set("Content-Type", "application/xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><StorageServiceProperties>%s</StorageServiceProperties>`,
			f.softDelete)
	case query.Get("comp") == "list":
		if f.list != 0 {
			writeAzureError(w, f.list, "AuthorizationPermissionMismatch")
			return
		}

		var body strings.Builder

		body.WriteString(`<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs>`)

		for _, name := range f.blobs {
			fmt.Fprintf(&body, `<Blob><Name>%s</Name><Properties></Properties></Blob>`, name)
		}

		body.WriteString(`</Blobs><NextMarker/></EnumerationResults>`)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(body.String()))
	case r.Method == http.MethodHead:
		if f.head != 0 {
			w.WriteHeader(f.head)
			return
		}

		w.Header().Set("ETag", `"0x1"`)

		if f.versionID != "" {
			w.Header().Set("x-ms-version-id", f.versionID)
		}

		w.WriteHeader(http.StatusOK)
	default:
		writeAzureError(w, http.StatusBadRequest, "UnexpectedRequest")
	}
}

func TestInspectAzureAccount(t *testing.T) {
	enabled := `<DeleteRetentionPolicy><Enabled>true</Enabled><Days>7</Days></DeleteRetentionPolicy>`
	disabled := `<DeleteRetentionPolicy><Enabled>false</Enabled></DeleteRetentionPolicy>`
	reservations := []string{"pfx/reservations/a.json", "pfx/reservations/b.json"}

	cases := []struct {
		name string
		fake *fakeAccount
		want storage.Info
	}{
		{
			"everything readable and enabled",
			&fakeAccount{hns: "false", softDelete: enabled, blobs: reservations, versionID: "2026-10-30T00:00:00.0000000Z"},
			storage.Info{Versioning: storage.Yes, SoftDelete: storage.Yes, HierarchicalNamespace: storage.No},
		},
		{
			"nothing enabled",
			&fakeAccount{hns: "false", softDelete: disabled, blobs: reservations},
			storage.Info{Versioning: storage.No, SoftDelete: storage.No, HierarchicalNamespace: storage.No},
		},
		{
			"hierarchical namespace",
			&fakeAccount{hns: "true", softDelete: disabled, blobs: reservations},
			storage.Info{Versioning: storage.No, SoftDelete: storage.No, HierarchicalNamespace: storage.Yes},
		},
		{
			"container-scoped role cannot read service properties or account info",
			&fakeAccount{accountInfo: http.StatusForbidden, serviceProps: http.StatusForbidden, blobs: reservations},
			storage.Info{Versioning: storage.No, SoftDelete: storage.Unknown, HierarchicalNamespace: storage.Unknown},
		},
		{
			"no reservations document to read",
			&fakeAccount{hns: "false", softDelete: enabled},
			storage.Info{Versioning: storage.Unknown, SoftDelete: storage.Yes, HierarchicalNamespace: storage.No},
		},
		{
			"reservations document not readable",
			&fakeAccount{hns: "false", softDelete: enabled, blobs: reservations, head: http.StatusForbidden},
			storage.Info{Versioning: storage.Unknown, SoftDelete: storage.Yes, HierarchicalNamespace: storage.No},
		},
		{
			"listing denied",
			&fakeAccount{hns: "false", softDelete: enabled, list: http.StatusForbidden},
			storage.Info{Versioning: storage.Unknown, SoftDelete: storage.Yes, HierarchicalNamespace: storage.No},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := tc.fake
			store, _ := newTestStore(t, fake.handle)

			info, err := store.Inspect(t.Context())
			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}

			if info != tc.want {
				t.Fatalf("Inspect() = %+v, want %+v", info, tc.want)
			}

			fake.mu.Lock()
			defer fake.mu.Unlock()

			for _, request := range fake.methodsAndURLs {
				if method, _, _ := strings.Cut(request, " "); method != http.MethodGet && method != http.MethodHead {
					t.Fatalf("Inspect sent a non-read request: %s", request)
				}

				if strings.HasPrefix(request, "HEAD ") && !strings.HasSuffix(strings.SplitN(request, "?", 2)[0], "/pfx/reservations/a.json") {
					t.Fatalf("Inspect read %s, want the first reservations document", request)
				}
			}
		})
	}
}

func TestInspectAzureUnexpectedErrorIsReturned(t *testing.T) {
	fake := &fakeAccount{accountInfo: http.StatusBadRequest}
	store, _ := newTestStore(t, fake.handle)

	if _, err := store.Inspect(t.Context()); err == nil {
		t.Fatal("Inspect() error = nil, want the account-info failure")
	}
}
