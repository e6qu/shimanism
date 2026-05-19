// Package harness provides test-only fixtures for spinning up a real
// shimanism shim instance with a chosen backend, addressable over HTTP
// so external clients (aws-sdk-go-v2, aws-cli, Terraform AWS provider,
// etc.) can drive it through their standard endpoint-override paths.
//
// The harness is not for production use. It produces a real shim,
// but configured for short-lived test runs (random port, no
// signature validation, in-memory state via the chosen backend).
package harness

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pubsubdomain "github.com/e6qu/shimanism/internal/pubsub/domain"
	awssnsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sns"
	awssqsreceivefront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sqs_receive"
	gcppubsubpsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/gcp_pubsub"
	queuedomain "github.com/e6qu/shimanism/internal/queue/domain"
	awssqsfront "github.com/e6qu/shimanism/internal/queue/frontends/aws_sqs"
	azuresbfront "github.com/e6qu/shimanism/internal/queue/frontends/azure_servicebus"
	gcpsubfront "github.com/e6qu/shimanism/internal/queue/frontends/gcp_pubsub"
	"github.com/e6qu/shimanism/internal/restxml"
	secretsdomain "github.com/e6qu/shimanism/internal/secrets/domain"
	awssmfront "github.com/e6qu/shimanism/internal/secrets/frontends/aws_secretsmanager"
	azurekvfront "github.com/e6qu/shimanism/internal/secrets/frontends/azure_keyvault"
	gcpsmfront "github.com/e6qu/shimanism/internal/secrets/frontends/gcp_secretmanager"
	"github.com/e6qu/shimanism/internal/storage/domain"
	awsfront "github.com/e6qu/shimanism/internal/storage/frontends/aws_s3"
	azurefront "github.com/e6qu/shimanism/internal/storage/frontends/azure_blob"
	gcsfront "github.com/e6qu/shimanism/internal/storage/frontends/gcs"
	storagegen "github.com/e6qu/shimanism/services/storage/gen"
)

// StorageServer is a started shim instance with its addressable URL.
type StorageServer struct {
	// URL points at the shim's root; pass it to clients via
	// `--endpoint-url` / `BaseEndpoint` / Terraform `endpoints { s3 = ... }`.
	URL string
	// Close shuts down the test server. Registered with t.Cleanup so
	// callers rarely need to invoke it directly.
	Close func()
}

// StartStorageServer starts a shim instance with the AWS S3 frontend
// backed by the given storage implementation. AWS-shaped clients
// (boto3, aws CLI, hashicorp/aws Terraform provider) can drive it
// via the standard endpoint-override path.
//
// Every request is logged to t.Log so conformance failures show the
// exact sequence of operations the client drove.
func StartStorageServer(t *testing.T, backend domain.Storage) *StorageServer {
	t.Helper()
	adapter := awsfront.New(backend)
	router := &restxml.Router{}
	storagegen.RegisterAmazonS3Routes(router, adapter)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: router})
	t.Cleanup(ts.Close)
	return &StorageServer{URL: ts.URL, Close: ts.Close}
}

// StartStorageServerGCS starts a shim instance with the GCS REST API
// frontend backed by the given storage implementation. GCP-shaped
// clients (cloud.google.com/go/storage, gcloud, hashicorp/google
// Terraform provider) drive it through the same endpoint-override
// path.
func StartStorageServerGCS(t *testing.T, backend domain.Storage) *StorageServer {
	t.Helper()
	srv := gcsfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &StorageServer{URL: ts.URL, Close: ts.Close}
}

// StartStorageServerAzureBlob starts a shim instance with the Azure
// Blob REST API frontend backed by the given storage implementation.
// Azure-shaped clients (azure-sdk-for-go/sdk/storage/azblob, az CLI,
// hashicorp/azurerm Terraform provider) drive it through the
// endpoint-override path.
func StartStorageServerAzureBlob(t *testing.T, backend domain.Storage) *StorageServer {
	t.Helper()
	srv := azurefront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &StorageServer{URL: ts.URL, Close: ts.Close}
}

// SecretsServer is a started secrets-shim instance with its
// addressable URL. Same shape as StorageServer; the URL goes to
// SDK / CLI / Terraform clients via their endpoint-override path.
type SecretsServer struct {
	URL   string
	Close func()
}

// StartSecretsServerAWS starts a shim instance with the AWS Secrets
// Manager frontend backed by the given secrets implementation.
// AWS-shaped clients (aws-sdk-go-v2/service/secretsmanager,
// aws secretsmanager CLI, hashicorp/aws Terraform provider) drive
// it via the standard endpoint-override path.
func StartSecretsServerAWS(t *testing.T, backend secretsdomain.Secrets) *SecretsServer {
	t.Helper()
	srv := awssmfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &SecretsServer{URL: ts.URL, Close: ts.Close}
}

// StartSecretsServerGCP starts a shim instance with the GCP Secret
// Manager frontend backed by the given secrets implementation.
// GCP-shaped clients (google.golang.org/api/secretmanager/v1,
// gcloud secrets, hashicorp/google Terraform provider) drive it
// via the endpoint-override path.
func StartSecretsServerGCP(t *testing.T, backend secretsdomain.Secrets) *SecretsServer {
	t.Helper()
	srv := gcpsmfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &SecretsServer{URL: ts.URL, Close: ts.Close}
}

// StartSecretsServerAzure starts a shim instance with the Azure
// Key Vault secrets-surface frontend backed by the given secrets
// implementation. The httptest server uses TLS so the Azure SDK
// will send auth headers — the SDK refuses to attach a bearer
// token to a plain-http request.
//
// Azure-shaped clients
// (azure-sdk-for-go/sdk/security/keyvault/azsecrets,
// az keyvault secret CLI, hashicorp/azurerm Terraform provider)
// drive it via the endpoint-override path.
func StartSecretsServerAzure(t *testing.T, backend secretsdomain.Secrets) *SecretsServer {
	t.Helper()
	srv := azurekvfront.New(backend)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &SecretsServer{URL: ts.URL, Close: ts.Close}
}

// QueueServer is a started queue-shim instance.
type QueueServer struct {
	URL   string
	Close func()
}

// StartQueueServerAWS starts a shim instance with the AWS SQS
// frontend backed by the given queue implementation.
func StartQueueServerAWS(t *testing.T, backend queuedomain.Queues) *QueueServer {
	t.Helper()
	srv := awssqsfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &QueueServer{URL: ts.URL, Close: ts.Close}
}

// StartQueueServerGCP starts a shim instance with the GCP Pub/Sub
// frontend backed by the given queue implementation.
func StartQueueServerGCP(t *testing.T, backend queuedomain.Queues) *QueueServer {
	t.Helper()
	srv := gcpsubfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &QueueServer{URL: ts.URL, Close: ts.Close}
}

// StartQueueServerAzure starts a shim instance with the Azure
// Service Bus REST frontend. The Azure SDK uses AMQP (not REST)
// for the data plane, so the official azservicebus SDK cannot
// drive this frontend — see frontends/azure_servicebus/server.go
// for the AMQP open question.
func StartQueueServerAzure(t *testing.T, backend queuedomain.Queues) *QueueServer {
	t.Helper()
	srv := azuresbfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &QueueServer{URL: ts.URL, Close: ts.Close}
}

// PubsubServer is a started pubsub-shim instance. AWS-shaped
// pubsub exposes a separate SNS endpoint (publish side) and SQS
// endpoint (receive side), so callers get two URLs to point their
// snsClient + sqsClient at. GCP and Azure expose a single URL each.
type PubsubServer struct {
	// SnsURL is the SNS endpoint (awsQuery / XML). Empty for non-AWS.
	SnsURL string
	// SqsURL is the SQS-shaped receive endpoint (awsJson1_0). Empty
	// for non-AWS.
	SqsURL string
	// URL is the single endpoint for non-AWS frontends.
	URL   string
	Close func()
}

// StartPubsubServerAWS starts a shim instance with the AWS SNS
// frontend + the SQS-shaped receive surface, both wired to the
// same pubsub backend. Returns two URLs the SDK clients should
// target through their BaseEndpoint override.
func StartPubsubServerAWS(t *testing.T, backend pubsubdomain.Pubsub) *PubsubServer {
	t.Helper()
	snsTs := httptest.NewServer(&logRoundTrip{t: t, mux: awssnsfront.New(backend)})
	sqsTs := httptest.NewServer(&logRoundTrip{t: t, mux: awssqsreceivefront.New(backend)})
	t.Cleanup(snsTs.Close)
	t.Cleanup(sqsTs.Close)
	return &PubsubServer{
		SnsURL: snsTs.URL,
		SqsURL: sqsTs.URL,
		Close:  func() { snsTs.Close(); sqsTs.Close() },
	}
}

// StartPubsubServerGCP starts a shim instance with the GCP Pub/Sub
// fanout frontend.
func StartPubsubServerGCP(t *testing.T, backend pubsubdomain.Pubsub) *PubsubServer {
	t.Helper()
	srv := gcppubsubpsfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &PubsubServer{URL: ts.URL, Close: ts.Close}
}

// logRoundTrip logs each request through the harness. Lightweight —
// no body capture, just method + path + query + response status.
type logRoundTrip struct {
	t   *testing.T
	mux http.Handler
}

func (l *logRoundTrip) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sw := &statusWriter{ResponseWriter: w, status: 200}
	l.mux.ServeHTTP(sw, r)
	suffix := ""
	if r.URL.RawQuery != "" {
		suffix = "?" + r.URL.RawQuery
	}
	l.t.Logf("[harness] %s %s%s -> %d", r.Method, r.URL.Path, suffix, sw.status)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
