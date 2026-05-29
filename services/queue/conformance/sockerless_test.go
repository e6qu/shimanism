// Sockerless lane for the queue service. See docs/sockerless-validation.md.
package conformance_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"
	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	awssqsTypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	pubsubraw "google.golang.org/api/pubsub/v1"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/internal/queue/domain"
	awsqueue "github.com/e6qu/shimanism/services/queue/backends/aws"
	azurequeue "github.com/e6qu/shimanism/services/queue/backends/azure"
	gcpqueue "github.com/e6qu/shimanism/services/queue/backends/gcp"
)

func insecureAWSHTTPClient() awsapi.HTTPClient {
	return awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	})
}

// TestSockerless_GCP_Queue_RetentionRoundTrip drives the shim's
// GCP queue backend's full lifecycle including the
// MessageRetentionSeconds attribute round-trip:
// CreateQueue → SetQueueAttributes(MessageRetentionSeconds, VisibilityTimeoutSeconds)
// → HeadQueue, asserts the values survive the shim's PATCH update
// + GCP's `messageRetentionDuration` → shim seconds round-trip.
func TestSockerless_GCP_Queue_RetentionRoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ctx := context.Background()
	svc, err := pubsubraw.NewService(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("pubsub client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpqueue.New(svc, gcpqueue.Config{ProjectID: project})

	queueName := "shim-sk-q-" + sockHex8()
	if _, err := backend.CreateQueue(ctx, queueName, domain.CreateQueueOptions{
		Attributes: domain.QueueAttributes{VisibilityTimeoutSeconds: 30},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteQueue(ctx, queueName) })

	wantRetention := 604800
	if err := backend.SetQueueAttributes(ctx, queueName, domain.QueueAttributes{
		MessageRetentionSeconds:  wantRetention,
		VisibilityTimeoutSeconds: 30,
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	q, err := backend.HeadQueue(ctx, queueName)
	if err != nil {
		t.Fatalf("HeadQueue: %v", err)
	}
	if q.Name != queueName {
		t.Errorf("HeadQueue.Name = %q, want %q", q.Name, queueName)
	}
	if q.Attributes.VisibilityTimeoutSeconds != 30 {
		t.Errorf("VisibilityTimeoutSeconds = %d, want 30", q.Attributes.VisibilityTimeoutSeconds)
	}
	if q.Attributes.MessageRetentionSeconds != wantRetention {
		t.Errorf("MessageRetentionSeconds = %d, want %d", q.Attributes.MessageRetentionSeconds, wantRetention)
	}
}

// TestSockerless_AWSSQSFrontendToGCPBackend_MessageRoundTrip drives
// the full through-shim E2E path for queues:
// aws-sdk-go-v2 SQS client → AWS-shaped shim frontend → GCP Pub/Sub
// queue backend → sockerless GCP simulator.
func TestSockerless_AWSSQSFrontendToGCPBackend_MessageRoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_GCP_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_GCP_ENDPOINT not set")
	}
	ctx := context.Background()
	svc, err := pubsubraw.NewService(ctx,
		option.WithEndpoint("http://"+endpoint+"/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("pubsub client: %v", err)
	}
	project := os.Getenv("SOCKERLESS_GCP_PROJECT")
	if project == "" {
		project = "shim-sockerless"
	}
	backend := gcpqueue.New(svc, gcpqueue.Config{ProjectID: project})
	srv := harness.StartQueueServerAWS(t, backend)
	cli := newSQSClient(t, srv.URL)

	queueName := "shim-sk-xq-" + sockHex8()
	create, err := cli.CreateQueue(ctx, &awssqs.CreateQueueInput{
		QueueName: awsapi.String(queueName),
		Attributes: map[string]string{
			"VisibilityTimeout":      "30",
			"MessageRetentionPeriod": "604800",
		},
	})
	if err != nil {
		t.Fatalf("CreateQueue through shim: %v", err)
	}
	queueURL := awsapi.ToString(create.QueueUrl)
	if queueURL == "" {
		t.Fatal("CreateQueue returned empty QueueUrl")
	}
	t.Cleanup(func() {
		_, _ = cli.DeleteQueue(ctx, &awssqs.DeleteQueueInput{QueueUrl: awsapi.String(queueURL)})
	})

	if _, err := cli.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:    awsapi.String(queueURL),
		MessageBody: awsapi.String("through-shim queue body"),
		MessageAttributes: map[string]awssqsTypes.MessageAttributeValue{
			"env": {DataType: awsapi.String("String"), StringValue: awsapi.String("test")},
		},
	}); err != nil {
		t.Fatalf("SendMessage through shim: %v", err)
	}

	recv, err := cli.ReceiveMessage(ctx, &awssqs.ReceiveMessageInput{
		QueueUrl:              awsapi.String(queueURL),
		MaxNumberOfMessages:   1,
		WaitTimeSeconds:       2,
		MessageAttributeNames: []string{"All"},
	})
	if err != nil {
		t.Fatalf("ReceiveMessage through shim: %v", err)
	}
	if len(recv.Messages) != 1 {
		t.Fatalf("ReceiveMessage count = %d, want 1", len(recv.Messages))
	}
	if got := awsapi.ToString(recv.Messages[0].Body); got != "through-shim queue body" {
		t.Errorf("Body = %q, want through-shim queue body", got)
	}
	if _, err := cli.DeleteMessage(ctx, &awssqs.DeleteMessageInput{
		QueueUrl:      awsapi.String(queueURL),
		ReceiptHandle: recv.Messages[0].ReceiptHandle,
	}); err != nil {
		t.Fatalf("DeleteMessage through shim: %v", err)
	}
}

// TestSockerless_AWS_Queue_AttributeRoundTrip exercises the shim's
// AWS SQS queue backend: CreateQueue Attributes
// (MessageRetentionPeriod, VisibilityTimeout) → HeadQueue, asserts
// the values survive.
func TestSockerless_AWS_Queue_AttributeRoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	if endpoint == "" {
		t.Skip("SOCKERLESS_AWS_SM_ENDPOINT not set (the AWS sim shares the port; reuse the SM endpoint)")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = insecureAWSHTTPClient()
	}
	sqsClient := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend := awsqueue.New(sqsClient)
	ctx := context.Background()

	queueName := "shim-sk-q-" + sockHex8()
	if _, err := backend.CreateQueue(ctx, queueName, domain.CreateQueueOptions{
		Attributes: domain.QueueAttributes{
			VisibilityTimeoutSeconds: 60,
			MessageRetentionSeconds:  86400,
		},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteQueue(ctx, queueName) })

	q, err := backend.HeadQueue(ctx, queueName)
	if err != nil {
		t.Fatalf("HeadQueue: %v", err)
	}
	if q.Attributes.VisibilityTimeoutSeconds != 60 {
		t.Errorf("VisibilityTimeoutSeconds = %d, want 60", q.Attributes.VisibilityTimeoutSeconds)
	}
	if q.Attributes.MessageRetentionSeconds != 86400 {
		t.Errorf("MessageRetentionSeconds = %d, want 86400", q.Attributes.MessageRetentionSeconds)
	}
}

func sockHex8() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TestSockerless_Azure_ServiceBus_Queue_CRUD exercises the shim's
// Azure Service Bus queue backend's admin surface against sockerless's
// new namespace-level ATOM XML admin protocol (sockerless#225,
// sockerless PR #225 merged 2026-05-26). CreateQueue →
// SetQueueAttributes (LockDuration + DefaultMessageTimeToLive
// updates) → HeadQueue → ListQueues → DeleteQueue.
//
// The AMQP data plane is not exercised — sockerless implements REST
// data-plane operations but not AMQP, and the shim's queue domain's
// SendMessage / ReceiveMessage round-trip goes through azservicebus
// AMQP. Test stays admin-only.
func TestSockerless_Azure_ServiceBus_Queue_CRUD(t *testing.T) {
	port := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if port == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	connStr := sockerlessAzureSBConnectionString()
	backend, err := azurequeue.New(azurequeue.Config{
		ConnectionString:   connStr,
		AdminClientOptions: sockerlessSBAdminClientOptions(port),
	})
	if err != nil {
		t.Fatalf("azurequeue.New: %v", err)
	}
	ctx := context.Background()

	name := "shim-sk-sbq-" + sockHex8()
	if _, err := backend.CreateQueue(ctx, name, domain.CreateQueueOptions{
		Attributes: domain.QueueAttributes{
			VisibilityTimeoutSeconds: 30,
			MessageRetentionSeconds:  60,
		},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteQueue(ctx, name) })

	if err := backend.SetQueueAttributes(ctx, name, domain.QueueAttributes{
		VisibilityTimeoutSeconds: 60,
		MessageRetentionSeconds:  604800,
	}); err != nil {
		t.Fatalf("SetQueueAttributes: %v", err)
	}

	q, err := backend.HeadQueue(ctx, name)
	if err != nil {
		t.Fatalf("HeadQueue: %v", err)
	}
	if q.Name != name {
		t.Errorf("HeadQueue.Name = %q, want %q", q.Name, name)
	}
	if q.Attributes.VisibilityTimeoutSeconds != 60 {
		t.Errorf("VisibilityTimeoutSeconds = %d, want 60",
			q.Attributes.VisibilityTimeoutSeconds)
	}
	if q.Attributes.MessageRetentionSeconds != 604800 {
		t.Errorf("MessageRetentionSeconds = %d, want 604800",
			q.Attributes.MessageRetentionSeconds)
	}

	list, err := backend.ListQueues(ctx, domain.ListQueuesOptions{})
	if err != nil {
		t.Fatalf("ListQueues: %v", err)
	}
	found := false
	for _, lq := range list.Queues {
		if lq.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListQueues did not contain %q", name)
	}
}

// sockerlessAzureSBConnectionString returns a Service Bus connection
// string targeted at the sockerless namespace `test-ns`. The SAS key
// is a fixed base64 string the sim doesn't actually verify; the
// custom transport pinned to the sim's port handles the routing.
func sockerlessAzureSBConnectionString() string {
	return "Endpoint=sb://test-ns.servicebus.windows.net/;" +
		"SharedAccessKeyName=RootManageSharedAccessKey;" +
		"SharedAccessKey=c29ja2VybGVzcy10ZXN0LWtleS1ub3QtdmVyaWZpZWQK"
}

// sockerlessSBAdminClientOptions builds admin.ClientOptions whose
// transport dials 127.0.0.1:<port> regardless of the request URL's
// host. The Host header (`test-ns.servicebus.windows.net`) survives
// — sockerless's `*.servicebus.*` host dispatcher parses the
// namespace prefix from it. InsecureSkipVerify because the sim's
// cert is self-signed.
func sockerlessSBAdminClientOptions(port string) *admin.ClientOptions {
	dialer := &net.Dialer{}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+port)
		},
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &admin.ClientOptions{
		ClientOptions: azcore.ClientOptions{
			Transport: &http.Client{Transport: transport},
		},
	}
}

// TestSockerless_Azure_ServiceBus_Queue_SendReceive drives the shim's
// Azure Service Bus queue backend's full data-plane round-trip
// against sockerless's raw AMQP/TLS transport (added in sockerless
// PR #231): CreateQueue (admin, ATOM XML) → SendMessage (azservicebus
// AMQP/TLS) → ReceiveMessages → DeleteMessage → DeleteQueue.
//
// The test driver is the Azure SDK throughout. The two transport
// knobs the SDK exposes — `admin.ClientOptions.Transport` for the
// ATOM HTTPS admin client and `azservicebus.ClientOptions.CustomEndpoint`
// + `TLSConfig` for the AMQP data client — point at the sim. The
// SDK speaks its native protocols on top; no transport adapter
// code lives in the test.
func TestSockerless_Azure_ServiceBus_Queue_SendReceive(t *testing.T) {
	httpPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if httpPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	amqpPort := os.Getenv("SOCKERLESS_AZURE_SB_AMQP_PORT")
	if amqpPort == "" {
		t.Skip("SOCKERLESS_AZURE_SB_AMQP_PORT not set")
	}
	backend, err := azurequeue.New(azurequeue.Config{
		ConnectionString:   sockerlessAzureSBConnectionString(),
		AdminClientOptions: sockerlessSBAdminClientOptions(httpPort),
		DataClientOptions: &azservicebus.ClientOptions{
			CustomEndpoint: "localhost:" + amqpPort,
			TLSConfig:      &tls.Config{InsecureSkipVerify: true},
		},
	})
	if err != nil {
		t.Fatalf("azurequeue.New: %v", err)
	}
	ctx := context.Background()

	name := "shim-sk-sbq-sr-" + sockHex8()
	if _, err := backend.CreateQueue(ctx, name, domain.CreateQueueOptions{
		Attributes: domain.QueueAttributes{
			VisibilityTimeoutSeconds: 30,
			MessageRetentionSeconds:  3600,
		},
	}); err != nil {
		t.Fatalf("CreateQueue: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteQueue(ctx, name) })

	body := []byte("shim-sb-amqp round-trip body")
	if _, err := backend.SendMessage(ctx, name, domain.SendMessageOptions{Body: body}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}

	msgs, err := backend.ReceiveMessages(ctx, name, domain.ReceiveMessagesOptions{
		MaxMessages: 1,
		WaitTime:    5,
	})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatalf("ReceiveMessages returned no messages")
	}
	if got := string(msgs[0].Body); got != string(body) {
		t.Errorf("ReceiveMessages body = %q, want %q", got, string(body))
	}
}

// TestSockerless_E2E_AzureServiceBus_Through_Shim_ApplyTF extends the
// 14.E through-shim Apply pattern (PR #58 storage, PR #59 KV) to
// Azure Service Bus. azurerm Terraform Apply creates the namespace
// + queue via sockerless's real Microsoft.ServiceBus ARM and emits
// the namespace's default SAS connection string as a Terraform
// output. The shim's azure_servicebus *backend* then drives a full
// Send/Receive against that connection string with the AMQP/TLS
// transport pinned at sockerless's separate AMQP listener via
// `CustomEndpoint`. End-to-end this validates that
// azurerm-produced connection strings parse cleanly in the shim's
// backend layer and route correctly to the SB data plane sockerless
// holds.
//
// Unlike the storage/KV cells, the shim's *frontend* isn't on the
// path here — Azure Service Bus data plane is AMQP, and shimanism's
// `internal/queue/frontends/azure_servicebus` is REST/ATOM-only
// (AMQP listener is out of scope by design — sockerless is the AMQP
// server). Through-shim coverage for SB therefore lives on the
// backend translation layer: the shim's azurequeue backend parsing
// the connection string and dispatching via `azservicebus.Client`.
//
// Linux-only (SSL_CERT_FILE platform limit); skips on darwin.
func TestSockerless_E2E_AzureServiceBus_Through_Shim_ApplyTF(t *testing.T) {
	// Blocked on sockerless#276: azurerm reads
	// `Microsoft.ServiceBus/namespaces/{name}/networkRuleSets/default`
	// after namespace creation to populate the `network_rule_set`
	// computed attribute, and sockerless's SB ARM surface doesn't
	// implement that sub-resource yet (catch-all 404). Re-enable once
	// #276 lands the GET handler.
	t.Skip("blocked on sockerless#276: namespaces/{name}/networkRuleSets/default not implemented in sockerless's Microsoft.ServiceBus ARM")
	azurePort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azurePort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	amqpPort := os.Getenv("SOCKERLESS_AZURE_SB_AMQP_PORT")
	if amqpPort == "" {
		t.Skip("SOCKERLESS_AZURE_SB_AMQP_PORT not set")
	}
	tfBin, err := exec.LookPath("terraform")
	if err != nil {
		t.Skipf("terraform not installed: %v", err)
	}
	systemCABundle := findSystemCABundleForSB()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found at known Unix paths — SSL_CERT_FILE workaround requires Linux")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set (the run script exports this)")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		resourceGroup  = "shim-sb-rg"
		namespaceName  = "shimsbns"
		queueName      = "applied-queue"
	)

	dir := t.TempDir()
	hcl := fmt.Sprintf(terraformAzureSBApplyConfig,
		"localhost:"+azurePort, // %[1]s metadata_host
		subscriptionID,         // %[2]s
		tenantID,               // %[3]s
		clientID,               // %[4]s
		resourceGroup,          // %[5]s
		namespaceName,          // %[6]s
		queueName,              // %[7]s
	)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(hcl), 0o644); err != nil {
		t.Fatalf("write main.tf: %v", err)
	}

	systemBytes, err := os.ReadFile(systemCABundle)
	if err != nil {
		t.Fatalf("read system CA bundle %s: %v", systemCABundle, err)
	}
	sockBytes, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert %s: %v", sockCertPath, err)
	}
	combinedCA := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedCA, append(append(systemBytes, '\n'), sockBytes...), 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}

	runTf := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(tfBin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"TF_IN_AUTOMATION=1",
			"TF_INPUT=0",
			"CHECKPOINT_DISABLE=1",
			"SSL_CERT_FILE="+combinedCA,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
	}
	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runTf(args...)
		if err != nil {
			t.Fatalf("terraform %s\nstdout:\n%s\nstderr:\n%s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("init", "-no-color")
	applyOut := mustRun("apply", "-no-color", "-auto-approve")
	t.Logf("terraform apply stdout:\n%s", applyOut)
	t.Cleanup(func() { _, _, _ = runTf("destroy", "-no-color", "-auto-approve") })

	connStrOut := mustRun("output", "-raw", "primary_connection_string")
	connStr := strings.TrimSpace(string(connStrOut))
	if connStr == "" {
		t.Fatal("terraform output primary_connection_string was empty")
	}
	t.Logf("connection string from azurerm: %s", connStr)

	backend, err := azurequeue.New(azurequeue.Config{
		ConnectionString:   connStr,
		AdminClientOptions: sockerlessSBAdminClientOptions(azurePort),
		DataClientOptions: &azservicebus.ClientOptions{
			CustomEndpoint: "localhost:" + amqpPort,
			TLSConfig:      &tls.Config{InsecureSkipVerify: true},
		},
	})
	if err != nil {
		t.Fatalf("azurequeue.New with terraform-emitted connection string: %v", err)
	}
	ctx := context.Background()

	body := []byte("apply-driven SB round-trip body")
	if _, err := backend.SendMessage(ctx, queueName, domain.SendMessageOptions{Body: body}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	msgs, err := backend.ReceiveMessages(ctx, queueName, domain.ReceiveMessagesOptions{
		MaxMessages: 1,
		WaitTime:    5,
	})
	if err != nil {
		t.Fatalf("ReceiveMessages: %v", err)
	}
	if len(msgs) == 0 {
		t.Fatalf("ReceiveMessages returned no messages")
	}
	if got := string(msgs[0].Body); got != string(body) {
		t.Errorf("ReceiveMessages body = %q, want %q", got, string(body))
	}
}

// findSystemCABundleForSB mirrors the helper in the storage / KV
// sockerless tests. Each conformance package compiles independently
// so this stays duplicated.
func findSystemCABundleForSB() string {
	for _, p := range []string{
		"/etc/ssl/certs/ca-certificates.crt",
		"/etc/pki/tls/certs/ca-bundle.crt",
		"/etc/ssl/ca-bundle.pem",
		"/etc/pki/tls/cacert.pem",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

const terraformAzureSBApplyConfig = `
terraform {
  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~> 4.0"
    }
  }
}

provider "azurerm" {
  features {}
  metadata_host                   = "%[1]s"
  subscription_id                 = "%[2]s"
  tenant_id                       = "%[3]s"
  client_id                       = "%[4]s"
  client_secret                   = "test-secret-do-not-use-in-prod"
  resource_provider_registrations = "none"
}

resource "azurerm_resource_group" "rg" {
  name     = "%[5]s"
  location = "eastus"
}

resource "azurerm_servicebus_namespace" "ns" {
  name                = "%[6]s"
  resource_group_name = azurerm_resource_group.rg.name
  location            = azurerm_resource_group.rg.location
  sku                 = "Standard"
}

resource "azurerm_servicebus_queue" "q" {
  name         = "%[7]s"
  namespace_id = azurerm_servicebus_namespace.ns.id
}

output "primary_connection_string" {
  value     = azurerm_servicebus_namespace.ns.default_primary_connection_string
  sensitive = true
}
`

// gcpHS256Bearer mints a test-mode HS256 JWT that the shim's
// gcpbearer middleware accepts.
func gcpHS256Bearer(t *testing.T, audience string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT","kid":"shim-test"}`))
	payloadJSON := []byte(`{"aud":"` + audience + `","exp":4102444800,"iat":1}`)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	signingInput := header + "." + payload
	mac := hmac.New(sha256.New, []byte("test-key-do-not-use-in-prod"))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

type gcpStaticTokenSource struct{ token string }

func (s gcpStaticTokenSource) Token() (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token, TokenType: "Bearer"}, nil
}

// TestSockerless_GCPPubsubQueueFrontendToAWSBackend_RoundTrip is the
// reverse-direction through-shim cell for queues: GCP Pub/Sub SDK
// drives the shim's GCP queue frontend (Pub/Sub-shaped), which
// routes through the shim's AWS SQS backend, which targets
// sockerless's AWS sim. GCP→AWS migration path for queues; the
// queue domain abstracts the SQS / Pub/Sub semantics so this is a
// real bidirectional translation test.
func TestSockerless_GCPPubsubQueueFrontendToAWSBackend_RoundTrip(t *testing.T) {
	awsEndpoint := os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	if awsEndpoint == "" {
		t.Skip("SOCKERLESS_AWS_SM_ENDPOINT not set")
	}
	if os.Getenv("AWS_ACCESS_KEY_ID") == "" {
		os.Setenv("AWS_ACCESS_KEY_ID", "test")
	}
	if os.Getenv("AWS_SECRET_ACCESS_KEY") == "" {
		os.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	}
	if os.Getenv("AWS_REGION") == "" {
		os.Setenv("AWS_REGION", "us-east-1")
	}
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		t.Fatalf("aws config: %v", err)
	}
	if os.Getenv("AWS_S3_CONFORMANCE_INSECURE_TLS") == "1" {
		cfg.HTTPClient = insecureAWSHTTPClient()
	}
	sqsClient := awssqs.NewFromConfig(cfg, func(o *awssqs.Options) {
		o.BaseEndpoint = awsapi.String(awsEndpoint)
	})
	backend := awsqueue.New(sqsClient)
	srv := harness.StartQueueServerGCP(t, backend)

	ctx := context.Background()
	svc, err := pubsubraw.NewService(ctx,
		option.WithEndpoint(srv.URL+"/"),
		option.WithTokenSource(gcpStaticTokenSource{token: gcpHS256Bearer(t, "https://pubsub.googleapis.com/")}),
	)
	if err != nil {
		t.Fatalf("pubsub client: %v", err)
	}

	project := "shim-sockerless"
	topicName := "projects/" + project + "/topics/shim-sk-rev-q-" + sockHex8()
	// Pub/Sub-shaped queue: a topic with attached subscription. The
	// shim's GCP queue frontend maps this to AWS SQS CreateQueue.
	if _, err := svc.Projects.Topics.Create(topicName, &pubsubraw.Topic{}).Do(); err != nil {
		t.Fatalf("Topics.Create through shim: %v", err)
	}
	t.Cleanup(func() {
		_, _ = svc.Projects.Topics.Delete(topicName).Do()
	})

	// HeadQueue equivalent via topics.get
	got, err := svc.Projects.Topics.Get(topicName).Do()
	if err != nil {
		t.Fatalf("Topics.Get through shim: %v", err)
	}
	if got.Name != topicName {
		t.Errorf("Topics.Get.Name = %q, want %q", got.Name, topicName)
	}
}
