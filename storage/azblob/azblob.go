// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package azblob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"

	"github.com/appvia/ipam/storage"
)

// ErrInvalidArgument reports configuration the backend refuses to use.
var ErrInvalidArgument = errors.New("azblob: invalid argument")

const (
	connectionStringEnv = "AZURE_STORAGE_CONNECTION_STRING"

	// The Azurite well-known development account, published in the Azurite
	// documentation; it is the only shared key the backend will use.
	azuriteAccountName = "devstoreaccount1"
	azuriteAccountKey  = "Eby8vdM02xNOcqFlqUwJPLlmEtlCDXJ1OUzFT50uSRZ6IFsuFq2UVErCz4I6tq/K1SZFPTOtr/KBHBeksoGMGw=="
)

// Config configures an Azure Blob backend.
type Config struct {
	// AccountURL is the blob service endpoint, https://<account>.blob.core.windows.net.
	AccountURL string
	// Container must already exist; the backend never creates or deletes it.
	Container string
	// Prefix is prepended to every key as a virtual directory.
	Prefix string
	// Credential is required unless AZURE_STORAGE_CONNECTION_STRING targets a
	// loopback emulator (Azurite), which is then used with shared-key auth.
	Credential azcore.TokenCredential
	// Transport optionally replaces the HTTP client.
	Transport policy.Transporter

	retry                 func(*policy.RetryOptions)
	parseConnectionString func(string) (string, error)
}

// Store is an Azure Blob Storage implementation of storage.Storage.
type Store struct {
	container *container.Client
	service   *service.Client
	prefix    string
	traced    bool
}

var _ storage.Storage = (*Store)(nil)

// New validates cfg and builds the backend. It performs no network requests.
func New(_ context.Context, cfg Config) (*Store, error) {
	if cfg.Container == "" {
		return nil, fmt.Errorf("%w: container is required", ErrInvalidArgument)
	}

	client, svc, err := newClients(cfg)
	if err != nil {
		return nil, err
	}

	prefix := strings.Trim(cfg.Prefix, "/")
	if prefix != "" {
		prefix += "/"
	}

	return &Store{container: client, service: svc, prefix: prefix, traced: traceable(cfg.Transport)}, nil
}

// newClients builds the container client used for data access and an
// account-level service client that Inspect uses to read blob service
// properties. Both share the same options, credential and endpoint guards.
func newClients(cfg Config) (*container.Client, *service.Client, error) {
	opts := clientOptions(cfg)

	if cfg.Credential != nil {
		accountURL, err := validateAccountURL(cfg.AccountURL)
		if err != nil {
			return nil, nil, err
		}

		client, err := container.NewClient(accountURL+"/"+url.PathEscape(cfg.Container), cfg.Credential, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}

		svc, err := service.NewClient(accountURL, cfg.Credential, (*service.ClientOptions)(opts))
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrInvalidArgument, err)
		}

		return client, svc, nil
	}

	conn := os.Getenv(connectionStringEnv)
	if conn == "" {
		return nil, nil, fmt.Errorf("%w: a credential is required", ErrInvalidArgument)
	}

	parse := cfg.parseConnectionString
	if parse == nil {
		parse = parseAzuriteConnectionString
	}

	endpoint, err := parse(conn)
	if err != nil {
		return nil, nil, err
	}

	if cfg.AccountURL != "" && strings.TrimRight(cfg.AccountURL, "/") != strings.TrimRight(endpoint, "/") {
		return nil, nil, fmt.Errorf("%w: account URL %q does not match the %s blob endpoint",
			ErrInvalidArgument, cfg.AccountURL, connectionStringEnv)
	}

	client, err := container.NewClientFromConnectionString(conn, cfg.Container, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s: %w", ErrInvalidArgument, connectionStringEnv, err)
	}

	svc, err := service.NewClientFromConnectionString(conn, (*service.ClientOptions)(opts))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s: %w", ErrInvalidArgument, connectionStringEnv, err)
	}

	// Check the URLs the SDK itself resolved, so no parsing difference between
	// the guard above and the SDK can send the shared key anywhere else.
	for _, resolved := range []string{client.URL(), svc.URL()} {
		if err := requireLoopbackURL(resolved); err != nil {
			return nil, nil, err
		}
	}

	return client, svc, nil
}

func clientOptions(cfg Config) *container.ClientOptions {
	retry := policy.RetryOptions{
		MaxRetries:    10,
		TryTimeout:    time.Minute,
		RetryDelay:    2 * time.Second,
		MaxRetryDelay: 30 * time.Second,
		ShouldRetry:   shouldRetry,
	}
	if cfg.retry != nil {
		cfg.retry(&retry)
	}

	return &container.ClientOptions{ClientOptions: azcore.ClientOptions{
		Retry:            retry,
		Transport:        cfg.Transport,
		PerRetryPolicies: []policy.Policy{outcomePolicy{}},
	}}
}

func validateAccountURL(raw string) (string, error) {
	u, err := url.Parse(raw)

	switch {
	case raw == "":
		return "", fmt.Errorf("%w: account URL is required", ErrInvalidArgument)
	case err != nil:
		return "", fmt.Errorf("%w: account URL: %w", ErrInvalidArgument, err)
	case u.Scheme != "https" || u.Host == "":
		return "", fmt.Errorf("%w: account URL %q must be https://<account>.blob.core.windows.net",
			ErrInvalidArgument, raw)
	case u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		return "", fmt.Errorf("%w: account URL must not carry a query, fragment or user info (no SAS tokens or keys)",
			ErrInvalidArgument)
	}

	return strings.TrimRight(u.String(), "/"), nil
}

// connectionStringKeys are the keys the SDK's connection string parser knows.
var connectionStringKeys = []string{
	"DefaultEndpointsProtocol", "AccountName", "AccountKey", "BlobEndpoint", "EndpointSuffix",
	"SharedAccessSignature", "QueueEndpoint", "TableEndpoint", "FileEndpoint",
	"UseDevelopmentStorage", "DevelopmentStorageProxyUri",
}

// parseAzuriteConnectionString accepts only the Azurite development account
// with an explicit BlobEndpoint. It splits like the SDK parser (exact,
// case-sensitive keys, trailing semicolons trimmed) and rejects every input a
// looser reading could interpret differently: duplicate keys, and known keys
// with other casing or surrounding whitespace, which the SDK would ignore and
// then fall back to https://<AccountName>.blob.core.windows.net.
func parseAzuriteConnectionString(conn string) (string, error) {
	invalid := func(reason string) error {
		return fmt.Errorf("%w: %s %s; shared-key auth is only accepted for the Azurite development "+
			"account on a loopback BlobEndpoint, use Azure AD authentication for real storage accounts",
			ErrInvalidArgument, connectionStringEnv, reason)
	}

	fields := make(map[string]string)

	for part := range strings.SplitSeq(strings.TrimRight(conn, ";"), ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return "", invalid("is malformed")
		}

		for _, known := range connectionStringKeys {
			if key != known && strings.EqualFold(strings.TrimSpace(key), known) {
				return "", invalid(fmt.Sprintf("has key %q instead of %q", key, known))
			}
		}

		if _, duplicate := fields[key]; duplicate {
			return "", invalid(fmt.Sprintf("repeats key %q", key))
		}

		fields[key] = value
	}

	_, hasSAS := fields["SharedAccessSignature"]

	switch {
	case fields["AccountName"] != azuriteAccountName || fields["AccountKey"] != azuriteAccountKey || hasSAS:
		return "", invalid("does not use the Azurite development account key")
	case fields["BlobEndpoint"] == "":
		return "", invalid("has no BlobEndpoint")
	}

	if err := requireLoopbackURL(fields["BlobEndpoint"]); err != nil {
		return "", err
	}

	return fields["BlobEndpoint"], nil
}

func requireLoopbackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || !isLoopback(u.Hostname()) {
		return fmt.Errorf("%w: %s must target a loopback endpoint (localhost, 127.0.0.0/8 or ::1)",
			ErrInvalidArgument, connectionStringEnv)
	}

	return nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}

	addr, err := netip.ParseAddr(host)

	return err == nil && ((addr.Is4() && addr.IsLoopback()) || addr == netip.IPv6Loopback())
}

func (s *Store) name(key string) string { return s.prefix + key }

// Get returns the blob's data and its ETag, verbatim.
func (s *Store) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	resp, err := s.container.NewBlobClient(s.name(key)).DownloadStream(ctx, nil)
	if err != nil {
		return nil, "", fmt.Errorf("azblob get %q: %w", key, mapError(err))
	}

	data, readErr := io.ReadAll(resp.Body)
	if err := errors.Join(readErr, resp.Body.Close()); err != nil {
		return nil, "", fmt.Errorf("azblob get %q: read body: %w", key, err)
	}

	if resp.ETag == nil {
		return nil, "", fmt.Errorf("azblob get %q: response has no ETag", key)
	}

	return data, storage.Version(*resp.ETag), nil
}

// CreateIfAbsent uploads the blob with If-None-Match: *.
func (s *Store) CreateIfAbsent(ctx context.Context, key string, data []byte) (storage.Version, error) {
	return s.upload(ctx, "create", key, data, &blob.ModifiedAccessConditions{IfNoneMatch: to.Ptr(azcore.ETagAny)})
}

// PutIfMatch replaces the blob only when its ETag equals version.
func (s *Store) PutIfMatch(
	ctx context.Context,
	key string,
	data []byte,
	version storage.Version,
) (storage.Version, error) {
	if !usableVersion(version) {
		return "", fmt.Errorf("azblob put %q: version %q: %w", key, version, storage.ErrPreconditionFailed)
	}

	return s.upload(ctx, "put", key, data, &blob.ModifiedAccessConditions{IfMatch: to.Ptr(azcore.ETag(version))})
}

func (s *Store) upload(
	ctx context.Context,
	op, key string,
	data []byte,
	conditions *blob.ModifiedAccessConditions,
) (storage.Version, error) {
	var etag *azcore.ETag

	err := s.write(ctx, op, key, func(ctx context.Context) error {
		resp, err := s.container.NewBlockBlobClient(s.name(key)).UploadBuffer(ctx, data,
			&blockblob.UploadBufferOptions{
				AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: conditions},
			})
		etag = resp.ETag

		return err
	})
	if err != nil {
		return "", err
	}

	if etag == nil {
		return "", fmt.Errorf("azblob %s %q: response has no ETag", op, key)
	}

	return storage.Version(*etag), nil
}

// DeleteIfMatch deletes the blob only when its ETag equals version.
func (s *Store) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	if !usableVersion(version) {
		return fmt.Errorf("azblob delete %q: version %q: %w", key, version, storage.ErrPreconditionFailed)
	}

	return s.write(ctx, "delete", key, func(ctx context.Context) error {
		_, err := s.container.NewBlobClient(s.name(key)).Delete(ctx, &blob.DeleteOptions{
			AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: &blob.ModifiedAccessConditions{
				IfMatch: to.Ptr(azcore.ETag(version)),
			}},
		})

		return err
	})
}

// usableVersion rejects versions that would weaken If-Match: an empty header
// may be ignored and "*" matches any existing blob.
func usableVersion(version storage.Version) bool {
	return version != "" && version != storage.Version(azcore.ETagAny)
}

// List returns the keys under prefix, sorted, with Config.Prefix stripped.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	opts := &container.ListBlobsFlatOptions{}
	if full := s.name(prefix); full != "" {
		opts.Prefix = &full
	}

	keys := []string{}
	pager := s.container.NewListBlobsFlatPager(opts)

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("azblob list %q: %w", prefix, mapError(err))
		}

		for _, item := range page.Segment.BlobItems {
			if item.Name != nil {
				keys = append(keys, strings.TrimPrefix(*item.Name, s.prefix))
			}
		}
	}

	sort.Strings(keys)

	return keys, nil
}

// reservationsPrefix is where the ipam client keeps reservations documents;
// Inspect reads one of them to detect blob versioning.
const reservationsPrefix = "reservations/"

// Inspect reports the account's hierarchical namespace (GetAccountInfo), blob
// soft delete (blob service properties) and blob versioning. Versioning is
// configured through Azure Resource Manager and is not visible to data-plane
// roles, so it is detected from data access: a reservations document whose
// properties carry an x-ms-version-id is versioned. Every request is a read; a
// property the caller may not read is storage.Unknown.
func (s *Store) Inspect(ctx context.Context) (storage.Info, error) {
	var info storage.Info

	account, err := s.container.GetAccountInfo(ctx, nil)
	if err != nil && !errors.Is(mapError(err), storage.ErrAccessDenied) {
		return storage.Info{}, fmt.Errorf("azblob get account info: %w", mapError(err))
	}

	if err == nil {
		info.HierarchicalNamespace = triState(account.IsHierarchicalNamespaceEnabled)
	}

	props, err := s.service.GetProperties(ctx, nil)
	if err != nil && !errors.Is(mapError(err), storage.ErrAccessDenied) {
		return storage.Info{}, fmt.Errorf("azblob get service properties: %w", mapError(err))
	}

	if err == nil && props.DeleteRetentionPolicy != nil {
		info.SoftDelete = triState(props.DeleteRetentionPolicy.Enabled)
	}

	info.Versioning, err = s.versioning(ctx)
	if err != nil {
		return storage.Info{}, err
	}

	return info, nil
}

func (s *Store) versioning(ctx context.Context) (storage.TriState, error) {
	keys, err := s.List(ctx, reservationsPrefix)
	if errors.Is(err, storage.ErrAccessDenied) {
		return storage.Unknown, nil
	}

	if err != nil {
		return storage.Unknown, err
	}

	for _, key := range keys {
		if !strings.HasSuffix(key, ".json") {
			continue
		}

		props, err := s.container.NewBlobClient(s.name(key)).GetProperties(ctx, nil)
		if err != nil {
			mapped := mapError(err)
			if errors.Is(mapped, storage.ErrAccessDenied) || errors.Is(mapped, storage.ErrNotFound) {
				return storage.Unknown, nil
			}

			return storage.Unknown, fmt.Errorf("azblob get properties %q: %w", key, mapped)
		}

		if props.VersionID != nil && *props.VersionID != "" {
			return storage.Yes, nil
		}

		return storage.No, nil
	}

	return storage.Unknown, nil
}

func triState(value *bool) storage.TriState {
	switch {
	case value == nil:
		return storage.Unknown
	case *value:
		return storage.Yes
	default:
		return storage.No
	}
}
