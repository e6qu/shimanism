// Conformance: GCP Cloud KMS-shaped frontend exercised by the official
// `gcloud kms` CLI, configured via the CLOUDKMS endpoint override so the
// CLI talks to the shim instead of real Cloud KMS. Covers keyrings
// create, keys create/list, and an encrypt/decrypt round-trip. Skipped
// if the `gcloud` binary isn't on PATH.
package conformance_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/e6qu/shimanism/internal/gcpbearer"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/kms/backends/inmem"
)

func requireGcloudKMS(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("gcloud")
	if err != nil {
		t.Skipf("gcloud not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runGcloudKMS(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	full := strings.TrimRight(srvURL, "/") + "/"
	jwt := gcpbearer.TestJWT(
		[]byte("test-key-do-not-use-in-prod"),
		"https://shim.test/",
		"https://cloudkms.googleapis.com/",
		15*time.Minute,
	)
	cmd := exec.Command(bin, append([]string{"--quiet"}, args...)...)
	cmd.Env = append(os.Environ(),
		"CLOUDSDK_API_ENDPOINT_OVERRIDES_CLOUDKMS="+full,
		"CLOUDSDK_AUTH_ACCESS_TOKEN="+jwt,
		"CLOUDSDK_CORE_DISABLE_PROMPTS=1",
		"CLOUDSDK_CORE_PROJECT="+gcpProject,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestGCPCLI_KMS_KeyAndCrypto(t *testing.T) {
	t.Parallel()
	gcloud := requireGcloudKMS(t)
	srv := harness.StartKMSServerGCP(t, inmem.New())

	const (
		ring = "shim-cli-ring"
		key  = "shim-cli-key"
	)
	run := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runGcloudKMS(t, srv.URL, gcloud, args...)
		if err != nil {
			t.Fatalf("gcloud %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// keyrings create (synthetic container on the shim).
	run("kms", "keyrings", "create", ring, "--location="+gcpLocation)

	// keys create (symmetric ENCRYPT_DECRYPT).
	run("kms", "keys", "create", key,
		"--location="+gcpLocation, "--keyring="+ring, "--purpose=encryption")

	// keys list contains the new key.
	list := run("kms", "keys", "list", "--location="+gcpLocation, "--keyring="+ring)
	if !strings.Contains(string(list), key) {
		t.Errorf("gcloud kms keys list missing %q:\n%s", key, list)
	}

	// encrypt / decrypt round-trip via files.
	dir := t.TempDir()
	ptFile := filepath.Join(dir, "plaintext")
	ctFile := filepath.Join(dir, "ciphertext")
	outFile := filepath.Join(dir, "roundtrip")
	plaintext := []byte("gcloud-kms-secret")
	if err := os.WriteFile(ptFile, plaintext, 0o644); err != nil {
		t.Fatalf("write plaintext: %v", err)
	}
	run("kms", "encrypt",
		"--location="+gcpLocation, "--keyring="+ring, "--key="+key,
		"--plaintext-file="+ptFile, "--ciphertext-file="+ctFile)
	run("kms", "decrypt",
		"--location="+gcpLocation, "--keyring="+ring, "--key="+key,
		"--ciphertext-file="+ctFile, "--plaintext-file="+outFile)

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read decrypted output: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypt round-trip = %q, want %q", got, plaintext)
	}
}
