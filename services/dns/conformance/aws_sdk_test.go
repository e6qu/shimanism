// Conformance: AWS Route 53-shaped frontend exercised by the official
// `aws-sdk-go-v2/service/route53` SDK. The SDK is pointed at the shim
// via BaseEndpoint; the shim's SigV4 verifier checks the request
// signature against the trusted test credentials.
package conformance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

// newRoute53Client builds an aws-sdk-go-v2 Route 53 client pointed at
// the shim. Same SigV4 credentials the verifier trusts.
func newRoute53Client(t *testing.T, endpoint string) *route53.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(),
		config.WithRegion("us-east-1"),
		config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
		}),
	)
	if err != nil {
		t.Fatalf("load aws config: %v", err)
	}
	return route53.NewFromConfig(cfg, func(o *route53.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
}

func TestAWSSDK_Route53_ZoneLifecycle(t *testing.T) {
	srv := harness.StartDNSServerAWS(t, inmem.New())
	cli := newRoute53Client(t, srv.URL)
	ctx := context.Background()

	// CreateHostedZone — public zone, with a comment.
	create, err := cli.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String("example.com."),
		CallerReference: aws.String("conformance-test-1"),
		HostedZoneConfig: &r53types.HostedZoneConfig{
			Comment:     aws.String("conformance fixture"),
			PrivateZone: false,
		},
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	if create.HostedZone == nil || aws.ToString(create.HostedZone.Name) != "example.com." {
		t.Fatalf("CreateHostedZone returned bad zone: %+v", create.HostedZone)
	}
	if create.HostedZone.Config == nil || aws.ToString(create.HostedZone.Config.Comment) != "conformance fixture" {
		t.Errorf("zone Comment not round-tripped: %+v", create.HostedZone.Config)
	}
	if create.DelegationSet == nil || len(create.DelegationSet.NameServers) == 0 {
		t.Errorf("DelegationSet missing or empty: %+v", create.DelegationSet)
	}
	id := aws.ToString(create.HostedZone.Id)

	// GetHostedZone by ID.
	get, err := cli.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("GetHostedZone: %v", err)
	}
	if get.HostedZone == nil || aws.ToString(get.HostedZone.Name) != "example.com." {
		t.Errorf("GetHostedZone wrong zone: %+v", get.HostedZone)
	}

	// ListHostedZones.
	list, err := cli.ListHostedZones(ctx, &route53.ListHostedZonesInput{})
	if err != nil {
		t.Fatalf("ListHostedZones: %v", err)
	}
	if len(list.HostedZones) != 1 || aws.ToString(list.HostedZones[0].Name) != "example.com." {
		t.Errorf("ListHostedZones unexpected: %+v", list.HostedZones)
	}

	// Add an A record.
	_, err = cli.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: aws.String("www.example.com."),
					Type: r53types.RRTypeA,
					TTL:  aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{
						{Value: aws.String("1.2.3.4")},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets UPSERT: %v", err)
	}

	// ListResourceRecordSets should now include the A record alongside
	// the cloud-managed SOA + NS.
	listRR, err := cli.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}
	foundA := false
	for _, rs := range listRR.ResourceRecordSets {
		if rs.Type == r53types.RRTypeA && aws.ToString(rs.Name) == "www.example.com." {
			foundA = true
			if len(rs.ResourceRecords) != 1 || aws.ToString(rs.ResourceRecords[0].Value) != "1.2.3.4" {
				t.Errorf("A record value not round-tripped: %+v", rs.ResourceRecords)
			}
		}
	}
	if !foundA {
		t.Errorf("A record not present in ListResourceRecordSets: %+v", listRR.ResourceRecordSets)
	}

	// Delete the A record so the zone can be deleted (Route 53 refuses
	// to delete zones with user-managed records).
	_, err = cli.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionDelete,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: aws.String("www.example.com."),
					Type: r53types.RRTypeA,
					TTL:  aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{
						{Value: aws.String("1.2.3.4")},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("ChangeResourceRecordSets DELETE: %v", err)
	}

	// DeleteHostedZone.
	_, err = cli.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: aws.String(id)})
	if err != nil {
		t.Fatalf("DeleteHostedZone: %v", err)
	}

	// Verify deletion: GetHostedZone surfaces NoSuchHostedZone via the
	// wrapped error envelope (the SDK decodes this back to a typed
	// error).
	_, err = cli.GetHostedZone(ctx, &route53.GetHostedZoneInput{Id: aws.String(id)})
	if err == nil {
		t.Fatalf("GetHostedZone after delete: expected error, got nil")
	}
	var nshz *r53types.NoSuchHostedZone
	if !errors.As(err, &nshz) {
		// Some SDK versions surface the wrapped error as a generic
		// smithy.APIError; accept either as long as the code matches.
		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "NoSuchHostedZone" {
			t.Fatalf("expected NoSuchHostedZone, got %T: %v", err, err)
		}
	}
}

func TestAWSSDK_Route53_TXTRecord_DoubleQuoted(t *testing.T) {
	srv := harness.StartDNSServerAWS(t, inmem.New())
	cli := newRoute53Client(t, srv.URL)
	ctx := context.Background()

	create, err := cli.CreateHostedZone(ctx, &route53.CreateHostedZoneInput{
		Name:            aws.String("txt.test."),
		CallerReference: aws.String("conformance-test-txt"),
	})
	if err != nil {
		t.Fatalf("CreateHostedZone: %v", err)
	}
	id := aws.ToString(create.HostedZone.Id)
	t.Cleanup(func() {
		_, _ = cli.DeleteHostedZone(context.Background(), &route53.DeleteHostedZoneInput{Id: aws.String(id)})
	})

	// TXT records on the Route 53 wire carry their value double-quoted.
	// The SDK sends the value as the caller provided it; the shim must
	// round-trip the quoted form unchanged so List shows the same value.
	const txtValue = `"v=spf1 -all"`
	_, err = cli.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(id),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: []r53types.Change{{
				Action: r53types.ChangeActionUpsert,
				ResourceRecordSet: &r53types.ResourceRecordSet{
					Name: aws.String("txt.test."),
					Type: r53types.RRTypeTxt,
					TTL:  aws.Int64(300),
					ResourceRecords: []r53types.ResourceRecord{
						{Value: aws.String(txtValue)},
					},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("UPSERT TXT: %v", err)
	}

	listRR, err := cli.ListResourceRecordSets(ctx, &route53.ListResourceRecordSetsInput{
		HostedZoneId:    aws.String(id),
		StartRecordName: aws.String("txt.test."),
		StartRecordType: r53types.RRTypeTxt,
	})
	if err != nil {
		t.Fatalf("ListResourceRecordSets: %v", err)
	}
	var got string
	for _, rs := range listRR.ResourceRecordSets {
		if rs.Type == r53types.RRTypeTxt {
			got = aws.ToString(rs.ResourceRecords[0].Value)
		}
	}
	if got != txtValue {
		t.Errorf("TXT round-trip: got %q, want %q", got, txtValue)
	}
}
