// Phase 2 conformance: GCP Secret Manager-shaped frontend
// exercised by the official `google.golang.org/api/secretmanager/v1`
// SDK. The SDK is pointed at the shim via WithEndpoint; auth is
// disabled (the shim accepts unsigned requests at this phase, same
// posture as the GCS frontend test in Phase 1).
package conformance_test

import (
	"context"
	"encoding/base64"
	"testing"

	smraw "google.golang.org/api/secretmanager/v1"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/secrets/backends/inmem"
)

func newGCPSecretManagerService(t *testing.T, endpoint string) *smraw.Service {
	t.Helper()
	svc, err := smraw.NewService(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new GCP Secret Manager service: %v", err)
	}
	return svc
}

func TestGCPSDK_SecretLifecycle(t *testing.T) {
	srv := harness.StartSecretsServerGCP(t, inmem.New())
	svc := newGCPSecretManagerService(t, srv.URL)
	ctx := context.Background()
	const parent = "projects/shim-conformance"

	// CreateSecret carries metadata; the value is added in a second
	// call (AddSecretVersion), exactly like the real GCP API.
	if _, err := svc.Projects.Secrets.Create(parent, &smraw.Secret{
		Labels: map[string]string{"env": "test"},
	}).SecretId("api-token").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Secrets.AddVersion(parent+"/secrets/api-token", &smraw.AddSecretVersionRequest{
		Payload: &smraw.SecretPayload{
			Data: base64.StdEncoding.EncodeToString([]byte("hello-shim")),
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("AddVersion: %v", err)
	}

	// Get the secret metadata.
	got, err := svc.Projects.Secrets.Get(parent + "/secrets/api-token").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Labels["env"] != "test" {
		t.Errorf("Get labels env = %q, want test", got.Labels["env"])
	}

	// Access the latest version.
	access, err := svc.Projects.Secrets.Versions.Access(parent + "/secrets/api-token/versions/latest").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Access latest: %v", err)
	}
	data, err := base64.StdEncoding.DecodeString(access.Payload.Data)
	if err != nil {
		t.Fatalf("decode access payload: %v", err)
	}
	if string(data) != "hello-shim" {
		t.Errorf("access data = %q, want hello-shim", data)
	}

	// AddVersion again, then access version 2 explicitly.
	if _, err := svc.Projects.Secrets.AddVersion(parent+"/secrets/api-token", &smraw.AddSecretVersionRequest{
		Payload: &smraw.SecretPayload{
			Data: base64.StdEncoding.EncodeToString([]byte("hello-shim-v2")),
		},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("AddVersion v2: %v", err)
	}
	access2, err := svc.Projects.Secrets.Versions.Access(parent + "/secrets/api-token/versions/2").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Access v2: %v", err)
	}
	data2, _ := base64.StdEncoding.DecodeString(access2.Payload.Data)
	if string(data2) != "hello-shim-v2" {
		t.Errorf("v2 data = %q, want hello-shim-v2", data2)
	}

	// ListSecrets returns the secret.
	list, err := svc.Projects.Secrets.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list.Secrets) != 1 {
		t.Fatalf("List count = %d, want 1", len(list.Secrets))
	}

	// ListSecretVersions returns both versions.
	versions, err := svc.Projects.Secrets.Versions.List(parent + "/secrets/api-token").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versions.Versions) != 2 {
		t.Fatalf("ListVersions count = %d, want 2", len(versions.Versions))
	}

	// DeleteSecret tears it down.
	if _, err := svc.Projects.Secrets.Delete(parent + "/secrets/api-token").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}
