// Conformance: Azure Event Hubs ARM-shaped frontend exercised by the official
// armeventhub SDK for control plane, plus franz-go/kgo for the Kafka data
// plane. The ARM client is pointed at the shim via custom CloudConfiguration;
// auth signs with a JWT the shim's azurebearer verifier trusts.
package conformance_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/eventhub/armeventhub"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/eventstream/backends/inmem"
)

const (
	azureEHSubscriptionID = "00000000-0000-0000-0000-000000000001"
	azureEHResourceGroup  = "shim-rg"
)

// azureEHCredential issues JWTs the shim's azurebearer verifier accepts.
type azureEHCredential struct{}

func (azureEHCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	jwt := azurebearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://management.azure.com/",
		15*time.Minute,
	)
	return azcore.AccessToken{Token: jwt, ExpiresOn: time.Now().Add(15 * time.Minute)}, nil
}

// newAzureEHClientOptions builds ARM client options pointing at the shim with
// the self-signed TLS cert accepted.
func newAzureEHClientOptions(endpoint string) *arm.ClientOptions {
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

func TestAzureSDK_EventHubsTopicBacksKafkaClientProduceFetch(t *testing.T) {
	backend := inmem.New()
	nsName := "shimns"

	kafkaSrv := harness.StartEventStreamKafkaServerCluster(t, backend, nsName)
	restSrv := harness.StartEventStreamServerAzure(t, backend, []string{kafkaSrv.Address})

	ctx := context.Background()
	opts := newAzureEHClientOptions(restSrv.URL)
	cred := azureEHCredential{}

	nsClient, err := armeventhub.NewNamespacesClient(azureEHSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new NamespacesClient: %v", err)
	}
	ehClient, err := armeventhub.NewEventHubsClient(azureEHSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new EventHubsClient: %v", err)
	}

	// Create namespace (cluster).
	createPoller, err := nsClient.BeginCreateOrUpdate(ctx, azureEHResourceGroup, nsName,
		armeventhub.EHNamespace{
			Location: to.Ptr("eastus"),
			SKU:      &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUName("Standard")), Tier: to.Ptr(armeventhub.SKUTierStandard)},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate namespace: %v", err)
	}
	createdNS, err := createPoller.PollUntilDone(ctx, nil)
	if err != nil {
		t.Fatalf("PollUntilDone namespace: %v", err)
	}
	if *createdNS.Name != nsName {
		t.Fatalf("namespace name = %q, want %q", *createdNS.Name, nsName)
	}
	if createdNS.Properties == nil || createdNS.Properties.KafkaEnabled == nil || !*createdNS.Properties.KafkaEnabled {
		t.Fatalf("namespace KafkaEnabled = false, want true")
	}

	// Get namespace.
	gotNS, err := nsClient.Get(ctx, azureEHResourceGroup, nsName, nil)
	if err != nil {
		t.Fatalf("Get namespace: %v", err)
	}
	if *gotNS.Name != nsName {
		t.Fatalf("Get namespace name = %q, want %q", *gotNS.Name, nsName)
	}

	// List namespaces.
	listPager := nsClient.NewListByResourceGroupPager(azureEHResourceGroup, nil)
	var found bool
	for listPager.More() {
		page, err := listPager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByResourceGroup: %v", err)
		}
		for _, ns := range page.Value {
			if *ns.Name == nsName {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("ListByResourceGroup: namespace %q not found", nsName)
	}

	// Create event hub (topic).
	topic := "orders"
	createEH, err := ehClient.CreateOrUpdate(ctx, azureEHResourceGroup, nsName, topic,
		armeventhub.Eventhub{
			Properties: &armeventhub.Properties{
				PartitionCount:         to.Ptr(int64(1)),
				MessageRetentionInDays: to.Ptr(int64(1)),
			},
		}, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdate eventhub: %v", err)
	}
	if *createEH.Name != topic {
		t.Fatalf("event hub name = %q, want %q", *createEH.Name, topic)
	}
	if createEH.Properties == nil || *createEH.Properties.PartitionCount != 1 {
		t.Fatalf("event hub partitionCount = %v, want 1", createEH.Properties)
	}

	// Get event hub.
	gotEH, err := ehClient.Get(ctx, azureEHResourceGroup, nsName, topic, nil)
	if err != nil {
		t.Fatalf("Get eventhub: %v", err)
	}
	if *gotEH.Name != topic {
		t.Fatalf("Get eventhub name = %q, want %q", *gotEH.Name, topic)
	}

	// List event hubs.
	ehPager := ehClient.NewListByNamespacePager(azureEHResourceGroup, nsName, nil)
	var ehFound bool
	for ehPager.More() {
		page, err := ehPager.NextPage(ctx)
		if err != nil {
			t.Fatalf("ListByNamespace: %v", err)
		}
		for _, eh := range page.Value {
			if *eh.Name == topic {
				ehFound = true
			}
		}
	}
	if !ehFound {
		t.Fatalf("ListByNamespace: event hub %q not found", topic)
	}

	// Kafka data-plane: produce and fetch through the Kafka TCP server.
	kclient, err := kgo.NewClient(
		kgo.SeedBrokers(kafkaSrv.Address),
		kgo.WithLogger(kgoTestLogger{t: t}),
		kgo.DefaultProduceTopic(topic),
		kgo.RecordPartitioner(kgo.ManualPartitioner()),
		kgo.DisableIdempotentWrite(),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
			topic: {0: kgo.NewOffset().At(0)},
		}),
	)
	if err != nil {
		t.Fatalf("kgo client: %v", err)
	}
	t.Cleanup(kclient.Close)

	produceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := kclient.ProduceSync(produceCtx, &kgo.Record{
		Partition: 0,
		Key:       []byte("event-1"),
		Value:     []byte("payload"),
	}).FirstErr(); err != nil {
		t.Fatalf("kgo produce: %v", err)
	}

	fetchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		fetches := kclient.PollFetches(fetchCtx)
		if err := fetches.Err0(); err != nil {
			t.Fatalf("kgo fetch: %v", err)
		}
		for _, record := range fetches.Records() {
			if record.Topic != topic || record.Partition != 0 {
				continue
			}
			if string(record.Key) != "event-1" || string(record.Value) != "payload" {
				t.Fatalf("fetched record = key %q value %q, want event-1/payload", record.Key, record.Value)
			}
			goto fetched
		}
	}

fetched:
	// Delete event hub.
	if _, err := ehClient.Delete(ctx, azureEHResourceGroup, nsName, topic, nil); err != nil {
		t.Fatalf("Delete eventhub: %v", err)
	}
	if _, err := ehClient.Get(ctx, azureEHResourceGroup, nsName, topic, nil); err == nil {
		t.Fatal("Get eventhub after delete: got nil, want not found")
	}

	// Delete namespace.
	delPoller, err := nsClient.BeginDelete(ctx, azureEHResourceGroup, nsName, nil)
	if err != nil {
		t.Fatalf("BeginDelete namespace: %v", err)
	}
	if _, err := delPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone namespace delete: %v", err)
	}
	if _, err := nsClient.Get(ctx, azureEHResourceGroup, nsName, nil); err == nil {
		t.Fatal("Get namespace after delete: got nil, want not found")
	}
}

func TestAzureSDK_EventHubsRejectsOutOfIntersection(t *testing.T) {
	backend := inmem.New()
	nsName := "shimns2"
	kafkaSrv := harness.StartEventStreamKafkaServerCluster(t, backend, nsName)
	restSrv := harness.StartEventStreamServerAzure(t, backend, []string{kafkaSrv.Address})

	ctx := context.Background()
	opts := newAzureEHClientOptions(restSrv.URL)
	cred := azureEHCredential{}

	nsClient, err := armeventhub.NewNamespacesClient(azureEHSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new NamespacesClient: %v", err)
	}
	ehClient, err := armeventhub.NewEventHubsClient(azureEHSubscriptionID, cred, opts)
	if err != nil {
		t.Fatalf("new EventHubsClient: %v", err)
	}

	// Basic SKU should be rejected (no Kafka support).
	p, err := nsClient.BeginCreateOrUpdate(ctx, azureEHResourceGroup, nsName,
		armeventhub.EHNamespace{
			Location: to.Ptr("eastus"),
			SKU:      &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUName("Basic")), Tier: to.Ptr(armeventhub.SKUTierBasic)},
		}, nil)
	if err == nil {
		_, err = p.PollUntilDone(ctx, nil)
	}
	if err == nil {
		t.Fatal("BeginCreateOrUpdate Basic SKU: got nil, want error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "basic") {
		t.Fatalf("Basic SKU error = %v, want message mentioning basic/Basic", err)
	}

	// Create a valid Standard namespace first.
	createPoller, err := nsClient.BeginCreateOrUpdate(ctx, azureEHResourceGroup, nsName,
		armeventhub.EHNamespace{
			Location: to.Ptr("eastus"),
			SKU:      &armeventhub.SKU{Name: to.Ptr(armeventhub.SKUName("Standard")), Tier: to.Ptr(armeventhub.SKUTierStandard)},
		}, nil)
	if err != nil {
		t.Fatalf("BeginCreateOrUpdate Standard namespace: %v", err)
	}
	if _, err := createPoller.PollUntilDone(ctx, nil); err != nil {
		t.Fatalf("PollUntilDone Standard namespace: %v", err)
	}

	// CaptureDescription should be rejected.
	_, err = ehClient.CreateOrUpdate(ctx, azureEHResourceGroup, nsName, "capture-test",
		armeventhub.Eventhub{
			Properties: &armeventhub.Properties{
				PartitionCount:     to.Ptr(int64(1)),
				CaptureDescription: &armeventhub.CaptureDescription{Enabled: to.Ptr(true)},
			},
		}, nil)
	if err == nil {
		t.Fatal("CreateOrUpdate with CaptureDescription: got nil, want error")
	}
	var respErr interface{ StatusCode() int }
	if errors.As(err, &respErr) && respErr.StatusCode() != 400 {
		t.Fatalf("CaptureDescription error status = %d, want 400", respErr.StatusCode())
	}
}
