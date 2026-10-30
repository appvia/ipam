// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/azblob"
	"github.com/appvia/ipam/storage/local"
	"github.com/appvia/ipam/storage/memory"
	"github.com/appvia/ipam/storage/s3"
)

// secretMarker stands in for a credential in every rejected URL. No error may echo it.
const secretMarker = "hunter2-secret-marker"

// azuriteKey is the publicly documented Azurite development key.
const azuriteKey = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="

// isolateEnv blanks every variable the Azure and storage credential chains read,
// so each case starts from a known state.
func isolateEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{
		"ARM_TENANT_ID", "ARM_CLIENT_ID", "ARM_CLIENT_ID_FILE_PATH", "ARM_CLIENT_SECRET",
		"ARM_CLIENT_SECRET_FILE_PATH", "ARM_CLIENT_CERTIFICATE", "ARM_CLIENT_CERTIFICATE_PATH",
		"ARM_CLIENT_CERTIFICATE_PASSWORD", "ARM_OIDC_TOKEN", "ARM_OIDC_TOKEN_FILE_PATH",
		"AZURE_FEDERATED_TOKEN_FILE", "ARM_OIDC_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN",
		"SYSTEM_ACCESSTOKEN", "ARM_OIDC_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_URL",
		"SYSTEM_OIDCREQUESTURI", "ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID",
		"ARM_OIDC_AZURE_SERVICE_CONNECTION_ID", "AZURESUBSCRIPTION_SERVICE_CONNECTION_ID",
		"ARM_USE_OIDC", "ARM_USE_MSI", "ARM_USE_CLI", "ARM_USE_AKS_WORKLOAD_IDENTITY",
		"AZURE_CLIENT_ID", "AZURE_TENANT_ID", "AZURE_STORAGE_CONNECTION_STRING",
	} {
		t.Setenv(name, "")
	}
}

func assertRoundTrip(t *testing.T, store storage.Storage) {
	t.Helper()

	want := []byte(`{"schema_version":1}`)

	created, err := store.CreateIfAbsent(t.Context(), "pools/a.json", want)
	if err != nil {
		t.Fatalf("CreateIfAbsent() error = %v", err)
	}

	got, version, err := store.Get(t.Context(), "pools/a.json")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("Get() = %q, want %q", got, want)
	}

	if version != created {
		t.Fatalf("Get() version = %q, want %q", version, created)
	}
}

func TestOpenRejectsInvalidURLs(t *testing.T) {
	isolateEnv(t)

	long := "s3://bkt/" + strings.Repeat("a", maxPrefixBytes+1)

	tests := []struct {
		name string
		url  string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"leading space", " s3://bkt/p"},
		{"trailing newline", "s3://bkt/p\n"},
		{"raw NUL byte", "s3://bkt/p\x00"},
		{"no scheme", "/tmp/x"},
		{"unknown scheme", "ftp://x"},
		{"opaque form", "s3:bkt/p"},
		{"s3 user info", "s3://user:" + secretMarker + "@bkt/p"},
		{"s3 secret parameter", "s3://bkt/p?secret_key=" + secretMarker},
		{"s3 secret parameter mixed case", "s3://bkt/p?region=eu-west-1&SAS=" + secretMarker},
		{"s3 secret as parameter name", "s3://bkt/p?" + secretMarker},
		{"s3 unknown parameter", "s3://bkt/p?foo=1"},
		{"s3 duplicate parameter", "s3://bkt/p?region=eu-west-1&region=us-east-1"},
		{"s3 path_style not boolean", "s3://bkt/p?path_style=" + secretMarker},
		{"s3 path_style empty", "s3://bkt/p?path_style="},
		{"s3 region empty", "s3://bkt/p?region="},
		{"s3 region invalid", "s3://bkt/p?region=EU_WEST"},
		{"s3 fragment", "s3://bkt/p#" + secretMarker},
		{"s3 endpoint scheme", "s3://bkt/p?region=eu-west-1&endpoint=ftp://s3.example.test"},
		{"s3 endpoint without scheme", "s3://bkt/p?region=eu-west-1&endpoint=s3.example.test"},
		{"s3 endpoint user info", "s3://bkt/p?region=eu-west-1&endpoint=https://u:" + secretMarker + "@s3.example.test"},
		{"s3 endpoint fragment", "s3://bkt/p?region=eu-west-1&endpoint=https://s3.example.test%23" + secretMarker},
		{"s3 endpoint without host", "s3://bkt/p?region=eu-west-1&endpoint=https://"},
		{"s3 endpoint query", "s3://bkt/p?region=eu-west-1&endpoint=https://s3.example.test?token=" + secretMarker},
		{"s3 malformed query", "s3://bkt/p?region=eu-west-1&token=" + secretMarker + "%zz"},
		{"s3 empty bucket", "s3:///p"},
		{"s3 bucket uppercase", "s3://BKT/p"},
		{"s3 bucket with port", "s3://bkt:443/p"},
		{"s3 IPv6 host", "s3://[::1]/p"},
		{"s3 unicode bucket", "s3://bücket/p"},
		{"s3 traversal", "s3://bkt/../etc"},
		{"s3 percent-encoded traversal", "s3://bkt/p/%2e%2e/q"},
		{"s3 double slash", "s3://bkt//p"},
		{"s3 NUL in prefix", "s3://bkt/p%00x?region=eu-west-1"},
		{"s3 backslash in prefix", "s3://bkt/a\\b?region=eu-west-1"},
		{"s3 prefix too long", long},
		{"azblob account uppercase", "azblob://ACCT.blob.core.windows.net/cnt"},
		{"azblob account suffix spoof", "azblob://acct.blob.core.windows.net.evil.example/cnt"},
		{"azblob account foreign host", "azblob://acct.evil.example/cnt"},
		{"azblob account with port", "azblob://acct.blob.core.windows.net:443/cnt"},
		{"azblob account missing container", "azblob://acct.blob.core.windows.net/"},
		{"azblob account user info", "azblob://user:" + secretMarker + "@acct.blob.core.windows.net/cnt"},
		{"azblob account query parameter", "azblob://acct.blob.core.windows.net/cnt?sig=" + secretMarker},
		{"azblob account empty query marker", "azblob://acct.blob.core.windows.net/cnt?"},
		{"azblob container query parameter", "azblob://cnt/p?sas=" + secretMarker},
		{"azblob container empty query marker", "azblob://cnt/p?"},
		{"azblob container too short", "azblob://ab/p"},
		{"azblob container 64 chars", "azblob://" + strings.Repeat("a", 64) + "/p"},
		{"azblob container uppercase", "azblob://UPPER/p"},
		{"azblob container leading hyphen", "azblob://-abc/p"},
		{"azblob container trailing hyphen", "azblob://abc-/p"},
		{"azblob container double hyphen", "azblob://ab--cd/p"},
		{"azblob account name too short", "azblob://ab.blob.core.windows.net/cnt"},
		{"azblob account name too long", "azblob://" + strings.Repeat("a", 25) + ".blob.core.windows.net/cnt"},
		{"azblob account container too short", "azblob://acct.blob.core.windows.net/ab/p"},
		{"azblob account container 64 chars", "azblob://acct.blob.core.windows.net/" + strings.Repeat("a", 64)},
		{"azblob container invalid", "azblob://Bad_Container/p"},
		{"azblob double slash", "azblob://cnt//p"},
		{"file with host", "file://host/tmp/x"},
		{"file relative", "file://tmp/x"},
		{"file opaque", "file:tmp/x"},
		{"file traversal", "file:///tmp/../x"},
		{"file percent-encoded traversal", "file:///tmp/%2e%2e/x"},
		{"file double slash", "file:////tmp/x"},
		{"file query", "file:///tmp/x?token=" + secretMarker},
		{"file fragment", "file:///tmp/x#" + secretMarker},
		{"file user info", "file://u:" + secretMarker + "@/tmp/x"},
		{"mem host", "mem://host"},
		{"mem path", "mem:///x"},
		{"mem query", "mem://?x=1"},
		{"file root", "file:///"},
		{"s3 dot segment", "s3://bkt/./p"},
		{"azblob prefix too long", "azblob://cnt/" + strings.Repeat("a", maxPrefixBytes+1)},
		{"s3 IP-style bucket", "s3://192.168.1.1/p"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The parser itself must reject every row, so a later backend
			// failure (missing region or credential) cannot hide a broken guard.
			if _, perr := parseOpenURL(tc.url); !errors.Is(perr, ErrInvalidArgument) {
				t.Fatalf("parseOpenURL() error = %v, want ErrInvalidArgument", perr)
			}

			store, err := OpenStorage(t.Context(), tc.url)
			if err == nil {
				t.Fatalf("OpenStorage() = %T, want an error", store)
			}

			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
			}

			if strings.Contains(err.Error(), secretMarker) {
				t.Fatalf("OpenStorage() error echoes a secret: %v", err)
			}
		})
	}
}

func TestOpenParsesTargets(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want openTarget
	}{
		{"s3 bucket only", "s3://bkt", openTarget{scheme: "s3", s3: s3.Config{Bucket: "bkt"}}},
		{"s3 trailing slash", "s3://bkt/", openTarget{scheme: "s3", s3: s3.Config{Bucket: "bkt"}}},
		{"s3 nested prefix", "s3://bkt/a/b/", openTarget{scheme: "s3", s3: s3.Config{Bucket: "bkt", Prefix: "a/b"}}},
		{
			"s3 all parameters",
			"s3://bkt/p?region=eu-west-1&endpoint=https://s3.example.test:9000&path_style=true",
			openTarget{scheme: "s3", s3: s3.Config{
				Bucket: "bkt", Prefix: "p", Region: "eu-west-1",
				Endpoint: "https://s3.example.test:9000", UsePathStyle: true,
			}},
		},
		{"s3 explicit path_style false", "s3://bkt?path_style=false", openTarget{scheme: "s3", s3: s3.Config{Bucket: "bkt"}}},
		{
			"azblob account form",
			"azblob://acct.blob.core.windows.net/cnt/p/q",
			openTarget{scheme: "azblob", azure: azblob.Config{
				AccountURL: "https://acct.blob.core.windows.net", Container: "cnt", Prefix: "p/q",
			}},
		},
		{"azblob container form", "azblob://cnt", openTarget{scheme: "azblob", azure: azblob.Config{Container: "cnt"}}},
		{"azblob container 63 chars", "azblob://" + strings.Repeat("a", 63) + "/p", openTarget{scheme: "azblob", azure: azblob.Config{Container: strings.Repeat("a", 63), Prefix: "p"}}},
		{"azblob container interior hyphen", "azblob://my-data/p", openTarget{scheme: "azblob", azure: azblob.Config{Container: "my-data", Prefix: "p"}}},
		{"azblob account 3 chars", "azblob://abc.blob.core.windows.net/cnt", openTarget{scheme: "azblob", azure: azblob.Config{AccountURL: "https://abc.blob.core.windows.net", Container: "cnt"}}},
		{"azblob account 24 chars", "azblob://" + strings.Repeat("a", 24) + ".blob.core.windows.net/cnt", openTarget{scheme: "azblob", azure: azblob.Config{AccountURL: "https://" + strings.Repeat("a", 24) + ".blob.core.windows.net", Container: "cnt"}}},
		{"file absolute", "file:///tmp/x", openTarget{scheme: "file", dir: "/tmp/x"}},
		{"mem", "mem://", openTarget{scheme: "mem"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseOpenURL(tc.url)
			if err != nil {
				t.Fatalf("parseOpenURL() error = %v", err)
			}

			if got.scheme != tc.want.scheme || got.s3 != tc.want.s3 || got.dir != tc.want.dir ||
				got.azure.AccountURL != tc.want.azure.AccountURL ||
				got.azure.Container != tc.want.azure.Container ||
				got.azure.Prefix != tc.want.azure.Prefix {
				t.Fatalf("parseOpenURL() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestOpenFileURLGivesWorkingLocalStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")

	store, err := OpenStorage(t.Context(), (&url.URL{Scheme: "file", Path: dir}).String())
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}

	if _, ok := store.(*local.Store); !ok {
		t.Fatalf("OpenStorage() = %T, want *local.Store", store)
	}

	assertRoundTrip(t, store)
}

func TestOpenMemURLIsUsableStore(t *testing.T) {
	store, err := OpenStorage(t.Context(), "mem://")
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}

	if _, ok := store.(*memory.Store); !ok {
		t.Fatalf("OpenStorage() = %T, want *memory.Store", store)
	}

	assertRoundTrip(t, store)

	client, err := Open(t.Context(), "mem://")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if client == nil {
		t.Fatal("Open() returned a nil client")
	}
}

func TestOpenS3BuildsStoreWithoutRequests(t *testing.T) {
	isolateEnv(t)

	var requests atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAFAKEFAKEFAKEFAKE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "fake-secret")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "absent"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent"))

	raw := "s3://bkt/p?region=eu-west-1&path_style=true&endpoint=" + url.QueryEscape(srv.URL)

	store, err := OpenStorage(t.Context(), raw)
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}

	if _, ok := store.(*s3.Store); !ok {
		t.Fatalf("OpenStorage() = %T, want *s3.Store", store)
	}

	if n := requests.Load(); n != 0 {
		t.Fatalf("construction made %d requests, want 0", n)
	}
}

func TestOpenAzureAccountFormBuildsWithoutNetwork(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ARM_USE_CLI", "false")
	t.Setenv("ARM_USE_MSI", "true")

	store, err := OpenStorage(t.Context(), "azblob://acct.blob.core.windows.net/cnt/p")
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}

	if _, ok := store.(*azblob.Store); !ok {
		t.Fatalf("OpenStorage() = %T, want *azblob.Store", store)
	}
}

func TestOpenAzureAccountFormWithoutCredentialSourceIsRejected(t *testing.T) {
	isolateEnv(t)
	t.Setenv("ARM_USE_CLI", "false")

	_, err := OpenStorage(t.Context(), "azblob://acct.blob.core.windows.net/cnt")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
	}
}

func TestOpenAzureContainerFormAcceptsLoopbackEmulator(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING",
		"DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey="+azuriteKey+
			";BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;")

	store, err := OpenStorage(t.Context(), "azblob://cnt/p")
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}

	if _, ok := store.(*azblob.Store); !ok {
		t.Fatalf("OpenStorage() = %T, want *azblob.Store", store)
	}
}

func TestOpenAzureContainerFormRejectsNonLoopbackConnectionString(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING",
		"DefaultEndpointsProtocol=https;AccountName=prodacct;AccountKey="+azuriteKey+";EndpointSuffix=core.windows.net")

	_, err := OpenStorage(t.Context(), "azblob://cnt/p")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
	}

	if !errors.Is(err, azblob.ErrInvalidArgument) {
		t.Fatalf("OpenStorage() error = %v, want the azblob sentinel kept in the chain", err)
	}

	if strings.Contains(err.Error(), azuriteKey) {
		t.Fatalf("OpenStorage() error echoes the account key: %v", err)
	}
}

func TestOpenAzureContainerFormRejectsEmptyQueryWithEmulator(t *testing.T) {
	isolateEnv(t)
	t.Setenv("AZURE_STORAGE_CONNECTION_STRING",
		"DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey="+azuriteKey+
			";BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;")

	_, err := OpenStorage(t.Context(), "azblob://cnt/p?")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
	}
}

func TestOpenAzureContainerFormRejectsSecretQueryWithEmulator(t *testing.T) {
	for _, raw := range []string{
		"azblob://cnt/p?sas=" + secretMarker,
		"azblob://cnt/p?sv=2020&sig=" + secretMarker,
	} {
		t.Run(raw, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("AZURE_STORAGE_CONNECTION_STRING",
				"DefaultEndpointsProtocol=http;AccountName=devstoreaccount1;AccountKey="+azuriteKey+
					";BlobEndpoint=http://127.0.0.1:10000/devstoreaccount1;")

			_, err := OpenStorage(t.Context(), raw)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
			}

			if strings.Contains(err.Error(), secretMarker) {
				t.Fatalf("OpenStorage() error echoes a secret: %v", err)
			}
		})
	}
}

func TestOpenAzureAccountFormRejectsQueryWithCredentialSource(t *testing.T) {
	for _, raw := range []string{
		"azblob://acct.blob.core.windows.net/cnt/p?sig=" + secretMarker,
		"azblob://acct.blob.core.windows.net/cnt/p?",
	} {
		t.Run(raw, func(t *testing.T) {
			isolateEnv(t)
			t.Setenv("ARM_USE_MSI", "true")
			t.Setenv("ARM_USE_CLI", "false")

			_, err := OpenStorage(t.Context(), raw)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
			}

			if strings.Contains(err.Error(), secretMarker) {
				t.Fatalf("OpenStorage() error echoes a secret: %v", err)
			}
		})
	}
}

func TestOpenS3ConfigurationErrorsAreInvalidArgument(t *testing.T) {
	t.Run("missing region", func(t *testing.T) {
		isolateEnv(t)
		t.Setenv("AWS_REGION", "")
		t.Setenv("AWS_DEFAULT_REGION", "")
		t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "absent"))
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent"))

		_, err := OpenStorage(t.Context(), "s3://bkt/p")
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
		}

		if !strings.Contains(err.Error(), "region is required") {
			t.Fatalf("OpenStorage() error = %v, want the backend message kept", err)
		}
	})
}

// The parser alone must reject these: OpenStorage would also fail later on a
// missing region or credential, which would hide a broken parser guard.
func TestOpenParserRejectsWithoutBackendHelp(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"file root", "file:///"},
		{"s3 dot segment", "s3://bkt/./p"},
		{"azblob prefix too long", "azblob://cnt/" + strings.Repeat("a", maxPrefixBytes+1)},
		{"azblob account prefix too long", "azblob://acct.blob.core.windows.net/cnt/" + strings.Repeat("a", maxPrefixBytes+1)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseOpenURL(tc.url); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("parseOpenURL() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestOpenS3IPStyleBucketIsRejectedByParser(t *testing.T) {
	if _, err := parseOpenURL("s3://192.168.1.1/p"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("parseOpenURL() error = %v, want ErrInvalidArgument", err)
	}
}

func TestOpenAzureContainerFormWithoutConnectionStringIsRejected(t *testing.T) {
	isolateEnv(t)

	_, err := OpenStorage(t.Context(), "azblob://cnt/p")
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("OpenStorage() error = %v, want ErrInvalidArgument", err)
	}
}
