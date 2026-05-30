package aws

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"

	"github.com/e6qu/shimanism/internal/dns/domain"
)

func TestCanonicalize(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"example.com", "example.com."},
		{"Example.COM", "example.com."},
		{"example.com.", "example.com."},
		{"  example.com  ", "example.com."},
		{"", "."},
	}
	for _, tc := range cases {
		if got := canonicalize(tc.in); got != tc.want {
			t.Errorf("canonicalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEncodeDecodeTXT(t *testing.T) {
	cases := []struct {
		raw, encoded string
	}{
		{"hello", `"hello"`},
		{"v=spf1 -all", `"v=spf1 -all"`},
		{`already "quoted"`, `"already \"quoted\""`},
	}
	for _, tc := range cases {
		got := encodeTXT(tc.raw)
		if got != tc.encoded {
			t.Errorf("encodeTXT(%q) = %q, want %q", tc.raw, got, tc.encoded)
		}
		// encodeTXT must be idempotent when given an already-encoded value.
		if got2 := encodeTXT(tc.encoded); got2 != tc.encoded {
			t.Errorf("encodeTXT(%q) = %q, want idempotent %q", tc.encoded, got2, tc.encoded)
		}
	}
	// decodeTXT strips the outer double quotes Route 53 returns.
	if got := decodeTXT(`"hello"`); got != "hello" {
		t.Errorf("decodeTXT: got %q, want %q", got, "hello")
	}
	if got := decodeTXT("unquoted"); got != "unquoted" {
		t.Errorf("decodeTXT(unquoted) = %q, want unchanged", got)
	}
}

func TestAWSRecordSetFromDomain_Errors(t *testing.T) {
	cases := []struct {
		name string
		rs   domain.RecordSet
	}{
		{"missing-name", domain.RecordSet{Type: domain.RecordTypeA, TTL: 60, Records: []string{"1.2.3.4"}}},
		{"missing-type", domain.RecordSet{Name: "www.example.com.", TTL: 60, Records: []string{"1.2.3.4"}}},
		{"zero-ttl", domain.RecordSet{Name: "www.example.com.", Type: domain.RecordTypeA, Records: []string{"1.2.3.4"}}},
		{"no-records", domain.RecordSet{Name: "www.example.com.", Type: domain.RecordTypeA, TTL: 60}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := awsRecordSetFromDomain(tc.rs)
			if err == nil {
				t.Fatalf("expected InvalidArgument for %s, got nil", tc.name)
			}
			if !domain.IsKind(err, domain.KindInvalidArgument) {
				t.Fatalf("err kind not InvalidArgument: %v", err)
			}
		})
	}
}

func TestAWSRecordSetFromDomain_ARecord(t *testing.T) {
	got, err := awsRecordSetFromDomain(domain.RecordSet{
		Name:    "WWW.example.com",
		Type:    domain.RecordTypeA,
		TTL:     60,
		Records: []string{"1.2.3.4", "5.6.7.8"},
	})
	if err != nil {
		t.Fatalf("awsRecordSetFromDomain: %v", err)
	}
	if name := aws.ToString(got.Name); name != "www.example.com." {
		t.Errorf("Name not canonicalised: %q", name)
	}
	if got.Type != r53types.RRTypeA {
		t.Errorf("Type = %s, want A", got.Type)
	}
	if got.TTL == nil || *got.TTL != 60 {
		t.Errorf("TTL = %v, want 60", got.TTL)
	}
	if len(got.ResourceRecords) != 2 {
		t.Fatalf("ResourceRecords len = %d, want 2", len(got.ResourceRecords))
	}
}

func TestAWSRecordSetFromDomain_TXTRoundTrip(t *testing.T) {
	rs := domain.RecordSet{
		Name:    "_dmarc.example.com.",
		Type:    domain.RecordTypeTXT,
		TTL:     300,
		Records: []string{"v=DMARC1; p=none"},
	}
	got, err := awsRecordSetFromDomain(rs)
	if err != nil {
		t.Fatalf("awsRecordSetFromDomain: %v", err)
	}
	if val := aws.ToString(got.ResourceRecords[0].Value); val != `"v=DMARC1; p=none"` {
		t.Errorf("TXT value not quoted: %q", val)
	}

	back := domainRecordSetFromAWS(*got)
	if back.Records[0] != rs.Records[0] {
		t.Errorf("round-trip mismatch: got %q, want %q", back.Records[0], rs.Records[0])
	}
}

func TestZoneFromAWS(t *testing.T) {
	hz := &r53types.HostedZone{
		Id:   aws.String("/hostedzone/Z123"),
		Name: aws.String("example.com."),
		Config: &r53types.HostedZoneConfig{
			Comment:     aws.String("my zone"),
			PrivateZone: true,
		},
	}
	ds := &r53types.DelegationSet{
		NameServers: []string{"ns-1.aws.", "ns-2.aws."},
	}
	z := zoneFromAWS(hz, ds)
	if z.Name != "example.com." {
		t.Errorf("Name = %q", z.Name)
	}
	if z.Visibility != domain.VisibilityPrivate {
		t.Errorf("Visibility = %s, want private", z.Visibility)
	}
	if z.Description != "my zone" {
		t.Errorf("Description = %q", z.Description)
	}
	if len(z.NameServers) != 2 {
		t.Errorf("NameServers = %v", z.NameServers)
	}
}

func TestZoneFromAWS_PublicByDefault(t *testing.T) {
	hz := &r53types.HostedZone{
		Id:   aws.String("/hostedzone/Z456"),
		Name: aws.String("example.org."),
	}
	z := zoneFromAWS(hz, nil)
	if z.Visibility != domain.VisibilityPublic {
		t.Errorf("Visibility = %s, want public", z.Visibility)
	}
	if z.NameServers != nil {
		t.Errorf("NameServers should be nil when no delegation set, got %v", z.NameServers)
	}
}
