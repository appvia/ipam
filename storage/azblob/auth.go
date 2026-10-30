// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package azblob

import (
	"fmt"
	"log"
	"os"
	"reflect"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/entrauth/aztfauth"
)

// AuthConfig holds the Azure authentication settings of the azapi Terraform
// provider, one field per azapi provider attribute. It is the only mapping of
// those settings onto aztfauth, shared by the ipam CLI and provider.
//
// ClientSecret, ClientCertificate, ClientCertificatePassword, OIDCToken and
// OIDCRequestToken are secrets: never log or persist them.
//
// The Use* flags are pointers so that an explicit false (for example the
// provider attribute use_cli = false) overrides an environment default in
// Merge; nil means "not set".
type AuthConfig struct {
	TenantID                     string
	ClientID                     string
	ClientIDFilePath             string
	ClientSecret                 string
	ClientSecretFilePath         string
	ClientCertificate            string
	ClientCertificatePath        string
	ClientCertificatePassword    string
	OIDCToken                    string
	OIDCTokenFilePath            string
	OIDCRequestToken             string
	OIDCRequestURL               string
	OIDCAzureServiceConnectionID string
	UseOIDC                      *bool
	UseMSI                       *bool
	UseCLI                       *bool
	UseAKSWorkloadIdentity       *bool

	// aksClientID and aksTenantID come from the variables the AKS workload
	// identity webhook injects; they apply only when UseAKSWorkloadIdentity is set.
	aksClientID string
	aksTenantID string

	clientOptions azcore.ClientOptions
}

// AuthConfigFromEnv reads the environment variables documented by the azapi
// provider. ARM_USE_CLI defaults to true; the other Use* flags default to false.
func AuthConfigFromEnv() AuthConfig {
	return AuthConfig{
		TenantID:                  os.Getenv("ARM_TENANT_ID"),
		ClientID:                  os.Getenv("ARM_CLIENT_ID"),
		ClientIDFilePath:          os.Getenv("ARM_CLIENT_ID_FILE_PATH"),
		ClientSecret:              os.Getenv("ARM_CLIENT_SECRET"),
		ClientSecretFilePath:      os.Getenv("ARM_CLIENT_SECRET_FILE_PATH"),
		ClientCertificate:         os.Getenv("ARM_CLIENT_CERTIFICATE"),
		ClientCertificatePath:     os.Getenv("ARM_CLIENT_CERTIFICATE_PATH"),
		ClientCertificatePassword: os.Getenv("ARM_CLIENT_CERTIFICATE_PASSWORD"),
		OIDCToken:                 os.Getenv("ARM_OIDC_TOKEN"),
		OIDCTokenFilePath:         firstEnv("ARM_OIDC_TOKEN_FILE_PATH", "AZURE_FEDERATED_TOKEN_FILE"),
		OIDCRequestToken: firstEnv("ARM_OIDC_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN",
			"SYSTEM_ACCESSTOKEN"),
		OIDCRequestURL: firstEnv("ARM_OIDC_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_URL",
			"SYSTEM_OIDCREQUESTURI"),
		OIDCAzureServiceConnectionID: firstEnv("ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID",
			"ARM_OIDC_AZURE_SERVICE_CONNECTION_ID", "AZURESUBSCRIPTION_SERVICE_CONNECTION_ID"),
		UseOIDC:                envBool("ARM_USE_OIDC", false),
		UseMSI:                 envBool("ARM_USE_MSI", false),
		UseCLI:                 envBool("ARM_USE_CLI", true),
		UseAKSWorkloadIdentity: envBool("ARM_USE_AKS_WORKLOAD_IDENTITY", false),
		aksClientID:            os.Getenv("AZURE_CLIENT_ID"),
		aksTenantID:            os.Getenv("AZURE_TENANT_ID"),
	}
}

// Merge returns a copy of a in which every non-zero field of override wins.
// The provider passes its attributes as override over AuthConfigFromEnv().
func (a AuthConfig) Merge(override AuthConfig) AuthConfig {
	merged := a
	dst := reflect.ValueOf(&merged).Elem()
	src := reflect.ValueOf(override)

	for i := range src.NumField() {
		if dst.Type().Field(i).IsExported() && !src.Field(i).IsZero() {
			dst.Field(i).Set(src.Field(i))
		}
	}

	if override.aksClientID != "" {
		merged.aksClientID = override.aksClientID
	}

	if override.aksTenantID != "" {
		merged.aksTenantID = override.aksTenantID
	}

	return merged
}

// Credential builds a lazily authenticating credential chain through aztfauth.
// It performs no network requests; tokens are fetched on first use. logger may
// be nil.
func (a AuthConfig) Credential(logger *log.Logger) (azcore.TokenCredential, error) {
	opt, err := a.options(logger)
	if err != nil {
		return nil, err
	}

	cred, err := aztfauth.NewCredential(opt)
	if err != nil {
		return nil, fmt.Errorf("azblob: build credential: %w", err)
	}

	return cred, nil
}

// options maps a onto aztfauth.Option the way azapi does. Client secret and
// certificate sources are enabled when their material is present (azapi
// always enables them and lets empty ones fail); OIDC sources need use_oidc
// (or AKS workload identity for the token file) plus their material.
func (a AuthConfig) options(logger *log.Logger) (aztfauth.Option, error) {
	tenantID, clientID := a.TenantID, a.ClientID
	useOIDC := boolValue(a.UseOIDC, false)
	useAKS := boolValue(a.UseAKSWorkloadIdentity, false)

	if useAKS {
		if a.aksTenantID != "" {
			if tenantID != "" && tenantID != a.aksTenantID {
				return aztfauth.Option{}, fmt.Errorf(
					"%w: tenant_id does not match the AKS workload identity tenant (AZURE_TENANT_ID)",
					ErrInvalidArgument)
			}

			tenantID = a.aksTenantID
		}

		if clientID == "" {
			clientID = a.aksClientID
		}
	}

	opt := aztfauth.Option{
		Logger:                 logger,
		TenantId:               tenantID,
		ClientId:               clientID,
		ClientIdFile:           a.ClientIDFilePath,
		UseClientSecret:        a.ClientSecret != "" || a.ClientSecretFilePath != "",
		ClientSecret:           a.ClientSecret,
		ClientSecretFile:       a.ClientSecretFilePath,
		UseClientCert:          a.ClientCertificate != "" || a.ClientCertificatePath != "",
		ClientCertBase64:       a.ClientCertificate,
		ClientCertPfxFile:      a.ClientCertificatePath,
		UseOIDCToken:           useOIDC && a.OIDCToken != "",
		OIDCToken:              a.OIDCToken,
		UseOIDCTokenFile:       (useOIDC || useAKS) && a.OIDCTokenFilePath != "",
		OIDCTokenFile:          a.OIDCTokenFilePath,
		UseOIDCTokenRequest:    useOIDC && a.OIDCRequestToken != "" && (a.OIDCRequestURL != "" || a.OIDCAzureServiceConnectionID != ""),
		OIDCRequestToken:       a.OIDCRequestToken,
		OIDCRequestURL:         a.OIDCRequestURL,
		ADOServiceConnectionId: a.OIDCAzureServiceConnectionID,
		UseMSI:                 boolValue(a.UseMSI, false),
		UseAzureCLI:            boolValue(a.UseCLI, true),
		ClientOptions:          a.clientOptions,
	}

	if a.ClientCertificatePassword != "" {
		opt.ClientCertPassword = []byte(a.ClientCertificatePassword)
	}

	if !opt.UseClientSecret && !opt.UseClientCert && !opt.UseOIDCToken && !opt.UseOIDCTokenFile &&
		!opt.UseOIDCTokenRequest && !opt.UseMSI && !opt.UseAzureCLI {
		return aztfauth.Option{}, fmt.Errorf("%w: no Azure authentication method is enabled", ErrInvalidArgument)
	}

	return opt, nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}

	return ""
}

// envBool follows azapi: a set variable is true only when it equals "true".
func envBool(name string, def bool) *bool {
	v := def
	if s := os.Getenv(name); s != "" {
		v = s == "true"
	}

	return &v
}

func boolValue(b *bool, def bool) bool {
	if b == nil {
		return def
	}

	return *b
}
