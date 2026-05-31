// Sockerless lane for the DNS service. See docs/sockerless-validation.md.
//
// Through-shim path:
//
//	AWS SDK Route 53 → shim's Route 53 frontend → domain.DNS
//	    → shim's AWS Route 53 backend → sockerless's Route 53 sim.
//
// Both the inbound and outbound legs use the official
// aws-sdk-go-v2/service/route53 client; the shim sits in the middle
// and translates frontend wire to domain to backend wire. Sockerless
// is the real-cloud stand-in (in-process Go simulator).
//
// Set SOCKERLESS_AWS_ENDPOINT to opt in (the run script defaults to
// https://localhost:14566).
package conformance_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"golang.org/x/oauth2"
	dnsraw "google.golang.org/api/dns/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/azurebearer"
	azuredfront "github.com/e6qu/shimanism/internal/dns/frontends/azure_dns"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	awsbackend "github.com/e6qu/shimanism/services/dns/backends/aws"
	gcpbackend "github.com/e6qu/shimanism/services/dns/backends/gcp"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

// TestSockerless_AWSRoute53_Through_Shim_ZoneLifecycle drives the
// shim's Route 53 frontend with an SDK call, then has the shim's AWS
// backend translate back to Route 53 calls against sockerless's Route
// 53 simulator. Both legs round-trip through real client and server
// code.
func TestSockerless_AWSRoute53_Through_Shim_ZoneLifecycle(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		t.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		t.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		})
	}
	// Backend leg: shim's AWS backend → sockerless Route 53 sim.
	r53Client := route53.NewFromConfig(cfg, func(o *route53.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend := awsbackend.New(r53Client)
	shim := harness.StartDNSServerAWS(t, backend)

	// Frontend leg: SDK client → shim's Route 53 frontend.
	frontendCfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: awsapi.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("frontend aws config: %v", err)
	}
	cli := route53.NewFromConfig(frontendCfg, func(o *route53.Options) {
		o.BaseEndpoint = awsapi.String(shim.URL)
	})
	ctx := context.Background()

	create, err := cli.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            awsapi.String("e2e.example.com."),
		CallerReference: awsapi.String("sockerless-through-shim-1"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	id := awsapi.ToString(create.HostedZone.Id)
	t.Cleanup(func() {
		// Best-effort cleanup; the test asserts explicit delete below.
		_, _ = cli.DeleteHostedZone(context.Background(), &route53.DeleteHostedZoneInput{Id: awsapi.String(id)})
	})
	_ = http.MethodGet

	// Add an A record.
	_, err = cli.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsapi.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: awsapi.String("api.e2e.example.com."),
					Type: r53types.RRTypeA,
					TTL:  awsapi.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{
						{Value: awsapi.String("10.0.0.1")},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("UPSERT A: %v", err)
	}

	listRR, err := cli.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId: awsapi.String(id),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}
	foundA := false
	for _, rs := range listRR.ResourceRecordSets {
		if rs.Type == r53types.RRTypeA && awsapi.ToString(rs.Name) == "api.e2e.example.com." {
			foundA = true
		}
	}
	if !foundA {
		t.Errorf("A record not present after through-shim UPSERT: %+v", listRR.ResourceRecordSets)
	}

	// Delete the A record, then the zone.
	_, err = cli.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsapi.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionDelete,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: awsapi.String("api.e2e.example.com."),
					Type: r53types.RRTypeA,
					TTL:  awsapi.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{
						{Value: awsapi.String("10.0.0.1")},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("DELETE A: %v", err)
	}
	if _, err := cli.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: awsapi.String(id)}); err != nil {
		t.Fatalf("DeleteHostedZone: %v", err)
	}
}

// TestSockerless_GCPCloudDNS_Through_Shim_ZoneLifecycle drives the
// shim's Cloud DNS frontend with an SDK call, then has the shim's
// GCP backend translate back to Cloud DNS calls against sockerless's
// Cloud DNS simulator.
func TestSockerless_GCPCloudDNS_Through_Shim_ZoneLifecycle(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	const project = "shim-sockerless"
	gcpSvc, err := dnsraw.NewService(context.Background(),
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "sockerless-test"})),
	)
	if err != nil {
		t.Fatalf("new GCP DNS service: %v", err)
	}
	backend := gcpbackend.New(gcpSvc, project)
	shim := harness.StartDNSServerGCP(t, backend)

	frontendJWT := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://dns.googleapis.com/",
		15*time.Minute,
	)
	cliSvc, err := dnsraw.NewService(context.Background(),
		option.WithEndpoint(shim.URL),
		option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: frontendJWT})),
	)
	if err != nil {
		t.Fatalf("new shim-facing DNS service: %v", err)
	}
	ctx := context.Background()

	created, err := cliSvc.ManagedZones.Create(project, &dnsraw.ManagedZone{
		Name: "e2e-example-com", DnsName: "e2e.example.com.", Visibility: "public",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() {
		_ = cliSvc.ManagedZones.Delete(project, created.Name).Context(context.Background()).Do()
	})

	if _, err := cliSvc.Changes.Create(project, created.Name, &dnsraw.Change{
		Additions: []*dnsraw.ResourceRecordSet{{
			Name: "api.e2e.example.com.", Type: "A", Ttl: 300,
			Rrdatas: []string{"10.0.0.1"},
		}},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Changes.Create: %v", err)
	}
}

// TestSockerless_AzureDNS_Through_Shim_ZoneLifecycle drives the shim's
// Azure DNS frontend with an armdns SDK call, then has the shim's
// Azure backend translate back to ARM calls against sockerless's
// Azure simulator (`public_dns.go`).
//
// Tracked as BUG-45: TLS cert plumbing on both legs (shim outbound +
// test inbound). Sockerless coverage exists; gap is shim test wiring.
func TestSockerless_AzureDNS_Through_Shim_ZoneLifecycle(t *testing.T) {
	t.Skip("BUG-45: Azure DNS through-shim TLS cert plumbing pending. Sockerless coverage exists; this is shim test wiring.")
}

// TestSockerless_AzureDNS_Through_Shim_Terraform_Apply exercises the
// shim's Azure DNS frontend in **ARM passthrough mode** end-to-end
// with `azurerm` Terraform Apply.
//
// Closes BUG-46. The shim's `/metadata/endpoints` handler points
// `resourceManager` at the shim itself and the rest of the cloud
// service URLs (login, graph, …) at sockerless's Azure ARM mock.
// `metadata_host = "<shim>"` makes azurerm fetch this metadata,
// acquire its service-principal token from sockerless's Entra ID
// stub, and then route ARM calls back through the shim — DNS-
// specific paths handled locally, resource-group + subscription
// paths forwarded to sockerless.
func TestSockerless_AzureDNS_Through_Shim_Terraform_Apply(t *testing.T) {
	azureTLSPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azureTLSPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	systemCABundle := findSystemCABundleForDNS()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found — SSL_CERT_FILE workaround requires Linux")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		resourceGroup  = "shim-dns-rg"
		zoneName       = "azure.example.com"
	)

	// 1. Build a reverse proxy from the shim → sockerless's Azure ARM
	//    endpoint. The proxy's transport trusts the sockerless self-
	//    signed cert via a RootCAs pool (no InsecureSkipVerify).
	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("append sockerless cert to pool")
	}
	proxy := httputil.NewSingleHostReverseProxy(sockerlessARM)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}}

	// 2. Fetch sockerless's JWKS so the shim's Azure bearer verifier
	//    accepts the tokens sockerless's Entra ID stub issues to the
	//    azurerm provider. Without this the shim returns 401 on every
	//    ARM call (real Azure validates JWTs against Microsoft Entra's
	//    JWKS at /<tenant>/discovery/v2.0/keys; we mirror that here).
	jwks := fetchSockerlessDNSJWKS(t, azureTLSPort, tenantID, sockCertPEM)

	// 3. Start the shim with passthrough → sockerless ARM and the
	//    cloud-metadata endpoint pointing auth + service URLs at
	//    sockerless. Bearer verifier configured against sockerless's
	//    JWKS. Drives DNS paths locally against an inmem backend.
	shim := harness.StartDNSServerAzureWithConfig(t, inmem.New(), azuredfront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer:   fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			Audience: "https://management.azure.com/",
			JWKS:     jwks,
		},
	})

	// 3. Combined CA bundle = system + sockerless cert + shim cert so
	//    Terraform's HTTPS handshakes succeed on both legs.
	dir := t.TempDir()
	systemBytes, err := os.ReadFile(systemCABundle)
	if err != nil {
		t.Fatalf("read system CA: %v", err)
	}
	combined := append(append([]byte{}, systemBytes...), '\n')
	combined = append(combined, sockCertPEM...)
	combined = append(combined, '\n')
	combined = append(combined, shim.CertPEM...)
	combinedPath := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedPath, combined, 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	// 4. Terraform config — single resource_manager endpoint, drives
	//    both DNS and RG operations through the shim.
	hcl := fmt.Sprintf(`
terraform {
  required_providers {
    azurerm = { source = "hashicorp/azurerm", version = "~> 4.0" }
  }
}

provider "azurerm" {
  features {}
  metadata_host                   = %q
  subscription_id                 = %q
  tenant_id                       = %q
  client_id                       = %q
  client_secret                   = "shim-test"
  use_oidc                        = false
  use_cli                         = false
  resource_provider_registrations = "none"
}

resource "azurerm_resource_group" "tf" {
  name     = %q
  location = "global"
}

resource "azurerm_dns_zone" "tf" {
  name                = %q
  resource_group_name = azurerm_resource_group.tf.name
}

resource "azurerm_dns_a_record" "www" {
  name                = "www"
  zone_name           = azurerm_dns_zone.tf.name
  resource_group_name = azurerm_resource_group.tf.name
  ttl                 = 300
  records             = ["1.2.3.4", "5.6.7.8"]
}
`, shimHost(shim.URL), subscriptionID, tenantID, clientID, resourceGroup, zoneName)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	runTf := func(args ...string) {
		t.Helper()
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1", "TF_INPUT=0", "CHECKPOINT_DISABLE=1",
			"TF_PLUGIN_CACHE_DIR="+terraformPluginCacheDirForDNSWorkdir(dir),
			"SSL_CERT_FILE="+combinedPath,
			"ARM_CLIENT_SECRET=shim-test",
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("terraform %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout.String(), stderr.String(), err)
		}
	}
	runTf("init", "-no-color")
	runTf("apply", "-auto-approve", "-no-color")
	runTf("destroy", "-auto-approve", "-no-color")
}

// shimHost extracts the `host:port` authority from a URL like
// `https://127.0.0.1:NN`. azurerm's `metadata_host` expects this
// shape (no scheme); it prepends https:// itself.
func shimHost(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Host
}

// fetchSockerlessDNSJWKS pulls sockerless's Entra ID stub JWKS so the
// shim's Azure bearer verifier validates tokens sockerless issues.
// Mirrors `services/secrets/conformance/sockerless_test.go`'s helper.
func fetchSockerlessDNSJWKS(t *testing.T, azurePort, tenantID string, certPEM []byte) *azurebearer.JWKS {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		t.Fatal("AppendCertsFromPEM: no certs parsed")
	}
	client := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}
	url := fmt.Sprintf("https://localhost:%s/%s/discovery/v2.0/keys", azurePort, tenantID)
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read JWKS body: %v", err)
	}
	var jwks azurebearer.JWKS
	if err := json.Unmarshal(body, &jwks); err != nil {
		t.Fatalf("parse JWKS: %v\nbody: %s", err, body)
	}
	if len(jwks.Keys) == 0 {
		t.Fatalf("JWKS at %s is empty", url)
	}
	return &jwks
}
