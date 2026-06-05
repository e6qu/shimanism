// Conformance: Azure Container Registry-shaped frontend exercised by
// the ACR-native token exchange plus the official go-containerregistry
// OCI client. The test exchanges an Entra-shaped token for an ACR refresh
// token, exchanges that for a scoped access token, then pushes/pulls a
// real image through /v2/ into the inmem content-addressable backend.
package conformance_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/registry/frontends/azure_acr"
	"github.com/e6qu/shimanism/services/registry/backends/inmem"
)

func acrAADJWT() string {
	return azurebearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://management.azure.com/",
		15*time.Minute,
	)
}

type acrArmCredential struct{}

func (acrArmCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: acrAADJWT(), ExpiresOn: time.Now().Add(15 * time.Minute)}, nil
}

func newACRARMClient(t *testing.T, endpoint string) *armcontainerregistry.RegistriesClient {
	t.Helper()
	httpClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}}
	client, err := armcontainerregistry.NewRegistriesClient(
		"00000000-0000-0000-0000-000000000000",
		acrArmCredential{},
		&arm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: httpClient,
				Cloud: cloud.Configuration{
					ActiveDirectoryAuthorityHost: "https://shim.test/",
					Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
						cloud.ResourceManager: {
							Audience: "https://management.azure.com/",
							Endpoint: endpoint,
						},
					},
				},
			},
		},
	)
	if err != nil {
		t.Fatalf("NewRegistriesClient: %v", err)
	}
	return client
}

func acrAccessToken(t *testing.T, baseURL, host, scope string) string {
	t.Helper()
	exchangeForm := url.Values{
		"grant_type":   {"access_token"},
		"service":      {host},
		"tenant":       {"shim-tenant"},
		"access_token": {acrAADJWT()},
	}
	resp, err := http.PostForm(baseURL+"/oauth2/exchange", exchangeForm)
	if err != nil {
		t.Fatalf("oauth2 exchange: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("oauth2 exchange status = %d body=%s", resp.StatusCode, body)
	}
	var ex struct {
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ex); err != nil {
		t.Fatalf("decode exchange: %v", err)
	}
	if ex.RefreshToken == "" {
		t.Fatal("exchange returned empty refresh_token")
	}

	tokenForm := url.Values{
		"grant_type":    {"refresh_token"},
		"service":       {host},
		"scope":         {scope},
		"refresh_token": {ex.RefreshToken},
	}
	resp, err = http.PostForm(baseURL+"/oauth2/token", tokenForm)
	if err != nil {
		t.Fatalf("oauth2 token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("oauth2 token status = %d body=%s", resp.StatusCode, body)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	if tok.AccessToken == "" {
		t.Fatal("token returned empty access_token")
	}
	return tok.AccessToken
}

func TestACR_ImagePushPull_WithTokenExchange(t *testing.T) {
	srv := httptest.NewServer(azure_acr.Handler(inmem.New()))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	const repo = "team/app"
	token := acrAccessToken(t, srv.URL, host, "repository:"+repo+":pull,push")

	ref, err := name.ParseReference(host+"/"+repo+":v1", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	opts := []remote.Option{
		remote.WithAuth(&authn.Bearer{Token: token}),
		remote.WithTransport(http.DefaultTransport),
	}

	img, err := random.Image(2048, 2)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	pushed, _ := img.Digest()
	if err := remote.Write(ref, img, opts...); err != nil {
		t.Fatalf("push through ACR shim: %v", err)
	}
	pulled, err := remote.Image(ref, opts...)
	if err != nil {
		t.Fatalf("pull through ACR shim: %v", err)
	}
	if got, _ := pulled.Digest(); got != pushed {
		t.Errorf("pulled digest = %s, want %s", got, pushed)
	}
}

func TestACR_ACRv1CatalogAndManifests(t *testing.T) {
	srv := httptest.NewServer(azure_acr.Handler(inmem.New()))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	const repo = "catalog/app"
	token := acrAccessToken(t, srv.URL, host, "repository:"+repo+":pull,push")
	ref, err := name.ParseReference(host+"/"+repo+":v2", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	opts := []remote.Option{
		remote.WithAuth(&authn.Bearer{Token: token}),
		remote.WithTransport(http.DefaultTransport),
	}
	img, err := random.Image(1024, 1)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	digest, _ := img.Digest()
	if err := remote.Write(ref, img, opts...); err != nil {
		t.Fatalf("push through ACR shim: %v", err)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/acr/v1/_catalog?api-version=2021-07-01", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET _catalog: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("_catalog status=%d body=%s", resp.StatusCode, body)
	}
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&catalog); err != nil {
		t.Fatalf("decode _catalog: %v", err)
	}
	if !contains(catalog.Repositories, repo) {
		t.Fatalf("_catalog repositories = %v, want %q", catalog.Repositories, repo)
	}

	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/acr/v1/"+repo+"/_manifests?api-version=2021-07-01", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET _manifests: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("_manifests status=%d body=%s", resp.StatusCode, body)
	}
	var manifests struct {
		Registry  string `json:"registry"`
		ImageName string `json:"imageName"`
		Manifests []struct {
			Digest string   `json:"digest"`
			Tags   []string `json:"tags"`
		} `json:"manifests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifests); err != nil {
		t.Fatalf("decode _manifests: %v", err)
	}
	if manifests.Registry != host || manifests.ImageName != repo {
		t.Fatalf("_manifests = %+v, want registry=%q imageName=%q", manifests, host, repo)
	}
	if len(manifests.Manifests) != 1 || manifests.Manifests[0].Digest != digest.String() || !contains(manifests.Manifests[0].Tags, "v2") {
		t.Fatalf("manifests = %+v, want digest %s tag v2", manifests.Manifests, digest)
	}
}

func TestACR_DataPlaneUnauthenticatedChallenged(t *testing.T) {
	srv := httptest.NewServer(azure_acr.Handler(inmem.New()))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/v2/")
	if err != nil {
		t.Fatalf("GET /v2/: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated /v2/ = %d, want 401", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer ") {
		t.Errorf("WWW-Authenticate = %q, want Bearer challenge", resp.Header.Get("WWW-Authenticate"))
	}
}

func TestACR_ARMRegistryHostShape(t *testing.T) {
	srv := httptest.NewServer(azure_acr.Handler(inmem.New()))
	defer srv.Close()

	path := "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/rg/providers/Microsoft.ContainerRegistry/registries/shimacr?api-version=2023-01-01-preview"
	req, _ := http.NewRequest(http.MethodPut, srv.URL+path, strings.NewReader(`{"location":"eastus","sku":{"name":"Basic"}}`))
	req.Header.Set("Authorization", "Bearer "+acrAADJWT())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT registry: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT registry status=%d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Name       string `json:"name"`
		Location   string `json:"location"`
		Properties struct {
			LoginServer string `json:"loginServer"`
		} `json:"properties"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode registry: %v", err)
	}
	if out.Name != "shimacr" || out.Location != "eastus" || out.Properties.LoginServer != srv.Listener.Addr().String() {
		t.Fatalf("registry = %+v", out)
	}
}

func TestAzureSDK_ACR_RegistryHostLifecycle(t *testing.T) {
	srv := httptest.NewTLSServer(azure_acr.Handler(inmem.New()))
	defer srv.Close()

	client := newACRARMClient(t, srv.URL)
	ctx := context.Background()
	const (
		rg   = "shim-conformance"
		name = "sdkacr"
	)
	poller, err := client.BeginCreate(ctx, rg, name, armcontainerregistry.Registry{
		Location: to.Ptr("eastus"),
		SKU:      &armcontainerregistry.SKU{Name: to.Ptr(armcontainerregistry.SKUNameBasic)},
	}, nil)
	if err != nil {
		t.Fatalf("BeginCreate: %v", err)
	}
	created, err := poller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone create: %v", err)
	}
	if created.Name == nil || *created.Name != name {
		t.Fatalf("created.Name = %v, want %s", created.Name, name)
	}
	if created.Properties == nil || created.Properties.LoginServer == nil || *created.Properties.LoginServer != srv.Listener.Addr().String() {
		t.Fatalf("created.Properties.LoginServer = %+v", created.Properties)
	}

	got, err := client.Get(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Properties == nil || got.Properties.ProvisioningState == nil || *got.Properties.ProvisioningState != armcontainerregistry.ProvisioningStateSucceeded {
		t.Fatalf("Get provisioning state = %+v", got.Properties)
	}

	delPoller, err := client.BeginDelete(ctx, rg, name, nil)
	if err != nil {
		t.Fatalf("BeginDelete: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone delete: %v", err)
	}
}
