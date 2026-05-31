// Conformance: Azure DNS-shaped frontend exercised by the official
// `armdns` SDK. The SDK is pointed at the shim via ClientOptions.Cloud
// custom configuration; auth signs with a JWT the shim's azurebearer
// verifier trusts.
package conformance_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/dns/armdns"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

// signedArmCredential signs JWTs the shim's azurebearer verifier accepts
// for the ARM audience.
type signedArmCredential struct{}

func (signedArmCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	jwt := azurebearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://management.azure.com/",
		15*time.Minute,
	)
	return azcore.AccessToken{Token: jwt, ExpiresOn: time.Now().Add(15 * time.Minute)}, nil
}

func newArmClientOptions(t *testing.T, endpoint string) *arm.ClientOptions {
	t.Helper()
	// httptest.NewTLSServer uses a self-signed cert; accept it via the
	// SDK transport.
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	return &arm.ClientOptions{
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
	}
}

func TestAzureSDK_DNS_ZoneLifecycle(t *testing.T) {
	srv := harness.StartDNSServerAzure(t, inmem.New())
	cred := signedArmCredential{}
	opts := newArmClientOptions(t, srv.URL)
	const (
		sub = "00000000-0000-0000-0000-000000000000"
		rg  = "shim-conformance"
	)
	zones, err := armdns.NewZonesClient(sub, cred, opts)
	if err != nil {
		t.Fatalf("NewZonesClient: %v", err)
	}
	rrSets, err := armdns.NewRecordSetsClient(sub, cred, opts)
	if err != nil {
		t.Fatalf("NewRecordSetsClient: %v", err)
	}
	ctx := context.Background()

	zoneName := "example.com"
	created, err := zones.CreateOrUpdate(ctx, rg, zoneName, armdns.Zone{
		Location: to.Ptr("global"),
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate zone: %v", err)
	}
	if created.Zone.Name == nil || *created.Zone.Name != zoneName {
		t.Errorf("zone Name = %v, want %s", created.Zone.Name, zoneName)
	}

	// Add an A record at the apex.
	_, err = rrSets.CreateOrUpdate(ctx, rg, zoneName, "@", armdns.RecordTypeA, armdns.RecordSet{
		Properties: &armdns.RecordSetProperties{
			TTL: to.Ptr(int64(300)),
			ARecords: []*armdns.ARecord{
				{IPv4Address: to.Ptr("1.2.3.4")},
			},
		},
	}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate A: %v", err)
	}

	got, err := rrSets.Get(ctx, rg, zoneName, "@", armdns.RecordTypeA, nil)
	if err != nil {
		t.Fatalf("Get A: %v", err)
	}
	if got.Properties == nil || len(got.Properties.ARecords) != 1 || got.Properties.ARecords[0].IPv4Address == nil ||
		*got.Properties.ARecords[0].IPv4Address != "1.2.3.4" {
		t.Errorf("A record round-trip mismatch: %+v", got.Properties)
	}

	// Delete the record so the zone can be deleted.
	if _, err := rrSets.Delete(ctx, rg, zoneName, "@", armdns.RecordTypeA, nil); err != nil {
		t.Fatalf("Delete A: %v", err)
	}
	poller, err := zones.BeginDelete(ctx, rg, zoneName, nil)
	if err != nil {
		t.Fatalf("BeginDelete zone: %v", err)
	}
	if _, err := poller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone: %v", err)
	}
}
