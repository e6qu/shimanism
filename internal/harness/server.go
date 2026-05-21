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

	apigatewaydomain "github.com/e6qu/shimanism/internal/apigateway/domain"
	awsapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/aws_apigatewayv2"
	azureapimfront "github.com/e6qu/shimanism/internal/apigateway/frontends/azure_apim"
	gcpapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/gcp_apigateway"
	cachedomain "github.com/e6qu/shimanism/internal/cache/domain"
	awsecfront "github.com/e6qu/shimanism/internal/cache/frontends/aws_elasticache"
	azureredisfront "github.com/e6qu/shimanism/internal/cache/frontends/azure_redis"
	gcpmsfront "github.com/e6qu/shimanism/internal/cache/frontends/gcp_memorystore"
	functionsdomain "github.com/e6qu/shimanism/internal/functions/domain"
	awslambdafront "github.com/e6qu/shimanism/internal/functions/frontends/aws_lambda"
	azurecafront "github.com/e6qu/shimanism/internal/functions/frontends/azure_containerapps"
	gcpcrfront "github.com/e6qu/shimanism/internal/functions/frontends/gcp_cloudrun"
	pubsubdomain "github.com/e6qu/shimanism/internal/pubsub/domain"
	awssnsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sns"
	awssqsreceivefront "github.com/e6qu/shimanism/internal/pubsub/frontends/aws_sqs_receive"
	azuresbtopicsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/azure_servicebus_topics"
	gcppubsubpsfront "github.com/e6qu/shimanism/internal/pubsub/frontends/gcp_pubsub"
	queuedomain "github.com/e6qu/shimanism/internal/queue/domain"
	awssqsfront "github.com/e6qu/shimanism/internal/queue/frontends/aws_sqs"
	azuresbfront "github.com/e6qu/shimanism/internal/queue/frontends/azure_servicebus"
	gcpsubfront "github.com/e6qu/shimanism/internal/queue/frontends/gcp_pubsub"
	rdbmsdomain "github.com/e6qu/shimanism/internal/rdbms/domain"
	awsrdsfront "github.com/e6qu/shimanism/internal/rdbms/frontends/aws_rds"
	azuredbadminfront "github.com/e6qu/shimanism/internal/rdbms/frontends/azure_dbadmin"
	gcpcloudsqlfront "github.com/e6qu/shimanism/internal/rdbms/frontends/gcp_cloudsql"
	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/azuresharedkey"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/restxml"
	secretsdomain "github.com/e6qu/shimanism/internal/secrets/domain"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
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

// init: AWS / GCP / Azure conformance lanes have all been migrated
// to sign with the verifier's trusted credentials (Phase 11.14a-o).
//
//   - AWS:   SigV4 with AKIAIOSFODNN7EXAMPLE / wJalrXUtnFEMI…
//   - GCP:   HS256 JWTs via gcpbearer.TestJWT
//   - Azure: HS256 JWTs via azurebearer.TestJWT (Bearer-shaped
//            frontends), SharedKey for Azure Blob Storage
//
// Every lane runs with verification enforced end-to-end. No
// per-cloud bypass is set here. The
// SHIMANISM_TEST_UNAUTHENTICATED_{AWS,GCP,AZURE} env vars still
// exist (each verifier middleware reads them) so a future
// production-deployment shape that wants to disable enforcement at
// runtime can do so, and so individual tests can `t.Setenv` a lane
// back on temporarily during debugging. The harness leaves them
// unset.
func init() {}

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
	// Phase 11.13 retrofit: SigV4 verifier on the S3 frontend. The
	// emitter for REST-XML uses internal/restxml.WriteError; we wrap
	// it in a closure that matches sigv4verifier's EmitError signature.
	verifier := sigv4verifier.New(sigv4verifier.StaticStore{
		AccessKey: "AKIAIOSFODNN7EXAMPLE",
		Secret:    "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, sigv4verifier.Options{Service: "s3", Region: "us-east-1"})
	emitErr := func(w http.ResponseWriter, status int, errorType, message string) {
		restxml.WriteError(w, status, errorType, message)
	}
	mw := sigv4verifier.Middleware(verifier, emitErr)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(router)})
	t.Cleanup(ts.Close)
	return &StorageServer{URL: ts.URL, Close: ts.Close}
}

// StartStorageServerGCS starts a shim instance with the GCS REST API
// frontend backed by the given storage implementation. GCP-shaped
// clients (cloud.google.com/go/storage, gcloud, hashicorp/google
// Terraform provider) drive it through the same endpoint-override
// path.
//
// Phase 11.13c retrofit: wraps the GCS handler with the GCP bearer
// verifier middleware. SHIMANISM_TEST_UNAUTHENTICATED=1 (set by the
// harness init) short-circuits verification.
func StartStorageServerGCS(t *testing.T, backend domain.Storage) *StorageServer {
	t.Helper()
	srv := gcsfront.New(backend)
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://storage.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &StorageServer{URL: ts.URL, Close: ts.Close}
}

// StartStorageServerAzureBlob starts a shim instance with the Azure
// Blob REST API frontend backed by the given storage implementation.
// Azure-shaped clients (azure-sdk-for-go/sdk/storage/azblob, az CLI,
// hashicorp/azurerm Terraform provider) drive it through the
// endpoint-override path.
//
// Phase 11.13d retrofit: wraps the Azure Blob handler with the
// SharedKey verifier middleware. SHIMANISM_TEST_UNAUTHENTICATED=1
// (set by the harness init) short-circuits verification.
func StartStorageServerAzureBlob(t *testing.T, backend domain.Storage) *StorageServer {
	t.Helper()
	srv := azurefront.New(backend)
	verifier := azuresharedkey.New(azuresharedkey.StaticStore{
		Account: "shimstorage",
		Key:     []byte("test-key-do-not-use-in-prod-this-is-32-bytes-of-junk"),
	})
	mw := azuresharedkey.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
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
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://secretmanager.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
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
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://vault.azure.net",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := azurebearer.Middleware(verifier, azurebearer.WithChallenge("https://vault.azure.net"))
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: mw(srv)})
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
	verifier := gcpbearer.New(gcpbearer.Options{
		Audience: "https://pubsub.googleapis.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
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
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://servicebus.azure.net",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := azurebearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
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
	verifier := gcpbearer.New(gcpbearer.Options{Audience: "https://pubsub.googleapis.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &PubsubServer{URL: ts.URL, Close: ts.Close}
}

// StartPubsubServerAzure starts a shim instance with the Azure
// Service Bus topics REST frontend. AMQP fidelity tier is deferred
// per Phase 3's open question (same posture for pubsub topics).
func StartPubsubServerAzure(t *testing.T, backend pubsubdomain.Pubsub) *PubsubServer {
	t.Helper()
	srv := azuresbtopicsfront.New(backend)
	verifier := azurebearer.New(azurebearer.Options{Audience: "https://servicebus.azure.net", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := azurebearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &PubsubServer{URL: ts.URL, Close: ts.Close}
}

// RDBMSServer is a started rdbms-shim instance.
type RDBMSServer struct {
	URL   string
	Close func()
}

// StartRDBMSServerAWS starts a shim instance with the AWS RDS
// awsQuery frontend backed by the given rdbms implementation.
func StartRDBMSServerAWS(t *testing.T, backend rdbmsdomain.RDBMS) *RDBMSServer {
	t.Helper()
	srv := awsrdsfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &RDBMSServer{URL: ts.URL, Close: ts.Close}
}

// StartRDBMSServerGCP starts a shim instance with the GCP Cloud
// SQL Admin REST frontend.
func StartRDBMSServerGCP(t *testing.T, backend rdbmsdomain.RDBMS) *RDBMSServer {
	t.Helper()
	srv := gcpcloudsqlfront.New(backend)
	verifier := gcpbearer.New(gcpbearer.Options{Audience: "https://sqladmin.googleapis.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &RDBMSServer{URL: ts.URL, Close: ts.Close}
}

// StartRDBMSServerAzure starts a shim instance with the Azure DB
// Admin REST frontend. ARM URL shape; SDK conformance is deferred
// (the SDK pollers expect Azure-AsyncOperation headers the shim
// doesn't emit at this phase).
func StartRDBMSServerAzure(t *testing.T, backend rdbmsdomain.RDBMS) *RDBMSServer {
	t.Helper()
	srv := azuredbadminfront.New(backend)
	verifier := azurebearer.New(azurebearer.Options{Audience: "https://management.azure.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := azurebearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &RDBMSServer{URL: ts.URL, Close: ts.Close}
}

// CacheServer is a started cache-shim instance.
type CacheServer struct {
	URL   string
	Close func()
}

// StartCacheServerAWS starts a shim instance with the AWS
// ElastiCache awsQuery frontend backed by the given cache
// implementation.
func StartCacheServerAWS(t *testing.T, backend cachedomain.Cache) *CacheServer {
	t.Helper()
	srv := awsecfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &CacheServer{URL: ts.URL, Close: ts.Close}
}

// StartCacheServerGCP starts a shim instance with the GCP
// Memorystore Admin REST frontend.
func StartCacheServerGCP(t *testing.T, backend cachedomain.Cache) *CacheServer {
	t.Helper()
	srv := gcpmsfront.New(backend)
	verifier := gcpbearer.New(gcpbearer.Options{Audience: "https://redis.googleapis.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &CacheServer{URL: ts.URL, Close: ts.Close}
}

// StartCacheServerAzure starts a shim instance with the Azure
// Cache for Redis REST frontend.
func StartCacheServerAzure(t *testing.T, backend cachedomain.Cache) *CacheServer {
	t.Helper()
	srv := azureredisfront.New(backend)
	verifier := azurebearer.New(azurebearer.Options{Audience: "https://management.azure.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := azurebearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &CacheServer{URL: ts.URL, Close: ts.Close}
}

// FunctionsServer is a started functions-shim instance.
type FunctionsServer struct {
	URL   string
	Close func()
}

// StartFunctionsServerAWS starts a shim instance with the AWS
// Lambda restJson1 frontend backed by the given functions
// implementation.
func StartFunctionsServerAWS(t *testing.T, backend functionsdomain.Functions) *FunctionsServer {
	t.Helper()
	srv := awslambdafront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &FunctionsServer{URL: ts.URL, Close: ts.Close}
}

// StartFunctionsServerGCP starts a shim instance with the GCP
// Cloud Run REST frontend.
func StartFunctionsServerGCP(t *testing.T, backend functionsdomain.Functions) *FunctionsServer {
	t.Helper()
	srv := gcpcrfront.New(backend)
	verifier := gcpbearer.New(gcpbearer.Options{Audience: "https://run.googleapis.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &FunctionsServer{URL: ts.URL, Close: ts.Close}
}

// StartFunctionsServerAzure starts a shim instance with the Azure
// Container Apps REST frontend.
func StartFunctionsServerAzure(t *testing.T, backend functionsdomain.Functions) *FunctionsServer {
	t.Helper()
	srv := azurecafront.New(backend)
	verifier := azurebearer.New(azurebearer.Options{Audience: "https://management.azure.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := azurebearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &FunctionsServer{URL: ts.URL, Close: ts.Close}
}

// APIGatewayServer is a started apigateway-shim instance.
type APIGatewayServer struct {
	URL   string
	Close func()
}

// StartAPIGatewayServerAWS starts a shim instance with the AWS
// API Gateway v2 restJson1 frontend.
func StartAPIGatewayServerAWS(t *testing.T, backend apigatewaydomain.APIGateway) *APIGatewayServer {
	t.Helper()
	srv := awsapigwfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &APIGatewayServer{URL: ts.URL, Close: ts.Close}
}

// StartAPIGatewayServerGCP starts a shim instance with the GCP API
// Gateway REST frontend. GCP-shaped clients
// (google.golang.org/api/apigateway/v1, gcloud api-gateway,
// hashicorp/google Terraform provider) drive it via the
// endpoint-override path.
func StartAPIGatewayServerGCP(t *testing.T, backend apigatewaydomain.APIGateway) *APIGatewayServer {
	t.Helper()
	srv := gcpapigwfront.New(backend)
	verifier := gcpbearer.New(gcpbearer.Options{Audience: "https://apigateway.googleapis.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := gcpbearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &APIGatewayServer{URL: ts.URL, Close: ts.Close}
}

// StartAPIGatewayServerAzure starts a shim instance with the Azure
// API Management ARM-style frontend. Azure-shaped clients
// (armapimanagement, az apim, hashicorp/azurerm Terraform provider)
// drive it via the endpoint-override path.
func StartAPIGatewayServerAzure(t *testing.T, backend apigatewaydomain.APIGateway) *APIGatewayServer {
	t.Helper()
	srv := azureapimfront.New(backend)
	verifier := azurebearer.New(azurebearer.Options{Audience: "https://management.azure.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := azurebearer.Middleware(verifier)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(srv)})
	t.Cleanup(ts.Close)
	return &APIGatewayServer{URL: ts.URL, Close: ts.Close}
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
