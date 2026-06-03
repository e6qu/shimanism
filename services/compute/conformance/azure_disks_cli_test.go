// Conformance: Azure Compute-shaped block storage exercised by the
// official `az disk` / `az snapshot` CLI through the shim + sockerless.
// Covers Phase 17: disk create/show/list/delete.
//
// Uses az cloud register + az login --service-principal against
// sockerless's Entra stub (same plumbing as the az vm test). Skips
// cleanly when SOCKERLESS_AZURE_TLS_PORT/_CERT or az CLI are absent.
// Linux-only via SSL_CERT_FILE.
package conformance_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/azurebearer"
	azurecomputefront "github.com/e6qu/shimanism/internal/compute/frontends/azure_compute"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/compute/backends/inmem"
)

// azSockerlessRunner is an `az` invoker wired to a custom cloud profile
// pointing at the shim (ARM) + sockerless (Entra), already logged in.
type azSockerlessRunner func(args ...string) ([]byte, []byte, error)

// newAzSockerlessComputeSession builds the shim+sockerless az session
// and returns a runner plus the resource-group name. It registers the
// custom cloud, logs in, and creates the resource group; all teardown
// is registered on t.Cleanup. The test is skipped if any prerequisite
// is missing.
func newAzSockerlessComputeSession(t *testing.T, profileName, resourceGroup string) (azSockerlessRunner, string) {
	t.Helper()
	azBin := requireAzCLIForCompute(t)
	azureTLSPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azureTLSPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	systemCA := findSystemCABundleForCompute()
	if systemCA == "" {
		t.Skip("no system CA bundle found — SSL_CERT_FILE workaround requires Linux")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000001"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		clientSecret   = "shim-test"
	)

	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("AppendCertsFromPEM: no certs parsed")
	}
	proxy := httputil.NewSingleHostReverseProxy(sockerlessARM)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}} //nolint:gosec

	jwks := fetchSockerlessComputeJWKS(t, azureTLSPort, tenantID, sockCertPEM)
	shim := harness.StartComputeServerAzureVMWithConfig(t, inmem.New(), azurecomputefront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			JWKS:   jwks,
		},
	})

	dir := t.TempDir()
	systemBytes, err := os.ReadFile(systemCA)
	if err != nil {
		t.Fatalf("read system CA: %v", err)
	}
	combined := append(append([]byte{}, systemBytes...), '\n')
	combined = append(combined, sockCertPEM...)
	combined = append(combined, '\n')
	combined = append(combined, shim.CertPEM...)
	combinedPath := filepath.Join(dir, "combined-ca.pem")
	if err := os.WriteFile(combinedPath, combined, 0o644); err != nil {
		t.Fatalf("write combined CA: %v", err)
	}
	azureConfigDir := filepath.Join(dir, ".azure")
	if err := os.MkdirAll(azureConfigDir, 0o755); err != nil {
		t.Fatalf("mkdir azure config: %v", err)
	}

	runAz := func(args ...string) ([]byte, []byte, error) {
		t.Helper()
		cmd := exec.Command(azBin, args...)
		cmd.Env = append(os.Environ(),
			"AZURE_CONFIG_DIR="+azureConfigDir,
			"SSL_CERT_FILE="+combinedPath,
			"REQUESTS_CA_BUNDLE="+combinedPath,
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return stdout.Bytes(), stderr.Bytes(), cmd.Run()
	}
	mustRunAz := func(args ...string) {
		t.Helper()
		stdout, stderr, err := runAz(args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
	}

	mustRunAz("cloud", "register",
		"--name", profileName,
		"--endpoint-resource-manager", shim.URL,
		"--endpoint-active-directory", sockerlessARM.String(),
		"--endpoint-active-directory-resource-id", shim.URL,
		"--endpoint-active-directory-graph-resource-id", sockerlessARM.String(),
		"--suffix-keyvault-dns", ".vault.localhost",
		"--suffix-storage-endpoint", "storage.localhost",
	)
	t.Cleanup(func() {
		_, _, _ = runAz("cloud", "set", "--name", "AzureCloud")
		_, _, _ = runAz("cloud", "unregister", "--name", profileName)
	})
	mustRunAz("cloud", "set", "--name", profileName)
	mustRunAz("login", "--service-principal",
		"--username", clientID, "--password", clientSecret,
		"--tenant", tenantID, "--allow-no-subscriptions",
	)
	mustRunAz("account", "set", "--subscription", subscriptionID)
	mustRunAz("group", "create", "--name", resourceGroup, "--location", "eastus")
	t.Cleanup(func() {
		_, _, _ = runAz("group", "delete", "--name", resourceGroup, "--yes", "--no-wait")
	})
	return runAz, resourceGroup
}

func TestAzureCLI_Compute_DiskLifecycle(t *testing.T) {
	runAz, rg := newAzSockerlessComputeSession(t, "shim-disk-cli", "shim-disk-cli-rg")

	must := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAz(args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// az disk create
	out := must("disk", "create",
		"--resource-group", rg,
		"--name", "cli-disk",
		"--size-gb", "16",
		"--sku", "Standard_LRS",
		"-o", "json",
	)
	var created struct {
		Name       string `json:"name"`
		DiskSizeGB int    `json:"diskSizeGb"`
	}
	if err := json.Unmarshal(out, &created); err != nil {
		t.Fatalf("az disk create: unmarshal: %v\nraw: %s", err, out)
	}
	if created.Name != "cli-disk" {
		t.Errorf("disk name = %q, want cli-disk", created.Name)
	}

	// az disk show
	must("disk", "show", "--resource-group", rg, "--name", "cli-disk", "-o", "json")

	// az disk list
	listOut := must("disk", "list", "--resource-group", rg, "-o", "json")
	if !strings.Contains(string(listOut), "cli-disk") {
		t.Errorf("az disk list does not contain cli-disk:\n%s", listOut)
	}

	// az disk delete
	must("disk", "delete", "--resource-group", rg, "--name", "cli-disk", "--yes")
}
