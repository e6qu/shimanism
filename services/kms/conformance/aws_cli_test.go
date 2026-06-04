// Conformance: AWS KMS driven by the `aws kms` CLI. Covers Phase 19:
// create-key, describe-key, list-keys, encrypt, decrypt. Skipped if the
// `aws` binary isn't on PATH.
package conformance_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/kms/backends/inmem"
)

func requireAWSCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed: %v", err)
	}
	return bin
}

func runAWSKMS(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{
		"--endpoint-url=" + srvURL,
		"--no-cli-pager",
		"--output", "json",
		"kms",
	}, args...)...)
	cmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AWS_DEFAULT_REGION=us-east-1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestAWSCLI_KMS_KeyAndCrypto(t *testing.T) {
	awsBin := requireAWSCLI(t)
	srv := harness.StartKMSServerAWS(t, inmem.New())

	run := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSKMS(t, srv.URL, awsBin, args...)
		if err != nil {
			t.Fatalf("aws kms %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// create-key
	out := run("create-key", "--description", "cli-test-key")
	var created struct {
		KeyMetadata struct {
			KeyId    string `json:"KeyId"`
			KeyState string `json:"KeyState"`
		} `json:"KeyMetadata"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("parse create-key: %v\nraw: %s", err, out)
	}
	keyID := created.KeyMetadata.KeyId
	if keyID == "" {
		t.Fatal("create-key returned empty KeyId")
	}
	if created.KeyMetadata.KeyState != "Enabled" {
		t.Errorf("KeyState = %q, want Enabled", created.KeyMetadata.KeyState)
	}

	// describe-key
	descOut := run("describe-key", "--key-id", keyID)
	if !strings.Contains(string(descOut), keyID) {
		t.Errorf("describe-key output missing key id:\n%s", descOut)
	}

	// list-keys
	listOut := run("list-keys")
	if !strings.Contains(string(listOut), keyID) {
		t.Errorf("list-keys output missing key id:\n%s", listOut)
	}

	// encrypt — plaintext is base64 on the wire; aws CLI accepts fileb:// or
	// a base64 string with --plaintext when the value is already base64.
	plaintext := "hello-kms"
	encOut := run("encrypt", "--key-id", keyID, "--plaintext", base64.StdEncoding.EncodeToString([]byte(plaintext)))
	var enc struct {
		CiphertextBlob string `json:"CiphertextBlob"`
	}
	if err := json.Unmarshal(encOut, &enc); err != nil {
		t.Fatalf("parse encrypt: %v\nraw: %s", err, encOut)
	}
	if enc.CiphertextBlob == "" {
		t.Fatal("encrypt returned empty CiphertextBlob")
	}

	// decrypt
	decOut := run("decrypt", "--ciphertext-blob", enc.CiphertextBlob)
	var dec struct {
		Plaintext string `json:"Plaintext"`
	}
	if err := json.Unmarshal(decOut, &dec); err != nil {
		t.Fatalf("parse decrypt: %v\nraw: %s", err, decOut)
	}
	got, err := base64.StdEncoding.DecodeString(dec.Plaintext)
	if err != nil {
		t.Fatalf("decode decrypt plaintext: %v", err)
	}
	if string(got) != plaintext {
		t.Errorf("decrypt round-trip = %q, want %q", got, plaintext)
	}
}
