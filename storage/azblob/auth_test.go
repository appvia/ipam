// Copyright 2026 Appvia Ltd.
// SPDX-License-Identifier: Apache-2.0

package azblob

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Azure/entrauth/aztfauth"
)

// authEnv lists every variable AuthConfigFromEnv reads, so each case starts clean.
var authEnv = []string{
	"ARM_TENANT_ID", "ARM_CLIENT_ID", "ARM_CLIENT_ID_FILE_PATH", "ARM_CLIENT_SECRET",
	"ARM_CLIENT_SECRET_FILE_PATH", "ARM_CLIENT_CERTIFICATE", "ARM_CLIENT_CERTIFICATE_PATH",
	"ARM_CLIENT_CERTIFICATE_PASSWORD", "ARM_OIDC_TOKEN", "ARM_OIDC_TOKEN_FILE_PATH",
	"AZURE_FEDERATED_TOKEN_FILE", "ARM_OIDC_REQUEST_TOKEN", "ACTIONS_ID_TOKEN_REQUEST_TOKEN",
	"SYSTEM_ACCESSTOKEN", "ARM_OIDC_REQUEST_URL", "ACTIONS_ID_TOKEN_REQUEST_URL",
	"SYSTEM_OIDCREQUESTURI", "ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID",
	"ARM_OIDC_AZURE_SERVICE_CONNECTION_ID", "AZURESUBSCRIPTION_SERVICE_CONNECTION_ID",
	"ARM_USE_OIDC", "ARM_USE_MSI", "ARM_USE_CLI", "ARM_USE_AKS_WORKLOAD_IDENTITY",
	"AZURE_CLIENT_ID", "AZURE_TENANT_ID",
}

func ptr[T any](v T) *T { return &v }

func TestAuthConfigFromEnvAndMerge(t *testing.T) {
	tests := []struct {
		name     string
		env      map[string]string
		override AuthConfig
		want     AuthConfig
	}{
		{
			name: "env only, including the alternate variable names",
			env: map[string]string{
				"ARM_TENANT_ID":                           "tenant-env",
				"ARM_CLIENT_ID":                           "client-env",
				"ARM_CLIENT_ID_FILE_PATH":                 "/env/client-id",
				"ARM_CLIENT_SECRET":                       "secret-env",
				"ARM_CLIENT_SECRET_FILE_PATH":             "/env/secret",
				"ARM_CLIENT_CERTIFICATE":                  "Y2VydA==",
				"ARM_CLIENT_CERTIFICATE_PATH":             "/env/cert.pfx",
				"ARM_CLIENT_CERTIFICATE_PASSWORD":         "cert-pass",
				"ARM_OIDC_TOKEN":                          "oidc-env",
				"AZURE_FEDERATED_TOKEN_FILE":              "/var/run/token",
				"ACTIONS_ID_TOKEN_REQUEST_TOKEN":          "gh-token",
				"SYSTEM_ACCESSTOKEN":                      "ado-token",
				"ACTIONS_ID_TOKEN_REQUEST_URL":            "https://gh.example/token",
				"SYSTEM_OIDCREQUESTURI":                   "https://ado.example/token",
				"AZURESUBSCRIPTION_SERVICE_CONNECTION_ID": "sc-azsub",
				"ARM_USE_OIDC":                            "true",
				"ARM_USE_MSI":                             "true",
				"ARM_USE_AKS_WORKLOAD_IDENTITY":           "true",
				"AZURE_CLIENT_ID":                         "aks-client",
				"AZURE_TENANT_ID":                         "aks-tenant",
			},
			want: AuthConfig{
				TenantID:                     "tenant-env",
				ClientID:                     "client-env",
				ClientIDFilePath:             "/env/client-id",
				ClientSecret:                 "secret-env",
				ClientSecretFilePath:         "/env/secret",
				ClientCertificate:            "Y2VydA==",
				ClientCertificatePath:        "/env/cert.pfx",
				ClientCertificatePassword:    "cert-pass",
				OIDCToken:                    "oidc-env",
				OIDCTokenFilePath:            "/var/run/token",
				OIDCRequestToken:             "gh-token",
				OIDCRequestURL:               "https://gh.example/token",
				OIDCAzureServiceConnectionID: "sc-azsub",
				UseOIDC:                      ptr(true),
				UseMSI:                       ptr(true),
				UseCLI:                       ptr(true),
				UseAKSWorkloadIdentity:       ptr(true),
				aksClientID:                  "aks-client",
				aksTenantID:                  "aks-tenant",
			},
		},
		{
			name: "env only, ADO request variables when GitHub ones are absent",
			env: map[string]string{
				"SYSTEM_ACCESSTOKEN":                      "ado-token",
				"SYSTEM_OIDCREQUESTURI":                   "https://ado.example/token",
				"ARM_OIDC_AZURE_SERVICE_CONNECTION_ID":    "sc-oidc",
				"AZURESUBSCRIPTION_SERVICE_CONNECTION_ID": "sc-azsub",
				"ARM_USE_CLI":                             "false",
			},
			want: AuthConfig{
				OIDCRequestToken:             "ado-token",
				OIDCRequestURL:               "https://ado.example/token",
				OIDCAzureServiceConnectionID: "sc-oidc",
				UseOIDC:                      ptr(false),
				UseMSI:                       ptr(false),
				UseCLI:                       ptr(false),
				UseAKSWorkloadIdentity:       ptr(false),
			},
		},
		{
			name: "attributes only",
			override: AuthConfig{
				TenantID:                     "tenant-attr",
				ClientID:                     "client-attr",
				ClientSecret:                 "secret-attr",
				OIDCRequestToken:             "req-attr",
				OIDCRequestURL:               "https://attr.example/token",
				OIDCAzureServiceConnectionID: "sc-attr",
				UseOIDC:                      ptr(true),
				UseCLI:                       ptr(false),
			},
			want: AuthConfig{
				TenantID:                     "tenant-attr",
				ClientID:                     "client-attr",
				ClientSecret:                 "secret-attr",
				OIDCRequestToken:             "req-attr",
				OIDCRequestURL:               "https://attr.example/token",
				OIDCAzureServiceConnectionID: "sc-attr",
				UseOIDC:                      ptr(true),
				UseMSI:                       ptr(false),
				UseCLI:                       ptr(false),
				UseAKSWorkloadIdentity:       ptr(false),
			},
		},
		{
			name: "mixed: set attributes win, unset attributes fall back to env, ARM_ names win over alternates",
			env: map[string]string{
				"ARM_TENANT_ID":                          "tenant-env",
				"ARM_CLIENT_ID":                          "client-env",
				"ARM_OIDC_REQUEST_TOKEN":                 "arm-token",
				"ACTIONS_ID_TOKEN_REQUEST_TOKEN":         "gh-token",
				"ARM_OIDC_REQUEST_URL":                   "https://arm.example/token",
				"ACTIONS_ID_TOKEN_REQUEST_URL":           "https://gh.example/token",
				"ARM_OIDC_TOKEN_FILE_PATH":               "/arm/token",
				"AZURE_FEDERATED_TOKEN_FILE":             "/var/run/token",
				"ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID": "sc-pipeline",
				"ARM_OIDC_AZURE_SERVICE_CONNECTION_ID":   "sc-oidc",
				"ARM_USE_MSI":                            "true",
				"ARM_USE_CLI":                            "false",
			},
			override: AuthConfig{
				TenantID: "tenant-attr",
				UseOIDC:  ptr(true),
				UseMSI:   ptr(false),
				UseCLI:   ptr(true),
			},
			want: AuthConfig{
				TenantID:                     "tenant-attr",
				ClientID:                     "client-env",
				OIDCTokenFilePath:            "/arm/token",
				OIDCRequestToken:             "arm-token",
				OIDCRequestURL:               "https://arm.example/token",
				OIDCAzureServiceConnectionID: "sc-pipeline",
				UseOIDC:                      ptr(true),
				UseMSI:                       ptr(false),
				UseCLI:                       ptr(true),
				UseAKSWorkloadIdentity:       ptr(false),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, name := range authEnv {
				t.Setenv(name, "")
			}

			for name, value := range tt.env {
				t.Setenv(name, value)
			}

			got := AuthConfigFromEnv().Merge(tt.override)

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("AuthConfigFromEnv().Merge() =\n%#v\nwant\n%#v", got, tt.want)
			}
		})
	}
}

func TestAuthConfigOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  AuthConfig
		want    aztfauth.Option
		wantErr error
	}{
		{
			name:   "zero value defaults to the Azure CLI like azapi",
			config: AuthConfig{},
			want:   aztfauth.Option{UseAzureCLI: true},
		},
		{
			name: "client secret material enables the client secret source",
			config: AuthConfig{
				TenantID: "t", ClientID: "c", ClientSecret: "s", UseCLI: ptr(false),
			},
			want: aztfauth.Option{TenantId: "t", ClientId: "c", UseClientSecret: true, ClientSecret: "s"},
		},
		{
			name: "client secret file and client id file",
			config: AuthConfig{
				TenantID: "t", ClientIDFilePath: "/c", ClientSecretFilePath: "/s", UseCLI: ptr(false),
			},
			want: aztfauth.Option{TenantId: "t", ClientIdFile: "/c", UseClientSecret: true, ClientSecretFile: "/s"},
		},
		{
			name: "client certificate path with password",
			config: AuthConfig{
				TenantID: "t", ClientID: "c", ClientCertificatePath: "/cert.pfx",
				ClientCertificatePassword: "pw", UseCLI: ptr(false),
			},
			want: aztfauth.Option{
				TenantId: "t", ClientId: "c", UseClientCert: true,
				ClientCertPfxFile: "/cert.pfx", ClientCertPassword: []byte("pw"),
			},
		},
		{
			name:   "inline client certificate",
			config: AuthConfig{TenantID: "t", ClientID: "c", ClientCertificate: "Y2VydA==", UseCLI: ptr(false)},
			want: aztfauth.Option{
				TenantId: "t", ClientId: "c", UseClientCert: true, ClientCertBase64: "Y2VydA==",
			},
		},
		{
			name: "use_oidc with every OIDC material enables each OIDC source",
			config: AuthConfig{
				TenantID: "t", ClientID: "c", OIDCToken: "tok", OIDCTokenFilePath: "/tok",
				OIDCRequestToken: "req", OIDCRequestURL: "https://req", OIDCAzureServiceConnectionID: "sc",
				UseOIDC: ptr(true), UseCLI: ptr(false),
			},
			want: aztfauth.Option{
				TenantId: "t", ClientId: "c",
				UseOIDCToken: true, OIDCToken: "tok",
				UseOIDCTokenFile: true, OIDCTokenFile: "/tok",
				UseOIDCTokenRequest: true, OIDCRequestToken: "req", OIDCRequestURL: "https://req",
				ADOServiceConnectionId: "sc",
			},
		},
		{
			name: "OIDC material without use_oidc is ignored, like azapi",
			config: AuthConfig{
				TenantID: "t", ClientID: "c", OIDCToken: "tok", OIDCRequestToken: "req",
				OIDCRequestURL: "https://req", UseCLI: ptr(false), UseMSI: ptr(true),
			},
			want: aztfauth.Option{
				TenantId: "t", ClientId: "c", OIDCToken: "tok", OIDCRequestToken: "req",
				OIDCRequestURL: "https://req", UseMSI: true,
			},
		},
		{
			name:   "use_oidc without any OIDC material enables no OIDC source",
			config: AuthConfig{UseOIDC: ptr(true)},
			want:   aztfauth.Option{UseAzureCLI: true},
		},
		{
			name: "AKS workload identity takes client, tenant and token file from the AKS variables",
			config: AuthConfig{
				OIDCTokenFilePath: "/var/run/token", UseAKSWorkloadIdentity: ptr(true), UseCLI: ptr(false),
				aksClientID: "aks-client", aksTenantID: "aks-tenant",
			},
			want: aztfauth.Option{
				TenantId: "aks-tenant", ClientId: "aks-client",
				UseOIDCTokenFile: true, OIDCTokenFile: "/var/run/token",
			},
		},
		{
			name: "AKS tenant mismatch is rejected like azapi",
			config: AuthConfig{
				TenantID: "other", OIDCTokenFilePath: "/var/run/token",
				UseAKSWorkloadIdentity: ptr(true), aksTenantID: "aks-tenant",
			},
			wantErr: ErrInvalidArgument,
		},
		{
			name:   "managed identity only",
			config: AuthConfig{ClientID: "c", UseMSI: ptr(true), UseCLI: ptr(false)},
			want:   aztfauth.Option{ClientId: "c", UseMSI: true},
		},
		{
			name:    "no source enabled is rejected",
			config:  AuthConfig{UseCLI: ptr(false), UseOIDC: ptr(true), TenantID: "t"},
			wantErr: ErrInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := tt.config.options(nil)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("options() error = %v, want %v", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("options() error = %v", err)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("options() =\n%#v\nwant\n%#v", got, tt.want)
			}
		})
	}
}

func TestCredentialRejectsConfigWithoutSource(t *testing.T) {
	t.Parallel()

	_, err := AuthConfig{UseCLI: ptr(false)}.Credential(nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Credential() error = %v, want %v", err, ErrInvalidArgument)
	}
}
