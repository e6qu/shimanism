// Conformance: AWS Route 53-shaped frontend exercised by the official
// `aws route53` CLI. Each command shells out against the shim's
// endpoint. Skipped if the `aws` binary isn't on PATH; CI's main lane
// has it preinstalled.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/dns/backends/inmem"
)

func requireAWSCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAWSRoute53(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srvURL, "--no-cli-pager"}, args...)...)
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

func TestAWSCLI_Route53_ZoneLifecycle(t *testing.T) {
	awsBin := requireAWSCLI(t)
	srv := harness.StartDNSServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSRoute53(t, srv.URL, awsBin, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	created := mustRun("route53", "create-hosted-zone",
		"--name", "cli.test.",
		"--caller-reference", "cli-conformance-1",
	)
	var createOut struct {
		HostedZone struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"HostedZone"`
		DelegationSet struct {
			NameServers []string `json:"NameServers"`
		} `json:"DelegationSet"`
	}
	if err := json.Unmarshal(created, &createOut); err != nil {
		t.Fatalf("parse create-hosted-zone output: %v\n%s", err, created)
	}
	if createOut.HostedZone.Name != "cli.test." {
		t.Errorf("create-hosted-zone Name = %q, want cli.test.", createOut.HostedZone.Name)
	}
	if len(createOut.DelegationSet.NameServers) == 0 {
		t.Errorf("DelegationSet.NameServers is empty")
	}

	// list-hosted-zones.
	listed := mustRun("route53", "list-hosted-zones")
	var listOut struct {
		HostedZones []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
		} `json:"HostedZones"`
	}
	if err := json.Unmarshal(listed, &listOut); err != nil {
		t.Fatalf("parse list-hosted-zones output: %v\n%s", err, listed)
	}
	if len(listOut.HostedZones) != 1 || listOut.HostedZones[0].Name != "cli.test." {
		t.Errorf("list-hosted-zones unexpected: %+v", listOut.HostedZones)
	}

	// get-hosted-zone --id <id>.
	mustRun("route53", "get-hosted-zone", "--id", createOut.HostedZone.Id)

	// delete-hosted-zone — succeeds because the zone has only managed records.
	mustRun("route53", "delete-hosted-zone", "--id", createOut.HostedZone.Id)

	// list-hosted-zones should now be empty.
	listed = mustRun("route53", "list-hosted-zones")
	listOut.HostedZones = nil
	if err := json.Unmarshal(listed, &listOut); err != nil {
		t.Fatalf("parse list-hosted-zones output: %v\n%s", err, listed)
	}
	if len(listOut.HostedZones) != 0 {
		t.Errorf("after delete, list-hosted-zones = %+v, want empty", listOut.HostedZones)
	}
}
