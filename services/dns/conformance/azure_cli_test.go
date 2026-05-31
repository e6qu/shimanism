// Conformance: Azure DNS-shaped frontend exercised by the official
// `az network dns` CLI through-shim against sockerless. Closes
// BUG-43.
//
// Unlike sockerless's own bash tests (which use `az rest` to bypass
// cloud-register), this test exercises the real `az network dns`
// subcommands — the actual user-facing CLI surface — by registering
// a custom `az cloud` profile pointing at the shim + sockerless and
// logging in via `az login --service-principal` against sockerless's
// Entra ID stub. Per-test `AZURE_CONFIG_DIR` isolates the profile
// from any system-wide `az` state.
//
// Linux-only via SSL_CERT_FILE platform limit.
package conformance_test

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"crypto/tls"

	"github.com/e6qu/shimanism/internal/azurebearer"
	azuredfront "github.com/e6qu/shimanism/internal/dns/frontends/azure_dns"
	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

func requireAzCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup: %v)", err)
	}
	return bin
}

func TestAzureCLI_DNS_ZoneLifecycle_ThroughShim(t *testing.T) {
	azBin := requireAzCLI(t)
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
	systemCABundle := findSystemCABundleForDNS()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found — SSL_CERT_FILE workaround requires Linux")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		clientSecret   = "shim-test"
		resourceGroup  = "shim-dns-cli-rg"
		zoneName       = "cli.azure.example"
		profileName    = "shim-conformance"
	)

	// Reverse proxy → sockerless ARM (TLS, explicit RootCAs pinning).
	sockerlessARM, err := url.Parse("https://localhost:" + azureTLSPort)
	if err != nil {
		t.Fatalf("parse sockerless URL: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(sockCertPEM) {
		t.Fatalf("AppendCertsFromPEM: no certs parsed")
	}
	proxy := httputil.NewSingleHostReverseProxy(sockerlessARM)
	proxy.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}

	jwks := fetchSockerlessDNSJWKS(t, azureTLSPort, tenantID, sockCertPEM)
	shim := harness.StartDNSServerAzureWithConfig(t, inmem.New(), azuredfront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			JWKS:   jwks,
		},
	})

	// Combined CA bundle so `az` trusts both the shim's httptest cert
	// and sockerless's cert. No InsecureSkipVerify — `az` is a child
	// process and goes through the system trust store via SSL_CERT_FILE.
	dir := t.TempDir()
	systemBytes, err := os.ReadFile(systemCABundle)
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
	_ = pem.Block{}

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
			"REQUESTS_CA_BUNDLE="+combinedPath, // older azure-cli reads this
		)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		return stdout.Bytes(), stderr.Bytes(), err
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

	// 1. Register a custom cloud pointing at the shim (ARM) + sockerless
	//    (Active Directory). Audience tokens get issued for is the
	//    resource-manager URL — shim's URL — so the shim's bearer
	//    verifier accepts them (with Audience="" in BearerOptions).
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

	// 2. Create + read + delete the zone via `az network dns zone`.
	mustRunAz("group", "create", "--name", resourceGroup, "--location", "global")
	t.Cleanup(func() { _, _, _ = runAz("group", "delete", "--name", resourceGroup, "--yes", "--no-wait") })

	mustRunAz("network", "dns", "zone", "create",
		"--name", zoneName,
		"--resource-group", resourceGroup,
	)

	mustRunAz("network", "dns", "zone", "show",
		"--name", zoneName,
		"--resource-group", resourceGroup,
	)

	mustRunAz("network", "dns", "zone", "delete",
		"--name", zoneName,
		"--resource-group", resourceGroup,
		"--yes",
	)
}
