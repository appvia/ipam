// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

func invalidOpenURLs() []struct{ name, url string } {
	long := "s3://bkt/" + strings.Repeat("a", maxPrefixBytes+1)

	return []struct{ name, url string }{
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
		{"malformed path escape", "s3://bkt/%zz"},
		{"azblob container with port", "azblob://cnt:10000/p"},
		{"azblob account prefix too long", "azblob://acct.blob.core.windows.net/cnt/" + strings.Repeat("a", maxPrefixBytes+1)},
		{"file empty query marker", "file:///synthetic/store?"},
		{"mem empty query marker", "mem://?"},
		{"file encoded backslash", "file:///synthetic/a%5Cb"},
		{"azblob encoded backslash", "azblob://cnt/a%5Cb"},
		{"s3 trailing double slash", "s3://bkt/a//"},
		{"azblob account trailing double slash", "azblob://acct.blob.core.windows.net/cnt//"},
	}
}

func TestOpenRejectsInvalidURLs(t *testing.T) {
	isolateEnv(t)

	for _, tc := range invalidOpenURLs() {
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
			}, s3EndpointHost: "s3.example.test"},
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

			if got.scheme != tc.want.scheme || got.s3 != tc.want.s3 || got.s3EndpointHost != tc.want.s3EndpointHost || got.dir != tc.want.dir ||
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

	store, err := OpenStorage(t.Context(), fileURL(dir))
	if err != nil {
		t.Fatalf("OpenStorage() error = %v", err)
	}

	localStore, ok := store.(*local.Store)
	if !ok {
		t.Fatalf("OpenStorage() = %T, want *local.Store", store)
	}

	t.Cleanup(func() { _ = localStore.Close() })

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

// fileURL turns a directory into a file URL that parses on every OS: on Windows
// C:\dir becomes file:///C:/dir.
func fileURL(dir string) string {
	path := filepath.ToSlash(dir)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestFileDir(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rel     string
		windows bool
		want    string
	}{
		{"posix path", "tmp/x", false, "/tmp/x"},
		{"posix drive-like path stays rooted", "C:/Users/x", false, "/C:/Users/x"},
		{"windows drive path", "C:/Users/x", true, "C:/Users/x"},
		{"windows lower-case drive", "d:/data", true, "d:/data"},
		{"windows path without a drive", "tmp/x", true, "/tmp/x"},
		{"windows bare drive", "C:", true, "/C:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := fileDir(tc.rel, tc.windows); got != tc.want {
				t.Fatalf("fileDir(%q, %v) = %q, want %q", tc.rel, tc.windows, got, tc.want)
			}
		})
	}
}

func assertBackendClassification(t *testing.T, raw string, want BackendClassification) {
	t.Helper()

	got, err := ClassifyBackend(raw)
	if err != nil || got != want {
		t.Fatalf("ClassifyBackend() = %q, %v; want %q, nil", got, err, want)
	}
}

func TestBackendClassificationDefaultS3(t *testing.T) {
	t.Run("AC-1", func(t *testing.T) {
		if BackendVerified != "verified" || BackendUnverified != "unverified" {
			t.Fatal("classification values do not match the public contract")
		}

		for _, raw := range []string{
			"s3://bkt", "s3://bkt/p", "s3://bkt/p?region=eu-west-1&path_style=true",
		} {
			assertBackendClassification(t, raw, BackendVerified)
		}
	})
}

func TestBackendClassificationAWSSuffixes(t *testing.T) {
	t.Run("AC-2", func(t *testing.T) {
		for _, endpoint := range []string{
			"https://s3.eu-west-1.amazonaws.com",
			"https://s3.cn-north-1.amazonaws.com.cn",
			"https://S3.EU-WEST-1.AMAZONAWS.COM:443/path",
			"http://S3.CN-NORTH-1.AMAZONAWS.COM.CN:9000/path",
		} {
			t.Run(endpoint, func(t *testing.T) {
				assertBackendClassification(t, "s3://bkt/p?endpoint="+url.QueryEscape(endpoint), BackendVerified)
			})
		}
	})
}

func TestVerifiedBackendCompatibility(t *testing.T) {
	t.Run("AC-3", func(t *testing.T) {
		for _, raw := range []string{
			"azblob://acct.blob.core.windows.net/cnt/p", "file:///synthetic/store", "mem://",
		} {
			assertBackendClassification(t, raw, BackendVerified)
		}

		t.Run("memory compatibility", TestOpenMemURLIsUsableStore)
		t.Run("file compatibility", TestOpenFileURLGivesWorkingLocalStore)
		t.Run("Azure account compatibility", TestOpenAzureAccountFormBuildsWithoutNetwork)
	})
}

func TestUnverifiedClassificationIndependentOfLocation(t *testing.T) {
	t.Run("AC-4", func(t *testing.T) {
		for _, bucket := range []string{"bkt", "amazonaws.com", "other-bucket"} {
			for _, prefix := range []string{"", "/amazonaws.com.cn", "/nested/prefix"} {
				for _, style := range []string{"", "&path_style=true", "&path_style=false"} {
					raw := "s3://" + bucket + prefix + "?endpoint=https://store.example.test:9000/path" + style
					assertBackendClassification(t, raw, BackendUnverified)
				}
			}
		}
	})
}

func TestBackendClassificationRejectsSuffixLookalikes(t *testing.T) {
	t.Run("AC-25", func(t *testing.T) {
		for _, endpoint := range []string{
			"https://amazonaws.com.example.test", "https://amazonaws.com.cn.example.test",
			"https://notamazonaws.com", "https://notamazonaws.com.cn",
			"https://amazonaws.com", "https://amazonaws.com.cn",
			"https://.amazonaws.com", "https://.amazonaws.com.cn",
			"https://store.example.test/amazonaws.com", "https://store.example.test/amazonaws.com.cn",
			"https://s3.amazonaws.com.", "https://s3.amazonaws.com.cn.",
			"http://192.0.2.1:9000/amazonaws.com", "http://[2001:db8::1]:9000/amazonaws.com.cn",
			"http://[fe80::1%25zone.amazonaws.com]:9000",
		} {
			t.Run(endpoint, func(t *testing.T) {
				assertBackendClassification(t, "s3://bkt/p?endpoint="+url.QueryEscape(endpoint), BackendUnverified)
			})
		}
	})
}

func TestClassifyBackendWithoutOpening(t *testing.T) {
	t.Run("AC-8", func(t *testing.T) {
		// No region or credential configuration is required for classification.
		assertBackendClassification(t, "s3://bkt", BackendVerified)
		assertBackendClassification(t, "s3://bkt?endpoint=https://store.example.test", BackendUnverified)
		assertClassificationIsPure(t)
	})
}

func TestClassificationAndOpenHaveNoIO(t *testing.T) {
	t.Run("AC-17/classification", func(t *testing.T) {
		assertClassificationIsPure(t)

		dir := filepath.Join(t.TempDir(), "not-created")
		for _, tc := range []struct {
			raw  string
			want BackendClassification
		}{
			{"mem://", BackendVerified},
			{fileURL(dir), BackendVerified},
			{"s3://bkt", BackendVerified},
			{"s3://bkt?endpoint=https://s3.amazonaws.com", BackendVerified},
			{"s3://bkt?endpoint=https://store.example.test", BackendUnverified},
			{"azblob://acct.blob.core.windows.net/cnt", BackendVerified},
			{"azblob://cnt", BackendUnverified},
		} {
			assertBackendClassification(t, tc.raw, tc.want)
		}

		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("classification created a local store: stat error = %v", err)
		}
	})
}

// Audit the entire classification call graph, including validation, rather than
// adding mutable constructor/credential hooks to a pure public API. Only pure
// standard-library calls and recursively checked local functions are allowed.
func assertClassificationIsPure(t *testing.T) {
	t.Helper()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "open.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	functions := make(map[string]*ast.FuncDecl)

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
			functions[fn.Name.Name] = fn
		}
	}

	allowed := map[string]bool{
		"len": true, "fmt.Errorf": true, "net.ParseIP": true,
		"url.Parse": true, "url.ParseQuery": true,
		"strings.TrimSpace": true, "strings.Contains": true, "strings.ContainsRune": true,
		"strings.HasPrefix": true, "strings.HasSuffix": true, "strings.ToLower": true,
		"strings.TrimSuffix": true, "strings.TrimPrefix": true, "strings.Split": true,
		"strings.Cut": true, "strings.CutSuffix": true,
		"s3BucketPattern.MatchString": true, "s3RegionPattern.MatchString": true,
		"azureAccountPattern.MatchString": true, "azureContainerPattern.MatchString": true,
		"u.Hostname": true, "endpoint.Hostname": true,
	}
	visited := make(map[string]bool)

	var check func(string)

	check = func(name string) {
		if visited[name] {
			return
		}

		visited[name] = true

		fn, ok := functions[name]
		if !ok {
			t.Fatalf("classification function %s not found in open.go", name)
		}

		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}

			callee := ""

			switch fun := call.Fun.(type) {
			case *ast.Ident:
				callee = fun.Name
			case *ast.SelectorExpr:
				if receiver, ok := fun.X.(*ast.Ident); ok {
					callee = receiver.Name + "." + fun.Sel.Name
				}
			}

			if _, ok := functions[callee]; ok {
				check(callee)
			} else if !allowed[callee] {
				t.Errorf("classification can reach non-pure call %q at %s; want zero constructors, credential reads, requests or diagnostic writes", callee, fset.Position(call.Pos()))
			}

			return true
		})
	}

	check("ClassifyBackend")
}

func TestClassificationNeverLearnsOrPromotes(t *testing.T) {
	t.Run("AC-18", func(t *testing.T) {
		var requests atomic.Int64

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			w.Header().Set("ETag", `"synthetic-version"`)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		t.Setenv("AWS_ACCESS_KEY_ID", "dummy-access-key")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "dummy-secret-key")
		t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
		t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "absent"))
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "absent"))

		raw := "s3://bkt/p?region=eu-west-1&path_style=true&endpoint=" + url.QueryEscape(srv.URL)
		for _, ctx := range []context.Context{t.Context(), WithAllowUnverifiedBackend(t.Context()), t.Context()} {
			assertBackendClassification(t, raw, BackendUnverified)

			store, err := OpenStorage(ctx, raw)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := store.CreateIfAbsent(ctx, "synthetic.json", []byte(`{}`)); err != nil {
				t.Fatal(err)
			}

			assertBackendClassification(t, raw, BackendUnverified)
		}

		if got := requests.Load(); got != 3 {
			t.Fatalf("successful storage uses = %d, want 3", got)
		}
	})
}

func TestConsentNeverBypassesURLValidation(t *testing.T) {
	t.Run("AC-23", func(t *testing.T) {
		for _, tc := range invalidOpenURLs() {
			t.Run(tc.name, func(t *testing.T) {
				classification, err := ClassifyBackend(tc.url)
				if classification != "" || !errors.Is(err, ErrInvalidArgument) {
					t.Fatalf("ClassifyBackend() = %q, %v; want empty result and ErrInvalidArgument", classification, err)
				}

				if strings.Contains(err.Error(), secretMarker) {
					t.Fatal("classification error echoes a secret marker")
				}

				for _, ctx := range []context.Context{t.Context(), WithAllowUnverifiedBackend(t.Context())} {
					store, err := OpenStorage(ctx, tc.url)
					if store != nil || !errors.Is(err, ErrInvalidArgument) {
						t.Fatalf("OpenStorage() = %T, %v; want nil and ErrInvalidArgument", store, err)
					}

					client, err := Open(ctx, tc.url)
					if client != nil || !errors.Is(err, ErrInvalidArgument) {
						t.Fatalf("Open() = %v, %v; want nil and ErrInvalidArgument", client, err)
					}
				}
			})
		}
	})
}

func TestOpeningConsentContextPreservesParent(t *testing.T) {
	type parentKey struct{}

	parent, cancel := context.WithDeadline(context.WithValue(t.Context(), parentKey{}, "synthetic-value"), time.Now().Add(time.Hour))
	t.Cleanup(cancel)

	derived := WithAllowUnverifiedBackend(parent)
	if parent.Value(allowUnverifiedBackendKey{}) != nil || t.Context().Value(allowUnverifiedBackendKey{}) != nil {
		t.Fatal("consent mutated the parent or an unrelated context")
	}

	if enabled, _ := derived.Value(allowUnverifiedBackendKey{}).(bool); !enabled {
		t.Fatal("derived context has no consent")
	}

	if got := derived.Value(parentKey{}); got != "synthetic-value" {
		t.Fatalf("derived context value = %v, want parent value", got)
	}

	wantDeadline, _ := parent.Deadline()
	if deadline, ok := derived.Deadline(); !ok || !deadline.Equal(wantDeadline) {
		t.Fatal("derived context did not preserve the deadline")
	}

	cancel()

	if !errors.Is(derived.Err(), context.Canceled) {
		t.Fatalf("derived context error = %v, want context.Canceled", derived.Err())
	}

	select {
	case <-derived.Done():
	default:
		t.Fatal("derived context did not preserve cancellation")
	}
}

func TestAzureContainerFormUnverifiedPolicy(t *testing.T) {
	t.Run("AC-3/classification", func(t *testing.T) {
		for _, connection := range []string{"", "synthetic-invalid-connection-string"} {
			t.Setenv("AZURE_STORAGE_CONNECTION_STRING", connection)
			assertBackendClassification(t, "azblob://cnt/p", BackendUnverified)
			assertBackendClassification(t, "azblob://acct.blob.core.windows.net/cnt/p", BackendVerified)
		}
	})
}

func TestUnverifiedBackendSentinel(t *testing.T) {
	if got := ErrUnverifiedBackend.Error(); got != "ipam: unverified backend" {
		t.Fatalf("sentinel text = %q", got)
	}

	err := fmt.Errorf("opening: %w", ErrUnverifiedBackend)
	if !errors.Is(err, ErrUnverifiedBackend) {
		t.Fatal("wrapped sentinel lost its identity")
	}

	for _, other := range []error{
		ErrInvalidArgument, ErrConflict, storage.ErrNotFound, storage.ErrPreconditionFailed,
		storage.ErrAccessDenied, storage.ErrUnknownOutcome,
	} {
		if errors.Is(err, other) {
			t.Fatalf("sentinel unexpectedly matches %v", other)
		}
	}
}
