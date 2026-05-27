// Sockerless lane for the queue service. See docs/sockerless-validation.md.
package conformance_test

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"os"
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
