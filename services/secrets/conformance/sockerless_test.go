// Sockerless lane for the secrets service. Mirror of
// services/storage/conformance/sockerless_test.go — points the
// shim's AWS Secrets Manager backend at a running sockerless AWS
// simulator instance. See doc/SOCKERLESS_VALIDATION.md.
//
// GCP Secret Manager and Azure Key Vault are not yet simulated by
// sockerless; those backends still gate on Track A.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net/http"
	"os"
	"testing"

	awsapi "github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/e6qu/shimanism/internal/secrets/domain"
	awsbackend "github.com/e6qu/shimanism/services/secrets/backends/aws"
)

// TestSockerless_AWSSecretsManager_RoundTrip drives the shim's AWS
// Secrets Manager backend → sockerless AWS sim. The Secrets Manager
// wire protocol is awsJson1.1 (POST / dispatched by X-Amz-Target);
// it doesn't have the path-prefix issue tracked at
// e6qu/sockerless#173 (which only affects S3).
//
// Set SOCKERLESS_AWS_SM_ENDPOINT (e.g. http://localhost:14566) to
// opt in. HTTP works fine here — secretsmanager doesn't use the
// streaming-signed-payload code path that forced TLS on the S3 lane.
func TestSockerless_AWSSecretsManager_RoundTrip(t *testing.T) {
	endpoint := os.Getenv("SOCKERLESS_AWS_SM_ENDPOINT")
	if endpoint == "" {
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
		cfg.HTTPClient = awshttp.NewBuildableClient().WithTransportOptions(func(tr *http.Transport) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		})
	}
	client := awssm.NewFromConfig(cfg, func(o *awssm.Options) {
		o.BaseEndpoint = awsapi.String(endpoint)
	})
	backend := awsbackend.New(client)
	ctx := context.Background()

	name := "shim-sockerless-" + randomHex(8)
	if _, err := backend.CreateSecret(ctx, name, domain.CreateSecretOptions{
		InitialValue: []byte("value-v1"),
	}); err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	t.Cleanup(func() { _ = backend.DeleteSecret(ctx, name, true) })

	list, err := backend.ListSecrets(ctx, domain.ListSecretsOptions{Prefix: name})
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	found := false
	for _, s := range list.Secrets {
		if s.Name == name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListSecrets did not contain %q", name)
	}

	// HeadSecret and GetSecretValue are skipped on sockerless: the
	// shim's AWS backend derives the monotonic version by calling
	// ListSecretVersionIds, which sockerless doesn't implement
	// (e6qu/sockerless#175). CreateSecret + ListSecrets +
	// DeleteSecret round-trip is the working subset today.
}

func randomHex(n int) string {
	b := make([]byte, n/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
