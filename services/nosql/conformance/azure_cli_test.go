// Conformance: Azure Cosmos Tables-shaped frontend exercised by the
// official `az cosmosdb table` CLI through-shim against sockerless.
// Closes BUG-50 (az CLI row).
//
// Unlike sockerless's own bash tests (which use `az rest` to bypass
// cloud-register), this test exercises the real `az cosmosdb table`
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
	"crypto/x509"

	"github.com/e6qu/shimanism/internal/azurebearer"
	"github.com/e6qu/shimanism/internal/harness"
	azurectfront "github.com/e6qu/shimanism/internal/nosql/frontends/azure_cosmos_tables"
	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

func TestAzureCLI_CosmosTable_Lifecycle_ThroughShim(t *testing.T) {
	azBin, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup: %v)", err)
	}
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
	systemCABundle := findSystemCABundleForNoSQL()
	if systemCABundle == "" {
		t.Skip("no system CA bundle found — SSL_CERT_FILE workaround requires Linux")
	}

	const (
		subscriptionID = "00000000-0000-0000-0000-000000000000"
		tenantID       = "00000000-0000-0000-0000-000000000000"
		clientID       = "00000000-0000-0000-0000-000000000000"
		clientSecret   = "shim-test"
		resourceGroup  = "shim-cosmostables-cli-rg"
		accountName    = "shimcosmosacct-cli"
		tableName      = "shimtable-cli"
		profileName    = "shim-cosmostables-conformance"
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

	jwks := fetchSockerlessNoSQLJWKS(t, azureTLSPort, tenantID, sockCertPEM)
	shim := harness.StartNoSQLServerAzureWithConfig(t, inmem.New(), azurectfront.Config{
		Passthrough:      proxy,
		MetadataLoginURL: sockerlessARM.String(),
		BearerOptions: azurebearer.Options{
			Issuer: fmt.Sprintf("https://sts.windows.net/%s/", tenantID),
			// Audience left empty — sockerless mints tokens with
			// aud = <shim_url>; the shim URL is dynamic at httptest
			// setup. Signature, Issuer, Exp/Nbf still checked.
			JWKS: jwks,
		},
	})

	// Combined CA bundle: system + sockerless + shim certs. `az` child
	// process reads it via SSL_CERT_FILE (Python openssl) and
	// REQUESTS_CA_BUNDLE (requests library fallback in older az versions).
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

	// 1. Register custom cloud: shim as ARM endpoint, sockerless as
	//    Active Directory. Audience tokens get issued for is the
	//    resource-manager URL — shim URL — so the shim's bearer
	//    verifier accepts them (Audience="" in BearerOptions).
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

	// 2. Resource group (ARM passthrough → sockerless resourcegroups.go).
	mustRunAz("group", "create", "--name", resourceGroup, "--location", "eastus")
	t.Cleanup(func() {
		_, _, _ = runAz("group", "delete", "--name", resourceGroup, "--yes", "--no-wait")
	})

	// 3. Cosmos DB account (ARM passthrough → sockerless cosmos.go).
	//    sockerless returns 200 with provisioningState=Succeeded, so
	//    `az cosmosdb create` treats it as synchronous success.
	mustRunAz("cosmosdb", "create",
		"--name", accountName,
		"--resource-group", resourceGroup,
		"--capabilities", "EnableTable",
		"--default-consistency-level", "Session",
		"--locations", "regionName=eastus", "failoverPriority=0", "isZoneRedundant=False",
	)
	t.Cleanup(func() {
		_, _, _ = runAz("cosmosdb", "delete",
			"--name", accountName,
			"--resource-group", resourceGroup,
			"--yes")
	})

	// 4. Create + show + list + delete the table via `az cosmosdb table`.
	mustRunAz("cosmosdb", "table", "create",
		"--account-name", accountName,
		"--name", tableName,
		"--resource-group", resourceGroup,
	)

	out := mustRunAz("cosmosdb", "table", "show",
		"--account-name", accountName,
		"--name", tableName,
		"--resource-group", resourceGroup,
	)
	if !strings.Contains(string(out), tableName) {
		t.Errorf("table show output missing table name %q:\n%s", tableName, out)
	}

	listOut := mustRunAz("cosmosdb", "table", "list",
		"--account-name", accountName,
		"--resource-group", resourceGroup,
	)
	if !strings.Contains(string(listOut), tableName) {
		t.Errorf("table list output missing table name %q:\n%s", tableName, listOut)
	}

	mustRunAz("cosmosdb", "table", "delete",
		"--account-name", accountName,
		"--name", tableName,
		"--resource-group", resourceGroup,
		"--yes",
	)
}
