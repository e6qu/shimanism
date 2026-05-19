// Phase 5 conformance: AWS RDS-shaped frontend exercised by the
// official `aws rds` CLI. Skipped if the `aws` binary isn't on
// PATH; CI's main lane has it preinstalled.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/rdbms/backends/inmem"
)

func requireAWSCLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed: %v", err)
	}
	return bin
}

func runAWSRDS(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"--endpoint-url=" + srvURL, "--no-cli-pager"}, args...)...)
	cmd.Env = append(os.Environ(),
		"AWS_ACCESS_KEY_ID=test",
		"AWS_SECRET_ACCESS_KEY=test",
		"AWS_DEFAULT_REGION=us-east-1",
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestAWSCLI_RDBMSLifecycle(t *testing.T) {
	bin := requireAWSCLI(t)
	srv := harness.StartRDBMSServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSRDS(t, srv.URL, bin, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	mustRun("rds", "create-db-instance",
		"--db-instance-identifier", "cli-test",
		"--engine", "postgres",
		"--engine-version", "15",
		"--db-instance-class", "db.t3.micro",
		"--master-username", "shimadmin",
		"--master-user-password", "cli-supersecret",
		"--allocated-storage", "20")

	out := mustRun("rds", "describe-db-instances",
		"--db-instance-identifier", "cli-test")
	var desc struct {
		DBInstances []struct {
			DBInstanceIdentifier string `json:"DBInstanceIdentifier"`
			DBInstanceStatus     string `json:"DBInstanceStatus"`
		} `json:"DBInstances"`
	}
	if err := json.Unmarshal(out, &desc); err != nil {
		t.Fatalf("decode describe: %v\nbody: %s", err, out)
	}
	if len(desc.DBInstances) != 1 {
		t.Fatalf("describe count = %d, want 1", len(desc.DBInstances))
	}

	mustRun("rds", "delete-db-instance",
		"--db-instance-identifier", "cli-test",
		"--skip-final-snapshot")
}
