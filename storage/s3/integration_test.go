// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package s3_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/appvia/ipam/storage"
	s3store "github.com/appvia/ipam/storage/s3"
	"github.com/appvia/ipam/storage/storagetest"
)

// TestS3Contract runs the shared contract against an S3 endpoint:
//
//	IPAM_TEST_S3_ENDPOINT   S3-compatible endpoint (empty for real AWS)
//	IPAM_TEST_S3_BUCKET     bucket (default "ipam-test" with an endpoint)
//	IPAM_TEST_S3_REGION     region (default AWS_REGION, then "us-east-1")
//	IPAM_TEST_S3_PATH_STYLE "true"/"false" (default true with an endpoint)
//	IPAM_TEST_S3_SKIP_CONDITIONAL_DELETE=1 skips the conditional-delete cases
//
// Credentials come from the AWS SDK default chain (AWS_ACCESS_KEY_ID etc.).
func TestS3Contract(t *testing.T) {
	endpoint := os.Getenv("IPAM_TEST_S3_ENDPOINT")
	bucket := os.Getenv("IPAM_TEST_S3_BUCKET")

	if endpoint == "" && bucket == "" {
		t.Skip("set IPAM_TEST_S3_ENDPOINT or IPAM_TEST_S3_BUCKET to run the S3 contract")
	}

	if bucket == "" {
		bucket = "ipam-test"
	}

	region := firstNonEmpty(os.Getenv("IPAM_TEST_S3_REGION"), os.Getenv("AWS_REGION"), "us-east-1")
	pathStyle := endpoint != ""

	if v := os.Getenv("IPAM_TEST_S3_PATH_STYLE"); v != "" {
		pathStyle = v == "true" || v == "1"
	}

	var opts storagetest.Options
	if os.Getenv("IPAM_TEST_S3_SKIP_CONDITIONAL_DELETE") == "1" {
		opts.SkipConditionalDelete = "IPAM_TEST_S3_SKIP_CONDITIONAL_DELETE=1: the emulator at " + endpoint +
			" (MinIO/gofakes3) IGNORES If-Match on DELETE, so conditional delete is NOT proven here; " +
			"run against real AWS (IPAM_TEST_S3_BUCKET without the skip flag) to prove it"
		t.Logf("WARNING: %s", opts.SkipConditionalDelete)
	}

	run := uuid.NewString()

	storagetest.RunWithOptions(t, func(t *testing.T) storage.Storage {
		store, err := s3store.New(t.Context(), s3store.Config{
			Bucket:       bucket,
			Prefix:       "ipam-it/" + run + "/" + uuid.NewString(),
			Region:       region,
			Endpoint:     endpoint,
			UsePathStyle: pathStyle,
		})
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}

		t.Cleanup(func() { cleanup(t, store) })

		return store
	}, opts)
}

// cleanup removes every object the subtest left under its prefix.
func cleanup(t *testing.T, store storage.Storage) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	keys, err := store.List(ctx, "")
	if err != nil {
		t.Errorf("cleanup list: %v", err)
		return
	}

	for _, key := range keys {
		_, version, err := store.Get(ctx, key)
		if err != nil {
			t.Errorf("cleanup get %q: %v", key, err)
			continue
		}

		if err := store.DeleteIfMatch(ctx, key, version); err != nil {
			t.Errorf("cleanup delete %q: %v", key, err)
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}
