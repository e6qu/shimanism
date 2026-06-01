// Conformance: AWS DynamoDB-shaped frontend exercised by the official
// `aws dynamodb` CLI. Each command shells out against the shim's
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
	"github.com/e6qu/shimanism/services/nosql/backends/inmem"
)

func requireAWSCLIForDDB(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed (PATH lookup failed: %v)", err)
	}
	return bin
}

func runAWSDDB(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
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

func TestAWSCLI_DynamoDB_TableAndItem(t *testing.T) {
	awsBin := requireAWSCLIForDDB(t)
	srv := harness.StartNoSQLServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSDDB(t, srv.URL, awsBin, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// create-table.
	mustRun("dynamodb", "create-table",
		"--table-name", "clikv",
		"--attribute-definitions", "AttributeName=id,AttributeType=S",
		"--key-schema", "AttributeName=id,KeyType=HASH",
		"--billing-mode", "PAY_PER_REQUEST",
	)

	// list-tables sees it.
	listOut := mustRun("dynamodb", "list-tables")
	var list struct {
		TableNames []string `json:"TableNames"`
	}
	if err := json.Unmarshal(listOut, &list); err != nil {
		t.Fatalf("list-tables decode: %v\n%s", err, listOut)
	}
	found := false
	for _, n := range list.TableNames {
		if n == "clikv" {
			found = true
		}
	}
	if !found {
		t.Errorf("list-tables didn't include clikv: %v", list.TableNames)
	}

	// put-item.
	mustRun("dynamodb", "put-item",
		"--table-name", "clikv",
		"--item", `{"id": {"S": "cli-a"}, "value": {"N": "42"}}`,
	)

	// get-item round-trip.
	getOut := mustRun("dynamodb", "get-item",
		"--table-name", "clikv",
		"--key", `{"id": {"S": "cli-a"}}`,
	)
	var got struct {
		Item map[string]map[string]string `json:"Item"`
	}
	if err := json.Unmarshal(getOut, &got); err != nil {
		t.Fatalf("get-item decode: %v\n%s", err, getOut)
	}
	if got.Item["value"]["N"] != "42" {
		t.Errorf("get-item value.N = %q, want 42\nfull: %+v", got.Item["value"]["N"], got.Item)
	}

	// delete-item.
	mustRun("dynamodb", "delete-item",
		"--table-name", "clikv",
		"--key", `{"id": {"S": "cli-a"}}`,
	)
	gone := mustRun("dynamodb", "get-item",
		"--table-name", "clikv",
		"--key", `{"id": {"S": "cli-a"}}`,
	)
	// `aws dynamodb get-item` on a missing key returns empty JSON object.
	if len(strings.TrimSpace(string(gone))) > 0 {
		var probe struct {
			Item map[string]any `json:"Item"`
		}
		_ = json.Unmarshal(gone, &probe)
		if len(probe.Item) != 0 {
			t.Errorf("get-item after delete returned item: %s", gone)
		}
	}

	// delete-table.
	mustRun("dynamodb", "delete-table", "--table-name", "clikv")
}
