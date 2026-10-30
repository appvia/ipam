// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package ipam

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/appvia/ipam/storage"
	"github.com/appvia/ipam/storage/azblob"
	"github.com/appvia/ipam/storage/local"
	"github.com/appvia/ipam/storage/memory"
	"github.com/appvia/ipam/storage/s3"
)

const maxPrefixBytes = 512

const azureBlobSuffix = ".blob.core.windows.net"

var (
	s3BucketPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	s3RegionPattern       = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	azureAccountPattern   = regexp.MustCompile(`^[a-z0-9]{3,24}$`)
	azureContainerPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

// openTarget is a parsed backend URL. Credentials never appear in it; they
// come from the environment.
type openTarget struct {
	scheme string
	s3     s3.Config
	dir    string
	azure  azblob.Config
}

// OpenStorage builds the storage backend named by rawURL. Supported forms:
//
//	s3://<bucket>/<prefix>?region=..&endpoint=..&path_style=true
//	azblob://<account>.blob.core.windows.net/<container>/<prefix>  (ARM_* credentials)
//	azblob://<container>/<prefix>                                  (AZURE_STORAGE_CONNECTION_STRING)
//	file:///abs/path
//	mem://                                                         (tests only)
//
// Unknown schemes, credentials in the URL and malformed values are rejected
// with ErrInvalidArgument. Error text never echoes the URL.
func OpenStorage(ctx context.Context, rawURL string) (storage.Storage, error) {
	target, err := parseOpenURL(rawURL)
	if err != nil {
		return nil, err
	}

	switch target.scheme {
	case "file":
		store, err := local.New(target.dir)
		if err != nil {
			return nil, err
		}

		return store, nil
	case "mem":
		return memory.New(), nil
	case "s3":
		store, err := s3.New(ctx, target.s3)
		if err != nil {
			return nil, backendArgumentError(err)
		}

		return store, nil
	default:
		return openAzure(ctx, target.azure)
	}
}

// Open builds the storage backend named by rawURL and wraps it in a Client.
func Open(ctx context.Context, rawURL string, opts ...Option) (*Client, error) {
	store, err := OpenStorage(ctx, rawURL)
	if err != nil {
		return nil, err
	}

	return New(store, opts...), nil
}

func openAzure(ctx context.Context, cfg azblob.Config) (storage.Storage, error) {
	if cfg.AccountURL != "" {
		cred, err := azblob.AuthConfigFromEnv().Credential(nil)
		if err != nil {
			if !errors.Is(err, azblob.ErrInvalidArgument) {
				err = fmt.Errorf("azblob credential: %w", err)
			}

			return nil, backendArgumentError(err)
		}

		cfg.Credential = cred
	}

	store, err := azblob.New(ctx, cfg)
	if err != nil {
		return nil, backendArgumentError(err)
	}

	return store, nil
}

// backendArgumentError reports a backend constructor error as
// ErrInvalidArgument. The backend's own "azblob: invalid argument" sentinel
// text is folded into "azblob:" so the message says "invalid argument" once;
// errors.Is still matches both sentinels.
func backendArgumentError(err error) error {
	message := err.Error()
	if rest, ok := strings.CutPrefix(message, azblob.ErrInvalidArgument.Error()+": "); ok {
		message = "azblob: " + rest
	}

	return &argumentError{message: message, err: err}
}

type argumentError struct {
	message string
	err     error
}

func (e *argumentError) Error() string { return ErrInvalidArgument.Error() + ": " + e.message }

func (e *argumentError) Unwrap() []error { return []error{ErrInvalidArgument, e.err} }

func invalidURL(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidArgument, reason)
}

func parseOpenURL(raw string) (openTarget, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || hasControl(raw) {
		return openTarget{}, invalidURL("URL is empty or has surrounding or control characters")
	}

	if strings.Contains(raw, "#") {
		return openTarget{}, invalidURL("URL must not carry a fragment")
	}

	u, err := url.Parse(raw)
	if err != nil {
		return openTarget{}, invalidURL("URL is malformed")
	}

	if u.Scheme == "" || u.Opaque != "" {
		return openTarget{}, invalidURL("URL must have the form scheme://...")
	}

	if u.User != nil {
		return openTarget{}, invalidURL("URL must not carry user info; credentials come from the environment")
	}

	switch u.Scheme {
	case "s3":
		return parseS3(u)
	case "azblob":
		return parseAzblob(u)
	case "file":
		return parseFile(u)
	case "mem":
		return parseMem(u)
	}

	return openTarget{}, invalidURL("unsupported URL scheme")
}

func parseS3(u *url.URL) (openTarget, error) {
	if !s3BucketPattern.MatchString(u.Host) || net.ParseIP(u.Host) != nil {
		return openTarget{}, invalidURL("s3 URL needs a valid bucket name as host")
	}

	prefix, err := blobPath(u.Path)
	if err != nil {
		return openTarget{}, err
	}

	if len(prefix) > maxPrefixBytes {
		return openTarget{}, invalidURL("s3 prefix is too long")
	}

	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return openTarget{}, invalidURL("s3 query is malformed")
	}

	cfg := s3.Config{Bucket: u.Host, Prefix: prefix}

	for key, values := range query {
		if len(values) != 1 {
			return openTarget{}, invalidURL("s3 query parameter is repeated")
		}

		value := values[0]

		switch key {
		case "region":
			if !s3RegionPattern.MatchString(value) {
				return openTarget{}, invalidURL("s3 region is invalid")
			}

			cfg.Region = value
		case "endpoint":
			if err := validateEndpoint(value); err != nil {
				return openTarget{}, err
			}

			cfg.Endpoint = value
		case "path_style":
			switch value {
			case "true":
				cfg.UsePathStyle = true
			case "false":
			default:
				return openTarget{}, invalidURL("s3 path_style must be true or false")
			}
		default:
			return openTarget{}, invalidURL("s3 URL has an unknown query parameter")
		}
	}

	return openTarget{scheme: "s3", s3: cfg}, nil
}

func parseAzblob(u *url.URL) (openTarget, error) {
	if u.RawQuery != "" || u.ForceQuery {
		return openTarget{}, invalidURL("azblob URL must not carry a query; SAS tokens and keys stay out of URLs")
	}

	if strings.Contains(u.Host, ":") {
		return openTarget{}, invalidURL("azblob host must not carry a port")
	}

	rel, err := blobPath(u.Path)
	if err != nil {
		return openTarget{}, err
	}

	if strings.Contains(u.Host, ".") {
		label, ok := strings.CutSuffix(u.Host, azureBlobSuffix)
		if !ok || !azureAccountPattern.MatchString(label) {
			return openTarget{}, invalidURL("azblob account host must be <account>.blob.core.windows.net")
		}

		container, prefix, _ := strings.Cut(rel, "/")
		if err := checkAzureNames(container, prefix); err != nil {
			return openTarget{}, err
		}

		return openTarget{scheme: "azblob", azure: azblob.Config{
			AccountURL: "https://" + u.Host,
			Container:  container,
			Prefix:     prefix,
		}}, nil
	}

	if err := checkAzureNames(u.Host, rel); err != nil {
		return openTarget{}, err
	}

	return openTarget{scheme: "azblob", azure: azblob.Config{Container: u.Host, Prefix: rel}}, nil
}

func checkAzureNames(container, prefix string) error {
	if len(container) < 3 || len(container) > 63 || !azureContainerPattern.MatchString(container) {
		return invalidURL("azblob container name is invalid")
	}

	if len(prefix) > maxPrefixBytes {
		return invalidURL("azblob prefix is too long")
	}

	return nil
}

func parseFile(u *url.URL) (openTarget, error) {
	if u.Host != "" || u.RawQuery != "" || u.ForceQuery {
		return openTarget{}, invalidURL("file URL must be file:///absolute/path")
	}

	rel, err := blobPath(u.Path)
	if err != nil {
		return openTarget{}, err
	}

	if rel == "" || !strings.HasPrefix(u.Path, "/") {
		return openTarget{}, invalidURL("file URL needs an absolute path")
	}

	return openTarget{scheme: "file", dir: "/" + rel}, nil
}

func parseMem(u *url.URL) (openTarget, error) {
	if u.Host != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery {
		return openTarget{}, invalidURL("mem URL takes no host, path or query")
	}

	return openTarget{scheme: "mem"}, nil
}

func blobPath(p string) (string, error) {
	rel := strings.TrimSuffix(strings.TrimPrefix(p, "/"), "/")
	if rel == "" {
		return "", nil
	}

	if strings.ContainsRune(rel, '\\') || hasControl(rel) {
		return "", invalidURL("path contains a backslash or control character")
	}

	for _, seg := range strings.Split(rel, "/") {
		if seg == "" {
			return "", invalidURL("path contains an empty segment ('//'); a trailing slash in a variable " +
				"such as $TMPDIR can cause it, so strip it, for example with ${TMPDIR%/}")
		}

		if seg == "." || seg == ".." {
			return "", invalidURL("path has an empty, dot or dot-dot segment")
		}
	}

	return rel, nil
}

func validateEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || strings.Contains(raw, "#") ||
		(u.Scheme != "http" && u.Scheme != "https") ||
		u.Host == "" || u.User != nil || u.RawQuery != "" || u.Opaque != "" {
		return invalidURL("s3 endpoint must be an http or https URL without user info, query or fragment")
	}

	return nil
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}

	return false
}
