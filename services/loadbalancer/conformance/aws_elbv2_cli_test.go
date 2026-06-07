// Conformance: AWS ELBv2-shaped frontend exercised by the official
// `aws elbv2` CLI. Each command shells out against the shim's endpoint.
// Skipped if the `aws` binary isn't on PATH.
package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/e6qu/shimanism/internal/harness"
	"github.com/e6qu/shimanism/services/loadbalancer/backends/inmem"
)

func requireAWSELBv2CLI(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("aws")
	if err != nil {
		t.Skipf("aws CLI not installed: %v", err)
	}
	return bin
}

func runAWSELBv2(t *testing.T, srvURL, bin string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	cmd := exec.Command(bin, append([]string{
		"--endpoint-url=" + srvURL,
		"--no-cli-pager",
		"--output", "json",
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

// TestAWSCLI_ELBv2_ALBLifecycle creates an Application LB + HTTP target
// group via the aws elbv2 CLI, verifies describe output, then deletes both.
func TestAWSCLI_ELBv2_ALBLifecycle(t *testing.T) {
	awsBin := requireAWSELBv2CLI(t)
	srv := harness.StartLoadBalancerServerAWS(t, inmem.New())

	mustRun := func(args ...string) []byte {
		t.Helper()
		stdout, stderr, err := runAWSELBv2(t, srv.URL, awsBin, args...)
		if err != nil {
			t.Fatalf("aws %s\nstdout: %s\nstderr: %s\nerr: %v",
				strings.Join(args, " "), stdout, stderr, err)
		}
		return stdout
	}

	// create-load-balancer (type application)
	createOut := mustRun("elbv2", "create-load-balancer",
		"--name", "cli-alb",
		"--type", "application",
	)
	var createResp struct {
		LoadBalancers []struct {
			LoadBalancerArn  string `json:"LoadBalancerArn"`
			LoadBalancerName string `json:"LoadBalancerName"`
			Type             string `json:"Type"`
		} `json:"LoadBalancers"`
	}
	if err := json.Unmarshal(createOut, &createResp); err != nil {
		t.Fatalf("parse create-load-balancer: %v\n%s", err, createOut)
	}
	if len(createResp.LoadBalancers) != 1 {
		t.Fatalf("create-load-balancer count = %d, want 1", len(createResp.LoadBalancers))
	}
	lbARN := createResp.LoadBalancers[0].LoadBalancerArn
	if lbARN == "" {
		t.Fatalf("create-load-balancer returned empty ARN")
	}
	if createResp.LoadBalancers[0].Type != "application" {
		t.Errorf("LB type = %q, want application", createResp.LoadBalancers[0].Type)
	}

	// create-target-group
	tgOut := mustRun("elbv2", "create-target-group",
		"--name", "cli-tg",
		"--protocol", "HTTP",
		"--port", "80",
		"--target-type", "instance",
	)
	var tgResp struct {
		TargetGroups []struct {
			TargetGroupArn string `json:"TargetGroupArn"`
			Protocol       string `json:"Protocol"`
		} `json:"TargetGroups"`
	}
	if err := json.Unmarshal(tgOut, &tgResp); err != nil {
		t.Fatalf("parse create-target-group: %v\n%s", err, tgOut)
	}
	if len(tgResp.TargetGroups) != 1 {
		t.Fatalf("create-target-group count = %d, want 1", len(tgResp.TargetGroups))
	}
	tgARN := tgResp.TargetGroups[0].TargetGroupArn
	if tgARN == "" {
		t.Fatalf("create-target-group returned empty ARN")
	}

	// describe-load-balancers — confirm our ALB is listed
	descOut := mustRun("elbv2", "describe-load-balancers")
	var descResp struct {
		LoadBalancers []struct {
			LoadBalancerArn string `json:"LoadBalancerArn"`
		} `json:"LoadBalancers"`
	}
	if err := json.Unmarshal(descOut, &descResp); err != nil {
		t.Fatalf("parse describe-load-balancers: %v\n%s", err, descOut)
	}
	found := false
	for _, lb := range descResp.LoadBalancers {
		if lb.LoadBalancerArn == lbARN {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("describe-load-balancers: cli-alb not found (ARN=%s)", lbARN)
	}

	// delete-target-group
	mustRun("elbv2", "delete-target-group", "--target-group-arn", tgARN)

	// delete-load-balancer
	mustRun("elbv2", "delete-load-balancer", "--load-balancer-arn", lbARN)

	// verify gone
	afterOut := mustRun("elbv2", "describe-load-balancers")
	var afterResp struct {
		LoadBalancers []struct {
			LoadBalancerArn string `json:"LoadBalancerArn"`
		} `json:"LoadBalancers"`
	}
	if err := json.Unmarshal(afterOut, &afterResp); err != nil {
		t.Fatalf("parse describe-load-balancers (after delete): %v\n%s", err, afterOut)
	}
	for _, lb := range afterResp.LoadBalancers {
		if lb.LoadBalancerArn == lbARN {
			t.Errorf("LB still present after delete: %s", lbARN)
		}
	}
}
