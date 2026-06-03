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
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
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
	computedomain "github.com/e6qu/shimanism/internal/compute/domain"
	awsec2front "github.com/e6qu/shimanism/internal/compute/frontends/aws_ec2"
	azurecomputefront "github.com/e6qu/shimanism/internal/compute/frontends/azure_compute"
	azurenetfront "github.com/e6qu/shimanism/internal/compute/frontends/azure_network"
	gcpcomputefront "github.com/e6qu/shimanism/internal/compute/frontends/gcp_compute"
	dnsdomain "github.com/e6qu/shimanism/internal/dns/domain"
	awsr53front "github.com/e6qu/shimanism/internal/dns/frontends/aws_route53"
	azuredfront "github.com/e6qu/shimanism/internal/dns/frontends/azure_dns"
	gcpdnsfront "github.com/e6qu/shimanism/internal/dns/frontends/gcp_clouddns"
	functionsdomain "github.com/e6qu/shimanism/internal/functions/domain"
	awslambdafront "github.com/e6qu/shimanism/internal/functions/frontends/aws_lambda"
	azurecafront "github.com/e6qu/shimanism/internal/functions/frontends/azure_containerapps"
	gcpcrfront "github.com/e6qu/shimanism/internal/functions/frontends/gcp_cloudrun"
	"github.com/e6qu/shimanism/internal/gcpbearer"
	lbdomain "github.com/e6qu/shimanism/internal/loadbalancer/domain"
	awselbv2front "github.com/e6qu/shimanism/internal/loadbalancer/frontends/aws_elbv2"
	azurelbfront "github.com/e6qu/shimanism/internal/loadbalancer/frontends/azure_lb"
	gcplbfront "github.com/e6qu/shimanism/internal/loadbalancer/frontends/gcp_lb"
	nosqldomain "github.com/e6qu/shimanism/internal/nosql/domain"
	awsddbfront "github.com/e6qu/shimanism/internal/nosql/frontends/aws_dynamodb"
	azurectfront "github.com/e6qu/shimanism/internal/nosql/frontends/azure_cosmos_tables"
	gcpfsfront "github.com/e6qu/shimanism/internal/nosql/frontends/gcp_firestore"
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
	azurekvfront "github.com/e6qu/shimanism/internal/secrets/frontends/azure_keyvault"
	gcpsmfront "github.com/e6qu/shimanism/internal/secrets/frontends/gcp_secretmanager"
	"github.com/e6qu/shimanism/internal/sigv4verifier"
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

// StartStorageServerAzureBlobAtPort starts the shim's azure_blob
// frontend on a FIXED TCP port with a caller-supplied SharedKey,
// for the through-shim `azurerm` Terraform Apply test that needs
// sockerless's ARM-emitted `primaryEndpoints.blob` URL to match a
// pre-known shim listener address.
//
// `account` is the storage account name (the verifier matches the
// Host / URL prefix against this). `key` is the raw SharedKey bytes
// the verifier will use — `azurerm` will have obtained the same
// bytes (base64-encoded) from sockerless's `listKeys`, derived via
// sockerless's documented `simListKey64(resourceID, kind)` shape.
// The test that wires this should derive its expected key from the
// same resource ID + key kind so both sides see identical bytes.
//
// `port` is the TCP port to bind (no randomization). Used to fix
// the listener address in advance of sockerless emitting it.
func StartStorageServerAzureBlobAtPort(t *testing.T, backend domain.Storage, port int, account string, key []byte) *StorageServer {
	t.Helper()
	srv := azurefront.New(backend)
	verifier := azuresharedkey.New(azuresharedkey.StaticStore{Account: account, Key: key})
	mw := azuresharedkey.Middleware(verifier)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("bind 127.0.0.1:%d: %v", port, err)
	}
	ts := httptest.NewUnstartedServer(&logRoundTrip{t: t, mux: mw(srv)})
	_ = ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
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

// DNSServer is a started DNS-shim instance with its addressable URL.
// Same shape as StorageServer / SecretsServer; the URL goes to
// SDK / CLI / Terraform clients via their endpoint-override path.
type DNSServer struct {
	URL   string
	Close func()
}

// StartDNSServerAWS starts a shim instance with the AWS Route 53
// frontend backed by the given DNS implementation. AWS-shaped clients
// (aws-sdk-go-v2/service/route53, aws route53 CLI, hashicorp/aws
// Terraform provider) drive it via the standard endpoint-override
// path.
func StartDNSServerAWS(t *testing.T, backend dnsdomain.DNS) *DNSServer {
	t.Helper()
	srv := awsr53front.Handler(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &DNSServer{URL: ts.URL, Close: ts.Close}
}

// StartDNSServerGCP starts a shim instance with the GCP Cloud DNS
// frontend backed by the given DNS implementation. GCP-shaped clients
// (google.golang.org/api/dns/v1, gcloud dns, hashicorp/google
// Terraform provider) drive it via the endpoint-override path.
func StartDNSServerGCP(t *testing.T, backend dnsdomain.DNS) *DNSServer {
	t.Helper()
	srv := gcpdnsfront.Handler(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &DNSServer{URL: ts.URL, Close: ts.Close}
}

// DNSServerTLS is a started DNS-shim instance addressable over HTTPS,
// with the self-signed certificate exported as PEM so Terraform-like
// callers can trust it via SSL_CERT_FILE. Used by the GCP Cloud DNS
// Terraform conformance test, where the hashicorp/google provider's
// `RemoveBasePathVersion` regex requires an HTTPS endpoint to match
// (the regex hard-codes `http[s]://`, accepting only HTTPS).
type DNSServerTLS struct {
	URL     string
	CertPEM []byte
	Close   func()
}

// StartDNSServerGCPTLS is the HTTPS variant of StartDNSServerGCP.
// Returns the auto-generated self-signed cert as PEM so callers can
// inject it into a CA bundle for child processes (Terraform).
func StartDNSServerGCPTLS(t *testing.T, backend dnsdomain.DNS) *DNSServerTLS {
	t.Helper()
	srv := gcpdnsfront.Handler(backend)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	cert := ts.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &DNSServerTLS{URL: ts.URL, CertPEM: certPEM, Close: ts.Close}
}

// StartDNSServerAzure starts a shim instance with the Azure DNS
// (public + private — one frontend, dispatch on path) backed by the
// given DNS implementation. Azure-shaped clients (`armdns` /
// `armprivatedns`, `az network dns`, hashicorp/azurerm) drive it
// via the standard endpoint-override path.
//
// The Azure SDK refuses to send Bearer tokens over plain HTTP, so
// this serves under TLS. Callers configure their client transport
// with `TLSClientConfig{InsecureSkipVerify: true}` to accept the
// httptest self-signed cert.
func StartDNSServerAzure(t *testing.T, backend dnsdomain.DNS) *DNSServer {
	t.Helper()
	srv := azuredfront.Handler(backend)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &DNSServer{URL: ts.URL, Close: ts.Close}
}

// StartDNSServerAzureWithPassthrough is the ARM-passthrough variant.
// Non-DNS ARM paths (resource groups, subscriptions, other
// Microsoft.Network resources) forward to the upstream handler.
// Used for end-to-end Terraform conformance where azurerm's single
// `endpoints { resource_manager = "..." }` config must satisfy both
// DNS-specific and generic ARM operations.
func StartDNSServerAzureWithPassthrough(t *testing.T, backend dnsdomain.DNS, upstream http.Handler) *DNSServerTLS {
	t.Helper()
	srv := azuredfront.HandlerWithPassthrough(backend, upstream)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	cert := ts.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &DNSServerTLS{URL: ts.URL, CertPEM: certPEM, Close: ts.Close}
}

// StartDNSServerAzureWithConfig exposes the full azure_dns.Config
// — passthrough + cloud-metadata endpoint — for tests that drive
// `azurerm` Terraform end-to-end through the shim. The metadata
// endpoint redirects auth + service URLs to `metadataLoginURL`
// while keeping ARM on the shim (BUG-46).
func StartDNSServerAzureWithConfig(t *testing.T, backend dnsdomain.DNS, cfg azuredfront.Config) *DNSServerTLS {
	t.Helper()
	srv := azuredfront.HandlerWithConfig(backend, cfg)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	cert := ts.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &DNSServerTLS{URL: ts.URL, CertPEM: certPEM, Close: ts.Close}
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

// StartSecretsServerAzureKVAtPort starts the Azure Key Vault
// secrets-surface frontend on a caller-chosen TCP port with TLS.
// Mirror of StartStorageServerAzureBlobAtPort for the data-plane
// `azurerm_key_vault_secret` Terraform Apply path: sockerless's
// real Microsoft.KeyVault ARM emits `properties.vaultUri` as
// `https://{vault}.vault.localhost:<port>/`, azurerm follows that
// URL with a Microsoft Entra Bearer token, and this server verifies
// the token against the caller-provided JWKS before letting the
// gen-generated handlers translate the SetSecret call onto the
// chosen secrets backend.
//
// Callers pass a TLS cert + key whose SAN covers `*.vault.localhost`
// (the test typically reuses sockerless's own self-signed cert,
// which is also trusted by the terraform process via SSL_CERT_FILE).
// `jwks` must already contain the RS256 public key sockerless
// publishes at `/{tenant}/discovery/v2.0/keys`; the caller fetches
// it out-of-band with a TLS client that trusts the same cert, then
// hands the parsed JWKS in — that keeps the in-process JWKS lookup
// purely local with no TLS-trust complications.
func StartSecretsServerAzureKVAtPort(t *testing.T, backend secretsdomain.Secrets, port int, tlsCertPEM, tlsKeyPEM []byte, jwks *azurebearer.JWKS, audience, issuer string) *SecretsServer {
	t.Helper()
	cert, err := tls.X509KeyPair(tlsCertPEM, tlsKeyPEM)
	if err != nil {
		t.Fatalf("load TLS cert/key: %v", err)
	}
	srv := azurekvfront.New(backend)
	verifier := azurebearer.New(azurebearer.Options{
		Audience: audience,
		Issuer:   issuer,
		JWKS:     jwks,
	})
	mw := azurebearer.Middleware(verifier, azurebearer.WithChallenge(audience))
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("bind 127.0.0.1:%d: %v", port, err)
	}
	ts := httptest.NewUnstartedServer(&logRoundTrip{t: t, mux: mw(srv)})
	_ = ts.Listener.Close()
	ts.Listener = ln
	ts.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ts.StartTLS()
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
	// r.Form is populated by the handler chain (SigV4 or ec2query router
	// calls ParseForm internally); safe to read here after dispatch.
	action := r.Form.Get("Action")
	if action != "" {
		suffix += " [" + action + "]"
	}
	l.t.Logf("[harness] %s %s%s -> %d", r.Method, r.URL.Path, suffix, sw.status)
}

// NoSQLServer is a started NoSQL-shim instance with its addressable URL.
type NoSQLServer struct {
	URL   string
	Close func()
}

// StartNoSQLServerAWS starts a shim instance with the AWS DynamoDB
// frontend backed by the given NoSQL implementation. DynamoDB-shaped
// clients (aws-sdk-go-v2/service/dynamodb, aws dynamodb CLI,
// hashicorp/aws Terraform provider) drive it via the standard
// endpoint-override path.
func StartNoSQLServerAWS(t *testing.T, backend nosqldomain.NoSQL) *NoSQLServer {
	t.Helper()
	srv := awsddbfront.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &NoSQLServer{URL: ts.URL, Close: ts.Close}
}

// StartNoSQLServerGCP starts a shim instance with the GCP Firestore
// Native frontend backed by the given NoSQL implementation.
// Firestore-shaped clients (google.golang.org/api/firestore/v1,
// gcloud firestore, hashicorp/google Terraform provider) drive it
// via the endpoint-override path.
func StartNoSQLServerGCP(t *testing.T, backend nosqldomain.NoSQL) *NoSQLServer {
	t.Helper()
	srv := gcpfsfront.Handler(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &NoSQLServer{URL: ts.URL, Close: ts.Close}
}

// StartNoSQLServerAzure starts a shim instance with the Azure
// Cosmos DB Table API frontend backed by the given NoSQL
// implementation. Tables-shaped clients
// (github.com/Azure/azure-sdk-for-go/sdk/data/aztables) drive it
// via SharedKey-signed requests against the standard endpoint
// override.
func StartNoSQLServerAzure(t *testing.T, backend nosqldomain.NoSQL) *NoSQLServer {
	t.Helper()
	srv := azurectfront.Handler(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &NoSQLServer{URL: ts.URL, Close: ts.Close}
}

// NoSQLServerTLS is the HTTPS variant of NoSQLServer, with the
// self-signed cert exported as PEM so Terraform / SDK clients can
// trust it via SSL_CERT_FILE / RootCAs pools without
// InsecureSkipVerify.
type NoSQLServerTLS struct {
	URL     string
	CertPEM []byte
	Close   func()
}

// StartNoSQLServerAzureWithPassthrough is the ARM-passthrough
// variant of StartNoSQLServerAzure. Non-Tables ARM paths
// (`/subscriptions/...`) forward to the upstream handler. Used for
// end-to-end Terraform conformance where azurerm's single
// `metadata_host` / `resource_manager_endpoint` config drives both
// Cosmos Tables data-plane operations and the ARM resource
// (`Microsoft.DocumentDB/databaseAccounts/.../tables/...`) lifecycle
// through one shim port.
func StartNoSQLServerAzureWithPassthrough(t *testing.T, backend nosqldomain.NoSQL, upstream http.Handler) *NoSQLServerTLS {
	t.Helper()
	srv := azurectfront.HandlerWithPassthrough(backend, upstream)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	cert := ts.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &NoSQLServerTLS{URL: ts.URL, CertPEM: certPEM, Close: ts.Close}
}

// StartNoSQLServerAzureWithConfig is the full-config variant for
// through-shim Terraform tests. It serves the Azure cloud-metadata
// endpoint at `/metadata/endpoints` (BUG-50 follow-on: returns the
// shim itself as `resourceManager` + `metadataLoginURL` as the
// `loginEndpoint` so azurerm acquires Entra tokens from
// sockerless), wraps ARM paths with the bearer verifier, and runs
// data-plane paths through the SharedKey verifier.
func StartNoSQLServerAzureWithConfig(t *testing.T, backend nosqldomain.NoSQL, cfg azurectfront.Config) *NoSQLServerTLS {
	t.Helper()
	srv := azurectfront.HandlerWithConfig(backend, cfg)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	cert := ts.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &NoSQLServerTLS{URL: ts.URL, CertPEM: certPEM, Close: ts.Close}
}

// ComputeServer is a started compute-shim instance with its
// addressable URL.
type ComputeServer struct {
	URL   string
	Close func()
}

// StartComputeServerAWS starts a shim instance with the AWS EC2
// (ec2Query) frontend backed by the given compute backend. The backend
// must implement both domain.Networking and domain.Instances.
// EC2-shaped clients (aws-sdk-go-v2/service/ec2, aws ec2 CLI,
// hashicorp/aws Terraform provider) drive it via BaseEndpoint.
func StartComputeServerAWS(t *testing.T, backend awsec2front.ComputeBackend) *ComputeServer {
	t.Helper()
	srv := awsec2front.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &ComputeServer{URL: ts.URL, Close: ts.Close}
}

// StartComputeServerGCP starts a shim instance with the GCP Compute
// Engine v1 (REST) frontend backed by the given compute backend. The
// backend must implement both domain.Networking and domain.Instances.
// GCP-shaped clients (google.golang.org/api/compute/v1,
// gcloud compute, hashicorp/google Terraform provider) drive it via
// the endpoint-override path.
func StartComputeServerGCP(t *testing.T, backend gcpcomputefront.ComputeBackend) *ComputeServer {
	t.Helper()
	srv := gcpcomputefront.Handler(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &ComputeServer{URL: ts.URL, Close: ts.Close}
}

// ComputeServerTLS is the HTTPS variant of ComputeServer, carrying the
// self-signed certificate as PEM so callers (Terraform child processes)
// can inject it into SSL_CERT_FILE alongside the system CA bundle.
// Required because the hashicorp/google provider's RemoveBasePathVersion
// regex only matches https:// endpoints — HTTP endpoints corrupt the URL.
type ComputeServerTLS struct {
	URL     string
	CertPEM []byte
	Close   func()
}

// StartComputeServerGCPTLS is the HTTPS variant of StartComputeServerGCP.
// Returns the auto-generated self-signed cert as PEM so callers can
// inject it into a combined CA bundle for child processes (Terraform).
func StartComputeServerGCPTLS(t *testing.T, backend gcpcomputefront.ComputeBackend) *ComputeServerTLS {
	t.Helper()
	srv := gcpcomputefront.Handler(backend)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	cert := ts.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &ComputeServerTLS{URL: ts.URL, CertPEM: certPEM, Close: ts.Close}
}

// StartComputeServerAzure starts a shim instance with the Azure Network
// ARM frontend backed by the given networking implementation. The
// httptest server uses TLS so the Azure SDK sends Bearer tokens.
// Azure-shaped clients (armnetwork/v6, az network CLI, hashicorp/azurerm
// Terraform provider) drive it via the endpoint-override path.
func StartComputeServerAzure(t *testing.T, backend computedomain.Networking) *ComputeServer {
	t.Helper()
	srv := azurenetfront.Handler(backend)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &ComputeServer{URL: ts.URL, Close: ts.Close}
}

// StartComputeServerAzureVM starts a shim instance with the Azure Compute
// ARM frontend (Microsoft.Compute/virtualMachines) backed by the given
// backend. The httptest server uses TLS so the Azure SDK sends Bearer
// tokens. Azure-shaped clients (armcompute/v6) drive it via the
// endpoint-override path.
func StartComputeServerAzureVM(t *testing.T, backend azurecomputefront.ComputeBackend) *ComputeServer {
	t.Helper()
	srv := azurecomputefront.Handler(backend)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &ComputeServer{URL: ts.URL, Close: ts.Close}
}

// ComputeServerAzureVMTLS is the HTTPS variant of ComputeServer for the
// Azure Compute frontend, carrying the self-signed cert as PEM so callers
// can inject it into a combined CA bundle for child processes (az CLI).
type ComputeServerAzureVMTLS struct {
	URL     string
	CertPEM []byte
	Close   func()
}

// StartComputeServerAzureVMWithConfig starts the Azure Compute VM frontend
// with the given Config (passthrough + metadata + JWKS bearer options).
// Returns the TLS server's cert so callers can build a combined CA bundle.
func StartComputeServerAzureVMWithConfig(t *testing.T, backend azurecomputefront.ComputeBackend, cfg azurecomputefront.Config) *ComputeServerAzureVMTLS {
	t.Helper()
	srv := azurecomputefront.HandlerWithConfig(backend, cfg)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	cert := ts.Certificate()
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw})
	return &ComputeServerAzureVMTLS{URL: ts.URL, CertPEM: certPEM, Close: ts.Close}
}

// LoadBalancerServer is a started load-balancer-shim instance.
type LoadBalancerServer struct {
	URL   string
	Close func()
}

// StartLoadBalancerServerAWS starts a shim instance with the AWS ELBv2
// (awsQuery) frontend backed by the given load-balancer implementation.
func StartLoadBalancerServerAWS(t *testing.T, backend lbdomain.LoadBalancers) *LoadBalancerServer {
	t.Helper()
	srv := awselbv2front.New(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &LoadBalancerServer{URL: ts.URL, Close: ts.Close}
}

// StartLoadBalancerServerGCP starts a shim instance with the GCP
// Compute Engine layer-4 LB (forwarding rules + backend services) REST
// frontend backed by the given load-balancer implementation.
func StartLoadBalancerServerGCP(t *testing.T, backend lbdomain.LoadBalancers) *LoadBalancerServer {
	t.Helper()
	srv := gcplbfront.Handler(backend)
	ts := httptest.NewServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &LoadBalancerServer{URL: ts.URL, Close: ts.Close}
}

// StartLoadBalancerServerAzure starts a shim instance with the Azure
// Network load balancer ARM frontend. Uses TLS so the Azure SDK sends
// Bearer tokens.
func StartLoadBalancerServerAzure(t *testing.T, backend lbdomain.LoadBalancers) *LoadBalancerServer {
	t.Helper()
	srv := azurelbfront.Handler(backend)
	ts := httptest.NewTLSServer(&logRoundTrip{t: t, mux: srv})
	t.Cleanup(ts.Close)
	return &LoadBalancerServer{URL: ts.URL, Close: ts.Close}
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
