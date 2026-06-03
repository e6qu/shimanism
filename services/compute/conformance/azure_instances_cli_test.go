// Conformance: Azure Compute-shaped frontend exercised by the official
// `az vm` CLI. Uses az cloud register + az login --service-principal
// against sockerless's Entra stub, then drives az vm list/show through
// the shim's azure_compute frontend.
//
// Sockerless acts as a real Azure cloud endpoint; the test uses the
// standard az CLI service-principal flow — no special paths for
// sockerless.
//
// Skipped if SOCKERLESS_AZURE_TLS_PORT is not set (sockerless not
// running), SOCKERLESS_AZURE_TLS_CERT is not set, az CLI is absent,
// or no system CA bundle is found (Linux-only via SSL_CERT_FILE).
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

func requireAzCLIForCompute(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup: %v)", err)
	}
	return bin
}

func TestAzureCLI_Compute_VMList(t *testing.T) {
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
		resourceGroup  = "shim-compute-cli-rg"
		profileName    = "shim-compute-conformance"
	)

	// Reverse proxy to sockerless ARM — handles passthrough paths
	// (resource groups, subscriptions, Entra token endpoint, etc.).
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

	// Fetch JWKS from sockerless's Entra stub so the shim's bearer
	// verifier accepts tokens az CLI acquires from it.
	jwks := fetchSockerlessComputeJWKS(t, azureTLSPort, tenantID, sockCertPEM)

	shim := harness.StartComputeServerAzureVMWithConfig(t, inmem.New(), azurecomputefront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			JWKS:   jwks,
		},
	})

	// Combined CA bundle: system CAs + sockerless cert + shim cert.
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
	mustRunAz := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAz(args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// Register shim as a custom az cloud; login via sockerless Entra.
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
	mustRunAz("login",
		"--service-principal",
		"--username", clientID,
		"--password", clientSecret,
		"--tenant", tenantID,
		"--allow-no-subscriptions",
	)
	mustRunAz("account", "set", "--subscription", subscriptionID)

	// Create a resource group (via passthrough to sockerless).
	mustRunAz("group", "create", "--name", resourceGroup, "--location", "global")
	t.Cleanup(func() {
		_, _, _ = runAz("group", "delete", "--name", resourceGroup, "--yes", "--no-wait")
	})

	// az vm list — expect an empty list (no VMs created yet).
	stdout := mustRunAz("vm", "list",
		"--resource-group", resourceGroup,
		"-o", "json",
	)
	var vms []any
	if err := json.Unmarshal(stdout, &vms); err != nil {
		t.Fatalf("az vm list: unmarshal: %v\nraw: %s", err, stdout)
	}
	if len(vms) != 0 {
		t.Errorf("expected 0 VMs, got %d", len(vms))
	}
}

// fetchSockerlessComputeJWKS fetches the JWKS from sockerless's Entra stub.
func fetchSockerlessComputeJWKS(t *testing.T, port, tenantID string, certPEM []byte) *azurebearer.JWKS {
	t.Helper()
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certPEM)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}} //nolint:gosec
	jwksURL := fmt.Sprintf("https://localhost:%s/%s/discovery/v2.0/keys", port, tenantID)
	resp, err := client.Get(jwksURL)
	if err != nil {
		t.Skipf("sockerless JWKS endpoint unreachable (%v) — skipping test", err)
	}
	defer resp.Body.Close()
	var jwks azurebearer.JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}
	return &jwks
}
