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
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/e6qu/shimanism/internal/harness"
	awsbackend "github.com/e6qu/shimanism/services/dns/backends/aws"
)

// TestSockerless_AWSRoute53_Through_Shim_ZoneLifecycle drives the
// shim's Route 53 frontend with an SDK call, then has the shim's AWS
// backend translate back to Route 53 calls against sockerless's Route
// 53 simulator. Both legs round-trip through real client and server
// code.
//
// Skipped pending sockerless#291: sockerless's Route 53 sim doesn't
// implement ListHostedZonesByName, which the shim's AWS backend uses
// for stateless name → HostedZoneId resolution. Re-enable once the
// upstream gap closes.
func TestSockerless_AWSRoute53_Through_Shim_ZoneLifecycle(t *testing.T) {
	t.Skip("blocked on sockerless#291: Route 53 sim missing ListHostedZonesByName")
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
