// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

// doctorHappyPath creates pool p through the CLI, runs
// 'ipam doctor --expect-write p' and asserts a PASS write probe and an
// unchanged reservations ETag.
func doctorHappyPath(t *testing.T, backend string, etag func() string, wantProtection string) {
	t.Helper()

	h := &harness{t: t, backend: backend, env: map[string]string{}, open: openClient}
	h.ok("pool", "create", "p", "--cidr", "10.0.0.0/16")

	before := etag()

	res := h.run("doctor", "--expect-write", "p", "-o", "json")
	t.Logf("doctor exit %d:\n%s%s", res.code, res.stdout, res.stderr)

	if res.code != exitOK {
		t.Fatalf("doctor exit %d, want 0", res.code)
	}

	report := decodeDoctor(t, res.stdout)
	if probe := report.find(t, "write-probe", "p"); probe.Status != "PASS" ||
		!strings.Contains(probe.Detail, "conditional writes honoured") {
		t.Fatalf("write probe = %+v, want PASS", probe)
	}

	if got := report.find(t, "data-protection", ""); got.Status != wantProtection {
		t.Fatalf("data-protection = %+v, want %s", got, wantProtection)
	}

	if after := etag(); after != before {
		t.Fatalf("reservations ETag changed: %s -> %s", before, after)
	}
}

func TestDoctorAzurite(t *testing.T) {
	conn := os.Getenv("AZURE_STORAGE_CONNECTION_STRING")
	if conn == "" {
		t.Skip("AZURE_STORAGE_CONNECTION_STRING is not set; start Azurite with scripts/emulators/start-azurite.sh")
	}

	ctx := t.Context()
	name := "ipam-doctor-" + uuid.NewString()

	containerClient, err := container.NewClientFromConnectionString(conn, name, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := containerClient.Create(ctx, nil); err != nil {
		t.Fatalf("create container: %v", err)
	}

	t.Cleanup(func() { _, _ = containerClient.Delete(context.Background(), nil) })

	// Test setup only: enable blob soft delete on the emulator account so the
	// data-protection check can pass. Doctor itself never changes settings.
	svc, err := service.NewClientFromConnectionString(conn, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svc.SetProperties(ctx, &service.SetPropertiesOptions{
		DeleteRetentionPolicy: &service.RetentionPolicy{Enabled: to.Ptr(true), Days: to.Ptr[int32](1)},
	}); err != nil {
		t.Fatalf("enable soft delete: %v", err)
	}

	blob := containerClient.NewBlobClient("doctor/reservations/p.json")
	etag := func() string {
		props, err := blob.GetProperties(ctx, nil)
		if err != nil {
			t.Fatalf("get reservations properties: %v", err)
		}

		return string(*props.ETag)
	}

	doctorHappyPath(t, "azblob://"+name+"/doctor", etag, "PASS")
}

func TestDoctorS3Emulator(t *testing.T) {
	endpoint := os.Getenv("IPAM_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("IPAM_TEST_S3_ENDPOINT is not set; start MinIO with scripts/emulators/start-s3.sh")
	}

	bucket := os.Getenv("IPAM_TEST_S3_BUCKET")
	if bucket == "" {
		bucket = "ipam-test"
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
	if err != nil {
		t.Fatal(err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})

	// Test setup only: enable versioning on the emulator bucket.
	if _, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &types.VersioningConfiguration{
			Status: types.BucketVersioningStatusEnabled,
		},
	}); err != nil {
		t.Fatalf("enable versioning: %v", err)
	}

	prefix := "doctor-" + uuid.NewString()
	etag := func() string {
		out, err := client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(bucket), Key: aws.String(prefix + "/reservations/p.json"),
		})
		if err != nil {
			t.Fatalf("head reservations: %v", err)
		}

		return aws.ToString(out.ETag) + " " + aws.ToString(out.VersionId)
	}

	backend := "s3://" + bucket + "/" + prefix + "?region=us-east-1&path_style=true&endpoint=" + endpoint
	doctorHappyPath(t, backend, etag, "PASS")
}
