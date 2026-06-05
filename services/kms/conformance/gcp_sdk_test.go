// Conformance: GCP Cloud KMS-shaped frontend exercised by the official
// google.golang.org/api/cloudkms/v1 REST SDK. Covers Phase 19.B:
// keyRings.create (synthetic), cryptoKeys.create/get/list, encrypt,
// decrypt, cryptoKeys.patch (rotation).
package conformance_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
	kmsraw "google.golang.org/api/cloudkms/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/kms/backends/inmem"
)

const (
	gcpProject  = "shim-conformance"
	gcpLocation = "us-central1"
	gcpKeyRing  = "shim-ring"
)

func newGCPKMSClient(t *testing.T, endpoint string) *kmsraw.Service {
	t.Helper()
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://cloudkms.googleapis.com/",
		15*time.Minute,
	)
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: jwt})
	svc, err := kmsraw.NewService(context.Background(),
		option.WithEndpoint(endpoint),
		option.WithTokenSource(ts),
	)
	if err != nil {
		t.Fatalf("build cloudkms client: %v", err)
	}
	return svc
}

func gcpRing() string {
	return "projects/" + gcpProject + "/locations/" + gcpLocation + "/keyRings/" + gcpKeyRing
}

func TestGCPSDK_KMS_KeyAndCrypto(t *testing.T) {
	srv := harness.StartKMSServerGCP(t, inmem.New())
	svc := newGCPKMSClient(t, srv.URL)
	ctx := context.Background()

	// keyRings.create (synthetic container).
	parent := "projects/" + gcpProject + "/locations/" + gcpLocation
	if _, err := svc.Projects.Locations.KeyRings.Create(parent, &kmsraw.KeyRing{}).
		KeyRingId(gcpKeyRing).Context(ctx).Do(); err != nil {
		t.Fatalf("keyRings.create: %v", err)
	}

	// cryptoKeys.create.
	ck, err := svc.Projects.Locations.KeyRings.CryptoKeys.Create(gcpRing(), &kmsraw.CryptoKey{
		Purpose: "ENCRYPT_DECRYPT",
	}).CryptoKeyId("shim-key").Context(ctx).Do()
	if err != nil {
		t.Fatalf("cryptoKeys.create: %v", err)
	}
	if !strings.HasSuffix(ck.Name, "/cryptoKeys/shim-key") {
		t.Errorf("cryptoKey name = %q, want suffix /cryptoKeys/shim-key", ck.Name)
	}
	keyName := gcpRing() + "/cryptoKeys/shim-key"

	// cryptoKeys.get.
	got, err := svc.Projects.Locations.KeyRings.CryptoKeys.Get(keyName).Context(ctx).Do()
	if err != nil {
		t.Fatalf("cryptoKeys.get: %v", err)
	}
	if !strings.HasSuffix(got.Name, "shim-key") {
		t.Errorf("get name = %q", got.Name)
	}

	// cryptoKeys.list.
	list, err := svc.Projects.Locations.KeyRings.CryptoKeys.List(gcpRing()).Context(ctx).Do()
	if err != nil {
		t.Fatalf("cryptoKeys.list: %v", err)
	}
	found := false
	for _, k := range list.CryptoKeys {
		if strings.HasSuffix(k.Name, "shim-key") {
			found = true
		}
	}
	if !found {
		t.Errorf("list does not contain shim-key (%d keys)", len(list.CryptoKeys))
	}

	// encrypt / decrypt.
	plaintext := []byte("gcp-kms-secret")
	enc, err := svc.Projects.Locations.KeyRings.CryptoKeys.Encrypt(keyName, &kmsraw.EncryptRequest{
		Plaintext: base64.StdEncoding.EncodeToString(plaintext),
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if enc.Ciphertext == "" {
		t.Fatal("encrypt returned empty ciphertext")
	}
	dec, err := svc.Projects.Locations.KeyRings.CryptoKeys.Decrypt(keyName, &kmsraw.DecryptRequest{
		Ciphertext: enc.Ciphertext,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	got2, err := base64.StdEncoding.DecodeString(dec.Plaintext)
	if err != nil {
		t.Fatalf("decode decrypt plaintext: %v", err)
	}
	if !bytes.Equal(got2, plaintext) {
		t.Errorf("decrypt round-trip = %q, want %q", got2, plaintext)
	}
}

func TestGCPSDK_KMS_Rotation(t *testing.T) {
	srv := harness.StartKMSServerGCP(t, inmem.New())
	svc := newGCPKMSClient(t, srv.URL)
	ctx := context.Background()

	parent := "projects/" + gcpProject + "/locations/" + gcpLocation
	svc.Projects.Locations.KeyRings.Create(parent, &kmsraw.KeyRing{}).KeyRingId(gcpKeyRing).Context(ctx).Do() //nolint:errcheck
	if _, err := svc.Projects.Locations.KeyRings.CryptoKeys.Create(gcpRing(), &kmsraw.CryptoKey{
		Purpose: "ENCRYPT_DECRYPT",
	}).CryptoKeyId("rot-key").Context(ctx).Do(); err != nil {
		t.Fatalf("cryptoKeys.create: %v", err)
	}
	keyName := gcpRing() + "/cryptoKeys/rot-key"

	// Patch with rotationPeriod enables rotation.
	if _, err := svc.Projects.Locations.KeyRings.CryptoKeys.Patch(keyName, &kmsraw.CryptoKey{
		RotationPeriod: "604800s",
	}).UpdateMask("rotationPeriod").Context(ctx).Do(); err != nil {
		t.Fatalf("cryptoKeys.patch (enable rotation): %v", err)
	}
}

// TestGCPSDK_KMS_KeyRingExistence verifies the frontend tracks keyRings
// honestly against the backend-of-record: an unknown ring is genuinely
// absent (404), not synthesized; a created ring reads back; and a key
// cannot be created in a ring that does not exist.
func TestGCPSDK_KMS_KeyRingExistence(t *testing.T) {
	srv := harness.StartKMSServerGCP(t, inmem.New())
	svc := newGCPKMSClient(t, srv.URL)
	ctx := context.Background()

	parent := "projects/" + gcpProject + "/locations/" + gcpLocation
	missing := parent + "/keyRings/never-created"

	// GET an unknown keyRing -> 404 (not a synthetic 200).
	if _, err := svc.Projects.Locations.KeyRings.Get(missing).Context(ctx).Do(); !isGCPStatus(err, 404) {
		t.Fatalf("get unknown keyRing: want 404, got %v", err)
	}

	// Creating a cryptoKey in a non-existent ring -> 404.
	if _, err := svc.Projects.Locations.KeyRings.CryptoKeys.Create(missing, &kmsraw.CryptoKey{
		Purpose: "ENCRYPT_DECRYPT",
	}).CryptoKeyId("orphan").Context(ctx).Do(); !isGCPStatus(err, 404) {
		t.Fatalf("create key in unknown ring: want 404, got %v", err)
	}

	// Create the ring, then it reads back.
	if _, err := svc.Projects.Locations.KeyRings.Create(parent, &kmsraw.KeyRing{}).
		KeyRingId(gcpKeyRing).Context(ctx).Do(); err != nil {
		t.Fatalf("keyRings.create: %v", err)
	}
	if _, err := svc.Projects.Locations.KeyRings.Get(gcpRing()).Context(ctx).Do(); err != nil {
		t.Fatalf("get existing keyRing: %v", err)
	}
	// Re-creating the same ring -> 409 ALREADY_EXISTS.
	if _, err := svc.Projects.Locations.KeyRings.Create(parent, &kmsraw.KeyRing{}).
		KeyRingId(gcpKeyRing).Context(ctx).Do(); !isGCPStatus(err, 409) {
		t.Fatalf("re-create keyRing: want 409, got %v", err)
	}
}

func isGCPStatus(err error, code int) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code == code
}
