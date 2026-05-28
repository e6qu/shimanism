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
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apigatewaydomain "github.com/e6qu/shimanism/internal/apigateway/domain"
	awsapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/aws_apigatewayv2"
	azureapimfront "github.com/e6qu/shimanism/internal/apigateway/frontends/azure_apim"
	gcpapigwfront "github.com/e6qu/shimanism/internal/apigateway/frontends/gcp_apigateway"
	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/azuresharedkey"
	cachedomain "github.com/e6qu/shimanism/internal/cache/domain"
	awsecfront "github.com/e6qu/shimanism/internal/cache/frontends/aws_elasticache"
	azureredisfront "github.com/e6qu/shimanism/internal/cache/frontends/azure_redis"
	gcpmsfront "github.com/e6qu/shimanism/internal/cache/frontends/gcp_memorystore"
	functionsdomain "github.com/e6qu/shimanism/internal/functions/domain"
	awslambdafront "github.com/e6qu/shimanism/internal/functions/frontends/aws_lambda"
	azurecafront "github.com/e6qu/shimanism/internal/functions/frontends/azure_containerapps"
	gcpcrfront "github.com/e6qu/shimanism/internal/functions/frontends/gcp_cloudrun"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/mockaad"
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
	"github.com/e6qu/shimanism/internal/restxml"
	secretsdomain "github.com/e6qu/shimanism/internal/secrets/domain"
	awssmfront "github.com/e6qu/shimanism/internal/secrets/frontends/aws_secretsmanager"
	azurearmkvfront "github.com/e6qu/shimanism/internal/secrets/frontends/azure_arm_keyvault"
	azurekvfront "github.com/e6qu/shimanism/internal/secrets/frontends/azure_keyvault"
	gcpsmfront "github.com/e6qu/shimanism/internal/secrets/frontends/gcp_secretmanager"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
	"github.com/e6qu/shimanism/internal/storage/domain"
	awsfront "github.com/e6qu/shimanism/internal/storage/frontends/aws_s3"
	azurearmstoragefront "github.com/e6qu/shimanism/internal/storage/frontends/azure_arm_storageaccounts"
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
//     frontends), SharedKey for Azure Blob Storage
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

// StartStorageServerAzureARM starts a shim instance with the
// Microsoft.Storage ARM frontend (storage accounts + blob containers
// at the control plane). Wrapped with the same `azurebearer`
// middleware all the other ARM-shimmed services use, configured for
// audience "https://management.azure.com/" + the shared HS256 test
// key. Phase 14.E unblocks `azurerm_storage_account` +
// `azurerm_storage_container` through-shim Terraform Apply.
//
// `blobEndpoint` (optional) is returned in synthetic StorageAccount
// `primaryEndpoints.blob` responses. Set it to the URL of a co-running
// `StartStorageServerAzureBlob` to make the `hashicorp/azurerm`
// Terraform provider auto-discover the blob data-plane endpoint.
// Pass "" to fall back to the `https://<account>.blob.core.windows.net/`
// default (suitable for non-Terraform-driven tests).
func StartStorageServerAzureARM(t *testing.T, backend domain.Storage, blobEndpoint ...string) *StorageServer {
	t.Helper()
	// TrackAccounts: needed for `hashicorp/azurerm` idempotency checks
	// (pre-create GET must 404 before any PUT). Real-cloud azurerm
	// expects this; ARM-SDK-driven tests don't care either way.
	opts := azurearmstoragefront.Options{TrackAccounts: true}
	if len(blobEndpoint) > 0 {
		opts.BlobEndpoint = blobEndpoint[0]
	}
	srv := azurearmstoragefront.New(backend, opts)
	verifier := azurebearer.New(azurebearer.Options{Audience: "https://management.azure.com/", TestKey: []byte("test-key-do-not-use-in-prod")})
	mw := azurebearer.Middleware(verifier)
	// Wrap with armResourcesStub so generic ARM routes (Microsoft.Resources/*)
	// that `hashicorp/azurerm` hits at init time don't 404. The storage
	// frontend itself only handles Microsoft.Storage routes.
	wrapped := armResourcesStub(srv)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(wrapped)})
	t.Cleanup(ts.Close)
	return &StorageServer{URL: ts.URL, Close: ts.Close}
}

// armResourcesStub serves the small subset of Microsoft.Resources
// ARM routes that hashicorp/azurerm hits at provider init time, then
// falls through to the wrapped service-specific ARM handler for
// everything else. azurerm enumerates resource providers + resource
// groups regardless of `resource_provider_registrations = "none"`,
// so a service-specific ARM frontend can't 404 on those paths.
//
// What it serves:
//
//	GET /subscriptions/{sub}/providers
//	  Empty providers list. azurerm caches the result; with "none"
//	  registration mode, the cache is referenced only for validation.
//	GET /subscriptions/{sub}/resourceGroups/{rg}
//	  Synthetic resource group response. Most azurerm_* resources
//	  read this to validate the rg exists; returning a generic OK
//	  is enough since the shim is rg-agnostic.
//	PUT /subscriptions/{sub}/resourceGroups/{rg}
//	  Acknowledge resource group create (azurerm_resource_group).
//
// All Microsoft.Storage/* (or whichever per-service paths the wrapped
// handler covers) fall through.
func armResourcesStub(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		// /subscriptions/{sub}/providers — list resource providers.
		if matched, _ := matchPath(path, "/subscriptions/+/providers"); matched && r.Method == http.MethodGet {
			writeARMJSON(w, http.StatusOK, map[string]any{"value": []any{}})
			return
		}
		// /subscriptions/{sub}/resourceGroups/{rg} — get/put a resource
		// group. The shim is rg-agnostic; treat as opaque routing.
		if matched, parts := matchPath(path, "/subscriptions/+/resourcegroups/+"); matched {
			switch r.Method {
			case http.MethodGet, http.MethodHead:
				writeARMJSON(w, http.StatusOK, syntheticResourceGroup(parts[0], parts[1]))
				return
			case http.MethodPut:
				writeARMJSON(w, http.StatusCreated, syntheticResourceGroup(parts[0], parts[1]))
				return
			case http.MethodDelete:
				w.WriteHeader(http.StatusOK)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// matchPath matches a slash-delimited template against `path`, with
// `+` as a wildcard for one URL segment. Returns the captured wildcard
// values in order. Case-insensitive for the literal segments (Azure
// is inconsistent — sometimes `resourcegroups`, sometimes `resourceGroups`).
func matchPath(path, template string) (bool, []string) {
	tParts := splitARMPath(template)
	pParts := splitARMPath(path)
	if len(tParts) != len(pParts) {
		return false, nil
	}
	var captures []string
	for i := range tParts {
		if tParts[i] == "+" {
			captures = append(captures, pParts[i])
			continue
		}
		if !strings.EqualFold(tParts[i], pParts[i]) {
			return false, nil
		}
	}
	return true, captures
}

func splitARMPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func writeARMJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func syntheticResourceGroup(sub, name string) map[string]any {
	return map[string]any{
		"id":         "/subscriptions/" + sub + "/resourceGroups/" + name,
		"name":       name,
		"type":       "Microsoft.Resources/resourceGroups",
		"location":   "eastus",
		"properties": map[string]any{"provisioningState": "Succeeded"},
	}
}

// SecretsServer is a started secrets-shim instance with its
// addressable URL. Same shape as StorageServer; the URL goes to
// SDK / CLI / Terraform clients via their endpoint-override path.
type SecretsServer struct {
	URL string
	// CertFile, when set, is the PEM-encoded cert of the TLS-fronted
	// SecretsServer (populated by StartSecretsServerAzure since it
	// uses httptest.NewTLSServer). Tests that hit the SecretsServer
	// over HTTPS from a subprocess (e.g. terraform via SSL_CERT_FILE)
	// need this to trust the self-signed cert.
	CertFile string
	Close    func()
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
	// Write the auto-generated cert to a temp file so subprocess
	// callers (terraform via SSL_CERT_FILE) can trust this server.
	certFile := filepath.Join(t.TempDir(), "secrets-azure-cert.pem")
	if err := os.WriteFile(certFile, certToPEM(ts.Certificate()), 0o644); err != nil {
		t.Fatalf("write secrets Azure cert: %v", err)
	}
	t.Cleanup(ts.Close)
	return &SecretsServer{URL: ts.URL, CertFile: certFile, Close: ts.Close}
}

// StartSecretsServerAzureARM starts a shim instance with the
// Microsoft.KeyVault ARM frontend (vault create/get/list/delete
// at the control plane). Wrapped with the same `azurebearer`
// middleware all the other ARM-shimmed services use, configured for
// audience "https://management.azure.com/" + the shared HS256 test
// key. Phase 14.E unblocks `azurerm_key_vault` through-shim
// Terraform Apply.
func StartSecretsServerAzureARM(t *testing.T, vaultURI ...string) *SecretsServer {
	t.Helper()
	opts := azurearmkvfront.Options{TrackVaults: true}
	if len(vaultURI) > 0 {
		opts.VaultURI = vaultURI[0]
	}
	srv := azurearmkvfront.New(opts)
	verifier := azurebearer.New(azurebearer.Options{
		Audience: "https://management.azure.com/",
		TestKey:  []byte("test-key-do-not-use-in-prod"),
	})
	mw := azurebearer.Middleware(verifier)
	// Wrap with armResourcesStub so generic ARM routes
	// (Microsoft.Resources/*) that hashicorp/azurerm hits at init
	// time don't 404. Same pattern as StartStorageServerAzureARM.
	wrapped := armResourcesStub(srv)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: mw(wrapped)})
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

// MockAADServer is a started mock-Microsoft-Entra HTTPS instance.
type MockAADServer struct {
	URL      string
	CertFile string // path to the PEM-encoded cert; suitable for SSL_CERT_FILE
	Close    func()
}

// StartMockAAD starts a mock Microsoft Entra authority that
// `hashicorp/azurerm` can use to exchange a (fake) client_secret
// for an HS256-signed bearer token. The mock serves both the cloud-
// metadata document (consumed via azurerm's `metadata_host`) and
// the OIDC discovery + token endpoints. Pass the ARM frontend's URL
// as `resourceManagerURL` so the metadata document routes
// management.azure.com traffic back to the shim.
//
// Returned over HTTPS via httptest.NewTLSServer (azurerm refuses
// HTTP for metadata_host). The self-signed cert is written to
// MockAADServer.CertFile so tests can set SSL_CERT_FILE pointing at
// it; without that the terraform invocation rejects the cert and
// the apply fails.
//
// Phase 14.E.4: the last piece for through-shim `azurerm` Terraform
// Apply. Real Entra is out-of-scope; this mock only exists so the
// 7+ skipped azurerm Terraform conformance tests can run.
func StartMockAAD(t *testing.T, resourceManagerURL string) *MockAADServer {
	t.Helper()
	srv := mockaad.NewServer(&mockaad.Options{
		ResourceManagerURL: resourceManagerURL,
	})
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	srv.SetSelfURL(ts.URL)
	// Write the auto-generated cert to a temp file so the test can
	// expose it via SSL_CERT_FILE to the terraform subprocess.
	certFile := filepath.Join(t.TempDir(), "mock-aad-cert.pem")
	certPEM := certToPEM(ts.Certificate())
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatalf("write mock-AAD cert: %v", err)
	}
	t.Cleanup(ts.Close)
	return &MockAADServer{URL: ts.URL, CertFile: certFile, Close: ts.Close}
}

func certToPEM(cert *x509.Certificate) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
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
