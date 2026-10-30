// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

type s3Store struct {
	client *s3.Client
	bucket string
}

func newS3Store(endpoint, bucket, accessKey, secretKey string, hc *http.Client) *s3Store {
	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		HTTPClient:  hc,
		// A 412 must surface exactly once; never retry at the SDK layer.
		Retryer: func() aws.Retryer { return aws.NopRetryer{} },
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})

	return &s3Store{client: client, bucket: bucket}
}

func s3Err(err error) error {
	var re *smithyhttp.ResponseError
	if errors.As(err, &re) {
		return &statusError{code: re.HTTPStatusCode(), err: err}
	}

	return err
}

func (s *s3Store) createOnlyRejected(err error) bool {
	return statusOf(err) == http.StatusPreconditionFailed
}

func (s *s3Store) ensureBucket(ctx context.Context) error {
	_, err := s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(s.bucket)})

	var owned *types.BucketAlreadyOwnedByYou
	if err == nil || errors.As(err, &owned) {
		return nil
	}

	return fmt.Errorf("create bucket %s: %w", s.bucket, s3Err(err))
}

func (s *s3Store) put(ctx context.Context, key string, body []byte, cond condition) (string, error) {
	in := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(body),
	}
	if cond.ifMatch != "" {
		in.IfMatch = aws.String(cond.ifMatch)
	}

	if cond.ifNoneMatchStar {
		in.IfNoneMatch = aws.String("*")
	}

	out, err := s.client.PutObject(ctx, in)
	if err != nil {
		return "", s3Err(err)
	}

	return aws.ToString(out.ETag), nil
}

func (s *s3Store) del(ctx context.Context, key, ifMatch string) error {
	in := &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)}
	if ifMatch != "" {
		in.IfMatch = aws.String(ifMatch)
	}

	_, err := s.client.DeleteObject(ctx, in)

	return s3Err(err)
}

func (s *s3Store) get(ctx context.Context, key string) ([]byte, string, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key)})
	if err != nil {
		return nil, "", s3Err(err)
	}

	body, err := readAndClose(out.Body)
	if err != nil {
		return nil, "", err
	}

	return body, aws.ToString(out.ETag), nil
}
