// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

// Package s3 implements storage.Storage on Amazon S3 (or an S3-compatible
// endpoint) using conditional writes: PutObject with If-None-Match/If-Match
// and DeleteObject with If-Match.
//
// Credentials come from the AWS SDK default chain only. Versions are the
// object ETags, returned and sent verbatim.
package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/appvia/ipam/storage"
)

const (
	propagationMaxAttempts = 10
	propagationMaxBackoff  = 30 * time.Second
)

// Config configures the S3 backend.
type Config struct {
	// Bucket is the bucket name. Required.
	Bucket string
	// Prefix is prepended to every key; a trailing "/" is added when missing.
	// Keys returned by List have it stripped.
	Prefix string
	// Region is the bucket region. When empty it is taken from the SDK
	// environment (AWS_REGION, shared config); it is never looked up via IMDS.
	Region string
	// Endpoint overrides the S3 endpoint, for S3-compatible servers.
	Endpoint string
	// UsePathStyle selects path-style addressing (bucket in the path).
	UsePathStyle bool
}

// Store is an S3-backed storage.Storage.
type Store struct {
	client *s3.Client
	bucket string
	prefix string
	// clientOptions are applied to every call; tests use them to remove the
	// SDK retry delay.
	clientOptions []func(*s3.Options)
}

var _ storage.Storage = (*Store)(nil)

// New builds an S3 store. It makes no network calls.
func New(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Bucket == "" {
		return nil, errors.New("s3: bucket is required")
	}

	var loadOptions []func(*config.LoadOptions) error
	if cfg.Region != "" {
		loadOptions = append(loadOptions, config.WithRegion(cfg.Region))
	}

	awsCfg, err := config.LoadDefaultConfig(ctx, loadOptions...)
	if err != nil {
		return nil, fmt.Errorf("s3: load AWS config: %w", err)
	}

	if awsCfg.Region == "" {
		return nil, errors.New("s3: region is required (set Config.Region or AWS_REGION)")
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = cfg.UsePathStyle

		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
			o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
		}
	})

	return &Store{client: client, bucket: cfg.Bucket, prefix: normalisePrefix(cfg.Prefix)}, nil
}

func normalisePrefix(prefix string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return ""
	}

	return prefix + "/"
}

func (s *Store) objectKey(key string) *string {
	return aws.String(s.prefix + key)
}

// Inspect reads the bucket's versioning state. S3 has no soft delete or
// hierarchical namespace. A caller denied s3:GetBucketVersioning gets Unknown
// versioning; any other failure is returned.
func (s *Store) Inspect(ctx context.Context) (storage.Info, error) {
	info := storage.Info{Versioning: storage.Unknown, SoftDelete: storage.No, HierarchicalNamespace: storage.No}

	out, err := s.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{
		Bucket: aws.String(s.bucket),
	}, s.callOptions(ctx)...)
	if err != nil {
		mapped := mapError(err)
		if errors.Is(mapped, storage.ErrAccessDenied) {
			return info, nil
		}

		return storage.Info{}, fmt.Errorf("s3: get bucket versioning: %w", mapped)
	}

	info.Versioning = storage.No
	if out.Status == types.BucketVersioningStatusEnabled {
		info.Versioning = storage.Yes
	}

	return info, nil
}

// Get returns the object data and its ETag.
func (s *Store) Get(ctx context.Context, key string) ([]byte, storage.Version, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    s.objectKey(key),
	}, s.callOptions(ctx)...)
	if err != nil {
		return nil, "", mapError(err)
	}

	defer func() { _ = out.Body.Close() }()

	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, "", fmt.Errorf("s3: read %q: %w", key, err)
	}

	etag := aws.ToString(out.ETag)
	if etag == "" {
		return nil, "", fmt.Errorf("s3: get %q: response has no ETag", key)
	}

	return data, storage.Version(etag), nil
}

// CreateIfAbsent writes the object only when no object exists at key.
func (s *Store) CreateIfAbsent(ctx context.Context, key string, data []byte) (storage.Version, error) {
	return s.put(ctx, key, data, &s3.PutObjectInput{IfNoneMatch: aws.String("*")})
}

// PutIfMatch overwrites the object only when its ETag equals version.
func (s *Store) PutIfMatch(ctx context.Context, key string, data []byte, version storage.Version) (storage.Version, error) {
	if version == "" {
		return "", fmt.Errorf("s3: put %q with an empty version: %w", key, storage.ErrPreconditionFailed)
	}

	return s.put(ctx, key, data, &s3.PutObjectInput{IfMatch: aws.String(string(version))})
}

func (s *Store) put(ctx context.Context, key string, data []byte, in *s3.PutObjectInput) (storage.Version, error) {
	in.Bucket = aws.String(s.bucket)
	in.Key = s.objectKey(key)
	in.Body = bytes.NewReader(data)
	in.ContentLength = aws.Int64(int64(len(data)))
	in.ContentType = aws.String("application/json")

	if err := ctx.Err(); err != nil {
		return "", err
	}

	outcome := new(writeOutcome)

	out, err := s.client.PutObject(ctx, in, append(s.callOptions(ctx), outcome.track)...)
	if err != nil {
		return "", outcome.mapError(err)
	}

	etag := aws.ToString(out.ETag)
	if etag == "" {
		return "", fmt.Errorf("s3: put %q succeeded but the response has no ETag: %w", key, storage.ErrUnknownOutcome)
	}

	return storage.Version(etag), nil
}

// DeleteIfMatch deletes the object only when its ETag equals version.
func (s *Store) DeleteIfMatch(ctx context.Context, key string, version storage.Version) error {
	if version == "" {
		return fmt.Errorf("s3: delete %q with an empty version: %w", key, storage.ErrPreconditionFailed)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	outcome := new(writeOutcome)

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:  aws.String(s.bucket),
		Key:     s.objectKey(key),
		IfMatch: aws.String(string(version)),
	}, append(s.callOptions(ctx), outcome.track)...)
	if err != nil {
		return outcome.mapError(err)
	}

	return nil
}

// List returns the keys under prefix, sorted, with Config.Prefix stripped.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(s.prefix + prefix),
	})

	var keys []string

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx, s.callOptions(ctx)...)
		if err != nil {
			return nil, mapError(err)
		}

		for _, object := range page.Contents {
			keys = append(keys, strings.TrimPrefix(aws.ToString(object.Key), s.prefix))
		}
	}

	slices.Sort(keys)

	return keys, nil
}

// callOptions swaps in the access-propagation retryer when the context asks
// for it; otherwise the client's default retryer applies and access denied
// fails immediately. The result is clipped so that callers appending
// per-call options never share s.clientOptions' backing array.
func (s *Store) callOptions(ctx context.Context) []func(*s3.Options) {
	if !storage.AccessPropagationRetry(ctx) {
		return slices.Clip(s.clientOptions)
	}

	return append([]func(*s3.Options){func(o *s3.Options) {
		o.Retryer = retry.NewStandard(func(so *retry.StandardOptions) {
			so.MaxAttempts = propagationMaxAttempts
			so.MaxBackoff = propagationMaxBackoff
			so.Retryables = append(slices.Clone(so.Retryables), retry.IsErrorRetryableFunc(retryAccessDenied))
		})
	}}, s.clientOptions...)
}

// writeOutcome records whether any attempt of one PutObject/DeleteObject,
// across the SDK's retries, may have been applied by S3 even though the call
// did not succeed.
//
// The boundary for a failed attempt is the moment request bytes may have left
// the client: net/http's httptrace WroteHeaders fires only after DNS, dial,
// TLS handshake and connection reuse have succeeded, so failures before it
// cannot have reached S3. A 408 or 5xx response proves the request arrived
// and may have been applied; any other response proves it was not.
type writeOutcome struct {
	mayHaveApplied atomic.Bool
}

// track installs the per-attempt recorder. It runs innermost in the
// deserialize step, below the retry loop, so it sees every attempt's raw
// response or transport error.
func (w *writeOutcome) track(o *s3.Options) {
	o.APIOptions = append(slices.Clip(o.APIOptions), func(stack *middleware.Stack) error {
		return stack.Deserialize.Add(middleware.DeserializeMiddlewareFunc("ipamWriteOutcome", w.record), middleware.After)
	})
}

func (w *writeOutcome) record(
	ctx context.Context, in middleware.DeserializeInput, next middleware.DeserializeHandler,
) (middleware.DeserializeOutput, middleware.Metadata, error) {
	var wroteHeaders atomic.Bool

	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() { wroteHeaders.Store(true) },
	})

	out, metadata, err := next.HandleDeserialize(ctx, in)

	if resp, isHTTP := out.RawResponse.(*smithyhttp.Response); isHTTP && resp != nil && err == nil {
		if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode >= http.StatusInternalServerError {
			w.mayHaveApplied.Store(true)
		}
	} else if err != nil && wroteHeaders.Load() {
		w.mayHaveApplied.Store(true)
	}

	return out, metadata, err
}

// mapError maps the final PutObject/DeleteObject error. If any attempt may
// have been applied the result is storage.ErrUnknownOutcome: even a final
// 412/409 may be the answer to our own earlier write. Otherwise nothing
// reached S3 or S3 answered definitively, and the usual mapping applies.
func (w *writeOutcome) mapError(err error) error {
	if w.mayHaveApplied.Load() {
		return fmt.Errorf("%w: %w", storage.ErrUnknownOutcome, err)
	}

	return mapError(err)
}

func retryAccessDenied(err error) aws.Ternary {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) && apiErr.ErrorCode() == "AccessDenied" {
		return aws.TrueTernary
	}

	return aws.UnknownTernary
}

// mapError maps S3 responses onto the storage sentinels, keeping the SDK error
// in the chain. Errors that do not map (including NoSuchBucket) are returned
// unchanged.
func mapError(err error) error {
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.ErrorCode() {
		case "NoSuchBucket":
			return err
		case "NoSuchKey", "NotFound":
			return fmt.Errorf("%w: %w", storage.ErrNotFound, err)
		case "PreconditionFailed", "ConditionalRequestConflict":
			return fmt.Errorf("%w: %w", storage.ErrPreconditionFailed, err)
		}
	}

	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case http.StatusNotFound:
			return fmt.Errorf("%w: %w", storage.ErrNotFound, err)
		case http.StatusPreconditionFailed:
			return fmt.Errorf("%w: %w", storage.ErrPreconditionFailed, err)
		case http.StatusForbidden:
			return fmt.Errorf("%w: %w", storage.ErrAccessDenied, err)
		}
	}

	return err
}
