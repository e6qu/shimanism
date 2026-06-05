package conformance_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"golang.org/x/oauth2"
	arraw "google.golang.org/api/artifactregistry/v1"
	"google.golang.org/api/option"

	frontendaws "github.com/e6qu/shimanism/internal/registry/frontends/aws_ecr"
	frontendazure "github.com/e6qu/shimanism/internal/registry/frontends/azure_acr"
	frontendgcp "github.com/e6qu/shimanism/internal/registry/frontends/gcp_artifactregistry"
	awsbackend "github.com/e6qu/shimanism/services/registry/backends/aws_ecr"
	azurebackend "github.com/e6qu/shimanism/services/registry/backends/azure_acr"
	gcpbackend "github.com/e6qu/shimanism/services/registry/backends/gcp_artifactregistry"
)

func TestSockerless_GCPAR_ThroughShim_ImagePushPull(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	baseURL := "http://" + endpoint
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: arBearerJWT(), TokenType: "Bearer"})
	svc, err := arraw.NewService(context.Background(),
		option.WithEndpoint(baseURL+"/"),
		option.WithTokenSource(tokenSource),
	)
	if err != nil {
		t.Fatalf("artifactregistry service: %v", err)
	}
	backend, err := gcpbackend.New(svc, gcpbackend.Config{
		Parent:           "projects/shim-sockerless/locations/us",
		DataPlaneBaseURL: baseURL,
		TokenSource:      tokenSource,
	})
	if err != nil {
		t.Fatalf("gcp backend: %v", err)
	}
	srv := httptest.NewServer(frontendgcp.Handler(backend))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	ref, err := name.ParseReference(host+"/shim-sockerless/registry/app:v1", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	opts := []remote.Option{
		remote.WithAuth(&authn.Bearer{Token: arBearerJWT()}),
		remote.WithTransport(http.DefaultTransport),
	}
	assertPushPullOrSkipKnownSockerlessGap(t, ref, "BUG-65", "405 Method Not Allowed", opts...)
}

func TestSockerless_AzureACR_ThroughShim_ImagePushPull(t *testing.T) {
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	certPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if certPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatalf("AppendCertsFromPEM: no sockerless cert parsed")
	}
	client := &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool}, //nolint:gosec
	}}
	sockURL := "https://localhost:" + port
	backend, err := azurebackend.New(azurebackend.Config{
		BaseURL:    sockURL,
		Credential: sockerlessACRCredential{sockerlessURL: sockURL, tenantID: acrTenantID, certPool: pool},
		HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("azure backend: %v", err)
	}
	srv := httptest.NewServer(frontendazure.Handler(backend))
	defer srv.Close()

	host := srv.Listener.Addr().String()
	const repo = "shim/registry"
	token := acrAccessToken(t, srv.URL, host, "repository:"+repo+":pull,push")
	ref, err := name.ParseReference(host+"/"+repo+":v1", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	assertPushPullOrSkipKnownSockerlessGap(t, ref, "BUG-66", "acr 404 Not Found",
		remote.WithAuth(&authn.Bearer{Token: token}),
		remote.WithTransport(http.DefaultTransport),
	)
}

func TestSockerless_AWSECR_ThroughShim_ImagePushPull(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
	}
	client := &http.Client{Transport: tr}
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{AccessKeyID: "test", SecretAccessKey: "test"},
		}),
		config.WithHTTPClient(client),
	)
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	ecrClient := ecr.NewFromConfig(cfg, func(o *ecr.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend, err := awsbackend.New(ecrClient, awsbackend.Config{HTTPClient: client})
	if err != nil {
		t.Fatalf("aws backend: %v", err)
	}
	srv := httptest.NewServer(frontendaws.New(backend))
	defer srv.Close()

	shimClient := newECRClient(t, srv.URL)
	const repo = "shim/registry"
	if _, err := shimClient.CreateRepository(context.Background(), &ecr.CreateRepositoryInput{
		RepositoryName: awsapi.String(repo),
	}); err != nil {
		t.Fatalf("CreateRepository through shim: %v", err)
	}
	auth, err := shimClient.GetAuthorizationToken(context.Background(), &ecr.GetAuthorizationTokenInput{})
	if err != nil {
		t.Fatalf("GetAuthorizationToken through shim: %v", err)
	}
	if len(auth.AuthorizationData) == 0 {
		t.Fatal("GetAuthorizationToken returned no authorization data")
	}
	user, pass, err := decodeECRBasicAuth(awsapi.ToString(auth.AuthorizationData[0].AuthorizationToken))
	if err != nil {
		t.Fatalf("decode authorization token: %v", err)
	}

	host := srv.Listener.Addr().String()
	ref, err := name.ParseReference(host+"/"+repo+":v1", name.Insecure)
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}
	assertPushPull(t, ref,
		remote.WithAuth(&authn.Basic{Username: user, Password: pass}),
		remote.WithTransport(http.DefaultTransport),
	)
}

type sockerlessACRCredential struct {
	sockerlessURL string
	tenantID      string
	certPool      *x509.CertPool
}

func (s sockerlessACRCredential) GetToken(ctx context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: s.certPool}}, //nolint:gosec
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {acrClientID},
		"client_secret": {acrClientSecret},
		"scope":         {"https://management.azure.com/.default"},
	}
	tokenURL := fmt.Sprintf("%s/%s/oauth2/v2.0/token", s.sockerlessURL, s.tenantID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return azcore.AccessToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return azcore.AccessToken{}, fmt.Errorf("sockerless token POST: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return azcore.AccessToken{}, fmt.Errorf("sockerless token: HTTP %d: %s", resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return azcore.AccessToken{}, fmt.Errorf("parse token response: %w", err)
	}
	return azcore.AccessToken{
		Token:     out.AccessToken,
		ExpiresOn: time.Now().Add(time.Duration(out.ExpiresIn) * time.Second),
	}, nil
}

func assertPushPullOrSkipKnownSockerlessGap(t *testing.T, ref name.Reference, bugID, failureNeedle string, opts ...remote.Option) {
	t.Helper()
	pushPull(t, ref, func(err error) {
		if strings.Contains(err.Error(), failureNeedle) {
			t.Skipf("%s: sockerless registry simulator gap: %v", bugID, err)
		}
		t.Fatalf("push through registry shim: %v", err)
	}, opts...)
}

func assertPushPull(t *testing.T, ref name.Reference, opts ...remote.Option) {
	t.Helper()
	pushPull(t, ref, func(err error) {
		t.Fatalf("push through registry shim: %v", err)
	}, opts...)
}

func pushPull(t *testing.T, ref name.Reference, onPushError func(error), opts ...remote.Option) {
	t.Helper()
	img, err := random.Image(2048, 2)
	if err != nil {
		t.Fatalf("random.Image: %v", err)
	}
	pushedDigest, err := img.Digest()
	if err != nil {
		t.Fatalf("img.Digest: %v", err)
	}
	if err := remote.Write(ref, img, opts...); err != nil {
		onPushError(err)
		return
	}
	pulled, err := remote.Image(ref, opts...)
	if err != nil {
		t.Fatalf("pull through registry shim: %v", err)
	}
	pulledDigest, err := pulled.Digest()
	if err != nil {
		t.Fatalf("pulled.Digest: %v", err)
	}
	if pulledDigest != pushedDigest {
		t.Fatalf("pulled digest = %s, want %s", pulledDigest, pushedDigest)
	}
}

func decodeECRBasicAuth(token string) (string, string, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", err
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", fmt.Errorf("authorization token is not user:pass")
	}
	return user, pass, nil
}
