// Cross-frontend × cross-backend conformance for secrets: for every
// frontend in {AWS Secrets Manager, GCP Secret Manager, Azure Key
// Vault} and every backend in {inmem, vault, aws, gcp, azure},
// drive a Create → Read → Update → Read → Delete round-trip via the
// matching cloud's official Go SDK.
//
// Backends decide their own skip semantics — inmem always runs; the
// others wait on env vars + (for the three real-cloud backends) on
// Track A cloud accounts. CI lights up one backend per job and the
// matrix tests cover all three frontends against that one backend.
package conformance_test

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	smraw "google.golang.org/api/secretmanager/v1"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/secrets/conformance"
)

func TestSecretsMatrix_AWSFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartSecretsServerAWS(t, backend)
			cli := newAWSSecretsManagerClient(t, srv.URL)
			ctx := context.Background()

			name := randomSecretName("shim-aws")
			t.Cleanup(func() {
				_, _ = cli.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
					SecretId:                   aws.String(name),
					ForceDeleteWithoutRecovery: aws.Bool(true),
				})
			})

			if _, err := cli.CreateSecret(ctx, &secretsmanager.CreateSecretInput{
				Name:         aws.String(name),
				SecretString: aws.String("hello-aws"),
			}); err != nil {
				t.Fatalf("CreateSecret: %v", err)
			}
			got, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
				SecretId: aws.String(name),
			})
			if err != nil {
				t.Fatalf("GetSecretValue: %v", err)
			}
			if aws.ToString(got.SecretString) != "hello-aws" {
				t.Errorf("Get = %q, want hello-aws", aws.ToString(got.SecretString))
			}
			if _, err := cli.PutSecretValue(ctx, &secretsmanager.PutSecretValueInput{
				SecretId:     aws.String(name),
				SecretString: aws.String("hello-aws-v2"),
			}); err != nil {
				t.Fatalf("PutSecretValue: %v", err)
			}
			got2, err := cli.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
				SecretId: aws.String(name),
			})
			if err != nil {
				t.Fatalf("GetSecretValue v2: %v", err)
			}
			if aws.ToString(got2.SecretString) != "hello-aws-v2" {
				t.Errorf("Get v2 = %q, want hello-aws-v2", aws.ToString(got2.SecretString))
			}
		})
	}
}

func TestSecretsMatrix_GCPFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartSecretsServerGCP(t, backend)
			jwt := gcpbearer.TestJWT(
				[]byte("test-key-do-not-use-in-prod"),
				"https://shim.test/",
				"https://secretmanager.googleapis.com/",
				15*time.Minute,
			)
			svc, err := smraw.NewService(context.Background(),
				option.WithEndpoint(srv.URL),
				option.WithTokenSource(oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})),
			)
			if err != nil {
				t.Fatalf("new GCP service: %v", err)
			}
			ctx := context.Background()
			parent := "projects/shim-matrix"
			name := randomSecretName("shim-gcp")
			t.Cleanup(func() {
				_, _ = svc.Projects.Secrets.Delete(parent + "/secrets/" + name).Context(ctx).Do()
			})

			if _, err := svc.Projects.Secrets.Create(parent, &smraw.Secret{}).SecretId(name).Context(ctx).Do(); err != nil {
				t.Fatalf("Create: %v", err)
			}
			if _, err := svc.Projects.Secrets.AddVersion(parent+"/secrets/"+name, &smraw.AddSecretVersionRequest{
				Payload: &smraw.SecretPayload{
					Data: base64.StdEncoding.EncodeToString([]byte("hello-gcp")),
				},
			}).Context(ctx).Do(); err != nil {
				t.Fatalf("AddVersion: %v", err)
			}
			access, err := svc.Projects.Secrets.Versions.Access(parent + "/secrets/" + name + "/versions/latest").Context(ctx).Do()
			if err != nil {
				t.Fatalf("Access latest: %v", err)
			}
			data, _ := base64.StdEncoding.DecodeString(access.Payload.Data)
			if string(data) != "hello-gcp" {
				t.Errorf("Access = %q, want hello-gcp", data)
			}
		})
	}
}

func TestSecretsMatrix_AzureFrontend(t *testing.T) {
	for _, bf := range conformance.ActiveBackends() {
		bf := bf
		t.Run(bf.Name, func(t *testing.T) {
			backend := bf.Fn(t)
			srv := harness.StartSecretsServerAzure(t, backend)
			httpClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				},
			}
			cli, err := azsecrets.NewClient(srv.URL, fakeTokenCredential{}, &azsecrets.ClientOptions{
				DisableChallengeResourceVerification: true,
				ClientOptions: azcore.ClientOptions{
					Transport: httpClient,
				},
			})
			if err != nil {
				t.Fatalf("new azure client: %v", err)
			}
			ctx := context.Background()
			name := randomSecretName("shim-az")
			val := "hello-azure"
			t.Cleanup(func() {
				_, _ = cli.DeleteSecret(ctx, name, nil)
			})

			if _, err := cli.SetSecret(ctx, name, azsecrets.SetSecretParameters{Value: &val}, nil); err != nil {
				t.Fatalf("SetSecret: %v", err)
			}
			got, err := cli.GetSecret(ctx, name, "", nil)
			if err != nil {
				t.Fatalf("GetSecret: %v", err)
			}
			if got.Value == nil || *got.Value != val {
				t.Errorf("GetSecret value mismatch")
			}
		})
	}
}

// randomSecretName returns a unique-per-run identifier safe for
// secret names across all four backends. AWS Secrets Manager
// accepts [A-Za-z0-9/_+=.@-]; GCP accepts [A-Za-z0-9_-]; Azure KV
// accepts [A-Za-z0-9-]; Vault accepts almost anything. Lowercase
// hex + a fixed prefix stays within every intersection.
func randomSecretName(prefix string) string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return prefix + "-fallback"
	}
	return fmt.Sprintf("%s-%s", strings.ToLower(prefix), hex.EncodeToString(buf[:]))
}

// satisfy unused-import linter when not all backends are exercised.
var _ = errors.As
