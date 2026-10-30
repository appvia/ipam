// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
)

type azureStore struct {
	container *container.Client
}

func newAzureStore(connectionString, containerName string, hc *http.Client) (*azureStore, error) {
	opts := &service.ClientOptions{ClientOptions: azcore.ClientOptions{
		Transport: hc,
		// A 412 must surface exactly once; a negative value means a single try.
		Retry: policy.RetryOptions{MaxRetries: -1},
	}}

	svc, err := service.NewClientFromConnectionString(connectionString, opts)
	if err != nil {
		return nil, fmt.Errorf("parse connection string: %w", err)
	}

	return &azureStore{container: svc.NewContainerClient(containerName)}, nil
}

func azErr(err error) error {
	var re *azcore.ResponseError
	if errors.As(err, &re) {
		return &statusError{code: re.StatusCode, err: err}
	}

	return err
}

// createOnlyRejected accepts 412 and Azure's documented create-only answer,
// 409 BlobAlreadyExists, which Azurite reproduces.
func (s *azureStore) createOnlyRejected(err error) bool {
	return statusOf(err) == http.StatusPreconditionFailed ||
		(statusOf(err) == http.StatusConflict && bloberror.HasCode(err, bloberror.BlobAlreadyExists))
}

func (s *azureStore) ensureContainer(ctx context.Context) error {
	_, err := s.container.Create(ctx, nil)
	if err == nil || bloberror.HasCode(err, bloberror.ContainerAlreadyExists) {
		return nil
	}

	return fmt.Errorf("create container: %w", azErr(err))
}

func (s *azureStore) put(ctx context.Context, key string, body []byte, cond condition) (string, error) {
	mac := &blob.ModifiedAccessConditions{}

	if cond.ifMatch != "" {
		etag := azcore.ETag(cond.ifMatch)
		mac.IfMatch = &etag
	}

	if cond.ifNoneMatchStar {
		etagAny := azcore.ETagAny
		mac.IfNoneMatch = &etagAny
	}

	resp, err := s.container.NewBlockBlobClient(key).Upload(ctx,
		streaming.NopCloser(bytes.NewReader(body)),
		&blockblob.UploadOptions{AccessConditions: &blob.AccessConditions{ModifiedAccessConditions: mac}})
	if err != nil {
		return "", azErr(err)
	}

	if resp.ETag == nil {
		return "", nil
	}

	return string(*resp.ETag), nil
}

func (s *azureStore) del(ctx context.Context, key, ifMatch string) error {
	opts := &blob.DeleteOptions{}

	if ifMatch != "" {
		etag := azcore.ETag(ifMatch)
		opts.AccessConditions = &blob.AccessConditions{
			ModifiedAccessConditions: &blob.ModifiedAccessConditions{IfMatch: &etag},
		}
	}

	_, err := s.container.NewBlobClient(key).Delete(ctx, opts)

	return azErr(err)
}

func (s *azureStore) get(ctx context.Context, key string) ([]byte, string, error) {
	resp, err := s.container.NewBlobClient(key).DownloadStream(ctx, nil)
	if err != nil {
		return nil, "", azErr(err)
	}

	body, err := readAndClose(resp.Body)
	if err != nil {
		return nil, "", err
	}

	etag := ""
	if resp.ETag != nil {
		etag = string(*resp.ETag)
	}

	return body, etag, nil
}

// serverVersion reads the version Azurite advertises in its Server header
// ("Azurite-Blob/3.37.0"); an unauthenticated request is enough.
func serverVersion(ctx context.Context, hc *http.Client, blobEndpoint string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobEndpoint, nil)
	if err != nil {
		return "", err
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}

	if _, err := readAndClose(resp.Body); err != nil {
		return "", err
	}

	server := resp.Header.Get("Server")
	if name, version, ok := strings.Cut(server, "/"); ok && strings.HasPrefix(name, "Azurite") {
		return version, nil
	}

	return "", fmt.Errorf("server header %q is not Azurite", server)
}
