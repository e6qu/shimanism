// Phase 8 conformance: Azure APIM-shaped frontend driven by `az`.
//
// `az` has no per-resource endpoint override (`gcloud`'s
// `--api-endpoint-overrides` or `aws`'s `--endpoint-url` have no
// `az` equivalent). What it does have is `az cloud register` + `az
// cloud set`, which point the *entire* `az` session at a custom
// cloud's endpoints — ARM URL, AD authority, storage suffix, etc.
//
// We use that. Trade-off: while the test is active, every `az`
// command in that shell hits the shim. The test scopes the switch
// tightly (register → set → run → unset → unregister) and runs
// in its own process so a developer's regular `az` session isn't
// affected.
//
// Auth: `az apim` constructs a request with a bearer token. The
// shim doesn't validate the token (BUG-18); any token the AD
// authority signs is accepted. The test requires the developer to
// have run `az login` against real Azure first so a token is
// available. CI without Azure credentials skips cleanly.
package conformance_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/apigateway/backends/inmem"
)

func TestAzCLI_APIGateway_Smoke(t *testing.T) {
	az, err := exec.LookPath("az")
	if err != nil {
		t.Skipf("az CLI not installed (PATH lookup failed: %v)", err)
	}

	// `az` requires a logged-in account to acquire the bearer token
	// it ships with each request. Without one, `az apim show` would
	// fail at the auth-acquire step before the request reaches the
	// shim. Skip cleanly on hosts that aren't `az login`-ed.
	listOut, err := exec.Command(az, "account", "list", "-o", "tsv").Output()
	if err != nil || len(bytes.TrimSpace(listOut)) == 0 {
		t.Skipf("no logged-in `az` account; run `az login` to enable this lane")
	}

	srv := harness.StartAPIGatewayServerAzure(t, inmem.New())

	cloudName := "shimanism-test"
	// Stub but well-formed AD endpoints. `az` parses them at
	// register-time; the shim doesn't validate the token, so the
	// token issued against real AD is accepted as-is.
	run := func(args ...string) ([]byte, []byte, error) {
		cmd := exec.Command(az, args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		return stdout.Bytes(), stderr.Bytes(), cmd.Run()
	}

	// Cleanup runs unconditionally — leave the developer's `az`
	// session pointing at AzureCloud.
	t.Cleanup(func() {
		_, _, _ = run("cloud", "set", "-n", "AzureCloud")
		_, _, _ = run("cloud", "unregister", "-n", cloudName)
	})

	if _, stderr, err := run(
		"cloud", "register",
		"-n", cloudName,
		"--endpoint-resource-manager", srv.URL,
		"--endpoint-active-directory", "https://login.microsoftonline.com",
		"--endpoint-active-directory-resource-id", "https://management.core.windows.net/",
		"--endpoint-active-directory-graph-resource-id", "https://graph.windows.net/",
		"--suffix-storage-endpoint", "core.windows.net",
		"--suffix-keyvault-dns", ".vault.azure.net",
		"--profile", "latest",
	); err != nil {
		t.Fatalf("az cloud register:\nstderr: %s\nerr: %v", stderr, err)
	}
	if _, stderr, err := run("cloud", "set", "-n", cloudName); err != nil {
		t.Fatalf("az cloud set:\nstderr: %s\nerr: %v", stderr, err)
	}

	// Smoke: list APIM services in a fake RG. Real Azure returns
	// 404 with `ResourceGroupNotFound` for a missing RG; the shim
	// returns 404 with the ARM error envelope. The lane's exit
	// criterion is "request reached the shim" — proven by the
	// harness's per-request t.Log (visible via `go test -v`) plus
	// the `az` exit being either success or the documented
	// not-found path. Auth / URL-construction errors would manifest
	// as different error strings.
	stdout, stderr, err := run(
		"apim", "list",
		"--resource-group", "shim-rg",
		"-o", "json",
	)
	if err != nil {
		s := string(stderr)
		// Accept the canonical "RG not found" path as proof the
		// request reached the shim. Anything else is a failure
		// (auth issue, URL construction error, etc).
		if !strings.Contains(s, "ResourceGroupNotFound") && !strings.Contains(s, "404") {
			t.Fatalf("az apim list (unexpected failure):\nstdout: %s\nstderr: %s\nerr: %v",
				stdout, stderr, err)
		}
	}
	t.Logf("az apim list output:\nstdout: %s\nstderr: %s", stdout, stderr)
}
