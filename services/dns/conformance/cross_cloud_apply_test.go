// Cross-cloud Apply cells for DNS — drive one cloud's SDK against
// the shim, materialize records in a different cloud via sockerless.
// Demonstrates the value-prop of shimanism: write portable DNS via
// any frontend, land them in any backend.
//
// Matrix (3 source frontends × 2 destination backends = 6 cells,
// excluding same-cloud passthroughs and the not-yet-shipped K8s
// peer row):
//
//	AWS Route 53 frontend       → GCP Cloud DNS backend  → sockerless
//	AWS Route 53 frontend       → Azure DNS backend      → sockerless
//	GCP Cloud DNS frontend      → AWS Route 53 backend   → sockerless
//	GCP Cloud DNS frontend      → Azure DNS backend      → sockerless
//	Azure DNS frontend          → AWS Route 53 backend   → sockerless
//	Azure DNS frontend          → GCP Cloud DNS backend  → sockerless
//
// Each cell exercises full through-shim flow with the destination
// cloud's backend translating shim's domain.DNS into destination-
// cloud SDK calls. Sockerless is the destination-cloud stand-in.
//
// All cells share the standard sockerless-gating: env vars from the
// run script. Azure cells additionally need the BUG-46 metadata +
// JWKS plumbing; Linux-only via SSL_CERT_FILE.
package conformance_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"
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
	dnsdomain "github.com/e6qu/shimanism/internal/dns/domain"
	azuredfront "github.com/e6qu/shimanism/internal/dns/frontends/azure_dns"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	awsbackend "github.com/e6qu/shimanism/services/dns/backends/aws"
	azurebackend "github.com/e6qu/shimanism/services/dns/backends/azure"
	corednsbackend "github.com/e6qu/shimanism/services/dns/backends/coredns"
	gcpbackend "github.com/e6qu/shimanism/services/dns/backends/gcp"
)

// ---------------- helpers ----------------

func sockerlessAWSConfig(t *testing.T) awsapi.Config {
	t.Helper()
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
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec
		})
	}
	return cfg
}

// sockerlessAzureBackend constructs the shim's Azure DNS backend
// wired to sockerless's Azure ARM mock under TLS. Used when Azure
// is the destination cloud in a cross-cloud cell.
func sockerlessAzureBackend(t *testing.T, azureTLSPort string, sockCertPEM []byte) (*azurebackend.Backend, string) {
	t.Helper()
	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("AppendCertsFromPEM")
	}
	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		resourceGroup  = "shim-dns-cross-cloud-rg"
	)
	cred := sockerlessTokenCred{
		sockerlessURL: sockerlessARM.String(),
		tenantID:      tenantID,
		certPool:      pool,
		scope:         sockerlessARM.String() + "/.default",
	}
	backend, err := azurebackend.New(cred, azurebackend.Options{
		SubscriptionID: subscriptionID,
		ResourceGroup:  resourceGroup,
		ClientOptions: &arm.ClientOptions{
			ClientOptions: azcore.ClientOptions{
				Transport: &http.Client{
					Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
				},
				Cloud: cloud.Configuration{
					ActiveDirectoryAuthorityHost: sockerlessARM.String() + "/",
					Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
						cloud.ResourceManager: {
							Audience: sockerlessARM.String(),
							Endpoint: sockerlessARM.String(),
						},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("azure backend: %v", err)
	}
	return backend, resourceGroup
}

func newShimRoute53Client(t *testing.T, shimURL string) *route53.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
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
	return route53.NewFromConfig(cfg, func(o *route53.Options) {
		o.BaseEndpoint = awsapi.String(shimURL)
	})
}

func newShimCloudDNSService(t *testing.T, shimURL, audience string) *dnsraw.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://dns.googleapis.com/",
		15*time.Minute,
	)
	tokenSource := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := dnsraw.NewService(context.Background(),
		option.WithEndpoint(shimURL),
		option.WithTokenSource(tokenSource),
	)
	if err != nil {
		t.Fatalf("shim cloud dns client: %v", err)
	}
	return svc
}

// ---------------- AWS source cells ----------------

func TestSockerless_DNS_AWSRoute53Frontend_To_GCPBackend(t *testing.T) {
	gcpEndpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if gcpEndpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	gcpSvc, err := dnsraw.NewService(context.Background(),
		option.WithEndpoint("http://"+gcpEndpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("gcp dns client: %v", err)
	}
	backend := gcpbackend.New(gcpSvc, "shim-cross-cloud")
	shim := harness.StartDNSServerAWS(t, backend)
	cli := newShimRoute53Client(t, shim.URL)
	runRoute53ZoneCRUDThroughShim(t, cli, "aws2gcp.example.")
}

func TestSockerless_DNS_AWSRoute53Frontend_To_AzureBackend(t *testing.T) {
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
	backend, _ := sockerlessAzureBackend(t, azureTLSPort, sockCertPEM)
	shim := harness.StartDNSServerAWS(t, backend)
	cli := newShimRoute53Client(t, shim.URL)
	runRoute53ZoneCRUDThroughShim(t, cli, "aws2azure.example.")
}

// runRoute53ZoneCRUDThroughShim drives the canonical Route 53 zone +
// record lifecycle against the shim, regardless of which destination-
// cloud backend sits behind it.
func runRoute53ZoneCRUDThroughShim(t *testing.T, cli *route53.Client, zoneName string) {
	t.Helper()
	ctx := context.Background()
	create, err := cli.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            awsapi.String(zoneName),
		CallerReference: awsapi.String("cross-cloud-" + zoneName),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	id := awsapi.ToString(create.HostedZone.Id)
	t.Cleanup(func() {
		_, _ = cli.DeleteHostedZone(context.Background(), &route53.DeleteHostedZoneInput{Id: awsapi.String(id)})
	})

	_, err = cli.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsapi.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: awsapi.String("api." + zoneName),
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

	// Best-effort delete chain so the zone deletion succeeds. Different
	// destination backends have different SOA + NS-handling semantics;
	// this is a happy-path test, so any cleanup failure is non-fatal.
	_, _ = cli.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: awsapi.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionDelete,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: awsapi.String("api." + zoneName),
					Type: r53types.RRTypeA,
					TTL:  awsapi.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{
						{Value: awsapi.String("10.0.0.1")},
					},
				},
			}},
		},
	})
}

// ---------------- GCP source cells ----------------

func TestSockerless_DNS_GCPCloudDNSFrontend_To_AWSBackend(t *testing.T) {
	awsEndpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if awsEndpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	cfg := sockerlessAWSConfig(t)
	r53Client := route53.NewFromConfig(cfg, func(o *route53.Options) {
		o.BaseEndpoint = awsapi.String(awsEndpoint)
	})
	backend := awsbackend.New(r53Client)
	shim := harness.StartDNSServerGCP(t, backend)
	cli := newShimCloudDNSService(t, shim.URL, "https://dns.googleapis.com/")
	runCloudDNSZoneCRUDThroughShim(t, cli, "shim-cross-cloud", "gcp2aws-example", "gcp2aws.example.")
}

func TestSockerless_DNS_GCPCloudDNSFrontend_To_AzureBackend(t *testing.T) {
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
	backend, _ := sockerlessAzureBackend(t, azureTLSPort, sockCertPEM)
	shim := harness.StartDNSServerGCP(t, backend)
	cli := newShimCloudDNSService(t, shim.URL, "https://dns.googleapis.com/")
	runCloudDNSZoneCRUDThroughShim(t, cli, "shim-cross-cloud", "gcp2azure-example", "gcp2azure.example.")
}

func runCloudDNSZoneCRUDThroughShim(t *testing.T, svc *dnsraw.Service, project, zoneID, dnsName string) {
	t.Helper()
	ctx := context.Background()
	created, err := svc.ManagedZones.Create(project, &dnsraw.ManagedZone{
		Name:       zoneID,
		DnsName:    dnsName,
		Visibility: "public",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}
	if created.DnsName != dnsName {
		t.Errorf("ManagedZones.Create returned DnsName=%q, want %q", created.DnsName, dnsName)
	}
	t.Cleanup(func() {
		_ = svc.ManagedZones.Delete(project, zoneID).Context(context.Background()).Do()
	})

	if _, err := svc.Changes.Create(project, zoneID, &dnsraw.Change{
		Additions: []*dnsraw.ResourceRecordSet{{
			Name: "api." + dnsName, Type: "A", Ttl: 300,
			Rrdatas: []string{"10.0.0.1"},
		}},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Changes.Create UPSERT A: %v", err)
	}

	_, _ = svc.Changes.Create(project, zoneID, &dnsraw.Change{
		Deletions: []*dnsraw.ResourceRecordSet{{
			Name: "api." + dnsName, Type: "A", Ttl: 300,
			Rrdatas: []string{"10.0.0.1"},
		}},
	}).Context(context.Background()).Do()
}

// ---------------- Azure source cells ----------------

func TestSockerless_DNS_AzureDNSFrontend_To_AWSBackend(t *testing.T) {
	awsEndpoint := os.Getenv("SOCKERLESS_AWS_ENDPOINT")
	if awsEndpoint == "" {
		t.Skip("SOCKERLESS_AWS_ENDPOINT not set")
	}
	cfg := sockerlessAWSConfig(t)
	r53Client := route53.NewFromConfig(cfg, func(o *route53.Options) {
		o.BaseEndpoint = awsapi.String(awsEndpoint)
	})
	backend := awsbackend.New(r53Client)
	runAzureDNSCRUDThroughShim(t, backend, "azure2aws.example")
}

func TestSockerless_DNS_AzureDNSFrontend_To_GCPBackend(t *testing.T) {
	gcpEndpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if gcpEndpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	gcpSvc, err := dnsraw.NewService(context.Background(),
		option.WithEndpoint("http://"+gcpEndpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("gcp dns client: %v", err)
	}
	backend := gcpbackend.New(gcpSvc, "shim-cross-cloud")
	runAzureDNSCRUDThroughShim(t, backend, "azure2gcp.example")
}

func runAzureDNSCRUDThroughShim(t *testing.T, backend dnsdomain.DNS, zoneName string) {
	t.Helper()
	// Azure source needs the BUG-46 plumbing (metadata + bearer + JWKS)
	// because the armdns SDK acquires an Entra token before any ARM call.
	azureTLSPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azureTLSPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set (required for Azure-source through-shim cells: SDK auth lives here)")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("AppendCertsFromPEM")
	}
	proxy := httputil.NewSingleHostReverseProxy(sockerlessARM)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}
	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		resourceGroup  = "shim-cross-cloud-rg"
	)
	jwks := fetchSockerlessDNSJWKS(t, azureTLSPort, tenantID, sockCertPEM)

	shim := harness.StartDNSServerAzureWithConfig(t, backend, azuredfront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			JWKS:   jwks,
		},
	})

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	armOpts := &arm.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: httpClient,
			Cloud: cloud.Configuration{
				ActiveDirectoryAuthorityHost: sockerlessARM.String() + "/",
				Services: map[cloud.ServiceName]cloud.ServiceConfiguration{
					cloud.ResourceManager: {
						Audience: sockerlessARM.String(),
						Endpoint: shim.URL,
					},
				},
			},
		},
	}
	cred := sockerlessTokenCred{
		sockerlessURL: sockerlessARM.String(),
		tenantID:      tenantID,
		certPool:      pool,
		scope:         sockerlessARM.String() + "/.default",
	}
	zones, err := armdns.NewZonesClient(subscriptionID, cred, armOpts)
	if err != nil {
		t.Fatalf("NewZonesClient: %v", err)
	}
	rrSets, err := armdns.NewRecordSetsClient(subscriptionID, cred, armOpts)
	if err != nil {
		t.Fatalf("NewRecordSetsClient: %v", err)
	}
	ctx := context.Background()

	if _, err := zones.CreateOrUpdate(ctx, resourceGroup, zoneName, armdns.Zone{
		Location: to.Ptr("global"),
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate zone: %v", err)
	}
	t.Cleanup(func() {
		poller, err := zones.BeginDelete(context.Background(), resourceGroup, zoneName, nil)
		if err != nil {
			return
		}
		_, _ = poller.PollUntilDone(context.Background(), nil)
	})

	if _, err := rrSets.CreateOrUpdate(ctx, resourceGroup, zoneName, "api", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL:      to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{{IPv4Address: to.Ptr("10.0.0.1")}},
		},
	}, nil); err != nil {
		t.Fatalf("CreateOrUpdate A record: %v", err)
	}

	if _, err := rrSets.Delete(ctx, resourceGroup, zoneName, "api", armdns.RecordTypeA, nil); err != nil {
		t.Fatalf("Delete A record: %v", err)
	}
}

// ---------------- K8s row cells (destination = CoreDNS / file-based) ----------------
//
// The CoreDNS backend is local — files in a directory. No sockerless
// dependency for the destination side. AWS / GCP cells need no
// sockerless at all (frontends accept local test creds). The Azure
// cell still needs the through-shim Azure setup (metadata + JWKS +
// bearer) because the armdns SDK acquires tokens before any ARM
// call, so SOCKERLESS_AZURE_TLS_PORT is still required.

func newCoreDNSBackend(t *testing.T) dnsdomain.DNS {
	t.Helper()
	b, err := corednsbackend.New(t.TempDir())
	if err != nil {
		t.Fatalf("coredns backend: %v", err)
	}
	return b
}

func TestSockerless_DNS_AWSRoute53Frontend_To_CoreDNSBackend(t *testing.T) {
	backend := newCoreDNSBackend(t)
	shim := harness.StartDNSServerAWS(t, backend)
	cli := newShimRoute53Client(t, shim.URL)
	runRoute53ZoneCRUDThroughShim(t, cli, "aws2coredns.example.")
}

func TestSockerless_DNS_GCPCloudDNSFrontend_To_CoreDNSBackend(t *testing.T) {
	backend := newCoreDNSBackend(t)
	shim := harness.StartDNSServerGCP(t, backend)
	cli := newShimCloudDNSService(t, shim.URL, "https://dns.googleapis.com/")
	runCloudDNSZoneCRUDThroughShim(t, cli, "shim-cross-cloud", "gcp2coredns-example", "gcp2coredns.example.")
}

func TestSockerless_DNS_AzureDNSFrontend_To_CoreDNSBackend(t *testing.T) {
	backend := newCoreDNSBackend(t)
	runAzureDNSCRUDThroughShim(t, backend, "azure2coredns.example")
}
