// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package s3_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/appvia/ipam/storage"
)

func versioningBody(status string) func(http.ResponseWriter, *http.Request, int) {
	return func(w http.ResponseWriter, _ *http.Request, _ int) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+
			`<VersioningConfiguration xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`+status+`</VersioningConfiguration>`)
	}
}

func TestInspectBucketVersioning(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		respond func(http.ResponseWriter, *http.Request, int)
		want    storage.TriState
		wantErr bool
	}{
		{"enabled", versioningBody("<Status>Enabled</Status>"), storage.Yes, false},
		{"suspended", versioningBody("<Status>Suspended</Status>"), storage.No, false},
		{"never enabled", versioningBody(""), storage.No, false},
		{"access denied", status(http.StatusForbidden, "AccessDenied"), storage.Unknown, false},
		{"missing bucket", status(http.StatusNotFound, "NoSuchBucket"), storage.Unknown, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := newFake(t, tc.respond)
			store := newStore(t, fake.server.URL, "root/")

			info, err := store.(storage.Inspector).Inspect(t.Context())
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Inspect() = %+v, want an error", info)
				}

				return
			}

			if err != nil {
				t.Fatalf("Inspect() error = %v", err)
			}

			want := storage.Info{Versioning: tc.want, SoftDelete: storage.No, HierarchicalNamespace: storage.No}
			if info != want {
				t.Fatalf("Inspect() = %+v, want %+v", info, want)
			}

			req := fake.request(t, 0)
			if req.Method != http.MethodGet || req.Path != "/"+testBucket || req.Query != "versioning=" && req.Query != "versioning" {
				t.Fatalf("request = %s %s?%s, want GET /%s?versioning", req.Method, req.Path, req.Query, testBucket)
			}

			if n := fake.count(); n != 1 {
				t.Fatalf("requests = %d, want 1 (GetBucketVersioning only, no writes)", n)
			}
		})
	}
}
