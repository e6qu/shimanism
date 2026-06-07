// Conformance: Azure Application Gateway-shaped frontend exercised by
// the official `az network application-gateway` CLI.
//
// Full end-to-end Azure CLI tests require:
//   - az CLI binary on PATH
//   - SOCKERLESS_AZURE_TLS_PORT set (sockerless Entra stub reachable)
//   - SOCKERLESS_AZURE_TLS_CERT set (PEM path for sockerless's TLS cert)
//   - A system CA bundle present (Linux-only via SSL_CERT_FILE)
//
// Without sockerless the test is skipped with a clear diagnostic.
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
	"github.com/e6qu/shimanism/internal/harness"
	azurelbfront "github.com/e6qu/shimanism/internal/loadbalancer/frontends/azure_lb"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

func requireAzCLIForLB(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup: %v)", err)
	}
	return bin
}

func fetchSockerlessLBJWKS(t *testing.T, port, tenantID string, certPEM []byte) *azurebearer.JWKS {
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

// TestAzureCLI_LB_AppGatewayList exercises az network application-gateway
// list against the shim's azure_lb frontend using sockerless's Entra stub for
// auth. Validates the CLI can authenticate and route to the shim; the
// create/show/delete lifecycle is covered by TestAzureSDK_AppGW_L7Lifecycle.
func TestAzureCLI_LB_AppGatewayList(t *testing.T) {
	azBin := requireAzCLIForLB(t)
	azureTLSPort := os.Getenv("SOCKERLESS_AZURE_TLS_PORT")
	if azureTLSPort == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_PORT not set — Azure CLI tests require sockerless Entra stub; skipping")
	}
	sockCertPath := os.Getenv("SOCKERLESS_AZURE_TLS_CERT")
	if sockCertPath == "" {
		t.Skip("SOCKERLESS_AZURE_TLS_CERT not set")
	}
	sockCertPEM, err := os.ReadFile(sockCertPath)
	if err != nil {
		t.Fatalf("read sockerless cert: %v", err)
	}
	systemCA := findSystemCABundleForLB()
	if systemCA == "" {
		t.Skip("no system CA bundle found — SSL_CERT_FILE workaround requires Linux")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000001"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		clientSecret   = "shim-test"
		resourceGroup  = "shim-lb-cli-rg"
		profileName    = "shim-lb-conformance"
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

	jwks := fetchSockerlessLBJWKS(t, azureTLSPort, tenantID, sockCertPEM)

	shim := harness.StartLoadBalancerServerAzureWithConfig(t, inmem.New(), azurelbfront.Config{
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
	mustRunAz := func(args ...string) []byte {
		t.Helper()
		out, stderr, err := runAz(args...)
		if err != nil {
			t.Fatalf("az %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), out, stderr, err)
		}
		return out
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
	mustRunAz("login",
		"--service-principal",
		"--username", clientID,
		"--password", clientSecret,
		"--tenant", tenantID,
		"--allow-no-subscriptions",
	)
	mustRunAz("account", "set", "--subscription", subscriptionID)

	mustRunAz("group", "create", "--name", resourceGroup, "--location", "global")
	t.Cleanup(func() {
		_, _, _ = runAz("group", "delete", "--name", resourceGroup, "--yes", "--no-wait")
	})

	stdout := mustRunAz("network", "application-gateway", "list",
		"--resource-group", resourceGroup,
		"-o", "json",
	)
	var gws []any
	if err := json.Unmarshal(stdout, &gws); err != nil {
		t.Fatalf("az network application-gateway list: unmarshal: %v\nraw: %s", err, stdout)
	}
	if len(gws) != 0 {
		t.Errorf("expected 0 application gateways, got %d", len(gws))
	}
}
